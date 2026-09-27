// I-02 supervisor_grace_period
//go:build unix

package industrial

import (
	"context"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_supervisor_grace_period(t *testing.T) {
	sup := inv.NewSupervisor("inv_g", inv.SupervisorSpec{CancelGraceSeconds: 1.0})
	_, e := sup.SpawnProcessGroup([]string{"sh", "-c", "trap 'exit 0' TERM; sleep 30"})
	if e != nil {
		t.Fatal(e)
	}
	t0 := time.Now()
	sup.RunCancelFlow(context.Background())
	if elapsed := time.Since(t0); elapsed > 1200*time.Millisecond {
		t.Fatalf("cancel took %s (>grace)", elapsed)
	}
}
