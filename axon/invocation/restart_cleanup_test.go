package axon

import (
	"context"
	"testing"
	"time"
)

func TestRestartWaitsForCleanupAndRespectsContext(t *testing.T) {
	key := testSigningKey(t)
	persistence := NewPersistentLog(t.TempDir())
	runtime := mustTestRuntimeWithOptions(t, key, LocalRuntimeOptions{Persistence: persistence})
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	continuation, err := NewRecoveryContinuation(func(context.Context, *AbilityContext, []byte) ([]byte, *AxonError) { return nil, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := mustProviderBinding(t, testDescriptorRef("restart_wait"), Sha256([]byte("schema")), Sha256([]byte("impl")), "test", func(_ context.Context, a *AbilityContext) ([]byte, *AxonError) {
		a.Supervisor.RegisterCleanup(func() { close(entered); <-release }, "gate")
		return nil, nil
	})
	binding, err = binding.WithRecovery(continuation)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.BindProvider(binding); err != nil {
		t.Fatal(err)
	}
	h := mustTestInvoke(t, runtime, key, "restart_wait", nil, "", &SupervisorSpec{PersistForRecovery: true})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cleanup not started")
	}
	before, errRead := persistence.ReadRecovery(h.InvocationID())
	if errRead != nil {
		t.Fatal(errRead)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := runtime.SuspendForRestart(ctx); err == nil || err.Reason != "recovery_suspend_context_cancelled" {
		t.Fatalf("restart skipped active cleanup: %v", err)
	}
	after, errRead := persistence.ReadRecovery(h.InvocationID())
	if errRead != nil || before.Phase != after.Phase {
		t.Fatal("recovery changed during cleanup")
	}
	runtime.mu.Lock()
	active := runtime.active[h.InvocationID()] && runtime.activeCount == 1
	runtime.mu.Unlock()
	if !active {
		t.Fatal("cleanup reservation released")
	}
	close(release)
	done, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, err := h.WaitResult(done); err != nil {
		t.Fatal(err)
	}
	ids, suspendErr := runtime.SuspendForRestart(done)
	if suspendErr != nil || len(ids) != 0 {
		t.Fatalf("terminal work reported suspended: %v %v", ids, suspendErr)
	}
}

func TestRestartPreservesCleanupFailure(t *testing.T) {
	for _, mode := range []string{"complete", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			key := testSigningKey(t)
			persistence := NewPersistentLog(t.TempDir())
			runtime := mustTestRuntimeWithOptions(t, key, LocalRuntimeOptions{Persistence: persistence})
			ready := make(chan struct{})
			continuation, err := NewRecoveryContinuation(func(context.Context, *AbilityContext, []byte) ([]byte, *AxonError) { return nil, nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			binding := mustProviderBinding(t, testDescriptorRef("restart_cleanup"), Sha256([]byte("schema")), Sha256([]byte("impl")), "test", func(ctx context.Context, ability *AbilityContext) ([]byte, *AxonError) {
				ability.Supervisor.RegisterCleanup(func() { panic("owned cleanup failure") }, "failure")
				close(ready)
				if mode != "complete" {
					<-ctx.Done()
				}
				return nil, nil
			})
			binding, err = binding.WithRecovery(continuation)
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.BindProvider(binding); err != nil {
				t.Fatal(err)
			}
			spec := &SupervisorSpec{PersistForRecovery: true}
			if mode == "deadline" {
				spec.ResourceLimit.WallSeconds = 0.2
			}
			handle := mustTestInvoke(t, runtime, key, "restart_cleanup", nil, "", spec)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if mode == "cancel" {
				if err := handle.Cancel("owned cancel"); err == nil {
					t.Fatal("cancel lost cleanup error")
				}
			}
			_, failed := handle.WaitResult(ctx)
			if failed == nil {
				t.Fatal("cleanup failure lost")
			}
			before, readErr := persistence.ReadRecovery(handle.InvocationID())
			if readErr != nil {
				t.Fatal(readErr)
			}
			_, restart := runtime.SuspendForRestart(ctx)
			if restart == nil || restart.Reason != "supervisor_cleanup_failed" {
				t.Fatalf("restart lost failure: %v", restart)
			}
			after, readErr := persistence.ReadRecovery(handle.InvocationID())
			if readErr != nil || after.Phase != before.Phase {
				t.Fatal("recovery phase changed")
			}
			runtime.mu.Lock()
			defer runtime.mu.Unlock()
			if !runtime.active[handle.InvocationID()] || runtime.activeCount != 1 || runtime.executions[handle.InvocationID()].suspended {
				t.Fatal("failed cleanup ownership released")
			}
		})
	}
}
