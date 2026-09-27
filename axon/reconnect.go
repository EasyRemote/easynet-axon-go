// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/reconnect.go
// Description: Automatic runtime transport reconnection for the Go SDK.
//
// Protocol Responsibility:
// - Detects loss of the gRPC channel to the Axon runtime via ping-style calls.
// - Re-establishes the bridge handle with exponential backoff + jitter.
// - Invokes a user-provided hook so the application can restore provider-owned
//   state after the transport reopens.
//
// Usage Contract:
// - Callers opt in by setting `SidecarTransport.AutoReconnect = true` and
//   optionally configuring `ReconnectOptions`. The `OnReconnect` hook is
//   invoked after the bridge is successfully re-opened but before any user
//   RPC is served. If the hook returns an error the transport treats the
//   attempt as failed and continues backing off.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package axon

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ReconnectHook is called after the SDK successfully reopens the bridge
// following a disconnect. Applications use it to restore provider-owned state
// and restart ambient subscriptions.
//
// The hook runs on the reconnect worker goroutine, not on any user call
// path, so long operations are acceptable. Returning an error causes the
// reconnect to be considered a failure and the backoff to continue.
type ReconnectHook func(ctx context.Context) error

// ReconnectOptions controls automatic reconnection behavior.
//
// Defaults use exponential backoff starting at one second, capped at 30
// seconds, with 20 percent jitter.
type ReconnectOptions struct {
	// InitialBackoffMs is the first delay used after a disconnect. Subsequent
	// failed attempts double the delay up to MaxBackoffMs. Default: 1000.
	InitialBackoffMs int
	// MaxBackoffMs caps the exponential backoff. Default: 30000.
	MaxBackoffMs int
	// OnReconnect is invoked after each successful bridge re-open so the caller
	// can restore application-owned state. Optional.
	OnReconnect ReconnectHook
	// HealthCheckIntervalMs controls how often the background loop checks
	// for a dropped bridge when there is no live traffic. 0 disables the
	// background loop entirely (disconnection is then only detected on the
	// next user call). Default: 15000.
	HealthCheckIntervalMs int
	// MaxAttempts bounds consecutive reconnect attempts before giving up.
	// 0 (default) means unlimited retries — matches historical Go behavior.
	// When set, a terminal failure stamps `lastReconnectErr` and leaves
	// `disconnected=true` so the next user call surfaces the error
	// immediately rather than retrying forever.
	MaxAttempts int
}

// applyDefaults normalizes zero values in-place.
func (o *ReconnectOptions) applyDefaults() {
	if o.InitialBackoffMs <= 0 {
		o.InitialBackoffMs = 1000
	}
	if o.MaxBackoffMs < o.InitialBackoffMs {
		// Preserve monotonicity: if the caller set InitialBackoffMs above the
		// default cap, the cap must not fall below it.
		o.MaxBackoffMs = 30000
		if o.InitialBackoffMs > o.MaxBackoffMs {
			o.MaxBackoffMs = o.InitialBackoffMs
		}
	}
	if o.HealthCheckIntervalMs == 0 {
		o.HealthCheckIntervalMs = 15000
	}
}

// reconnectState holds the runtime state of the auto-reconnect worker.
// It is embedded into SidecarTransport via enableAutoReconnect.
type reconnectState struct {
	options ReconnectOptions
	// disconnected is set when a call observes a transport error suggesting
	// the bridge is no longer usable. The background worker watches this
	// flag and triggers a reconnect cycle.
	disconnected atomic.Bool
	// generation counts successful (re)connects. Consumers (e.g. stream
	// subscribers) can cache this and re-establish when it advances.
	generation atomic.Uint64
	// lastReconnectErr records the last failure for diagnostics. Guarded by mu.
	lastReconnectErr error
	mu               sync.Mutex
	// cancel cancels the background health-check goroutine on Close.
	cancel context.CancelFunc
	// wg tracks the background goroutine for graceful shutdown.
	wg sync.WaitGroup
	// started guards double-start.
	started atomic.Bool
}

// enableAutoReconnect lazily initializes the reconnect state and starts the
// background health-check goroutine (if configured). Safe to call repeatedly;
// subsequent calls are no-ops.
//
// Must be called with s.mu held, or from a context where no concurrent writer
// exists (e.g. before the first user RPC).
func (s *SidecarTransport) enableAutoReconnect() {
	if !s.AutoReconnect {
		return
	}
	if s.reconnect == nil {
		opts := s.ReconnectOptions
		opts.applyDefaults()
		s.reconnect = &reconnectState{options: opts}
	}
	if s.reconnect.started.Swap(true) {
		return
	}
	if s.reconnect.options.HealthCheckIntervalMs <= 0 {
		return // caller opted out of the background loop
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.reconnect.cancel = cancel
	s.reconnect.wg.Add(1)
	go s.reconnectWorker(ctx)
}

// markDisconnected flags the transport for reconnection on the next cycle.
// Safe to call from any goroutine.
func (s *SidecarTransport) markDisconnected() {
	if s.reconnect == nil {
		return
	}
	s.reconnect.disconnected.Store(true)
}

// ReconnectGeneration returns a counter that increments on every successful
// (re)connect. Callers that need to re-establish stateful resources (like
// watch streams) after a reconnect can compare the current value against a
// cached snapshot.
func (s *SidecarTransport) ReconnectGeneration() uint64 {
	if s.reconnect == nil {
		return 0
	}
	return s.reconnect.generation.Load()
}

// LastReconnectError returns the most recent failure from the reconnect
// worker, or nil if the last attempt succeeded (or none has happened yet).
// Useful for health endpoints and debugging.
func (s *SidecarTransport) LastReconnectError() error {
	if s.reconnect == nil {
		return nil
	}
	s.reconnect.mu.Lock()
	defer s.reconnect.mu.Unlock()
	return s.reconnect.lastReconnectErr
}

// reconnectWorker runs until the context is cancelled. It wakes up periodically
// and, if the disconnected flag is set, drives reconnection with exponential
// backoff until it succeeds or the context is cancelled.
func (s *SidecarTransport) reconnectWorker(ctx context.Context) {
	defer s.reconnect.wg.Done()
	interval := time.Duration(s.reconnect.options.HealthCheckIntervalMs) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.reconnect.disconnected.Load() {
				continue
			}
			s.driveReconnect(ctx)
		}
	}
}

// driveReconnect retries the bridge open with exponential backoff + jitter.
// It clears the `disconnected` flag once a (re)connect + hook completes.
// When `MaxAttempts > 0`, the loop exits after that many failed attempts and
// leaves the transport in the disconnected state so the next user call
// observes the terminal error via `lastReconnectErr`.
func (s *SidecarTransport) driveReconnect(ctx context.Context) {
	backoff := time.Duration(s.reconnect.options.InitialBackoffMs) * time.Millisecond
	max := time.Duration(s.reconnect.options.MaxBackoffMs) * time.Millisecond
	maxAttempts := s.reconnect.options.MaxAttempts
	attempts := 0
	for {
		if ctx.Err() != nil {
			return
		}
		// Proactively drop any stale bridge handle so ensureClient rebuilds it.
		s.retireReconnectClient()
		if ctx.Err() != nil {
			return
		}

		err := s.ensureClient()
		if ctx.Err() != nil {
			s.retireReconnectClient()
			return
		}
		if err == nil && s.reconnect.options.OnReconnect != nil {
			err = s.reconnect.options.OnReconnect(ctx)
		}
		if ctx.Err() != nil {
			s.retireReconnectClient()
			return
		}
		s.reconnect.mu.Lock()
		s.reconnect.lastReconnectErr = err
		s.reconnect.mu.Unlock()
		if err == nil {
			s.reconnect.generation.Add(1)
			s.reconnect.disconnected.Store(false)
			return
		}

		attempts++
		if maxAttempts > 0 && attempts >= maxAttempts {
			// Give up — leave disconnected=true so callers surface the error.
			return
		}

		// Sleep with ±20% jitter, capped.
		jitter := time.Duration(rand.Int63n(int64(backoff/5) + 1)) //nolint:gosec // non-crypto
		sleep := backoff + jitter - (backoff / 5)
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
		// Exponential backoff, clamped.
		backoff *= 2
		if backoff > max {
			backoff = max
		}
	}
}

// retireReconnectClient releases stale or cancelled client ownership.
func (s *SidecarTransport) retireReconnectClient() {
	s.mu.Lock()
	defer s.mu.Unlock()
	handle := s.handle
	s.handle = 0
	if s.bridge != nil && handle != 0 {
		_ = s.bridge.CloseClient(handle)
	}
}

// isTransportError returns true if the error looks like a dropped connection
// rather than an application-level error. We keep the heuristic permissive
// (substring match on common transport error fragments) because the dendrite
// bridge returns errors as opaque strings via FFI — no structured codes.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	markers := []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"transport is closing",
		"no such host",
		"network is unreachable",
		"deadline exceeded", // treat connection-phase deadline as transport failure
		"unavailable",       // gRPC UNAVAILABLE
		"eof",
		"axon_node_unreachable",
		"axon_peer_disconnected",
	}
	for _, m := range markers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// shutdownReconnect stops the background worker and waits for it to exit.
// Called from SidecarTransport.Close.
func (s *SidecarTransport) shutdownReconnect() {
	if s.reconnect == nil {
		return
	}
	if s.reconnect.cancel != nil {
		s.reconnect.cancel()
	}
	s.reconnect.wg.Wait()
}
