package axon

import (
	"errors"
	"testing"
	"time"
)

func TestLoopbackDeliversFramesInOrder(t *testing.T) {
	src, sink := Loopback(4)
	for i := 0; i < 3; i++ {
		if err := src.Push(NewStreamFrame([]byte{byte(i)}), 0); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	src.Close("done")
	for i := 0; i < 3; i++ {
		f, ok := sink.NextFrame(time.Second)
		if !ok {
			t.Fatalf("next_frame returned !ok at %d", i)
		}
		if f.Terminal {
			t.Fatalf("unexpected terminal at %d", i)
		}
		if len(f.Data) != 1 || f.Data[0] != byte(i) {
			t.Fatalf("bad frame at %d: %v", i, f.Data)
		}
	}
	f, ok := sink.NextFrame(time.Second)
	if !ok || !f.Terminal {
		t.Fatalf("expected terminal, got %+v ok=%v", f, ok)
	}
}

func TestCreditBoundsProducer(t *testing.T) {
	src, sink := Loopback(2)
	_ = src.Push(NewStreamFrame([]byte{0}), 0)
	_ = src.Push(NewStreamFrame([]byte{1}), 0)
	err := src.Push(NewStreamFrame([]byte{2}), 50*time.Millisecond)
	if !errors.Is(err, ErrStreamTimeout) {
		t.Fatalf("expected Timeout, got %v", err)
	}
	_, ok := sink.NextFrame(time.Second)
	if !ok {
		t.Fatalf("failed to drain")
	}
	if err := src.Push(NewStreamFrame([]byte{2}), 100*time.Millisecond); err != nil {
		t.Fatalf("third push after drain should succeed: %v", err)
	}
}

func TestCancelProducesTerminal(t *testing.T) {
	src, sink := Loopback(4)
	_ = src.Push(NewStreamFrame([]byte{0}), 0)
	sink.Cancel("consumer cancelled")
	sawTerm := false
	for i := 0; i < 5; i++ {
		f, ok := sink.NextFrame(200 * time.Millisecond)
		if !ok {
			continue
		}
		if f.Terminal {
			if f.Meta["reason"] != "consumer cancelled" {
				t.Fatalf("reason mismatch: %v", f.Meta)
			}
			sawTerm = true
			break
		}
	}
	if !sawTerm {
		t.Fatalf("terminal frame never arrived after cancel")
	}
}

func TestTerminalSeenOnce(t *testing.T) {
	src, sink := Loopback(4)
	src.Close("eof")
	f, ok := sink.NextFrame(time.Second)
	if !ok || !f.Terminal {
		t.Fatalf("expected terminal")
	}
	_, ok = sink.NextFrame(100 * time.Millisecond)
	if ok {
		t.Fatalf("second call after terminal must return !ok")
	}
}
