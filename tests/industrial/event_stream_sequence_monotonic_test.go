// I-09 event_stream_sequence_monotonic
package industrial

import (
	"context"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_event_stream_sequence_monotonic(t *testing.T) {
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "tricky", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		for i := 0; i < 20; i++ {
			_ = ac.EmitProgress([]byte{byte('a')}, "")
		}
		time.Sleep(10 * time.Millisecond)
		for i := 0; i < 20; i++ {
			_ = ac.EmitProgress([]byte{byte('b')}, "")
		}
		return []byte("ok"), nil
	})
	h, _ := invokeIndustrial(t, context.Background(), rt, key, "tricky", nil, "", nil)
	h.Wait(context.Background())
	events := rt.CoreOf(h.InvocationID()).SnapshotEvents()
	for i, ev := range events {
		if ev.Sequence != uint64(i) {
			t.Fatalf("seq[%d]=%d", i, ev.Sequence)
		}
	}
}
