// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/streams.go
// Description: Client-local primitives for Axon-semantics streams —
//   credit-based flow control, cooperative cancellation, terminal-
//   frame invariant. Go port of the Python reference implementation
//   (sdk/python/axon_sdk/streams.py).
//
// Protocol Responsibility:
// - Mirrors the three load-bearing properties of stream.proto at the
//   SDK boundary: credit-based backpressure, cooperative cancellation,
//   and guaranteed-one terminal frame per logical stream.
//
// Implementation Notes:
// - Idiomatic Go: the (Source, Sink) pair shares an *inner; channels
//   carry both frames and credit, which maps naturally onto Go's
//   CSP concurrency.
// - Queue slot = 1 unit of credit. The initial channel buffer is
//   sized at initialCredit+1 so the terminal frame always has a
//   reserved slot and cannot be starved by non-terminal frames.
// - Cancellation is monotonic: once Cancel is called, the cancel
//   channel is closed; Push returns ErrStreamCancelled, and the
//   next NextFrame returns a synthetic terminal frame with reason.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package axon

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// StreamFrame is one unit of data on an Axon-semantics stream.
// A Terminal frame marks end-of-stream; at most one terminal frame
// reaches the consumer per logical stream.
type StreamFrame struct {
	Data     []byte
	Meta     map[string]string
	Terminal bool
}

// NewStreamFrame builds a non-terminal frame with the given payload.
func NewStreamFrame(data []byte) StreamFrame {
	return StreamFrame{Data: data, Meta: map[string]string{}}
}

// TerminalFrame builds a terminal frame carrying a reason string.
func TerminalFrame(reason string) StreamFrame {
	return StreamFrame{
		Meta:     map[string]string{"reason": reason},
		Terminal: true,
	}
}

// Errors
var (
	// ErrStreamCancelled is returned by Push when the stream has been
	// cancelled by either side.
	ErrStreamCancelled = errors.New("stream cancelled")
	// ErrStreamTimeout is returned by Push when no credit became
	// available within the timeout.
	ErrStreamTimeout = errors.New("stream push timeout (no credit)")
)

// ---------------------------------------------------------------------
// Shared inner state
// ---------------------------------------------------------------------

type streamInner struct {
	mu sync.Mutex

	frames chan StreamFrame
	// credit is a counting semaphore of tokens available to the producer.
	creditCh chan struct{}

	cancelCh     chan struct{}
	cancelOnce   sync.Once
	cancelReason string

	closeOnce sync.Once

	// track whether the terminal frame has been delivered to the
	// consumer so a second NextFrame returns nothing.
	terminalSeen bool
	maxCredit    int
}

func newStreamInner(initialCredit int) *streamInner {
	if initialCredit < 1 {
		initialCredit = 1
	}
	inner := &streamInner{
		// +1 reserves a slot for the terminal frame even when the
		// queue is otherwise full.
		frames:    make(chan StreamFrame, initialCredit+1),
		creditCh:  make(chan struct{}, initialCredit),
		cancelCh:  make(chan struct{}),
		maxCredit: initialCredit,
	}
	for i := 0; i < initialCredit; i++ {
		inner.creditCh <- struct{}{}
	}
	return inner
}

func (s *streamInner) push(frame StreamFrame, timeout time.Duration) error {
	select {
	case <-s.cancelCh:
		return fmt.Errorf("%w: %s", ErrStreamCancelled, s.cancelReason)
	default:
	}

	// Terminal frames do not consume credit; they always have a slot
	// reserved (frames chan buffer size = initialCredit+1).
	if !frame.Terminal {
		if timeout <= 0 {
			// Non-blocking attempt + unbounded wait if caller passed 0.
			select {
			case <-s.creditCh:
			case <-s.cancelCh:
				return ErrStreamCancelled
			}
		} else {
			t := time.NewTimer(timeout)
			defer t.Stop()
			select {
			case <-s.creditCh:
			case <-s.cancelCh:
				return ErrStreamCancelled
			case <-t.C:
				return ErrStreamTimeout
			}
		}
	}

	select {
	case s.frames <- frame:
		return nil
	case <-s.cancelCh:
		if !frame.Terminal {
			// restore credit — we never sent the frame
			select {
			case s.creditCh <- struct{}{}:
			default:
			}
		}
		return ErrStreamCancelled
	}
}

func (s *streamInner) nextFrame(timeout time.Duration) (StreamFrame, bool) {
	s.mu.Lock()
	if s.terminalSeen {
		s.mu.Unlock()
		return StreamFrame{}, false
	}
	s.mu.Unlock()

	var f StreamFrame
	var ok bool
	if timeout <= 0 {
		f, ok = <-s.frames, true
		if !ok {
			return StreamFrame{}, false
		}
	} else {
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case f = <-s.frames:
			ok = true
		case <-t.C:
			return StreamFrame{}, false
		}
	}
	if !ok {
		return StreamFrame{}, false
	}
	if f.Terminal {
		s.mu.Lock()
		s.terminalSeen = true
		s.mu.Unlock()
		return f, true
	}
	// grant one unit of credit back
	select {
	case s.creditCh <- struct{}{}:
	default:
		// credit cap reached (should not happen under correct usage)
	}
	return f, true
}

func (s *streamInner) cancel(reason string) {
	s.cancelOnce.Do(func() {
		s.cancelReason = reason
		close(s.cancelCh)
		// push a terminal frame so any waiting consumer wakes up.
		// Use non-blocking because the queue has a reserved terminal slot.
		select {
		case s.frames <- TerminalFrame(reason):
		default:
		}
	})
}

func (s *streamInner) close(reason string) {
	// best-effort: if already cancelled, skip
	select {
	case <-s.cancelCh:
		return
	default:
	}
	s.closeOnce.Do(func() {
		select {
		case s.frames <- TerminalFrame(reason):
		default:
			s.cancel(reason)
		}
	})
}

func (s *streamInner) grantCredit(n int) {
	for i := 0; i < n; i++ {
		select {
		case s.creditCh <- struct{}{}:
		default:
			return
		}
	}
}

// ---------------------------------------------------------------------
// Public handles
// ---------------------------------------------------------------------

// StreamSource is the producer-side handle. Safe for concurrent use.
type StreamSource struct{ inner *streamInner }

// Push enqueues a frame. If timeout is zero, the call blocks until
// credit is available or the stream is cancelled. Returns
// ErrStreamCancelled or ErrStreamTimeout.
func (s *StreamSource) Push(frame StreamFrame, timeout time.Duration) error {
	return s.inner.push(frame, timeout)
}

// Cancel transitions the stream to cancelled; further Pushes fail.
func (s *StreamSource) Cancel(reason string) { s.inner.cancel(reason) }

// Close emits a terminal frame (best-effort). After close, further
// pushes will fail.
func (s *StreamSource) Close(reason string) { s.inner.close(reason) }

// StreamSink is the consumer-side handle. Safe for concurrent use.
type StreamSink struct{ inner *streamInner }

// NextFrame returns the next frame. The second return value is false
// if the timeout elapsed with no frame or if the stream has already
// ended (and the consumer already saw the terminal frame).
func (s *StreamSink) NextFrame(timeout time.Duration) (StreamFrame, bool) {
	return s.inner.nextFrame(timeout)
}

// GrantCredit widens the in-flight window for the producer.
func (s *StreamSink) GrantCredit(n int) { s.inner.grantCredit(n) }

// Cancel transitions the stream to cancelled; the next NextFrame
// returns a terminal frame carrying the cancel reason.
func (s *StreamSink) Cancel(reason string) { s.inner.cancel(reason) }

// Cancelled reports whether the stream has been cancelled.
func (s *StreamSink) Cancelled() bool {
	select {
	case <-s.inner.cancelCh:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------
// Factory + helpers
// ---------------------------------------------------------------------

// DefaultLoopbackInitialCredit matches the Python/Node/Swift/Java default for
// Loopback. The bounded frame window absorbs routine producer/consumer jitter
// without making backpressure ineffective. Pass 0 to use this default.
const DefaultLoopbackInitialCredit = 64

// Loopback creates a paired (source, sink) on a fresh in-process
// loopback. Identical semantics to an Axon gRPC round-trip between
// two abilities on the same runtime, but without the FFI hop.
//
// Pass `initialCredit = 0` to use DefaultLoopbackInitialCredit.
func Loopback(initialCredit int) (*StreamSource, *StreamSink) {
	if initialCredit <= 0 {
		initialCredit = DefaultLoopbackInitialCredit
	}
	inner := newStreamInner(initialCredit)
	return &StreamSource{inner: inner}, &StreamSink{inner: inner}
}

// DriveProducer runs fn on a new goroutine, passing the source. Always
// closes the source on return (best-effort) so consumers observe a
// clean terminal frame.
func DriveProducer(src *StreamSource, fn func(*StreamSource)) {
	go func() {
		defer src.Close("producer eof")
		fn(src)
	}()
}

// Drain consumes frames from sink until the terminal frame arrives.
// Returns the number of non-terminal frames delivered. On every frame
// it calls consumer(frame).
func Drain(sink *StreamSink, idleTimeout time.Duration, consumer func(StreamFrame)) int {
	count := 0
	for {
		f, ok := sink.NextFrame(idleTimeout)
		if !ok {
			continue
		}
		if f.Terminal {
			return count
		}
		consumer(f)
		count++
	}
}
