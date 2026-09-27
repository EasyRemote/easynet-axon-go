// I-08 event_stream_resumption
package industrial

import (
	"context"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_event_stream_resumption(t *testing.T) {
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "ticks", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		for i := 0; i < 100; i++ {
			_ = ac.EmitProgress([]byte{byte(i & 0xff)}, "")
			time.Sleep(200 * time.Microsecond)
		}
		return []byte("done"), nil
	})
	h, _ := invokeIndustrial(t, context.Background(), rt, key, "ticks", nil, "", nil)

	firstHalf := []*inv.InvocationEvent{}
	s1 := h.Events(0)
	for {
		ev := s1.Next(context.Background())
		if ev == nil {
			break
		}
		firstHalf = append(firstHalf, ev)
		pc := 0
		for _, e := range firstHalf {
			if e.EventType == "progress" {
				pc++
			}
		}
		if pc >= 50 {
			break
		}
	}
	s1.Close()

	lastSeq := firstHalf[len(firstHalf)-1].Sequence
	s2 := h.Events(lastSeq + 1)
	secondHalf := []*inv.InvocationEvent{}
	for {
		ev := s2.Next(context.Background())
		if ev == nil {
			break
		}
		secondHalf = append(secondHalf, ev)
	}
	h.Wait(context.Background())

	seen := map[uint64]bool{}
	var prev int64 = -1
	for _, ev := range append(firstHalf, secondHalf...) {
		if int64(ev.Sequence) <= prev {
			t.Fatalf("non-monotonic: %d <= %d", ev.Sequence, prev)
		}
		prev = int64(ev.Sequence)
		if seen[ev.Sequence] {
			t.Fatalf("duplicate seq %d", ev.Sequence)
		}
		seen[ev.Sequence] = true
	}
	pc := 0
	for seq := range seen {
		for _, ev := range firstHalf {
			if ev.Sequence == seq && ev.EventType == "progress" {
				pc++
				break
			}
		}
		for _, ev := range secondHalf {
			if ev.Sequence == seq && ev.EventType == "progress" {
				pc++
				break
			}
		}
	}
	if pc != 100 {
		t.Fatalf("progress count=%d", pc)
	}
}
