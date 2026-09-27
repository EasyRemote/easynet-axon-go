//go:build unix

package industrial

import (
	inv "axon.run/sdk/go/axon/invocation"
	"context"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorConcurrentProcessQuota(t *testing.T) {
	sup := inv.NewSupervisor("concurrent_quota", inv.SupervisorSpec{ResourceLimit: inv.ResourceLimit{SubprocessCount: 1}, CancelGraceSeconds: 0.01})
	defer sup.RunCancelFlow(context.Background())
	var wg sync.WaitGroup
	var accepted atomic.Int32
	var unexpected atomic.Int32
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := sup.SpawnProcessGroup([]string{"/bin/sh", "-c", "sleep 10"})
			if err == nil {
				accepted.Add(1)
			} else if err.Kind != inv.KindResourceExhausted {
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if accepted.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("accepted=%d unexpected=%d; quota=1", accepted.Load(), unexpected.Load())
	}
}

func TestSupervisorClosingRejectsSpawnAndDrainsCleanup(t *testing.T) {
	sup := inv.NewSupervisor("closing_admission", inv.SupervisorSpec{CancelGraceSeconds: 0.01})
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	sup.OnCancel(func() { close(entered); <-release })
	go func() { sup.RunCancelFlow(context.Background()); close(finished) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel callback not entered")
	}
	var cleaned atomic.Int32
	sup.RegisterCleanup(func() { cleaned.Add(1) }, "registered_during_closing")
	child, err := sup.SpawnProcessGroup([]string{"/bin/sh", "-c", "sleep 10"})
	close(release)
	// Clean a counterexample child even when the old terminal snapshot missed it.
	if child != nil {
		_ = syscall.Kill(-child.Pgid, syscall.SIGKILL)
		_ = child.Cmd.Wait()
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal flow did not finish")
	}
	if err == nil || err.Kind != inv.KindInvalidArgument || err.Reason != "supervisor_cleanup_started" {
		t.Errorf("closing spawn must reject: %v", err)
	}
	if cleaned.Load() != 1 || !sup.CleanupComplete() {
		t.Errorf("cleanup=%d complete=%v", cleaned.Load(), sup.CleanupComplete())
	}
	child, err = sup.SpawnProcessGroup([]string{"/bin/sh", "-c", "sleep 10"})
	if child != nil {
		_ = syscall.Kill(-child.Pgid, syscall.SIGKILL)
		_ = child.Cmd.Wait()
	}
	if err == nil || err.Reason != "supervisor_cleanup_started" {
		t.Errorf("closed spawn must reject: %v", err)
	}
}
