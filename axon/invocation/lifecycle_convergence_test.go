package axon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestOutputBackpressureBlocksAtCanonicalCapacity(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	emitted := make(chan struct{})
	mustRegisterTestAbility(
		t,
		runtime,
		testAbilityURA("bounded_output"),
		func(ctx context.Context, ability *AbilityContext) ([]byte, *AxonError) {
			for index := 0; index <= canonicalStreamCapacity; index++ {
				if err := ability.EmitProgress([]byte{byte(index)}, ""); err != nil {
					return nil, err
				}
			}
			close(emitted)
			<-ctx.Done()
			return nil, nil
		},
	)
	handle := mustTestInvoke(t, runtime, key, "bounded_output", nil, "", nil)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if handle.LifecycleSnapshot().OutputBuffered == canonicalStreamCapacity {
			break
		}
		time.Sleep(time.Millisecond)
	}
	snapshot := handle.LifecycleSnapshot()
	if snapshot.OutputBuffered != canonicalStreamCapacity {
		t.Fatalf("output buffered = %d", snapshot.OutputBuffered)
	}
	select {
	case <-emitted:
		t.Fatal("producer crossed the output capacity without acknowledgement")
	default:
	}
	stream := handle.Events(0)
	progress := 0
	for progress <= canonicalStreamCapacity {
		event := stream.Next(context.Background())
		if event == nil {
			t.Fatal("event stream closed before bounded output drained")
		}
		if event.EventType == "progress" {
			progress++
		}
	}
	select {
	case <-emitted:
	case <-time.After(time.Second):
		t.Fatal("producer did not resume after output acknowledgement")
	}
	if err := handle.Cancel("test_complete"); err != nil {
		t.Fatal(err)
	}
}

func TestParentDeadlineWaitsForChildTerminalEvidence(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		key := testSigningKey(t)
		runtime := mustTestRuntime(t, key)
		mustRegisterTestAbility(
			t,
			runtime,
			testAbilityURA("deadline_parent"),
			func(ctx context.Context, _ *AbilityContext) ([]byte, *AxonError) {
				<-ctx.Done()
				return nil, nil
			},
		)
		mustRegisterTestAbility(
			t,
			runtime,
			testAbilityURA("deadline_child"),
			func(ctx context.Context, _ *AbilityContext) ([]byte, *AxonError) {
				<-ctx.Done()
				return nil, nil
			},
		)
		parent := mustTestInvoke(
			t,
			runtime,
			key,
			"deadline_parent",
			nil,
			"",
			&SupervisorSpec{
				ResourceLimit: ResourceLimit{WallSeconds: 0.025},
			},
		)
		child := mustTestInvoke(
			t,
			runtime,
			key,
			"deadline_child",
			nil,
			parent.InvocationID(),
			&SupervisorSpec{
				ResourceLimit: ResourceLimit{WallSeconds: 1},
			},
		)
		if state := parent.Wait(context.Background()); state != StateTimedOut {
			t.Fatalf("iteration %d parent state = %s", iteration, state)
		}
		if state := child.Wait(context.Background()); state != StateTimedOut {
			t.Fatalf("iteration %d child state = %s", iteration, state)
		}
		childTerminal := 0
		for _, event := range runtime.CoreOf(parent.InvocationID()).SnapshotEvents() {
			if event.EventType == "child_terminal" &&
				event.ChildInvocationID == child.InvocationID() {
				childTerminal++
			}
		}
		if childTerminal != 1 {
			t.Fatalf(
				"iteration %d child_terminal events = %d",
				iteration,
				childTerminal,
			)
		}
	}
}

func TestNaturalCompletionClosesCleanupBarrierBeforeReceipt(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	var cleanup atomic.Bool
	mustRegisterTestAbility(
		t,
		runtime,
		testAbilityURA("completion_cleanup"),
		func(_ context.Context, ability *AbilityContext) ([]byte, *AxonError) {
			ability.Supervisor.RegisterCleanup(func() {
				cleanup.Store(true)
			}, "completion_cleanup")
			return []byte("complete"), nil
		},
	)
	handle := mustTestInvoke(t, runtime, key, "completion_cleanup", nil, "", nil)
	if state := handle.Wait(context.Background()); state != StateCompleted {
		t.Fatalf("state = %s", state)
	}
	receipts := handle.SnapshotReceipts()
	terminal := receipts[len(receipts)-1]
	if !cleanup.Load() || !terminal.CleanupComplete() {
		t.Fatal("completion receipt crossed the cleanup barrier")
	}
}

func TestPanickedAbilityFailsThroughCleanupBarrier(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	var cleanup atomic.Bool
	mustRegisterTestAbility(
		t,
		runtime,
		testAbilityURA("panicked_ability"),
		func(_ context.Context, ability *AbilityContext) ([]byte, *AxonError) {
			ability.Supervisor.RegisterCleanup(func() {
				cleanup.Store(true)
			}, "panic_cleanup")
			panic("provider fault")
		},
	)
	handle := mustTestInvoke(t, runtime, key, "panicked_ability", nil, "", nil)
	if state := handle.Wait(context.Background()); state != StateFailed {
		t.Fatalf("state = %s", state)
	}
	receipts := handle.SnapshotReceipts()
	terminal := receipts[len(receipts)-1]
	if !cleanup.Load() ||
		!terminal.CleanupComplete() ||
		terminal.Reason() != "ability_handler_panicked" {
		t.Fatal("panicked ability bypassed canonical failed cleanup")
	}
}

func TestChildDispatchRollbackClosesRegisteredChild(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	releaseParent := make(chan struct{})
	mustRegisterTestAbility(
		t,
		runtime,
		testAbilityURA("dispatch_parent"),
		func(context.Context, *AbilityContext) ([]byte, *AxonError) {
			<-releaseParent
			return []byte("complete"), nil
		},
	)
	mustRegisterTestAbility(
		t,
		runtime,
		testAbilityURA("dispatch_child"),
		func(ctx context.Context, _ *AbilityContext) ([]byte, *AxonError) {
			<-ctx.Done()
			return nil, nil
		},
	)
	parent := mustTestInvoke(t, runtime, key, "dispatch_parent", nil, "", nil)
	runtime.beforeChildSpawnCommit = func() {
		close(releaseParent)
		if state := parent.Wait(context.Background()); state != StateCompleted {
			t.Fatalf("parent state = %s", state)
		}
	}
	_, _, invokeErr := runtime.InvokeDescriptorBoundRequest(
		context.Background(),
		mustTestRequest(
			t,
			key,
			"dispatch_child",
			nil,
			parent.InvocationID(),
			nil,
		),
	)
	if invokeErr == nil {
		t.Fatal("child dispatch crossed a closed parent")
	}
	children := runtime.ChildrenOf(parent.InvocationID())
	if len(children) != 1 {
		t.Fatalf("registered children = %v", children)
	}
	child := runtime.CoreOf(children[0])
	if child == nil || child.WaitTerminal(context.Background()) != StateFailed {
		t.Fatal("registered child was not terminally rolled back")
	}
	runtime.mu.Lock()
	active := runtime.activeCount
	runtime.mu.Unlock()
	if active != 0 {
		t.Fatalf("active invocation leases = %d", active)
	}
}

func TestPersistedInvocationResumesSameIdentityExactlyOnce(t *testing.T) {
	key := testSigningKey(t)
	persistence := NewPersistentLog(t.TempDir())
	checkpointed := make(chan struct{})
	resumeStarted := make(chan struct{})
	releaseResume := make(chan struct{})
	var cleanup atomic.Bool
	continuation, err := NewRecoveryContinuation(
		func(
			_ context.Context,
			ability *AbilityContext,
			checkpoint []byte,
		) ([]byte, *AxonError) {
			if string(checkpoint) != "checkpoint-v1" {
				return nil, ErrInternal("checkpoint_mismatch")
			}
			ability.Supervisor.RegisterCleanup(func() {
				cleanup.Store(true)
			}, "recovery_cleanup")
			close(resumeStarted)
			<-releaseResume
			return []byte("recovered"), nil
		},
		[]byte("initial"),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := func(
		ctx context.Context,
		ability *AbilityContext,
	) ([]byte, *AxonError) {
		if err := ability.CheckpointRecovery([]byte("checkpoint-v1")); err != nil {
			return nil, err
		}
		select {
		case <-checkpointed:
		default:
			close(checkpointed)
		}
		<-ctx.Done()
		return nil, nil
	}
	bind := func(runtime *LocalRuntime) {
		binding := mustProviderBinding(
			t,
			testDescriptorRef("recoverable"),
			Sha256([]byte("recovery-schema")),
			Sha256([]byte("recovery-implementation")),
			"canonical-go-recovery-test-v1",
			handler,
		)
		binding, err = binding.WithRecovery(continuation)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.BindProvider(binding); err != nil {
			t.Fatal(err)
		}
	}

	original := mustTestRuntimeWithOptions(
		t,
		key,
		LocalRuntimeOptions{Persistence: persistence},
	)
	bind(original)
	handle := mustTestInvoke(
		t,
		original,
		key,
		"recoverable",
		nil,
		"",
		&SupervisorSpec{PersistForRecovery: true},
	)
	select {
	case <-checkpointed:
	case <-time.After(time.Second):
		t.Fatal("checkpoint was not persisted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	suspended, suspendErr := original.SuspendForRestart(ctx)
	if suspendErr != nil {
		t.Fatal(suspendErr)
	}
	if len(suspended) != 1 || suspended[0] != handle.InvocationID() {
		t.Fatalf("suspended = %v", suspended)
	}

	replacement := mustTestRuntimeWithOptions(
		t,
		key,
		LocalRuntimeOptions{Persistence: persistence},
	)
	bind(replacement)
	recovered, recoverErr := replacement.RecoverPersistedInvocations(
		context.Background(),
		60*time.Millisecond,
	)
	if recoverErr != nil {
		t.Fatal(recoverErr)
	}
	if len(recovered) != 1 ||
		recovered[0].InvocationID() != handle.InvocationID() {
		t.Fatalf("recovered invocation = %v", recovered)
	}
	select {
	case <-resumeStarted:
	case <-time.After(time.Second):
		t.Fatal("recovery continuation did not start")
	}
	time.Sleep(150 * time.Millisecond)
	contender := mustTestRuntimeWithOptions(
		t,
		key,
		LocalRuntimeOptions{Persistence: persistence},
	)
	bind(contender)
	duplicate, recoverErr := contender.RecoverPersistedInvocations(
		context.Background(),
		60*time.Millisecond,
	)
	if recoverErr != nil {
		t.Fatal(recoverErr)
	}
	if len(duplicate) != 0 {
		t.Fatalf("live recovery lease admitted %d duplicate continuations", len(duplicate))
	}
	close(releaseResume)
	if state := recovered[0].Wait(context.Background()); state != StateCompleted {
		t.Fatalf("recovered state = %s", state)
	}
	if !cleanup.Load() {
		t.Fatal("recovered completion skipped cleanup")
	}
	replayed, recoverErr := replacement.RecoverPersistedInvocations(
		context.Background(),
		time.Second,
	)
	if recoverErr != nil {
		t.Fatal(recoverErr)
	}
	if len(replayed) != 0 {
		t.Fatalf("closed recovery replayed: %d", len(replayed))
	}
}

func TestExpiredRecoveryLeaseIsReclaimableAndFenced(t *testing.T) {
	persistence := NewPersistentLog(t.TempDir())
	const invocationID = "inv_recovery_lease"
	if err := persistence.InitializeRecovery(RecoverySnapshot{
		InvocationID: invocationID,
		Phase:        "suspended",
	}); err != nil {
		t.Fatal(err)
	}
	first, err := persistence.ClaimRecovery(
		"runtime-one",
		20*time.Millisecond,
		1,
	)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	time.Sleep(30 * time.Millisecond)
	second, err := persistence.ClaimRecovery(
		"runtime-two",
		time.Second,
		1,
	)
	if err != nil || len(second) != 1 {
		t.Fatalf("expired lease was not reclaimed: %v, %v", second, err)
	}
	event := PersistedLifecycleEvent{
		Sequence:        0,
		EventType:       "accepted",
		State:           StateAccepted,
		TimestampUnixMs: time.Now().UnixMilli(),
	}
	if err := persistence.AppendRecoveredLifecycleEvent(
		invocationID,
		"runtime-one",
		event,
	); err == nil {
		t.Fatal("stale recovery owner appended after fencing")
	}
	if err := persistence.AppendRecoveredLifecycleEvent(
		invocationID,
		"runtime-two",
		event,
	); err != nil {
		t.Fatal(err)
	}
}
