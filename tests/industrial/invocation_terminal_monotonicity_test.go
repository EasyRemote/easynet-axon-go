// I-13 invocation_terminal_monotonicity
package industrial

import (
	"context"
	"testing"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_invocation_terminal_monotonicity(t *testing.T) {
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "once", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		return []byte("done"), nil
	})
	h, _ := invokeIndustrial(t, context.Background(), rt, key, "once", nil, "", nil)
	state := h.Wait(context.Background())
	if state != inv.StateCompleted {
		t.Fatalf("state=%s", state)
	}
	if !h.IsTerminal() {
		t.Fatal("not terminal")
	}

	if err := h.Cancel("post_terminal"); err != nil {
		t.Fatal(err)
	}
	if h.CurrentState() != inv.StateCompleted {
		t.Fatal("state mutated")
	}

	_, err := h.Send([]byte("ignored"), "")
	if err == nil || err.Kind != inv.KindInvalidArgument || err.Reason != "invocation_terminal" {
		t.Fatalf("expected invalid_argument/invocation_terminal, got %v", err)
	}
}
