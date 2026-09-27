// I-04 supervisor_resource_limit
//go:build unix

package industrial

import (
	"context"
	"testing"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_supervisor_resource_limit(t *testing.T) {
	sup := inv.NewSupervisor("inv_rl", inv.SupervisorSpec{
		ResourceLimit:      inv.ResourceLimit{SubprocessCount: 2},
		CancelGraceSeconds: 0.2,
	})
	_, e1 := sup.SpawnProcessGroup([]string{"sh", "-c", "sleep 0.5"})
	_, e2 := sup.SpawnProcessGroup([]string{"sh", "-c", "sleep 0.5"})
	_, e3 := sup.SpawnProcessGroup([]string{"sh", "-c", "sleep 0.5"})
	if e1 != nil || e2 != nil {
		t.Fatal("first two must succeed")
	}
	if e3 == nil || e3.Kind != inv.KindResourceExhausted {
		t.Fatalf("third must be rex, got %v", e3)
	}
	sup.RunCancelFlow(context.Background())
}
