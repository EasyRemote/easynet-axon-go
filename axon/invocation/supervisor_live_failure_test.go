package axon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestLiveCleanupCallbackFailureCannotCertifyComplete(t *testing.T) {
	s := NewSupervisor("callback_failure", SupervisorSpec{})
	calls := 0
	s.RegisterCleanup(func() { panic("owned callback failure") }, "failure")
	s.RegisterCleanup(func() { calls++ }, "independent")
	s.RunCompletionFlow(context.Background())
	if s.CleanupComplete() {
		t.Fatal("failed callback certified cleanup")
	}
	if calls != 1 {
		t.Fatal("independent callback was not attempted")
	}
}

func TestLiveCleanupNativeFailureRetainsSharedOutcome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX owned process fixture")
	}
	for _, unconfirmed := range []bool{false, true} {
		t.Run(fmt.Sprint(unconfirmed), func(t *testing.T) {
			t.Setenv("AXON_SUPERVISOR_STATE_DIR", t.TempDir())
			s := NewSupervisor("owned", SupervisorSpec{PersistForRecovery: true, CancelGraceSeconds: 0.001})
			_, err := s.SpawnProcessGroup([]string{"/bin/sh", "-c", "exec sleep 0.15"})
			if err != nil {
				t.Fatal(err)
			}
			s.signalGroup = func(pgid int, sig syscall.Signal) (bool, error) {
				if unconfirmed {
					return false, nil
				}
				if sig == 0 {
					return nativeGroupSignal(pgid, sig)
				}
				return false, syscall.EPERM
			}
			called := false
			s.RegisterCleanup(func() { called = true }, "after_exit")
			done := make(chan struct{})
			go func() { s.RunCancelFlow(context.Background()); close(done) }()
			s.RunCompletionFlow(context.Background())
			<-done
			first := s.CleanupError()
			if first == nil || s.CleanupComplete() {
				t.Fatal("failed cleanup was certified")
			}
			s.RunCompletionFlow(context.Background())
			if first != s.CleanupError() {
				t.Fatal("joiners did not share result")
			}
			if _, err := os.Stat(statePath(s.InvocationID)); err != nil {
				t.Fatal(err)
			}
			if called == unconfirmed {
				t.Fatal("callbacks must wait for confirmed exit")
			}
		})
	}
}

func TestLiveCleanupRemovalFailureIsVisible(t *testing.T) {
	t.Setenv("AXON_SUPERVISOR_STATE_DIR", t.TempDir())
	s := NewSupervisor("remove", SupervisorSpec{PersistForRecovery: true})
	p := statePath(s.InvocationID)
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "owned"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s.RunCompletionFlow(context.Background())
	if s.CleanupError() == nil || s.CleanupComplete() {
		t.Fatal("failed removal certified cleanup")
	}
}

func TestRuntimeCleanupFailureWakesWaitersWithoutReceipt(t *testing.T) {
	for _, mode := range []string{"complete", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			key := testSigningKey(t)
			rt := mustTestRuntime(t, key)
			ready, release := make(chan struct{}), make(chan struct{})
			mustRegisterTestAbility(t, rt, testAbilityURA("cleanup_failure"), func(ctx context.Context, a *AbilityContext) ([]byte, *AxonError) {
				a.Supervisor.RegisterCleanup(func() { panic("owned cleanup failure") }, "failure")
				close(ready)
				<-release
				if mode != "complete" {
					<-ctx.Done()
				}
				return nil, nil
			})
			spec := &SupervisorSpec{CancelGraceSeconds: 0.001}
			if mode == "deadline" {
				spec.ResourceLimit.WallSeconds = 0.3
			}
			h := mustTestInvoke(t, rt, key, "cleanup_failure", nil, "", spec)
			<-ready
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream := h.Events(uint64(len(h.core.SnapshotEvents())))
			drained := make(chan *InvocationEvent, 1)
			go func() { drained <- stream.Next(ctx) }()
			close(release)
			if mode == "cancel" {
				if err := h.Cancel("owned cancellation"); err == nil {
					t.Fatal("cancel lost cleanup error")
				}
			}
			state, err := h.WaitResult(ctx)
			if err == nil || state.IsTerminal() || h.FinalizationError() == nil {
				t.Fatalf("state=%v err=%v", state, err)
			}
			select {
			case event := <-drained:
				if event != nil || stream.Err() == nil {
					t.Fatal("reader lost failure")
				}
			case <-ctx.Done():
				t.Fatal("reader stranded")
			}
			for _, receipt := range h.core.SnapshotReceipts() {
				if receipt.CleanupComplete() {
					t.Fatal("false cleanup receipt")
				}
			}
		})
	}
}

func TestParentCleanupFailureRetainsChildError(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			key := testSigningKey(t)
			rt := mustTestRuntime(t, key)
			ready := make(chan struct{})
			mustRegisterTestAbility(t, rt, testAbilityURA("cleanup_parent"), func(ctx context.Context, _ *AbilityContext) ([]byte, *AxonError) { <-ctx.Done(); return nil, nil })
			mustRegisterTestAbility(t, rt, testAbilityURA("cleanup_child"), func(ctx context.Context, a *AbilityContext) ([]byte, *AxonError) {
				a.Supervisor.RegisterCleanup(func() { panic("owned child cleanup failure") }, "failure")
				close(ready)
				<-ctx.Done()
				return nil, nil
			})
			spec := &SupervisorSpec{CancelGraceSeconds: 0.001}
			if deadline {
				spec.ResourceLimit.WallSeconds = 0.5
			}
			parent := mustTestInvoke(t, rt, key, "cleanup_parent", nil, "", spec)
			child := mustTestInvoke(t, rt, key, "cleanup_child", nil, parent.InvocationID(), nil)
			<-ready
			if !deadline {
				if err := parent.Cancel("owned parent cancel"); err == nil {
					t.Fatal("parent discarded child error")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			for _, handle := range []*InvocationHandle{parent, child} {
				state, err := handle.WaitResult(ctx)
				if err == nil || state.IsTerminal() || handle.FinalizationError() == nil {
					t.Fatalf("state=%v err=%v", state, err)
				}
			}
		})
	}
}
