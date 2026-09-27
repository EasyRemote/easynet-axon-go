package axon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDurableFinalizationReportsCleanupFailure(t *testing.T) {
	for _, mode := range []string{"complete", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			key := testSigningKey(t)
			directory := t.TempDir()
			persistence := NewPersistentLog(directory)
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
			replacement := mustTestRuntimeWithOptions(t, key, LocalRuntimeOptions{Persistence: NewPersistentLog(directory)})
			_, recoveryErr := replacement.RecoverPersistedInvocations(ctx, time.Second)
			if recoveryErr == nil || recoveryErr.Reason != "recovery_finalization_required" {
				t.Fatalf("recovery hid finalization: %v", recoveryErr)
			}
			if after.Phase != "finalizing" {
				t.Fatalf("phase=%s", after.Phase)
			}
			if err := persistence.MarkRecoveryPhase(handle.InvocationID(), "suspended"); err == nil {
				t.Fatal("finalization ownership downgraded to resumable work")
			}
			runtime.mu.Lock()
			defer runtime.mu.Unlock()
			if !runtime.active[handle.InvocationID()] || runtime.activeCount != 1 || runtime.executions[handle.InvocationID()].suspended {
				t.Fatal("failed cleanup ownership released")
			}
		})
	}
}

func TestFinalizingWriteFailureRetainsCapacity(t *testing.T) {
	key := testSigningKey(t)
	directory := t.TempDir()
	persistence := NewPersistentLog(directory)
	runtime := mustTestRuntimeWithOptions(t, key, LocalRuntimeOptions{Persistence: persistence})
	ready, release := make(chan struct{}), make(chan struct{})
	var cleanups atomic.Int32
	continuation, err := NewRecoveryContinuation(func(context.Context, *AbilityContext, []byte) ([]byte, *AxonError) { return nil, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := mustProviderBinding(t, testDescriptorRef("write_failure"), Sha256([]byte("schema")), Sha256([]byte("impl")), "test", func(_ context.Context, ability *AbilityContext) ([]byte, *AxonError) {
		ability.Supervisor.RegisterCleanup(func() { cleanups.Add(1) }, "count")
		close(ready)
		<-release
		return nil, nil
	})
	binding, err = binding.WithRecovery(continuation)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.BindProvider(binding); err != nil {
		t.Fatal(err)
	}
	handle := mustTestInvoke(t, runtime, key, "write_failure", nil, "", &SupervisorSpec{PersistForRecovery: true})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := os.Mkdir(filepath.Join(directory, handle.InvocationID()+".recovery.json.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	close(release)
	_, failure := handle.WaitResult(ctx)
	typed, ok := failure.(*AxonError)
	if !ok || !strings.HasPrefix(typed.Reason, "recovery_finalization_persist_failed:") {
		t.Fatalf("failure=%v", failure)
	}
	if cleanups.Load() != 0 {
		t.Fatal("cleanup ran after failed write")
	}
	snapshot, readErr := persistence.ReadRecovery(handle.InvocationID())
	if readErr != nil || snapshot.Phase != "running" {
		t.Fatalf("snapshot=%v error=%v", snapshot, readErr)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.activeCount != 1 || !runtime.active[handle.InvocationID()] {
		t.Fatal("capacity released")
	}
}
