// I-14 invocation_parent_child_cancel_propagation
package industrial

import (
	"context"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_invocation_parent_child_cancel_propagation(t *testing.T) {
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "child", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		<-ac.CancelCh()
		return []byte("child"), nil
	})
	registerIndustrialAbility(rt, "root", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		for i := 0; i < 5; i++ {
			if _, e := invokeIndustrialRuntime(ctx, ac.Runtime, key, "child", nil, ac.InvocationID, nil); e != nil {
				return nil, e
			}
		}
		<-ac.CancelCh()
		return []byte("root"), nil
	})
	h, _ := invokeIndustrial(t, context.Background(), rt, key, "root", nil, "", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(rt.ChildrenOf(h.InvocationID())) == 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(rt.ChildrenOf(h.InvocationID())) != 5 {
		t.Fatal("children did not spawn")
	}
	t0 := time.Now()
	if e := rt.Cancel(context.Background(), h.InvocationID(), "probe"); e != nil {
		t.Fatal(e)
	}
	elapsed := time.Since(t0)
	for _, id := range rt.ChildrenOf(h.InvocationID()) {
		c := rt.CoreOf(id)
		if !c.IsTerminal() {
			t.Fatalf("child %s not terminal", id)
		}
		if c.CurrentState() != inv.StateCancelled {
			t.Fatalf("child %s state=%s", id, c.CurrentState())
		}
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("propagation too slow: %s", elapsed)
	}
}
