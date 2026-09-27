// I-01 supervisor_process_group
//go:build unix

package industrial

import (
	"syscall"
	"testing"
	"time"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_supervisor_process_group(t *testing.T) {
	sup := inv.NewSupervisor("inv_test", inv.SupervisorSpec{CancelGraceSeconds: 0.5})
	h, e := sup.SpawnProcessGroup([]string{"sh", "-c", "sleep 30 & sleep 30 & wait"})
	if e != nil {
		t.Fatal(e)
	}
	if h.Pgid == syscall.Getpgrp() {
		t.Fatal("setsid failed")
	}
	_ = syscall.Kill(-h.Pgid, syscall.SIGTERM)
	// Use Wait — it reaps the zombie, unlike signal-0 which can return
	// EPERM on macOS after the process has exited.
	done := make(chan error, 1)
	go func() { done <- h.Cmd.Wait() }()
	select {
	case <-done:
		return
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-h.Pgid, syscall.SIGKILL)
		select {
		case <-done:
			return
		case <-time.After(1 * time.Second):
			t.Fatal("process group did not die")
		}
	}
}
