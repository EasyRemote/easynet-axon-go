// I-05 supervisor_cleanup_before_receipt
//go:build unix

package industrial

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_supervisor_cleanup_before_receipt(t *testing.T) {
	var cleanup atomic.Bool
	rt, key := newIndustrialRuntime(t)
	registerIndustrialAbility(rt, "guarded", func(ctx context.Context, ac *inv.AbilityContext) ([]byte, *inv.AxonError) {
		if _, e := ac.Supervisor.SpawnProcessGroup([]string{"sh", "-c", "sleep 30"}); e != nil {
			return nil, e
		}
		ac.Supervisor.RegisterCleanup(func() { cleanup.Store(true) }, "sentinel")
		<-ac.CancelCh()
		return []byte("ok"), nil
	})
	spec := inv.SupervisorSpec{CancelGraceSeconds: 0.2}
	h, _ := invokeIndustrial(t, context.Background(), rt, key, "guarded", nil, "", &spec)
	time.Sleep(100 * time.Millisecond)
	if e := rt.Cancel(context.Background(), h.InvocationID(), "probe"); e != nil {
		t.Fatal(e)
	}
	core := rt.CoreOf(h.InvocationID())
	if core.CurrentState() != inv.StateCancelled {
		t.Fatalf("state=%s", core.CurrentState())
	}
	receipts := core.SnapshotReceipts()
	terminal := receipts[len(receipts)-1]
	if !terminal.CleanupComplete() {
		t.Fatal("cleanup_complete=false on terminal receipt")
	}
	if !cleanup.Load() {
		t.Fatal("cleanup callback did not run")
	}
}
