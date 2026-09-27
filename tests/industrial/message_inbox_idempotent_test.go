// I-11 message_inbox_idempotent
package industrial

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_message_inbox_idempotent(t *testing.T) {
	var count atomic.Int64
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "counter", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		for {
			m := ac.RecvMessage(ctx, 500*time.Millisecond)
			if m == nil {
				return []byte("done"), nil
			}
			count.Add(1)
		}
	})
	h, _ := invokeIndustrial(t, context.Background(), rt, key, "counter", nil, "", nil)
	a1, _ := rt.SendMessage(context.Background(), h.InvocationID(), []byte("x"), "dup-42")
	a2, _ := rt.SendMessage(context.Background(), h.InvocationID(), []byte("x"), "dup-42")
	if a1.Deduplicated {
		t.Fatal("first delivery must not be dedup")
	}
	if !a2.Deduplicated {
		t.Fatal("second delivery must be dedup")
	}
	if a1.AcceptedSequence != a2.AcceptedSequence {
		t.Fatal("seq mismatch")
	}
	h.Wait(context.Background())
	if count.Load() != 1 {
		t.Fatalf("expected 1 delivery, got %d", count.Load())
	}
}
