package axon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// InvocationState is the canonical invocation lifecycle state.
type InvocationState string

const (
	StateUnspecified InvocationState = "UNSPECIFIED"
	StateAccepted    InvocationState = "ACCEPTED"
	StateAdmitted    InvocationState = "ADMITTED"
	StateDispatched  InvocationState = "DISPATCHED"
	StateRunning     InvocationState = "RUNNING"
	StateCompleted   InvocationState = "COMPLETED"
	StateFailed      InvocationState = "FAILED"
	StateTimedOut    InvocationState = "TIMED_OUT"
	StateCancelled   InvocationState = "CANCELLED"
)

const (
	canonicalPendingCapacity       = 256
	canonicalStreamCapacity        = 64
	canonicalBidiCapacity          = 64
	canonicalTerminalPropagationMs = 500
)

// IsTerminal reports whether the state is terminal.
func (s InvocationState) IsTerminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateTimedOut, StateCancelled:
		return true
	default:
		return false
	}
}

// InvocationEvent is one immutable lifecycle observation.
type InvocationEvent struct {
	InvocationID       string
	Sequence           uint64
	EventType          string
	State              InvocationState
	TimestampUnixMs    int64
	Payload            []byte
	PayloadContentType string
	ParentInvocationID string
	Reason             string
	ChildInvocationID  string
}

// RuntimeLifecycleLimits is the canonical provider capacity contract.
type RuntimeLifecycleLimits struct {
	PendingInvocations         int
	PendingChildren            int
	OutputFrames               int
	InputFrames                int
	RecoveryInvocations        int
	TerminalPropagationBoundMs int
	PendingOverflowReason      string
	ChildOverflowReason        string
	OutputOverflowReason       string
	InputOverflowReason        string
	RecoveryOverflowReason     string
}

// LifecycleSnapshot is a point-in-time view of runtime-owned lifecycle data.
type LifecycleSnapshot struct {
	State                      InvocationState
	EffectiveDeadlineUnixMs    int64
	OutputCapacity             int
	OutputBuffered             int
	OutputOverflowReason       string
	InputCapacity              int
	InputBuffered              int
	InputOverflowReason        string
	TerminalPropagationBoundMs int
}

type terminalOwner uint8

const (
	terminalOwnerNone terminalOwner = iota
	terminalOwnerCompletion
	terminalOwnerFailure
	terminalOwnerCallerCancel
	terminalOwnerParentCancel
	terminalOwnerDeadline
	terminalOwnerRecovery
)

type eventPersistenceSink func(
	event *InvocationEvent,
	receipt SignedInvocationReceipt,
) error

// InvocationCore is the authoritative state machine for one invocation.
type InvocationCore struct {
	InvocationID       string
	ParentInvocationID string

	mu                   sync.Mutex
	state                InvocationState
	events               []*InvocationEvent
	terminal             chan struct{}
	finalizationErr      *AxonError
	terminalClaim        terminalOwner
	changed              chan struct{}
	outputBuffered       int
	acknowledgedProgress map[uint64]struct{}
	effectiveDeadline    time.Time
	inputDepth           func() int
	receiptProvider      CanonicalReceiptProvider
	receiptContext       *BoundReceiptContext
	persistenceSink      eventPersistenceSink
	onTerminal           func(InvocationState)
}

func newInvocationCore(
	invocationID string,
	parentInvocationID string,
	receiptProvider CanonicalReceiptProvider,
	receiptContext *BoundReceiptContext,
	effectiveDeadline time.Time,
	persistenceSink eventPersistenceSink,
) *InvocationCore {
	return &InvocationCore{
		InvocationID:         invocationID,
		ParentInvocationID:   parentInvocationID,
		state:                StateUnspecified,
		terminal:             make(chan struct{}),
		changed:              make(chan struct{}),
		acknowledgedProgress: make(map[uint64]struct{}),
		effectiveDeadline:    effectiveDeadline,
		receiptProvider:      receiptProvider,
		receiptContext:       receiptContext,
		persistenceSink:      persistenceSink,
	}
}

func (c *InvocationCore) signalChangedLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// CurrentState returns the authoritative state at call time.
func (c *InvocationCore) CurrentState() InvocationState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// IsTerminal reports whether the invocation reached a terminal state.
func (c *InvocationCore) IsTerminal() bool {
	return c.CurrentState().IsTerminal()
}

// SnapshotEvents returns defensive event copies.
func (c *InvocationCore) SnapshotEvents() []*InvocationEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*InvocationEvent, len(c.events))
	for index, event := range c.events {
		copy := *event
		copy.Payload = append([]byte(nil), event.Payload...)
		out[index] = &copy
	}
	return out
}

// SnapshotReceipts returns immutable provider-accepted receipts.
func (c *InvocationCore) SnapshotReceipts() []SignedInvocationReceipt {
	return c.receiptProvider.SnapshotSignedReceipts(c.InvocationID)
}

func (c *InvocationCore) claimTerminal(owner terminalOwner) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.IsTerminal() || c.terminalClaim != terminalOwnerNone {
		return false
	}
	c.terminalClaim = owner
	c.signalChangedLocked()
	return true
}

func (c *InvocationCore) terminalClaimed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terminalClaim != terminalOwnerNone
}

func (c *InvocationCore) terminalClaimOwner() terminalOwner {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terminalClaim
}

func (c *InvocationCore) setInputDepth(depth func() int) {
	c.mu.Lock()
	c.inputDepth = depth
	c.mu.Unlock()
}

func (c *InvocationCore) setOnTerminal(callback func(InvocationState)) {
	c.mu.Lock()
	c.onTerminal = callback
	c.mu.Unlock()
}

// LifecycleSnapshot returns provider-owned limits and live buffer occupancy.
func (c *InvocationCore) LifecycleSnapshot() LifecycleSnapshot {
	c.mu.Lock()
	var deadlineUnixMs int64
	if !c.effectiveDeadline.IsZero() {
		deadlineUnixMs = c.effectiveDeadline.UnixMilli()
	}
	state := c.state
	outputBuffered := c.outputBuffered
	inputDepth := c.inputDepth
	c.mu.Unlock()
	inputBuffered := 0
	if inputDepth != nil {
		inputBuffered = inputDepth()
	}
	return LifecycleSnapshot{
		State:                      state,
		EffectiveDeadlineUnixMs:    deadlineUnixMs,
		OutputCapacity:             canonicalStreamCapacity,
		OutputBuffered:             outputBuffered,
		OutputOverflowReason:       "stream_backpressure_exhausted",
		InputCapacity:              canonicalBidiCapacity,
		InputBuffered:              inputBuffered,
		InputOverflowReason:        "bidi_backpressure_exhausted",
		TerminalPropagationBoundMs: canonicalTerminalPropagationMs,
	}
}

// FinalizationError exposes failure without fabricating an execution terminal.
func (c *InvocationCore) FinalizationError() *AxonError {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finalizationErr
}

func (c *InvocationCore) failFinalization(err *AxonError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.IsTerminal() || c.finalizationErr != nil {
		return
	}
	c.finalizationErr = err.WithInvocationID(c.InvocationID)
	close(c.terminal)
	c.signalChangedLocked()
}

// WaitTerminal blocks until the terminal transition commits or ctx ends.
func (c *InvocationCore) WaitTerminal(ctx context.Context) InvocationState {
	select {
	case <-ctx.Done():
		return c.CurrentState()
	case <-c.terminal:
		return c.CurrentState()
	}
}

func expectedTransition(
	current InvocationState,
	eventType string,
	target InvocationState,
) bool {
	switch eventType {
	case "accepted":
		return current == StateUnspecified && target == StateAccepted
	case "admitted":
		return current == StateAccepted && target == StateAdmitted
	case "dispatched":
		return current == StateAdmitted && target == StateDispatched
	case "running":
		return current == StateDispatched && target == StateRunning
	case "progress", "message_received", "message_consumed", "child_spawned", "child_terminal":
		return current == StateRunning && target == StateRunning
	case "completed":
		return !current.IsTerminal() && current != StateUnspecified && target == StateCompleted
	case "failed":
		return !current.IsTerminal() && current != StateUnspecified && target == StateFailed
	case "timed_out":
		return !current.IsTerminal() && current != StateUnspecified && target == StateTimedOut
	case "cancelled":
		return !current.IsTerminal() && current != StateUnspecified && target == StateCancelled
	default:
		return false
	}
}

func (c *InvocationCore) emit(
	eventType string,
	state InvocationState,
	payload []byte,
	payloadContentType string,
	reason string,
	cleanupComplete bool,
	childInvocationID string,
) (*InvocationEvent, *AxonError) {
	return c.emitContext(
		context.Background(),
		eventType,
		state,
		payload,
		payloadContentType,
		reason,
		cleanupComplete,
		childInvocationID,
		0,
		false,
	)
}

func (c *InvocationCore) emitContext(
	ctx context.Context,
	eventType string,
	state InvocationState,
	payload []byte,
	payloadContentType string,
	reason string,
	cleanupComplete bool,
	childInvocationID string,
	timestampUnixMs int64,
	restoring bool,
) (*InvocationEvent, *AxonError) {
	for {
		c.mu.Lock()
		if c.state.IsTerminal() {
			c.mu.Unlock()
			return nil, ErrInvalidArgument("invocation_terminal").WithInvocationID(c.InvocationID)
		}
		if c.finalizationErr != nil {
			err := c.finalizationErr
			c.mu.Unlock()
			return nil, err
		}
		if !expectedTransition(c.state, eventType, state) {
			current := c.state
			c.mu.Unlock()
			err := ErrInternal("invalid_lifecycle_transition")
			err.Message = string(current) + ":" + eventType + ":" + string(state)
			return nil, err.WithInvocationID(c.InvocationID)
		}
		if !restoring && c.terminalClaim != terminalOwnerNone &&
			eventType != "child_terminal" && !state.IsTerminal() {
			c.mu.Unlock()
			return nil, ErrInvalidArgument("invocation_terminal_pending").WithInvocationID(c.InvocationID)
		}
		if state.IsTerminal() && !restoring && c.terminalClaim == terminalOwnerNone {
			c.mu.Unlock()
			return nil, ErrInternal("terminal_transition_without_owner").WithInvocationID(c.InvocationID)
		}
		if !restoring && eventType == "progress" &&
			c.outputBuffered >= canonicalStreamCapacity {
			changed := c.changed
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ErrCancelled("stream_backpressure_wait_cancelled").WithInvocationID(c.InvocationID)
			case <-changed:
				continue
			}
		}

		sequence := uint64(len(c.events))
		if timestampUnixMs == 0 {
			timestampUnixMs = time.Now().UnixMilli()
		}
		event := &InvocationEvent{
			InvocationID:       c.InvocationID,
			Sequence:           sequence,
			EventType:          eventType,
			State:              state,
			TimestampUnixMs:    timestampUnixMs,
			Payload:            append([]byte(nil), payload...),
			PayloadContentType: payloadContentType,
			ParentInvocationID: c.ParentInvocationID,
			Reason:             reason,
			ChildInvocationID:  childInvocationID,
		}
		receipt, err := c.receiptProvider.AppendSignedReceipt(
			c.receiptContext,
			ReceiptAppendInput{
				ReceiptType:        eventType,
				State:              state,
				TimestampUnixMs:    timestampUnixMs,
				Payload:            append([]byte(nil), payload...),
				PayloadContentType: payloadContentType,
				CleanupComplete:    cleanupComplete,
				Reason:             reason,
				ChildInvocationID:  childInvocationID,
			},
		)
		if err != nil {
			c.mu.Unlock()
			if axonErr, ok := err.(*AxonError); ok {
				return nil, axonErr.WithInvocationID(c.InvocationID)
			}
			return nil, ErrInternal("receipt_append_failed:" + err.Error()).WithInvocationID(c.InvocationID)
		}
		var persistenceErr error
		if c.persistenceSink != nil && !restoring {
			persistenceErr = c.persistenceSink(event, receipt)
		}
		c.events = append(c.events, event)
		c.state = state
		if eventType == "progress" && !restoring {
			c.outputBuffered++
		}
		callback := c.onTerminal
		nowTerminal := state.IsTerminal()
		c.signalChangedLocked()
		c.mu.Unlock()

		if nowTerminal {
			if callback != nil {
				callback(state)
			}
			close(c.terminal)
		}
		if persistenceErr != nil {
			return event, ErrInternal(
				"lifecycle_persistence_failed:" + persistenceErr.Error(),
			).WithInvocationID(c.InvocationID)
		}
		return event, nil
	}
}

func (c *InvocationCore) restoreEvent(event PersistedLifecycleEvent) *AxonError {
	_, err := c.emitContext(
		context.Background(),
		event.EventType,
		event.State,
		event.Payload,
		event.PayloadContentType,
		event.Reason,
		event.CleanupComplete,
		event.ChildInvocationID,
		event.TimestampUnixMs,
		true,
	)
	return err
}

func (c *InvocationCore) acknowledge(event *InvocationEvent) {
	if event == nil || event.EventType != "progress" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.acknowledgedProgress[event.Sequence]; exists {
		return
	}
	c.acknowledgedProgress[event.Sequence] = struct{}{}
	if c.outputBuffered > 0 {
		c.outputBuffered--
	}
	c.signalChangedLocked()
}

// FetchEvents waits for an event at fromOffset or terminal completion.
func (c *InvocationCore) FetchEvents(ctx context.Context, fromOffset uint64) []*InvocationEvent {
	return c.fetchEvents(ctx, fromOffset, nil)
}

// fetchEvents shares event waiting with a reader-local stop channel. A nil
// stop channel preserves the public context-only fetch contract.
func (c *InvocationCore) fetchEvents(ctx context.Context, fromOffset uint64, stop <-chan struct{}) []*InvocationEvent {
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		c.mu.Lock()
		if uint64(len(c.events)) > fromOffset {
			tail := c.events[fromOffset:]
			out := make([]*InvocationEvent, len(tail))
			copy(out, tail)
			c.mu.Unlock()
			return out
		}
		if c.state.IsTerminal() || c.finalizationErr != nil {
			c.mu.Unlock()
			return nil
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-stop:
			return nil
		case <-changed:
		}
	}
}

// EventStream is the resumable per-invocation event iterator.
type EventStream struct {
	core      *InvocationCore
	offset    uint64
	buffer    []*InvocationEvent
	closed    chan struct{}
	closeOnce sync.Once
}

// Events opens an event stream at fromOffset.
func (c *InvocationCore) Events(fromOffset uint64) *EventStream {
	return &EventStream{core: c, offset: fromOffset, closed: make(chan struct{})}
}

// CurrentOffset returns the sequence expected by the next call to Next.
func (s *EventStream) CurrentOffset() uint64 { return s.offset }

// Close stops this consumer.
func (s *EventStream) Close() { s.closeOnce.Do(func() { close(s.closed) }) }

// Err reports finalization failure after event drain; nil is not a terminal proof.
func (s *EventStream) Err() *AxonError { return s.core.FinalizationError() }

// Next returns the next event, or nil after terminal drain/context end.
func (s *EventStream) Next(ctx context.Context) *InvocationEvent {
	select {
	case <-s.closed:
		return nil
	default:
	}
	if len(s.buffer) == 0 {
		batch := s.core.fetchEvents(ctx, s.offset, s.closed)
		select {
		case <-s.closed:
			return nil
		default:
		}
		for _, event := range batch {
			if event.Sequence >= s.offset {
				s.buffer = append(s.buffer, event)
			}
		}
	}
	if len(s.buffer) == 0 {
		return nil
	}
	event := s.buffer[0]
	s.buffer = s.buffer[1:]
	s.offset = event.Sequence + 1
	s.core.acknowledge(event)
	return event
}

// InvocationHandle is the public reference to one invocation.
type InvocationHandle struct {
	core    *InvocationCore
	control *invocationControl
}

func newHandle(core *InvocationCore, control *invocationControl) *InvocationHandle {
	return &InvocationHandle{core: core, control: control}
}

func (h *InvocationHandle) InvocationID() string          { return h.core.InvocationID }
func (h *InvocationHandle) ParentInvocationID() string    { return h.core.ParentInvocationID }
func (h *InvocationHandle) CurrentState() InvocationState { return h.core.CurrentState() }
func (h *InvocationHandle) IsTerminal() bool              { return h.core.IsTerminal() }

// LifecycleSnapshot returns runtime-owned lifecycle evidence.
func (h *InvocationHandle) LifecycleSnapshot() LifecycleSnapshot {
	return h.core.LifecycleSnapshot()
}

// SnapshotReceipts returns immutable canonical receipts.
func (h *InvocationHandle) SnapshotReceipts() []SignedInvocationReceipt {
	return h.core.SnapshotReceipts()
}

// Events opens a resumable event stream.
func (h *InvocationHandle) Events(fromOffset uint64) *EventStream {
	return h.core.Events(fromOffset)
}

// Wait blocks until terminal or context cancellation.
func (h *InvocationHandle) Wait(ctx context.Context) InvocationState {
	return h.core.WaitTerminal(ctx)
}

// FinalizationError preserves the error separately from the legacy state-only Wait.
func (h *InvocationHandle) FinalizationError() *AxonError { return h.core.FinalizationError() }

// WaitResult waits for signed finalization or returns cleanup/context failure.
func (h *InvocationHandle) WaitResult(ctx context.Context) (InvocationState, error) {
	state := h.core.WaitTerminal(ctx)
	if err := h.core.FinalizationError(); err != nil {
		return state, err
	}
	return state, ctx.Err()
}

// Cancel is idempotent and terminal-stable.
func (h *InvocationHandle) Cancel(reason string) *AxonError {
	return h.control.cancel(context.Background(), reason)
}

// Send routes one bounded inbound message.
func (h *InvocationHandle) Send(payload []byte, messageID string) (string, *AxonError) {
	ack, err := h.control.send(context.Background(), payload, messageID)
	if err != nil {
		return "", err
	}
	return ack.MessageID, nil
}

// CloseInput permanently seals inbound delivery.
func (h *InvocationHandle) CloseInput() *AxonError {
	return h.control.closeInput()
}

// NewInvocationID returns a fresh invocation identifier.
func NewInvocationID() string {
	var bytes [8]byte
	_, _ = rand.Read(bytes[:])
	return "inv_" + hex.EncodeToString(bytes[:])
}
