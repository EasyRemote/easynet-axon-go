package axon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MaxBidiBufferedFrames bounds the frame history retained by a session.
const MaxBidiBufferedFrames = 1024

// BidiState is the canonical bidirectional session lifecycle state.
type BidiState string

const (
	BidiOpening          BidiState = "Opening"
	BidiOpen             BidiState = "Open"
	BidiCancelRequested  BidiState = "CancelRequested"
	BidiHalfClosedLocal  BidiState = "HalfClosedLocal"
	BidiHalfClosedRemote BidiState = "HalfClosedRemote"
	BidiDraining         BidiState = "Draining"
	BidiTerminal         BidiState = "Terminal"
	BidiClosed           BidiState = "Closed"
	BidiCancelled        BidiState = "Cancelled"
	BidiFailed           BidiState = "Failed"
)

// BidiTransport supplies typed frames behind a BidiSession.
type BidiTransport interface {
	Send(context.Context, BidiFrame) error
	Receive(context.Context) (BidiFrame, error)
	CloseSend(context.Context) error
	Close(context.Context) error
}

// BidiCancellationTransport is implemented by transports with explicit
// cancellation support.
type BidiCancellationTransport interface {
	Cancel(context.Context, string) error
}

// BidiTransportFunc adapts functions into a BidiTransport.
type BidiTransportFunc struct {
	SendFunc      func(context.Context, BidiFrame) error
	ReceiveFunc   func(context.Context) (BidiFrame, error)
	CloseSendFunc func(context.Context) error
	CloseFunc     func(context.Context) error
	CancelFunc    func(context.Context, string) error
}

func (f BidiTransportFunc) Send(ctx context.Context, frame BidiFrame) error {
	if f.SendFunc == nil {
		return bidiValidationError("bidi send transport function is required")
	}
	return f.SendFunc(ctx, frame)
}

func (f BidiTransportFunc) Receive(ctx context.Context) (BidiFrame, error) {
	if f.ReceiveFunc == nil {
		return BidiFrame{}, bidiValidationError("bidi receive transport function is required")
	}
	return f.ReceiveFunc(ctx)
}

func (f BidiTransportFunc) CloseSend(ctx context.Context) error {
	if f.CloseSendFunc == nil {
		return bidiValidationError("bidi close-send transport function is required")
	}
	return f.CloseSendFunc(ctx)
}

func (f BidiTransportFunc) Close(ctx context.Context) error {
	if f.CloseFunc == nil {
		return nil
	}
	return f.CloseFunc(ctx)
}

func (f BidiTransportFunc) Cancel(ctx context.Context, reason string) error {
	if f.CancelFunc == nil {
		return bidiValidationError("bidi cancellation is unsupported by the transport")
	}
	return f.CancelFunc(ctx, reason)
}

// BidiSessionOptions configures a canonical bidirectional session.
type BidiSessionOptions struct {
	SessionID         string
	InitialState      BidiState
	MaxBufferedFrames int
}

// BidiOutcome is the immutable result of a session lifecycle command.
type BidiOutcome struct {
	sessionID string
	state     BidiState
	terminal  bool
	reason    string
}

func (o BidiOutcome) SessionID() string { return o.sessionID }
func (o BidiOutcome) State() BidiState  { return o.state }
func (o BidiOutcome) Terminal() bool    { return o.terminal }
func (o BidiOutcome) Reason() string    { return o.reason }

// BidiSession owns the explicit lifecycle and bounded frame history for one
// bidirectional invocation.
type BidiSession struct {
	sessionID   string
	transport   BidiTransport
	maxBuffered int

	stateMu        sync.Mutex
	state          BidiState
	lastSendSeq    uint64
	lastRecvSeq    uint64
	sentFrames     []BidiFrame
	receivedFrames []BidiFrame
	terminalFrame  *BidiFrame
	remoteDone     bool

	sendMu  sync.Mutex
	recvMu  sync.Mutex
	closeMu sync.Mutex
}

// NewBidiSession constructs a session after the transport has accepted its
// opening frame.
func NewBidiSession(
	transport BidiTransport,
	options BidiSessionOptions,
) (*BidiSession, error) {
	if transport == nil {
		return nil, bidiValidationError("bidi transport is required")
	}
	sessionID := strings.TrimSpace(options.SessionID)
	if sessionID == "" {
		return nil, bidiValidationError("bidi session_id is required")
	}
	state := options.InitialState
	if state == "" {
		state = BidiOpening
	}
	if state != BidiOpening && state != BidiOpen {
		return nil, bidiValidationError("bidi initial state must be Opening or Open")
	}
	maxBuffered := options.MaxBufferedFrames
	if maxBuffered == 0 {
		maxBuffered = MaxBidiBufferedFrames
	}
	if maxBuffered < 0 || maxBuffered > MaxBidiBufferedFrames {
		return nil, bidiValidationError(
			fmt.Sprintf(
				"bidi max_buffered_frames must be between 0 and %d",
				MaxBidiBufferedFrames,
			),
		)
	}
	return &BidiSession{
		sessionID:      sessionID,
		transport:      transport,
		maxBuffered:    maxBuffered,
		state:          state,
		sentFrames:     make([]BidiFrame, 0, maxBuffered),
		receivedFrames: make([]BidiFrame, 0, maxBuffered),
	}, nil
}

// NewBidiStreamSession adapts the provider-backed signed bridge stream to the
// canonical session lifecycle.
func NewBidiStreamSession(
	stream *BidiStream,
	options BidiSessionOptions,
	receiveTimeoutMs int,
) (*BidiSession, error) {
	if stream == nil {
		return nil, bidiValidationError("bidi stream is required")
	}
	return NewBidiSession(
		&bidiStreamTransport{stream: stream, receiveTimeoutMs: receiveTimeoutMs},
		options,
	)
}

func (s *BidiSession) SessionID() string {
	if s == nil {
		return ""
	}
	return s.sessionID
}

func (s *BidiSession) State() BidiState {
	if s == nil {
		return BidiFailed
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.state
}

func (s *BidiSession) MaxBufferedFrames() int {
	if s == nil {
		return 0
	}
	return s.maxBuffered
}

func (s *BidiSession) SentFrames() []BidiFrame {
	if s == nil {
		return nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return cloneBidiFrames(s.sentFrames)
}

func (s *BidiSession) ReceivedFrames() []BidiFrame {
	if s == nil {
		return nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return cloneBidiFrames(s.receivedFrames)
}

func (s *BidiSession) TerminalFrame() (BidiFrame, error) {
	if s == nil {
		return BidiFrame{}, bidiValidationError("bidi session is not initialized")
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.terminalFrame == nil {
		return BidiFrame{}, bidiValidationError("bidi terminal frame has not been seen")
	}
	return cloneBidiFrame(*s.terminalFrame), nil
}

// Send emits one typed frame while the local direction is open.
func (s *BidiSession) Send(ctx context.Context, frame BidiFrame) error {
	if err := s.requireReady(ctx); err != nil {
		return err
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.stateMu.Lock()
	if s.state != BidiOpen && s.state != BidiHalfClosedRemote {
		s.stateMu.Unlock()
		return bidiValidationError("bidi send path is closed")
	}
	if err := validateOrderedBidiFrame(frame, s.lastSendSeq, "sent"); err != nil {
		s.state = BidiFailed
		s.stateMu.Unlock()
		return err
	}
	s.stateMu.Unlock()

	if err := s.transport.Send(ctx, cloneBidiFrame(frame)); err != nil {
		s.fail()
		return bidiTransportError("bidi send transport failed", err)
	}

	s.stateMu.Lock()
	s.lastSendSeq = frame.Sequence
	s.sentFrames = appendBoundedBidiFrame(s.sentFrames, frame, s.maxBuffered)
	s.stateMu.Unlock()
	return nil
}

// Receive reads one typed frame and applies its lifecycle transition.
func (s *BidiSession) Receive(ctx context.Context) (BidiFrame, error) {
	if err := s.requireReady(ctx); err != nil {
		return BidiFrame{}, err
	}
	s.recvMu.Lock()
	defer s.recvMu.Unlock()

	s.stateMu.Lock()
	if s.state == BidiClosed ||
		s.remoteDone ||
		(s.state == BidiFailed && s.terminalFrame == nil) {
		s.stateMu.Unlock()
		return BidiFrame{}, bidiValidationError("bidi receive path is closed")
	}
	s.stateMu.Unlock()

	frame, err := s.transport.Receive(ctx)
	if err != nil {
		s.fail()
		return BidiFrame{}, bidiTransportError("bidi receive transport failed", err)
	}
	if frame.Kind == BidiFrameTimeout {
		return frame, nil
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if frame.Kind == BidiFrameDone {
		if s.terminalFrame == nil {
			s.state = BidiFailed
			return BidiFrame{}, bidiValidationError(
				"bidi transport ended before a terminal receipt",
			)
		}
		s.remoteDone = true
		return frame, nil
	}
	if s.terminalFrame != nil {
		return BidiFrame{}, bidiValidationError(
			"bidi frame received after terminal receipt",
		)
	}
	if err := validateOrderedBidiFrame(frame, s.lastRecvSeq, "received"); err != nil {
		s.state = BidiFailed
		return BidiFrame{}, err
	}
	s.lastRecvSeq = frame.Sequence
	s.receivedFrames = appendBoundedBidiFrame(
		s.receivedFrames,
		frame,
		s.maxBuffered,
	)
	if err := s.applyFrameLocked(frame); err != nil {
		s.state = BidiFailed
		return BidiFrame{}, err
	}
	return cloneBidiFrame(frame), nil
}

// CloseSend half-closes the local direction without cancelling the session.
func (s *BidiSession) CloseSend(ctx context.Context) (BidiOutcome, error) {
	if err := s.requireReady(ctx); err != nil {
		return BidiOutcome{}, err
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	state, err := s.transition(bidiEventLocalClose)
	if err != nil {
		return BidiOutcome{}, bidiValidationError("bidi send path is closed")
	}

	if err := s.transport.CloseSend(ctx); err != nil {
		s.fail()
		return BidiOutcome{}, bidiTransportError(
			"bidi close-send transport failed",
			err,
		)
	}
	state = s.State()
	return s.outcome(state, "", isTerminalBidiState(state)), nil
}

// Cancel requests cancellation. Terminal completion remains receipt-driven.
func (s *BidiSession) Cancel(
	ctx context.Context,
	reason string,
) (BidiOutcome, error) {
	if err := s.requireReady(ctx); err != nil {
		return BidiOutcome{}, err
	}
	cancelTransport, ok := s.transport.(BidiCancellationTransport)
	if !ok {
		return BidiOutcome{}, bidiValidationError(
			"bidi cancellation is unsupported by the transport",
		)
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if _, err := s.transition(bidiEventCancel); err != nil {
		return BidiOutcome{}, bidiValidationError("bidi session is terminal")
	}
	if err := cancelTransport.Cancel(ctx, reason); err != nil {
		s.fail()
		return BidiOutcome{}, bidiTransportError(
			"bidi cancel transport failed",
			err,
		)
	}
	state := s.State()
	return s.outcome(state, reason, isTerminalBidiState(state)), nil
}

// Close releases transport resources after a terminal state.
func (s *BidiSession) Close(ctx context.Context) error {
	if err := s.requireReady(ctx); err != nil {
		return err
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()

	s.stateMu.Lock()
	if s.state == BidiClosed {
		s.stateMu.Unlock()
		return nil
	}
	if s.state != BidiTerminal &&
		s.state != BidiCancelled &&
		s.state != BidiFailed {
		s.stateMu.Unlock()
		return bidiValidationError("bidi session must be terminal before close")
	}
	s.stateMu.Unlock()

	if err := s.transport.Close(ctx); err != nil {
		s.fail()
		return bidiTransportError("bidi close transport failed", err)
	}
	_, err := s.transition(bidiEventClose)
	return err
}

// NewBidiBinaryFrame creates an ordered binary content frame.
func NewBidiBinaryFrame(
	sequence uint64,
	streamID uint32,
	data []byte,
	pts uint64,
) (BidiFrame, error) {
	if sequence == 0 {
		return BidiFrame{}, bidiValidationError("bidi frame sequence is required")
	}
	return BidiFrame{
		Kind:     BidiFrameBinary,
		Sequence: sequence,
		StreamID: streamID,
		Data:     append([]byte(nil), data...),
		PTS:      pts,
	}, nil
}

type bidiSessionEvent uint8

const (
	bidiEventLocalClose bidiSessionEvent = iota + 1
	bidiEventRemoteClose
	bidiEventCancel
	bidiEventComplete
	bidiEventCancelled
	bidiEventFail
	bidiEventTransportFail
	bidiEventClose
)

func (s *BidiSession) transition(event bidiSessionEvent) (BidiState, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	next, ok := nextBidiState(s.state, event)
	if !ok {
		return s.state, bidiValidationError(
			fmt.Sprintf("invalid bidi transition from %s on event %d", s.state, event),
		)
	}
	s.state = next
	return next, nil
}

func nextBidiState(current BidiState, event bidiSessionEvent) (BidiState, bool) {
	switch event {
	case bidiEventLocalClose:
		switch current {
		case BidiOpen:
			return BidiHalfClosedLocal, true
		case BidiHalfClosedRemote:
			return BidiDraining, true
		}
	case bidiEventRemoteClose:
		switch current {
		case BidiOpening, BidiOpen:
			return BidiHalfClosedRemote, true
		case BidiHalfClosedLocal:
			return BidiDraining, true
		}
	case bidiEventCancel:
		switch current {
		case BidiOpening, BidiOpen, BidiHalfClosedLocal, BidiHalfClosedRemote, BidiDraining:
			return BidiCancelRequested, true
		}
	case bidiEventComplete:
		switch current {
		case BidiOpening, BidiOpen, BidiCancelRequested, BidiHalfClosedLocal,
			BidiHalfClosedRemote, BidiDraining:
			return BidiTerminal, true
		}
	case bidiEventCancelled:
		switch current {
		case BidiOpening, BidiOpen, BidiCancelRequested, BidiHalfClosedLocal,
			BidiHalfClosedRemote, BidiDraining:
			return BidiCancelled, true
		}
	case bidiEventFail:
		switch current {
		case BidiOpening, BidiOpen, BidiCancelRequested, BidiHalfClosedLocal,
			BidiHalfClosedRemote, BidiDraining:
			return BidiFailed, true
		}
	case bidiEventTransportFail:
		switch current {
		case BidiOpening, BidiOpen, BidiCancelRequested, BidiHalfClosedLocal,
			BidiHalfClosedRemote, BidiDraining:
			return BidiFailed, true
		}
	case bidiEventClose:
		switch current {
		case BidiTerminal, BidiCancelled, BidiFailed, BidiClosed:
			return BidiClosed, true
		}
	}
	return current, false
}

func (s *BidiSession) applyFrameLocked(frame BidiFrame) error {
	if frame.Kind == BidiFrameControl &&
		strings.EqualFold(fmt.Sprint(frame.Control["kind"]), "eof") {
		next, ok := nextBidiState(s.state, bidiEventRemoteClose)
		if !ok {
			return bidiValidationError("unexpected remote bidi close")
		}
		s.state = next
		return nil
	}
	if frame.Kind != BidiFrameReceipt {
		if s.state == BidiOpening {
			s.state = BidiOpen
		}
		return nil
	}
	if s.terminalFrame != nil {
		return bidiValidationError("duplicate terminal bidi receipt")
	}

	var event bidiSessionEvent
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(frame.Receipt["state"]))) {
	case "completed", "terminal":
		event = bidiEventComplete
	case "cancelled", "canceled":
		event = bidiEventCancelled
	case "failed":
		event = bidiEventFail
	default:
		if s.state == BidiOpening {
			s.state = BidiOpen
		}
		return nil
	}
	next, ok := nextBidiState(s.state, event)
	if !ok {
		return bidiValidationError("unexpected terminal bidi receipt")
	}
	s.state = next
	terminal := cloneBidiFrame(frame)
	s.terminalFrame = &terminal
	return nil
}

func (s *BidiSession) fail() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if next, ok := nextBidiState(s.state, bidiEventTransportFail); ok {
		s.state = next
	}
}

func (s *BidiSession) requireReady(ctx context.Context) error {
	if s == nil || s.transport == nil {
		return bidiValidationError("bidi session is not initialized")
	}
	if ctx == nil {
		return bidiValidationError("bidi context is required")
	}
	return ctx.Err()
}

func (s *BidiSession) outcome(
	state BidiState,
	reason string,
	terminal bool,
) BidiOutcome {
	return BidiOutcome{
		sessionID: s.sessionID,
		state:     state,
		terminal:  terminal,
		reason:    reason,
	}
}

func validateOrderedBidiFrame(
	frame BidiFrame,
	lastSequence uint64,
	direction string,
) error {
	if frame.Kind == "" {
		return bidiValidationError("bidi frame kind is required")
	}
	if frame.Sequence == 0 {
		return bidiValidationError("bidi frame sequence is required")
	}
	if frame.Sequence <= lastSequence {
		return bidiValidationError(
			fmt.Sprintf("bidi %s frames must be strictly ordered", direction),
		)
	}
	return nil
}

func appendBoundedBidiFrame(
	frames []BidiFrame,
	frame BidiFrame,
	limit int,
) []BidiFrame {
	if limit <= 0 {
		return frames
	}
	if len(frames) == limit {
		copy(frames, frames[1:])
		frames[len(frames)-1] = cloneBidiFrame(frame)
		return frames
	}
	return append(frames, cloneBidiFrame(frame))
}

func cloneBidiFrames(frames []BidiFrame) []BidiFrame {
	cloned := make([]BidiFrame, len(frames))
	for index, frame := range frames {
		cloned[index] = cloneBidiFrame(frame)
	}
	return cloned
}

func cloneBidiFrame(frame BidiFrame) BidiFrame {
	frame.Data = append([]byte(nil), frame.Data...)
	frame.Receipt = cloneBidiMap(frame.Receipt)
	frame.Control = cloneBidiMap(frame.Control)
	return frame
}

func cloneBidiMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneBidiValue(value)
	}
	return cloned
}

func cloneBidiValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneBidiMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneBidiValue(item)
		}
		return cloned
	case []byte:
		return append([]byte(nil), typed...)
	default:
		return value
	}
}

func isTerminalBidiState(state BidiState) bool {
	switch state {
	case BidiTerminal, BidiClosed, BidiCancelled, BidiFailed:
		return true
	default:
		return false
	}
}

func bidiValidationError(message string) error {
	return DendriteError{Code: ErrCodeValidation, Message: message}
}

func bidiTransportError(message string, err error) error {
	if err == nil {
		return DendriteError{Code: ErrCodeStream, Message: message}
	}
	var dendriteError DendriteError
	if errors.As(err, &dendriteError) {
		return err
	}
	return DendriteError{
		Code:    ErrCodeStream,
		Message: message + ": " + err.Error(),
	}
}

type bidiStreamTransport struct {
	stream           *BidiStream
	receiveTimeoutMs int
}

func (t *bidiStreamTransport) Send(ctx context.Context, frame BidiFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if frame.Kind != BidiFrameBinary {
		return bidiValidationError(
			"provider-backed bidi stream accepts binary content frames",
		)
	}
	return t.stream.Send(frame.StreamID, frame.Data, frame.PTS)
}

func (t *bidiStreamTransport) Receive(ctx context.Context) (BidiFrame, error) {
	if err := ctx.Err(); err != nil {
		return BidiFrame{}, err
	}
	timeoutMs := t.receiveTimeoutMs
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return BidiFrame{}, context.DeadlineExceeded
		}
		deadlineMs := int(remaining.Milliseconds())
		if deadlineMs < 1 {
			deadlineMs = 1
		}
		if timeoutMs <= 0 || deadlineMs < timeoutMs {
			timeoutMs = deadlineMs
		}
	}
	return t.stream.Recv(timeoutMs)
}

func (t *bidiStreamTransport) CloseSend(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.stream.SendEOF()
}

func (t *bidiStreamTransport) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.stream.Close()
}
