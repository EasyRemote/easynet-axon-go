// Tests for ResumingStream: the core loop must pass through healthy streams,
// reopen on transport errors, propagate application errors, and cap retries.

package axon

import (
	"errors"
	"fmt"
	"testing"
)

type scriptStep struct {
	chunk       []byte
	done        bool
	transport   string // non-empty → return as transport error
	application string // non-empty → return as plain error (not transport)
}

type scriptSource struct {
	steps      []scriptStep
	closed     bool
	closeCalls int
}

func (s *scriptSource) RecvChunk() ([]byte, bool, error) {
	if len(s.steps) == 0 {
		return nil, true, nil
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	switch {
	case step.done:
		return nil, true, nil
	case step.transport != "":
		return nil, false, errors.New(step.transport)
	case step.application != "":
		return nil, false, errors.New(step.application)
	default:
		return step.chunk, false, nil
	}
}

func (s *scriptSource) CloseStream() error {
	s.closeCalls++
	s.closed = true
	return nil
}

func TestResumingStream_PassesThroughChunks(t *testing.T) {
	t.Parallel()
	src := &scriptSource{steps: []scriptStep{
		{chunk: []byte("a")},
		{chunk: []byte("b")},
		{done: true},
	}}
	rs := NewResumingStream(src, func() (StreamChunkSource, error) {
		t.Fatalf("factory must not run when stream is healthy")
		return nil, nil
	}, ResumePolicy{})
	var got []string
	for {
		chunk, err := rs.Recv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if chunk == nil {
			break
		}
		got = append(got, string(chunk))
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("unexpected chunks: %v", got)
	}
	if rs.AttemptsUsed() != 0 {
		t.Fatalf("attempts: want 0 got %d", rs.AttemptsUsed())
	}
}

func TestResumingStream_ReconnectsOnTransportError(t *testing.T) {
	t.Parallel()
	initial := &scriptSource{steps: []scriptStep{
		{chunk: []byte("x")},
		{transport: "connection refused"},
	}}
	var factoryCalls int
	rs := NewResumingStream(initial, func() (StreamChunkSource, error) {
		factoryCalls++
		return &scriptSource{steps: []scriptStep{
			{chunk: []byte("y")},
			{chunk: []byte("z")},
			{done: true},
		}}, nil
	}, ResumePolicy{})
	var got []string
	for {
		chunk, err := rs.Recv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if chunk == nil {
			break
		}
		got = append(got, string(chunk))
	}
	if len(got) != 3 || got[0] != "x" || got[1] != "y" || got[2] != "z" {
		t.Fatalf("unexpected chunks after reconnect: %v", got)
	}
	if rs.AttemptsUsed() != 1 {
		t.Fatalf("expected 1 reconnect, got %d", rs.AttemptsUsed())
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls: want 1 got %d", factoryCalls)
	}
	if !initial.closed {
		t.Fatalf("initial source should have been closed during reconnect")
	}
}

func TestResumingStream_PropagatesApplicationErrors(t *testing.T) {
	t.Parallel()
	src := &scriptSource{steps: []scriptStep{
		{chunk: []byte("a")},
		{application: "invalid argument"},
	}}
	rs := NewResumingStream(src, func() (StreamChunkSource, error) {
		t.Fatalf("factory must not run on application error")
		return nil, nil
	}, ResumePolicy{})
	chunk, err := rs.Recv()
	if err != nil {
		t.Fatalf("first recv: %v", err)
	}
	if string(chunk) != "a" {
		t.Fatalf("first chunk: got %q", chunk)
	}
	if _, err := rs.Recv(); err == nil {
		t.Fatalf("expected application error on second recv")
	}
	if rs.AttemptsUsed() != 0 {
		t.Fatalf("app errors should not consume attempts: got %d", rs.AttemptsUsed())
	}
}

func TestResumingStream_GivesUpAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	// Every open and every recv fails with a transport error. With
	// MaxAttempts=2 the consumer should see the failure surface after 2 retries.
	initial := &scriptSource{steps: []scriptStep{{transport: "Unavailable"}}}
	factoryFails := 0
	rs := NewResumingStream(initial, func() (StreamChunkSource, error) {
		factoryFails++
		return &scriptSource{steps: []scriptStep{{transport: "Unavailable"}}}, nil
	}, ResumePolicy{MaxAttempts: 2})
	_, err := rs.Recv()
	if err == nil {
		t.Fatalf("expected transport error to surface after MaxAttempts")
	}
	if !isTransportError(err) {
		t.Fatalf("surfaced error should be transport-classified, got %v", err)
	}
	if rs.AttemptsUsed() != 2 {
		t.Fatalf("attempts used: want 2 got %d", rs.AttemptsUsed())
	}
}

func TestResumingStream_DeferredOpensLazily(t *testing.T) {
	t.Parallel()
	var factoryCalls int
	rs := NewResumingStream(nil, func() (StreamChunkSource, error) {
		factoryCalls++
		return &scriptSource{steps: []scriptStep{
			{chunk: []byte("q")},
			{done: true},
		}}, nil
	}, ResumePolicy{})
	chunk, err := rs.Recv()
	if err != nil || string(chunk) != "q" {
		t.Fatalf("first recv: chunk=%q err=%v", chunk, err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls: want 1 got %d", factoryCalls)
	}
}

func TestNewResumingStream_PanicsOnNilFactory(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for nil factory")
		}
	}()
	_ = NewResumingStream(nil, nil, ResumePolicy{})
}

func TestResumingStream_RejectsNilFactoryResult(t *testing.T) {
	calls := 0
	rs := NewResumingStream(nil, func() (StreamChunkSource, error) {
		calls++
		return nil, nil
	}, ResumePolicy{})
	chunk, err := rs.Recv()
	if err == nil || chunk != nil || isTransportError(err) {
		t.Fatalf("invalid factory result must be an application error: chunk=%q err=%v", chunk, err)
	}
	if calls != 1 || rs.AttemptsUsed() != 0 {
		t.Fatalf("invalid result retried: calls=%d attempts=%d", calls, rs.AttemptsUsed())
	}
	if err := rs.Close(); err != nil {
		t.Fatal(err)
	}
}

type closeFailureSource struct {
	cause error
	calls int
}

func (s *closeFailureSource) RecvChunk() ([]byte, bool, error) { return nil, true, nil }
func (s *closeFailureSource) CloseStream() error               { s.calls++; return s.cause }

func TestResumingStream_ClosePreservesFailureCauseAndDetachesSource(t *testing.T) {
	cause := errors.New("native handle release failed")
	source := &closeFailureSource{cause: cause}
	rs := NewResumingStream(source, func() (StreamChunkSource, error) {
		t.Fatal("close must not reopen")
		return nil, nil
	}, ResumePolicy{})
	err := rs.Close()
	if !errors.Is(err, cause) {
		t.Fatalf("lost close failure cause: %v", err)
	}
	if err.Error() != "axon: close during resume: native handle release failed" {
		t.Fatalf("changed diagnostic: %v", err)
	}
	if err := rs.Close(); err != nil || source.calls != 1 {
		t.Fatalf("close repeated: calls=%d err=%v", source.calls, err)
	}
}

func TestResumingStream_NormalEndRetiresSourceOnce(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var cause error
			if fail {
				cause = errors.New("cleanup unavailable")
			}
			src := &closeFailureSource{cause: cause}
			r := NewResumingStream(src, func() (StreamChunkSource, error) { t.Fatal("EOF must not reopen"); return nil, nil }, ResumePolicy{})
			_, first := r.Recv()
			if !errors.Is(first, cause) || src.calls != 1 {
				t.Fatalf("EOF cleanup: err=%v calls=%d", first, src.calls)
			}
			for i := 0; i < 3; i++ {
				chunk, err := r.Recv()
				if chunk != nil || err != first {
					t.Fatalf("terminal changed: %v %v", chunk, err)
				}
			}
			if err := r.Close(); err != nil || src.calls != 1 {
				t.Fatalf("cleanup repeated: %v %d", err, src.calls)
			}
		})
	}
}

func TestResumingStream_ExplicitCloseBeforeEndStillAllowsOpen(t *testing.T) {
	src := &scriptSource{}
	calls := 0
	r := NewResumingStream(src, func() (StreamChunkSource, error) {
		calls++
		return &scriptSource{steps: []scriptStep{{chunk: []byte("next")}}}, nil
	}, ResumePolicy{})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	chunk, err := r.Recv()
	if err != nil || string(chunk) != "next" || calls != 1 || src.closeCalls != 1 {
		t.Fatalf("reopen: %q %v %d", chunk, err, calls)
	}
	r.Close()
}
