package axon

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// AbilityFn is one provider-bound ability implementation.
type AbilityFn func(ctx context.Context, ac *AbilityContext) ([]byte, *AxonError)

// AbilityContext is the runtime-owned execution context.
type AbilityContext struct {
	InvocationID       string
	ParentInvocationID string
	Payload            []byte
	Inbox              *MessageInbox
	Supervisor         *Supervisor
	Runtime            *LocalRuntime

	core      *InvocationCore
	execution context.Context
	cancelled *atomic.Bool
	cancelCh  chan struct{}
}

// EmitProgress emits one bounded output frame.
func (a *AbilityContext) EmitProgress(payload []byte, contentType string) *AxonError {
	_, err := a.core.emitContext(
		a.execution,
		"progress",
		StateRunning,
		payload,
		contentType,
		"",
		false,
		"",
		0,
		false,
	)
	return err
}

// RecvMessage receives one inbound frame and emits its consumption receipt.
func (a *AbilityContext) RecvMessage(
	ctx context.Context,
	timeout time.Duration,
) *InboundMessage {
	message := a.Inbox.Recv(ctx, timeout)
	if message == nil {
		return nil
	}
	_, _ = a.core.emit(
		"message_consumed",
		StateRunning,
		message.Payload,
		message.ContentType,
		"",
		false,
		"",
	)
	return message
}

// CheckpointRecovery durably replaces this invocation's continuation token.
func (a *AbilityContext) CheckpointRecovery(checkpoint []byte) *AxonError {
	return a.Runtime.checkpointRecovery(a.InvocationID, checkpoint)
}

// Cancelled reports whether a terminal stop signal was issued.
func (a *AbilityContext) Cancelled() bool {
	return a.cancelled.Load()
}

// CancelCh closes for cancellation, timeout, or runtime-owned terminal stop.
func (a *AbilityContext) CancelCh() <-chan struct{} {
	return a.cancelCh
}

// LocalRuntimeOptions configures generic runtime providers.
type LocalRuntimeOptions struct {
	Persistence *PersistentLog
}

type runtimeExecution struct {
	cancel    context.CancelFunc
	done      chan struct{}
	suspended bool
	persisted bool
	recovered bool
}

// LocalRuntime is the canonical in-process invocation provider.
type LocalRuntime struct {
	mu                     sync.Mutex
	abilities              map[string]ProviderBinding
	cores                  map[string]*InvocationCore
	supervisors            map[string]*Supervisor
	inboxes                map[string]*MessageInbox
	children               map[string]map[string]struct{}
	activeChildren         map[string]int
	cancelFlags            map[string]chan struct{}
	cancelSignalled        map[string]bool
	cancelledBs            map[string]*atomic.Bool
	executions             map[string]*runtimeExecution
	generations            map[string]uint64
	nextGeneration         uint64
	cancelIntents          map[invocationToken]*cancelIntent
	active                 map[string]bool
	activeCount            int
	newInvocationID        func() string
	beforeCancelWinner     func()
	beforeChildSpawnCommit func()

	axiomEnvelopes map[string]SignedEnvelope
	persistence    *PersistentLog
	runtimeID      string

	admissionMu       sync.Mutex
	admissionReplay   *nonceReplayStore
	admissionResolver KeyResolver
	receiptProvider   CanonicalReceiptProvider
}

// NewLocalRuntime constructs a runtime with mandatory authority providers.
func NewLocalRuntime(
	resolver KeyResolver,
	receiptProvider CanonicalReceiptProvider,
) (*LocalRuntime, *AxonError) {
	return NewLocalRuntimeWithOptions(
		resolver,
		receiptProvider,
		LocalRuntimeOptions{},
	)
}

// NewLocalRuntimeWithOptions constructs a runtime with generic persistence.
func NewLocalRuntimeWithOptions(
	resolver KeyResolver,
	receiptProvider CanonicalReceiptProvider,
	options LocalRuntimeOptions,
) (*LocalRuntime, *AxonError) {
	if resolver == nil {
		return nil, rejectSig("key_resolver_required")
	}
	if receiptProvider == nil {
		return nil, ErrPermissionDenied("canonical_receipt_provider_not_configured")
	}
	return &LocalRuntime{
		abilities:              make(map[string]ProviderBinding),
		cores:                  make(map[string]*InvocationCore),
		supervisors:            make(map[string]*Supervisor),
		inboxes:                make(map[string]*MessageInbox),
		children:               make(map[string]map[string]struct{}),
		activeChildren:         make(map[string]int),
		cancelFlags:            make(map[string]chan struct{}),
		cancelSignalled:        make(map[string]bool),
		cancelledBs:            make(map[string]*atomic.Bool),
		executions:             make(map[string]*runtimeExecution),
		generations:            make(map[string]uint64),
		nextGeneration:         1,
		cancelIntents:          make(map[invocationToken]*cancelIntent),
		active:                 make(map[string]bool),
		newInvocationID:        NewInvocationID,
		beforeCancelWinner:     func() {},
		beforeChildSpawnCommit: func() {},
		axiomEnvelopes:         make(map[string]SignedEnvelope),
		persistence:            options.Persistence,
		runtimeID:              NewInvocationID(),
		admissionReplay:        newNonceReplayStore(),
		admissionResolver:      resolver,
		receiptProvider:        receiptProvider,
	}, nil
}

// LifecycleLimits returns the single canonical provider capacity contract.
func (r *LocalRuntime) LifecycleLimits() RuntimeLifecycleLimits {
	return RuntimeLifecycleLimits{
		PendingInvocations:         canonicalPendingCapacity,
		PendingChildren:            canonicalPendingCapacity,
		OutputFrames:               canonicalStreamCapacity,
		InputFrames:                canonicalBidiCapacity,
		RecoveryInvocations:        canonicalPendingCapacity,
		TerminalPropagationBoundMs: canonicalTerminalPropagationMs,
		PendingOverflowReason:      "pending_invocation_limit",
		ChildOverflowReason:        "pending_child_dispatch_limit",
		OutputOverflowReason:       "stream_backpressure_exhausted",
		InputOverflowReason:        "bidi_backpressure_exhausted",
		RecoveryOverflowReason:     "recovery_concurrency_limit",
	}
}

// SetAdmissionDedupWindowMs updates the caller nonce replay window.
func (r *LocalRuntime) SetAdmissionDedupWindowMs(windowMs int64) {
	r.admissionMu.Lock()
	defer r.admissionMu.Unlock()
	r.admissionReplay = r.admissionReplay.withWindowMs(windowMs)
}

// AxiomEnvelopeOf returns the exact caller-signed request evidence.
func (r *LocalRuntime) AxiomEnvelopeOf(invocationID string) (SignedEnvelope, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	signed, ok := r.axiomEnvelopes[invocationID]
	if !ok {
		return SignedEnvelope{}, false
	}
	signed.Signature = cloneCallerSignature(signed.Signature)
	signed.Envelope.CausalContext = cloneCausalContext(signed.Envelope.CausalContext)
	return signed, true
}

// BindProvider atomically installs descriptor and implementation evidence.
func (r *LocalRuntime) BindProvider(binding ProviderBinding) *AxonError {
	if binding.handler == nil ||
		binding.descriptor.descriptorRef.Raw == "" ||
		binding.implementation.descriptorRef.Raw == "" {
		return ErrInvalidArgument("provider_binding_invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.abilities[binding.descriptor.descriptorRef.AbilityURA] = binding
	return nil
}

func (r *LocalRuntime) coreOf(invocationID string) *InvocationCore {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cores[invocationID]
}

// CoreOf returns the lifecycle aggregate for inspection.
func (r *LocalRuntime) CoreOf(invocationID string) *InvocationCore {
	return r.coreOf(invocationID)
}

// ChildrenOf returns deterministic direct child identities, including closed
// children retained for audit.
func (r *LocalRuntime) ChildrenOf(invocationID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	children := r.children[invocationID]
	ids := make([]string, 0, len(children))
	for id := range children {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SendMessage routes one frame through the generation-checked control plane.
func (r *LocalRuntime) SendMessage(
	ctx context.Context,
	invocationID string,
	payload []byte,
	messageID string,
) (*MessageAck, *AxonError) {
	token, err := r.currentToken(invocationID)
	if err != nil {
		return nil, err
	}
	return r.sendWithControl(ctx, token, payload, messageID)
}

// Cancel requests idempotent caller-owned cancellation.
func (r *LocalRuntime) Cancel(
	ctx context.Context,
	invocationID string,
	reason string,
) *AxonError {
	token, err := r.currentToken(invocationID)
	if err != nil {
		return err
	}
	return r.cancelWithControl(ctx, token, reason)
}

// InvokeDescriptorBoundRequest verifies, admits, and dispatches one complete
// caller-signed request. The runtime never synthesizes caller authority.
func (r *LocalRuntime) InvokeDescriptorBoundRequest(
	ctx context.Context,
	request DescriptorBoundInvocationRequest,
) (*InvocationHandle, SignedEnvelope, *AxonError) {
	if request.callMode != CallModeRPC {
		err := ErrInvalidArgument("descriptor_bound_request_mode_unsupported")
		err.Message = string(request.callMode)
		return nil, SignedEnvelope{}, err
	}
	envelope := request.envelope.Envelope()
	abilityRef, err := ParseAbilityDescriptorRef(envelope.Ability)
	if err != nil {
		return nil, SignedEnvelope{}, envelopeIncomplete(err)
	}
	if envelope.ArgsDigest != Sha256(request.payload) {
		digestErr := ErrInvalidArgument(ReasonEnvelopeIncomplete)
		digestErr.Message = "args_digest_payload_mismatch"
		return nil, SignedEnvelope{}, digestErr
	}
	canonical, err := request.CanonicalBytes()
	if err != nil {
		return nil, SignedEnvelope{}, envelopeIncomplete(err)
	}
	if len(canonical) == 0 {
		return nil, SignedEnvelope{}, ErrInternal(
			"canonical_descriptor_bound_invocation_bytes_empty",
		)
	}

	r.mu.Lock()
	binding, ok := r.abilities[abilityRef.AbilityURA]
	r.mu.Unlock()
	if !ok {
		return nil, SignedEnvelope{}, ErrInvalidArgument(
			"unknown_ability:" + abilityRef.AbilityURA,
		)
	}
	if binding.descriptor.descriptorRef.Raw != abilityRef.Raw {
		return nil, SignedEnvelope{}, ErrInvalidArgument(
			"registered_descriptor_ref_mismatch",
		)
	}

	r.admissionMu.Lock()
	resolver := r.admissionResolver
	replay := r.admissionReplay
	r.admissionMu.Unlock()
	admission, admissionErr := verifyDescriptorBoundAdmission(
		request.envelope,
		request.signature,
		resolver,
		replay,
		r.receiptProvider,
		NowMs(),
	)
	if admissionErr != nil {
		return nil, SignedEnvelope{}, admissionErr
	}
	descriptorEvidence, err := resolvedDescriptorEvidence(admission, binding)
	if err != nil {
		return nil, SignedEnvelope{}, envelopeIncomplete(err)
	}
	implementationEvidence, err := registeredImplementationEvidence(binding)
	if err != nil {
		return nil, SignedEnvelope{}, envelopeIncomplete(err)
	}

	effectiveDeadline, reserveErr := r.reserveInvocation(
		request.parentInvocationID,
		request.supervisorSpec,
		"pending_invocation_limit",
	)
	if reserveErr != nil {
		return nil, SignedEnvelope{}, reserveErr
	}
	reserved := true
	defer func() {
		if reserved {
			r.releaseUnboundReservation(request.parentInvocationID)
		}
	}()

	invocationID := r.newInvocationID()
	receiptContext, err := r.receiptProvider.Bind(
		invocationID,
		admission,
		descriptorEvidence,
		implementationEvidence,
	)
	if err != nil {
		if axonErr, ok := err.(*AxonError); ok {
			return nil, SignedEnvelope{}, axonErr
		}
		return nil, SignedEnvelope{}, ErrInternal(
			"receipt_context_bind_failed:" + err.Error(),
		)
	}

	signed := SignedEnvelope{
		Envelope:  envelope,
		Signature: cloneCallerSignature(request.signature),
	}
	if request.supervisorSpec != nil && request.supervisorSpec.PersistForRecovery {
		if r.persistence == nil {
			return nil, SignedEnvelope{}, ErrInvalidArgument(
				"persistence_provider_required",
			)
		}
		if binding.recovery == nil {
			return nil, SignedEnvelope{}, ErrInvalidArgument(
				"recovery_continuation_required",
			)
		}
		snapshot := RecoverySnapshot{
			InvocationID:       invocationID,
			Envelope:           envelope,
			Signature:          cloneCallerSignature(request.signature),
			Payload:            append([]byte(nil), request.payload...),
			ParentInvocationID: request.parentInvocationID,
			SupervisorSpec:     *cloneSupervisorSpec(request.supervisorSpec),
			AbilityURA:         abilityRef.AbilityURA,
			Checkpoint:         append([]byte(nil), binding.recovery.initialCheckpoint...),
			Phase:              "running",
		}
		if !effectiveDeadline.IsZero() {
			snapshot.EffectiveDeadlineUnixMs = effectiveDeadline.UnixMilli()
		}
		if err := r.persistence.InitializeRecovery(snapshot); err != nil {
			return nil, SignedEnvelope{}, ErrInternal(
				"recovery_initialization_failed:" + err.Error(),
			)
		}
	}

	handle, invokeErr := r.invokeRegistered(
		invocationID,
		binding,
		request.payload,
		request.parentInvocationID,
		request.supervisorSpec,
		receiptContext,
		effectiveDeadline,
		request.supervisorSpec != nil && request.supervisorSpec.PersistForRecovery,
		terminalOwnerCompletion,
	)
	reserved = false
	if invokeErr != nil {
		return nil, SignedEnvelope{}, invokeErr
	}
	r.mu.Lock()
	r.axiomEnvelopes[invocationID] = signed
	r.mu.Unlock()
	return handle, signed, nil
}

func (r *LocalRuntime) reserveInvocation(
	parentInvocationID string,
	spec *SupervisorSpec,
	rootOverflowReason string,
) (time.Time, *AxonError) {
	now := time.Now()
	var requestedDeadline time.Time
	if spec != nil && spec.ResourceLimit.WallSeconds > 0 {
		requestedDeadline = now.Add(
			time.Duration(spec.ResourceLimit.WallSeconds * float64(time.Second)),
		)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeCount >= canonicalPendingCapacity {
		reason := rootOverflowReason
		if parentInvocationID != "" {
			reason = "pending_child_dispatch_limit"
		}
		return time.Time{}, ErrResourceExhausted(reason).WithRetryAfterMs(100)
	}
	var effectiveDeadline = requestedDeadline
	if parentInvocationID != "" {
		parent := r.cores[parentInvocationID]
		if parent == nil {
			return time.Time{}, ErrInvalidArgument("unknown_parent_invocation").
				WithInvocationID(parentInvocationID)
		}
		if parent.CurrentState() != StateRunning || parent.terminalClaimed() {
			return time.Time{}, ErrInvalidArgument("parent_invocation_not_running").
				WithInvocationID(parentInvocationID)
		}
		if r.activeChildren[parentInvocationID] >= canonicalPendingCapacity {
			return time.Time{}, ErrResourceExhausted(
				"pending_child_dispatch_limit",
			).WithRetryAfterMs(100)
		}
		parentDeadline := parent.effectiveDeadline
		if !parentDeadline.IsZero() &&
			(effectiveDeadline.IsZero() || parentDeadline.Before(effectiveDeadline)) {
			effectiveDeadline = parentDeadline
		}
		r.activeChildren[parentInvocationID]++
	}
	r.activeCount++
	return effectiveDeadline, nil
}

func (r *LocalRuntime) releaseUnboundReservation(parentInvocationID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeCount > 0 {
		r.activeCount--
	}
	if parentInvocationID != "" && r.activeChildren[parentInvocationID] > 0 {
		r.activeChildren[parentInvocationID]--
	}
}

func (r *LocalRuntime) invokeRegistered(
	invocationID string,
	binding ProviderBinding,
	payload []byte,
	parentInvocationID string,
	spec *SupervisorSpec,
	receiptContext *BoundReceiptContext,
	effectiveDeadline time.Time,
	persisted bool,
	completionOwner terminalOwner,
) (handle *InvocationHandle, resultErr *AxonError) {
	registered := false
	defer func() {
		if resultErr != nil && !registered {
			r.releaseUnboundReservation(parentInvocationID)
			if persisted {
				_ = r.persistence.MarkRecoveryPhase(invocationID, "closed")
			}
		}
	}()
	if spec == nil {
		spec = &SupervisorSpec{}
	}
	supervisor := NewSupervisor(invocationID, *cloneSupervisorSpec(spec))
	inbox := newMessageInbox(
		invocationID,
		canonicalBidiCapacity,
		"bidi_backpressure_exhausted",
	)
	var persistenceSink eventPersistenceSink
	if persisted {
		persistenceSink = func(
			event *InvocationEvent,
			receipt SignedInvocationReceipt,
		) error {
			return r.persistence.AppendLifecycleEvent(
				invocationID,
				PersistedLifecycleEvent{
					Sequence:           event.Sequence,
					EventType:          event.EventType,
					State:              event.State,
					TimestampUnixMs:    event.TimestampUnixMs,
					Payload:            append([]byte(nil), event.Payload...),
					PayloadContentType: event.PayloadContentType,
					Reason:             event.Reason,
					CleanupComplete:    receipt.CleanupComplete(),
					ChildInvocationID:  event.ChildInvocationID,
					ReceiptHash:        receipt.SelfHash(),
				},
			)
		}
	}
	core := newInvocationCore(
		invocationID,
		parentInvocationID,
		r.receiptProvider,
		receiptContext,
		effectiveDeadline,
		persistenceSink,
	)
	core.setInputDepth(inbox.Depth)
	core.setOnTerminal(func(state InvocationState) {
		r.onInvocationTerminal(invocationID, parentInvocationID, state)
	})
	inbox.RegisterObserver(func(message *InboundMessage) {
		_, _ = core.emit(
			"message_received",
			StateRunning,
			message.Payload,
			message.ContentType,
			"",
			false,
			"",
		)
	})

	cancelCh := make(chan struct{})
	var cancelled atomic.Bool
	executionContext, executionCancel := context.WithCancel(context.Background())
	execution := &runtimeExecution{
		cancel:    executionCancel,
		done:      make(chan struct{}),
		persisted: persisted,
	}

	if _, err := core.emit("accepted", StateAccepted, nil, "", "", false, ""); err != nil {
		return nil, err
	}
	if _, err := core.emit("admitted", StateAdmitted, nil, "", "", false, ""); err != nil {
		return nil, err
	}
	if _, err := core.emit("dispatched", StateDispatched, nil, "", "", false, ""); err != nil {
		return nil, err
	}
	if _, err := core.emit("running", StateRunning, nil, "", "", false, ""); err != nil {
		return nil, err
	}

	r.mu.Lock()
	generation := r.nextGeneration
	r.nextGeneration++
	token := invocationToken{invocationID: invocationID, generation: generation}
	r.generations[invocationID] = generation
	r.cores[invocationID] = core
	r.supervisors[invocationID] = supervisor
	r.inboxes[invocationID] = inbox
	r.cancelFlags[invocationID] = cancelCh
	r.cancelledBs[invocationID] = &cancelled
	r.executions[invocationID] = execution
	r.active[invocationID] = true
	if parentInvocationID != "" {
		if r.children[parentInvocationID] == nil {
			r.children[parentInvocationID] = make(map[string]struct{})
		}
		r.children[parentInvocationID][invocationID] = struct{}{}
	}
	r.mu.Unlock()
	registered = true

	if parentInvocationID != "" {
		r.beforeChildSpawnCommit()
		parent := r.coreOf(parentInvocationID)
		if parent == nil {
			resultErr = ErrInternal("parent_registry_lost").
				WithInvocationID(parentInvocationID)
			r.closeUnstartedInvocation(
				core,
				supervisor,
				execution,
				"parent_registry_lost",
			)
			return nil, resultErr
		}
		if _, err := parent.emit(
			"child_spawned",
			StateRunning,
			nil,
			"",
			"",
			false,
			invocationID,
		); err != nil {
			r.closeUnstartedInvocation(
				core,
				supervisor,
				execution,
				"child_dispatch_not_committed",
			)
			return nil, err
		}
	}

	control := &invocationControl{runtime: r, token: token}
	abilityContext := &AbilityContext{
		InvocationID:       invocationID,
		ParentInvocationID: parentInvocationID,
		Payload:            append([]byte(nil), payload...),
		Inbox:              inbox,
		Supervisor:         supervisor,
		Runtime:            r,
		core:               core,
		execution:          executionContext,
		cancelled:          &cancelled,
		cancelCh:           cancelCh,
	}

	go r.runAbility(
		executionContext,
		binding.handler,
		abilityContext,
		supervisor,
		execution,
		completionOwner,
	)
	if !effectiveDeadline.IsZero() {
		go r.watchDeadline(token, effectiveDeadline, execution.done)
	}
	return newHandle(core, control), nil
}

func (r *LocalRuntime) finishCleanup(core *InvocationCore, supervisor *Supervisor, ctx context.Context, cancelled bool) *AxonError {
	if supervisor.Spec.PersistForRecovery {
		var failure error
		if r.persistence == nil {
			failure = ErrInternal("recovery_persistence_missing")
		} else {
			failure = r.persistence.BeginFinalization(core.InvocationID, r.runtimeID)
		}
		if failure != nil {
			err := ErrUnavailable("recovery_finalization_persist_failed:" + failure.Error()).WithInvocationID(core.InvocationID)
			core.failFinalization(err)
			return err
		}
	}
	if cancelled {
		supervisor.RunCancelFlow(ctx)
	} else {
		supervisor.RunCompletionFlow(ctx)
	}
	if failure := supervisor.CleanupError(); failure != nil {
		err, ok := failure.(*AxonError)
		if !ok {
			err = ErrInternal("supervisor_cleanup_failed")
		}
		core.failFinalization(err)
		// Keep registration, reservation and recovery while native resources remain unproven.
		return err
	}
	return nil
}

func (r *LocalRuntime) closeUnstartedInvocation(
	core *InvocationCore,
	supervisor *Supervisor,
	execution *runtimeExecution,
	reason string,
) {
	defer close(execution.done)
	execution.cancel()
	r.mu.Lock()
	suspended := execution.suspended
	claimed := !suspended && core.claimTerminal(terminalOwnerFailure)
	r.mu.Unlock()
	if suspended {
		return
	}
	if claimed {
		if r.finishCleanup(core, supervisor, context.Background(), false) != nil {
			return
		}
		_, _ = core.emit(
			"failed",
			StateFailed,
			nil,
			"",
			reason,
			true,
			"",
		)
	} else {
		_ = core.WaitTerminal(context.Background())
	}
}

func (r *LocalRuntime) runAbility(
	runContext context.Context,
	handler AbilityFn,
	ability *AbilityContext,
	supervisor *Supervisor,
	execution *runtimeExecution,
	completionOwner terminalOwner,
) {
	defer close(execution.done)
	result, invocationErr := executeAbility(handler, runContext, ability)

	owner := completionOwner
	if invocationErr != nil {
		owner = terminalOwnerFailure
	}
	r.mu.Lock()
	claimed := !execution.suspended && ability.core.claimTerminal(owner)
	r.mu.Unlock()
	if !claimed {
		return
	}
	if invocationErr != nil {
		if r.finishCleanup(ability.core, supervisor, context.Background(), false) != nil {
			return
		}
		_, _ = ability.core.emit(
			"failed",
			StateFailed,
			nil,
			"",
			invocationErr.Reason,
			true,
			"",
		)
		return
	}
	if r.finishCleanup(ability.core, supervisor, context.Background(), false) != nil {
		return
	}
	_, _ = ability.core.emit(
		"completed",
		StateCompleted,
		result,
		"",
		"",
		true,
		"",
	)
}

func executeAbility(
	handler AbilityFn,
	ctx context.Context,
	ability *AbilityContext,
) (result []byte, invocationErr *AxonError) {
	defer func() {
		if recover() != nil {
			result = nil
			invocationErr = ErrInternal("ability_handler_panicked").
				WithInvocationID(ability.InvocationID)
		}
	}()
	return handler(ctx, ability)
}

func (r *LocalRuntime) watchDeadline(
	token invocationToken,
	deadline time.Time,
	executionDone <-chan struct{},
) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-timer.C:
		_ = r.timeoutWithControl(context.Background(), token, "runtime_deadline")
	case <-executionDone:
	}
}

func (r *LocalRuntime) onInvocationTerminal(
	invocationID string,
	parentInvocationID string,
	state InvocationState,
) {
	var parent *InvocationCore
	r.mu.Lock()
	if r.active[invocationID] {
		r.active[invocationID] = false
		if r.activeCount > 0 {
			r.activeCount--
		}
		if parentInvocationID != "" && r.activeChildren[parentInvocationID] > 0 {
			r.activeChildren[parentInvocationID]--
		}
	}
	if parentInvocationID != "" {
		parent = r.cores[parentInvocationID]
	}
	r.mu.Unlock()
	if parent != nil {
		_, _ = parent.emit(
			"child_terminal",
			StateRunning,
			nil,
			"",
			string(state),
			false,
			invocationID,
		)
	}
}

func (r *LocalRuntime) sendWithControl(
	ctx context.Context,
	token invocationToken,
	payload []byte,
	messageID string,
) (*MessageAck, *AxonError) {
	r.mu.Lock()
	if err := r.validateControlLocked(token); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if r.inboxes[token.invocationID].InputClosed() {
		r.mu.Unlock()
		return nil, ErrInvalidArgument("invocation_input_closed").
			WithInvocationID(token.invocationID)
	}
	core := r.cores[token.invocationID]
	inbox := r.inboxes[token.invocationID]
	r.mu.Unlock()
	if core.IsTerminal() || core.terminalClaimed() {
		return nil, ErrInvalidArgument("invocation_terminal").
			WithInvocationID(token.invocationID)
	}
	return inbox.Deliver(ctx, payload, messageID, "")
}

func (r *LocalRuntime) cancelWithControl(
	ctx context.Context,
	token invocationToken,
	reason string,
) *AxonError {
	return r.cancelWithControlOwner(
		ctx,
		token,
		reason,
		terminalOwnerCallerCancel,
	)
}

func (r *LocalRuntime) cancelWithControlOwner(
	ctx context.Context,
	token invocationToken,
	reason string,
	owner terminalOwner,
) *AxonError {
	r.mu.Lock()
	if err := r.validateControlLocked(token); err != nil {
		r.mu.Unlock()
		return err
	}
	if intent := r.cancelIntents[token]; intent != nil {
		r.mu.Unlock()
		select {
		case <-intent.done:
			return intent.err
		case <-ctx.Done():
			return ErrCancelled("cancel_acknowledgement_context_cancelled").
				WithInvocationID(token.invocationID)
		}
	}
	core := r.cores[token.invocationID]
	if core.IsTerminal() {
		r.mu.Unlock()
		_ = core.WaitTerminal(ctx)
		return nil
	}
	intent := &cancelIntent{
		phase:  cancelWinnerRunning,
		reason: reason,
		owner:  owner,
		done:   make(chan struct{}),
	}
	r.cancelIntents[token] = intent
	r.mu.Unlock()

	go r.runCancelWinner(context.Background(), token, intent)
	select {
	case <-intent.done:
		return intent.err
	case <-ctx.Done():
		return ErrCancelled("cancel_acknowledgement_context_cancelled").
			WithInvocationID(token.invocationID)
	}
}

func (r *LocalRuntime) runCancelWinner(
	ctx context.Context,
	token invocationToken,
	intent *cancelIntent,
) {
	defer func() {
		if recover() != nil {
			cancelErr := ErrInternal("cancel_winner_panicked").
				WithInvocationID(token.invocationID)
			r.closePanickedCancelWinner(token, intent)
			r.completeCancelIntent(intent, cancelErr)
		}
	}()
	r.beforeCancelWinner()
	r.mu.Lock()
	if err := r.validateControlLocked(token); err != nil {
		r.mu.Unlock()
		r.completeCancelIntent(intent, err)
		return
	}
	core := r.cores[token.invocationID]
	if !core.claimTerminal(intent.owner) {
		r.mu.Unlock()
		_ = core.WaitTerminal(ctx)
		r.completeCancelIntent(intent, core.FinalizationError())
		return
	}
	supervisor := r.supervisors[token.invocationID]
	children := r.childTokensLocked(token.invocationID)
	r.mu.Unlock()

	r.signalTerminalStop(token.invocationID)
	for _, child := range children {
		if err := r.cancelWithControlOwner(
			ctx,
			child,
			"parent_terminal:CANCELLED",
			terminalOwnerParentCancel,
		); err != nil {
			core.failFinalization(err)
			r.completeCancelIntent(intent, err)
			return
		}
	}
	if err := r.finishCleanup(core, supervisor, ctx, true); err != nil {
		r.completeCancelIntent(intent, err)
		return
	}
	_, err := core.emit(
		"cancelled",
		StateCancelled,
		nil,
		"",
		intent.reason,
		true,
		"",
	)
	r.completeCancelIntent(intent, err)
}

func (r *LocalRuntime) closePanickedCancelWinner(
	token invocationToken,
	intent *cancelIntent,
) {
	defer func() { _ = recover() }()
	r.mu.Lock()
	if err := r.validateControlLocked(token); err != nil {
		r.mu.Unlock()
		return
	}
	core := r.cores[token.invocationID]
	supervisor := r.supervisors[token.invocationID]
	owner := core.terminalClaimOwner()
	if owner == terminalOwnerNone {
		if !core.claimTerminal(terminalOwnerFailure) {
			r.mu.Unlock()
			_ = core.WaitTerminal(context.Background())
			return
		}
		owner = terminalOwnerFailure
	}
	r.mu.Unlock()

	r.signalTerminalStop(token.invocationID)
	if owner == terminalOwnerCallerCancel || owner == terminalOwnerParentCancel {
		if r.finishCleanup(core, supervisor, context.Background(), true) != nil {
			return
		}
		_, _ = core.emit(
			"cancelled",
			StateCancelled,
			nil,
			"",
			intent.reason,
			true,
			"",
		)
		return
	}
	if owner == terminalOwnerFailure {
		if r.finishCleanup(core, supervisor, context.Background(), false) != nil {
			return
		}
		_, _ = core.emit(
			"failed",
			StateFailed,
			nil,
			"",
			"cancel_winner_panicked",
			true,
			"",
		)
	}
}

func (r *LocalRuntime) timeoutWithControl(
	ctx context.Context,
	token invocationToken,
	reason string,
) *AxonError {
	r.mu.Lock()
	if err := r.validateControlLocked(token); err != nil {
		r.mu.Unlock()
		return err
	}
	core := r.cores[token.invocationID]
	if core.IsTerminal() {
		r.mu.Unlock()
		_ = core.WaitTerminal(ctx)
		return core.FinalizationError()
	}
	if !core.claimTerminal(terminalOwnerDeadline) {
		r.mu.Unlock()
		_ = core.WaitTerminal(ctx)
		return core.FinalizationError()
	}
	supervisor := r.supervisors[token.invocationID]
	children := r.childTokensLocked(token.invocationID)
	r.mu.Unlock()

	r.signalTerminalStop(token.invocationID)
	for _, child := range children {
		if err := r.timeoutWithControl(
			ctx,
			child,
			"parent_terminal:TIMED_OUT",
		); err != nil {
			core.failFinalization(err)
			return err
		}
	}
	if err := r.finishCleanup(core, supervisor, ctx, true); err != nil {
		return err
	}
	_, err := core.emit(
		"timed_out",
		StateTimedOut,
		nil,
		"",
		reason,
		true,
		"",
	)
	return err
}

func (r *LocalRuntime) childTokensLocked(parentInvocationID string) []invocationToken {
	children := r.children[parentInvocationID]
	ids := make([]string, 0, len(children))
	for id := range children {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	tokens := make([]invocationToken, 0, len(ids))
	for _, id := range ids {
		tokens = append(tokens, invocationToken{
			invocationID: id,
			generation:   r.generations[id],
		})
	}
	return tokens
}

func (r *LocalRuntime) signalTerminalStop(invocationID string) {
	r.mu.Lock()
	if !r.cancelSignalled[invocationID] {
		r.cancelSignalled[invocationID] = true
		if flag := r.cancelledBs[invocationID]; flag != nil {
			flag.Store(true)
		}
		if channel := r.cancelFlags[invocationID]; channel != nil {
			close(channel)
		}
	}
	execution := r.executions[invocationID]
	r.mu.Unlock()
	if execution != nil {
		execution.cancel()
	}
}

func (r *LocalRuntime) checkpointRecovery(
	invocationID string,
	checkpoint []byte,
) *AxonError {
	r.mu.Lock()
	execution := r.executions[invocationID]
	r.mu.Unlock()
	if execution == nil || !execution.persisted || r.persistence == nil {
		return ErrInvalidArgument("invocation_not_recoverable").
			WithInvocationID(invocationID)
	}
	var err error
	if execution.recovered {
		err = r.persistence.CheckpointRecoveredInvocation(
			invocationID,
			r.runtimeID,
			checkpoint,
		)
	} else {
		err = r.persistence.CheckpointRecovery(invocationID, checkpoint)
	}
	if err != nil {
		return ErrInternal("recovery_checkpoint_failed:" + err.Error()).
			WithInvocationID(invocationID)
	}
	return nil
}

// SuspendForRestart relinquishes running persisted invocations without
// issuing terminal receipts.
func (r *LocalRuntime) SuspendForRestart(ctx context.Context) ([]string, *AxonError) {
	type target struct {
		id        string
		execution *runtimeExecution
	}
	r.mu.Lock()
	targets := make([]target, 0)
	for id, execution := range r.executions {
		if execution.persisted && r.active[id] && !execution.suspended {
			targets = append(targets, target{id, execution})
		}
	}
	r.mu.Unlock()
	sort.Slice(targets, func(i, j int) bool { return targets[i].id < targets[j].id })
	suspended := make([]string, 0, len(targets))
	for _, item := range targets {
		if ctx.Err() != nil {
			return nil, ErrCancelled("recovery_suspend_context_cancelled").WithInvocationID(item.id)
		}
		r.mu.Lock()
		execution := r.executions[item.id]
		if execution != item.execution || !r.active[item.id] || execution.suspended {
			r.mu.Unlock()
			continue
		}
		core := r.cores[item.id]
		if core.terminalClaimed() || core.IsTerminal() {
			r.mu.Unlock()
			select {
			case <-core.terminal:
				if err := core.FinalizationError(); err != nil {
					return nil, err
				}
			case <-ctx.Done():
				return nil, ErrCancelled("recovery_suspend_context_cancelled").WithInvocationID(item.id)
			}
			continue
		}
		// Terminal claims and suspension share the registry lock. Persist before
		// relinquishing the reservation, so a failed write cannot release ownership.
		if err := r.persistence.MarkRecoveryPhase(item.id, "suspended"); err != nil {
			r.mu.Unlock()
			return nil, ErrInternal("recovery_suspend_persist_failed:" + err.Error()).WithInvocationID(item.id)
		}
		execution.suspended = true
		r.active[item.id] = false
		if r.activeCount > 0 {
			r.activeCount--
		}
		parentID := core.ParentInvocationID
		if parentID != "" && r.activeChildren[parentID] > 0 {
			r.activeChildren[parentID]--
		}
		r.mu.Unlock()
		execution.cancel()
		select {
		case <-execution.done:
		case <-ctx.Done():
			return nil, ErrCancelled("recovery_suspend_context_cancelled").WithInvocationID(item.id)
		}
		suspended = append(suspended, item.id)
	}
	return suspended, nil
}

// RecoverPersistedInvocations leases and resumes durable continuations.
func (r *LocalRuntime) RecoverPersistedInvocations(
	ctx context.Context,
	lease time.Duration,
) ([]*InvocationHandle, *AxonError) {
	if r.persistence == nil {
		return nil, ErrInvalidArgument("persistence_provider_required")
	}
	r.mu.Lock()
	available := canonicalPendingCapacity - r.activeCount
	r.mu.Unlock()
	if available <= 0 {
		return nil, ErrResourceExhausted("recovery_concurrency_limit").
			WithRetryAfterMs(100)
	}
	snapshots, err := r.persistence.ClaimRecovery(r.runtimeID, lease, available)
	if err != nil {
		if typed, ok := err.(*AxonError); ok {
			return nil, typed
		}
		return nil, ErrInternal("recovery_lease_failed:" + err.Error())
	}
	handles := make([]*InvocationHandle, 0, len(snapshots))
	for _, snapshot := range snapshots {
		handle, recoverErr := r.recoverSnapshot(ctx, snapshot, lease)
		if recoverErr != nil {
			_ = r.persistence.MarkRecoveryPhase(snapshot.InvocationID, "suspended")
			return nil, recoverErr
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

func (r *LocalRuntime) recoverSnapshot(
	_ context.Context,
	snapshot RecoverySnapshot,
	lease time.Duration,
) (*InvocationHandle, *AxonError) {
	envelope, err := NewDescriptorBoundEnvelope(snapshot.Envelope)
	if err != nil {
		return nil, envelopeIncomplete(err)
	}
	abilityRef, err := ParseAbilityDescriptorRef(snapshot.Envelope.Ability)
	if err != nil {
		return nil, envelopeIncomplete(err)
	}
	r.mu.Lock()
	binding, ok := r.abilities[abilityRef.AbilityURA]
	if !ok || binding.recovery == nil {
		r.mu.Unlock()
		return nil, ErrInvalidArgument("recovery_provider_not_bound")
	}
	if r.activeCount >= canonicalPendingCapacity {
		r.mu.Unlock()
		return nil, ErrResourceExhausted("recovery_concurrency_limit").
			WithRetryAfterMs(100)
	}
	r.activeCount++
	if snapshot.ParentInvocationID != "" {
		r.activeChildren[snapshot.ParentInvocationID]++
	}
	r.mu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			r.releaseUnboundReservation(snapshot.ParentInvocationID)
		}
	}()

	r.admissionMu.Lock()
	resolver := r.admissionResolver
	replay := r.admissionReplay
	r.admissionMu.Unlock()
	admission, admissionErr := verifyDescriptorBoundAdmission(
		envelope,
		snapshot.Signature,
		resolver,
		replay,
		r.receiptProvider,
		NowMs(),
	)
	if admissionErr != nil {
		return nil, admissionErr
	}
	descriptorEvidence, err := resolvedDescriptorEvidence(admission, binding)
	if err != nil {
		return nil, envelopeIncomplete(err)
	}
	implementationEvidence, err := registeredImplementationEvidence(binding)
	if err != nil {
		return nil, envelopeIncomplete(err)
	}
	receiptContext, err := r.receiptProvider.Bind(
		snapshot.InvocationID,
		admission,
		descriptorEvidence,
		implementationEvidence,
	)
	if err != nil {
		if axonErr, ok := err.(*AxonError); ok {
			return nil, axonErr
		}
		return nil, ErrInternal("recovery_receipt_bind_failed:" + err.Error())
	}

	var effectiveDeadline time.Time
	if snapshot.EffectiveDeadlineUnixMs != 0 {
		effectiveDeadline = time.UnixMilli(snapshot.EffectiveDeadlineUnixMs)
	}
	supervisor := NewSupervisor(snapshot.InvocationID, snapshot.SupervisorSpec)
	inbox := newMessageInbox(
		snapshot.InvocationID,
		canonicalBidiCapacity,
		"bidi_backpressure_exhausted",
	)
	persistenceSink := func(
		event *InvocationEvent,
		receipt SignedInvocationReceipt,
	) error {
		return r.persistence.AppendRecoveredLifecycleEvent(
			snapshot.InvocationID,
			r.runtimeID,
			PersistedLifecycleEvent{
				Sequence:           event.Sequence,
				EventType:          event.EventType,
				State:              event.State,
				TimestampUnixMs:    event.TimestampUnixMs,
				Payload:            append([]byte(nil), event.Payload...),
				PayloadContentType: event.PayloadContentType,
				Reason:             event.Reason,
				CleanupComplete:    receipt.CleanupComplete(),
				ChildInvocationID:  event.ChildInvocationID,
				ReceiptHash:        receipt.SelfHash(),
			},
		)
	}
	core := newInvocationCore(
		snapshot.InvocationID,
		snapshot.ParentInvocationID,
		r.receiptProvider,
		receiptContext,
		effectiveDeadline,
		persistenceSink,
	)
	core.setInputDepth(inbox.Depth)
	core.setOnTerminal(func(state InvocationState) {
		r.onInvocationTerminal(
			snapshot.InvocationID,
			snapshot.ParentInvocationID,
			state,
		)
	})
	for _, event := range snapshot.Events {
		if restoreErr := core.restoreEvent(event); restoreErr != nil {
			return nil, restoreErr
		}
	}
	if core.CurrentState() != StateRunning {
		return nil, ErrInvalidArgument("recovery_snapshot_not_running").
			WithInvocationID(snapshot.InvocationID)
	}
	inbox.RegisterObserver(func(message *InboundMessage) {
		_, _ = core.emit(
			"message_received",
			StateRunning,
			message.Payload,
			message.ContentType,
			"",
			false,
			"",
		)
	})

	cancelCh := make(chan struct{})
	var cancelled atomic.Bool
	executionContext, executionCancel := context.WithCancel(context.Background())
	execution := &runtimeExecution{
		cancel:    executionCancel,
		done:      make(chan struct{}),
		persisted: true,
		recovered: true,
	}
	r.mu.Lock()
	generation := r.nextGeneration
	r.nextGeneration++
	token := invocationToken{
		invocationID: snapshot.InvocationID,
		generation:   generation,
	}
	r.generations[snapshot.InvocationID] = generation
	r.cores[snapshot.InvocationID] = core
	r.supervisors[snapshot.InvocationID] = supervisor
	r.inboxes[snapshot.InvocationID] = inbox
	r.cancelFlags[snapshot.InvocationID] = cancelCh
	r.cancelledBs[snapshot.InvocationID] = &cancelled
	r.executions[snapshot.InvocationID] = execution
	r.active[snapshot.InvocationID] = true
	r.axiomEnvelopes[snapshot.InvocationID] = SignedEnvelope{
		Envelope:  snapshot.Envelope,
		Signature: cloneCallerSignature(snapshot.Signature),
	}
	r.mu.Unlock()
	reserved = false

	abilityContext := &AbilityContext{
		InvocationID:       snapshot.InvocationID,
		ParentInvocationID: snapshot.ParentInvocationID,
		Payload:            append([]byte(nil), snapshot.Payload...),
		Inbox:              inbox,
		Supervisor:         supervisor,
		Runtime:            r,
		core:               core,
		execution:          executionContext,
		cancelled:          &cancelled,
		cancelCh:           cancelCh,
	}
	checkpoint := append([]byte(nil), snapshot.Checkpoint...)
	handler := func(
		ctx context.Context,
		ability *AbilityContext,
	) ([]byte, *AxonError) {
		return binding.recovery.resume(ctx, ability, checkpoint)
	}
	go r.runAbility(
		executionContext,
		handler,
		abilityContext,
		supervisor,
		execution,
		terminalOwnerRecovery,
	)
	go r.renewRecoveryLease(
		snapshot.InvocationID,
		execution,
		lease,
	)
	if !effectiveDeadline.IsZero() {
		go r.watchDeadline(token, effectiveDeadline, execution.done)
	}
	return newHandle(
		core,
		&invocationControl{runtime: r, token: token},
	), nil
}

func (r *LocalRuntime) renewRecoveryLease(
	invocationID string,
	execution *runtimeExecution,
	lease time.Duration,
) {
	interval := lease / 3
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		select {
		case <-execution.done:
			return
		case <-timer.C:
			if err := r.persistence.RenewRecoveryLease(
				invocationID,
				r.runtimeID,
				lease,
			); err != nil {
				r.abandonRecoveredExecution(invocationID, execution)
				return
			}
		}
	}
}

func (r *LocalRuntime) abandonRecoveredExecution(
	invocationID string,
	execution *runtimeExecution,
) {
	r.mu.Lock()
	if r.executions[invocationID] != execution ||
		execution.suspended ||
		r.cores[invocationID].IsTerminal() {
		r.mu.Unlock()
		return
	}
	execution.suspended = true
	parentID := r.cores[invocationID].ParentInvocationID
	if r.active[invocationID] {
		r.active[invocationID] = false
		if r.activeCount > 0 {
			r.activeCount--
		}
		if parentID != "" && r.activeChildren[parentID] > 0 {
			r.activeChildren[parentID]--
		}
	}
	r.mu.Unlock()
	execution.cancel()
}

type invocationToken struct {
	invocationID string
	generation   uint64
}

func (r *LocalRuntime) currentToken(
	invocationID string,
) (invocationToken, *AxonError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	generation, ok := r.generations[invocationID]
	if !ok {
		return invocationToken{}, ErrInvalidArgument("unknown_invocation").
			WithInvocationID(invocationID)
	}
	return invocationToken{
		invocationID: invocationID,
		generation:   generation,
	}, nil
}

type invocationControl struct {
	runtime *LocalRuntime
	token   invocationToken
}

func (c *invocationControl) cancel(
	ctx context.Context,
	reason string,
) *AxonError {
	return c.runtime.cancelWithControl(ctx, c.token, reason)
}

func (c *invocationControl) send(
	ctx context.Context,
	payload []byte,
	messageID string,
) (*MessageAck, *AxonError) {
	return c.runtime.sendWithControl(ctx, c.token, payload, messageID)
}

func (c *invocationControl) closeInput() *AxonError {
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	if err := c.runtime.validateControlLocked(c.token); err != nil {
		return err
	}
	c.runtime.inboxes[c.token.invocationID].CloseInput()
	return nil
}

func (r *LocalRuntime) validateControlLocked(
	token invocationToken,
) *AxonError {
	if current, ok := r.generations[token.invocationID]; !ok ||
		current != token.generation {
		return ErrInvalidArgument("stale_invocation_control").
			WithInvocationID(token.invocationID)
	}
	return nil
}

type cancelPhase uint8

const (
	cancelWinnerRunning cancelPhase = iota
	cancelCompleted
)

type cancelIntent struct {
	phase  cancelPhase
	reason string
	owner  terminalOwner
	done   chan struct{}
	err    *AxonError
}

func (r *LocalRuntime) completeCancelIntent(
	intent *cancelIntent,
	err *AxonError,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if intent.phase == cancelCompleted {
		return
	}
	intent.err = err
	intent.phase = cancelCompleted
	close(intent.done)
}

func envelopeIncomplete(err error) *AxonError {
	wrapped := ErrInvalidArgument(ReasonEnvelopeIncomplete)
	if axonErr, ok := err.(*AxonError); ok {
		if axonErr.Message != "" {
			wrapped.Message = axonErr.Message
		} else {
			wrapped.Message = axonErr.Reason
		}
	} else if err != nil {
		wrapped.Message = err.Error()
	}
	return wrapped
}

func parentReceiptsFromCausal(causal CausalContext) []ReceiptRef {
	switch causal.Form {
	case CausalScalar:
		if causal.Scalar == nil {
			return nil
		}
		return []ReceiptRef{*causal.Scalar}
	case CausalList:
		return append([]ReceiptRef(nil), causal.List...)
	default:
		return nil
	}
}
