package axon

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentCancelSharesLatchedIntentAndResult(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	registered := make(chan struct{})
	var cleanup atomic.Bool
	mustRegisterTestAbility(t, runtime, testAbilityURA("waits_for_cancel"), func(ctx context.Context, ability *AbilityContext) ([]byte, *AxonError) {
		ability.Supervisor.RegisterCleanup(func() {
			cleanup.Store(true)
		}, "concurrent_cancel_cleanup")
		close(registered)
		<-ability.CancelCh()
		return nil, nil
	})
	handle := mustTestInvoke(t, runtime, key, "waits_for_cancel", nil, "", nil)
	<-registered

	const callers = 16
	start := make(chan struct{})
	results := make(chan *AxonError, callers)
	reasons := make(map[string]struct{}, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := 0; index < callers; index++ {
		reason := fmt.Sprintf("reason-%d", index)
		reasons[reason] = struct{}{}
		go func() {
			defer wait.Done()
			<-start
			results <- handle.Cancel(reason)
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for result := range results {
		if result != nil {
			t.Fatalf("all callers must observe success, got %v", result)
		}
	}
	if state := handle.Wait(context.Background()); state != StateCancelled {
		t.Fatalf("terminal state = %v, want cancelled", state)
	}
	receipts := handle.SnapshotReceipts()
	terminal := receipts[len(receipts)-1]
	if terminal.State() != string(StateCancelled) {
		t.Fatalf("terminal receipt state = %v", terminal.State())
	}
	if !cleanup.Load() || !terminal.CleanupComplete() {
		t.Fatal("cancel acknowledgement preceded registered cleanup")
	}
	if _, ok := reasons[terminal.Reason()]; !ok {
		t.Fatalf("terminal reason = %q, want one latched caller reason", terminal.Reason())
	}
	cancelledCount := 0
	for _, receipt := range receipts {
		if receipt.State() == string(StateCancelled) {
			cancelledCount++
		}
	}
	if cancelledCount != 1 {
		t.Fatalf("cancelled receipts = %d, want 1", cancelledCount)
	}
}

func TestConcurrentCancelSharesWinnerChildError(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	mustRegisterTestAbility(t, runtime, testAbilityURA("waits_for_cancel"), func(ctx context.Context, ability *AbilityContext) ([]byte, *AxonError) {
		<-ability.CancelCh()
		return nil, nil
	})
	handle := mustTestInvoke(t, runtime, key, "waits_for_cancel", nil, "", nil)
	runtime.mu.Lock()
	runtime.children[handle.InvocationID()] = make(map[string]struct{})
	runtime.children[handle.InvocationID()]["missing-child"] = struct{}{}
	runtime.mu.Unlock()

	const callers = 16
	start := make(chan struct{})
	results := make(chan *AxonError, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := 0; index < callers; index++ {
		go func(reason string) {
			defer wait.Done()
			<-start
			results <- handle.Cancel(reason)
		}(fmt.Sprintf("reason-%d", index))
	}
	close(start)
	wait.Wait()
	close(results)
	var shared *AxonError
	for result := range results {
		if result == nil {
			t.Fatal("all callers must observe the child cancellation error")
		}
		if shared == nil {
			shared = result
		} else if result != shared {
			t.Fatalf("callers observed distinct error objects: %p != %p", result, shared)
		}
	}
}

func TestHandleControlRejectsABAInvocationIDReuse(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	runtime.newInvocationID = func() string { return "inv_reused" }
	mustRegisterTestAbility(t, runtime, testAbilityURA("waits_for_cancel"), func(ctx context.Context, ability *AbilityContext) ([]byte, *AxonError) {
		<-ability.CancelCh()
		return nil, nil
	})
	first := mustTestInvoke(t, runtime, key, "waits_for_cancel", nil, "", nil)
	_, _, secondErr := runtime.InvokeDescriptorBoundRequest(
		context.Background(),
		mustTestRequest(t, key, "waits_for_cancel", nil, "", nil),
	)
	if secondErr == nil || secondErr.Reason != "receipt_context_already_bound" {
		t.Fatalf("duplicate invocation id must fail before control reuse: %v", secondErr)
	}
	if err := first.Cancel("current"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentHandleCancelSharesWinnerPanic(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	runtime.beforeCancelWinner = func() { panic("injected winner panic") }
	mustRegisterTestAbility(t, runtime, testAbilityURA("waits_for_cancel"), func(ctx context.Context, ability *AbilityContext) ([]byte, *AxonError) {
		<-ability.CancelCh()
		return nil, nil
	})
	handle := mustTestInvoke(t, runtime, key, "waits_for_cancel", nil, "", nil)

	const callers = 16
	results := make(chan *AxonError, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := 0; index < callers; index++ {
		go func() { defer wait.Done(); results <- handle.Cancel("panic") }()
	}
	wait.Wait()
	close(results)
	var shared *AxonError
	for result := range results {
		if result == nil || result.Reason != "cancel_winner_panicked" {
			t.Fatalf("result = %v", result)
		}
		if shared == nil {
			shared = result
		} else if shared != result {
			t.Fatal("callers observed different panic results")
		}
	}
}
