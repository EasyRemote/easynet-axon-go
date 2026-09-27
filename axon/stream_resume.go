// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/stream_resume.go
// Description: Auto-resuming wrapper around a server-streaming chunk source.
//
// Protocol Responsibility:
// - When a chunk `Recv` fails with a transport-level error, transparently
//   re-open the stream via a caller-supplied factory and continue yielding.
// - Caps the number of reconnect attempts so a permanently-dead stream
//   surfaces the failure instead of spinning.
//
// Implementation Notes:
// - The FFI bridge does not currently expose per-frame `sequence` or the
//   per-session `resume_token`, so a reconnected stream begins fresh rather
//   than replaying from a checkpoint. Callers that need exactly-once
//   semantics must still idempotency-key their individual chunks.
// - When the bridge surface exposes resume_token we can upgrade this
//   wrapper to use the ResumeStream RPC without breaking its public API.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package axon

import (
	"errors"
	"fmt"
)

// StreamChunkSource is any object that can produce chunks one at a time and
// be closed. Implementations are typically thin wrappers over the FFI
// server-stream handle, or test fakes.
type StreamChunkSource interface {
	// RecvChunk returns (chunk, done, err). `done=true` means end-of-stream
	// (chunk is ignored). Transport errors trigger the resume machinery;
	// application errors propagate to the caller.
	RecvChunk() (chunk []byte, done bool, err error)
	// CloseStream releases the native handle. Called on both normal end
	// and during reconnect-teardown; must tolerate being called twice.
	CloseStream() error
}

// StreamFactory returns a fresh `StreamChunkSource` when invoked. Used by
// `ResumingStream` to re-open the stream on transport failure.
type StreamFactory func() (StreamChunkSource, error)

// ResumePolicy caps reconnect attempts and is exposed as a struct so new
// fields can be added later without a breaking change.
type ResumePolicy struct {
	// MaxAttempts is the cap on reconnects per stream; default 3 when <= 0.
	// Keeping this finite prevents dead-connection spin in the consumer.
	MaxAttempts int
}

// ResumingStream wraps a `StreamChunkSource` with automatic reopen on
// transport-classified errors. Zero value is not usable; use NewResumingStream.
type ResumingStream struct {
	factory  StreamFactory
	policy   ResumePolicy
	current  StreamChunkSource
	attempts int
	ended    bool
	endError error
}

// NewResumingStream constructs from an already-open source plus the factory
// to use on reconnect. For lazy-open semantics, pass `initial=nil`.
func NewResumingStream(initial StreamChunkSource, factory StreamFactory, policy ResumePolicy) *ResumingStream {
	if factory == nil {
		// Refuse to build an unusable stream — the caller almost certainly
		// wrote a bug. Panic here is better than an opaque nil-deref later.
		panic("axon: ResumingStream requires a non-nil factory")
	}
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 3
	}
	return &ResumingStream{
		factory: factory,
		policy:  policy,
		current: initial,
	}
}

// AttemptsUsed returns the number of reconnect attempts consumed so far.
// Useful for telemetry — consumers can surface "stream reconnected N times"
// in their health output.
func (r *ResumingStream) AttemptsUsed() int {
	return r.attempts
}

// Recv returns the next chunk, (nil, nil) on end-of-stream, or an error.
// On transport errors it re-opens via the factory and transparently retries
// up to `MaxAttempts`. Application errors (validation, policy, not-found)
// propagate to the caller unchanged — callers can detect them via
// `isTransportError` if they want special handling.
func (r *ResumingStream) Recv() ([]byte, error) {
	if r.ended {
		return nil, r.endError
	}
	for {
		if r.current == nil {
			fresh, err := r.factory()
			if err != nil {
				if !isTransportError(err) || !r.canRetry() {
					return nil, err
				}
				r.attempts++
				continue
			}
			if fresh == nil {
				return nil, errors.New("axon: stream factory returned a nil source")
			}
			r.current = fresh
		}
		chunk, done, err := r.current.RecvChunk()
		if done {
			// Latch completion before caller-owned cleanup; never reopen after EOF.
			r.ended = true
			r.endError = r.closeCurrent()
			return nil, r.endError
		}
		if err == nil {
			return chunk, nil
		}
		if !isTransportError(err) {
			return nil, err
		}
		if !r.canRetry() {
			return nil, err
		}
		r.closeCurrent()
		r.attempts++
	}
}

// Close releases the underlying source. Safe to call multiple times.
// Before EOF a later Recv may reopen; normal EOF remains terminal.
func (r *ResumingStream) Close() error {
	return r.closeCurrent()
}

func (r *ResumingStream) canRetry() bool {
	return r.attempts < r.policy.MaxAttempts
}

func (r *ResumingStream) closeCurrent() error {
	if r.current == nil {
		return nil
	}
	s := r.current
	r.current = nil
	if err := s.CloseStream(); err != nil {
		// Wrap so the caller can tell this was a teardown-time failure
		// rather than an operational error on the new stream.
		return fmt.Errorf("axon: close during resume: %w", err)
	}
	return nil
}
