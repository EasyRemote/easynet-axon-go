package axon

import (
	"context"
	"errors"
	"testing"
)

type memoryBidiSessionTransport struct {
	received     []BidiFrame
	sent         []BidiFrame
	closeSend    bool
	closed       bool
	cancelled    bool
	cancelReason string
	closeErr     error
}

func (t *memoryBidiSessionTransport) Send(
	_ context.Context,
	frame BidiFrame,
) error {
	t.sent = append(t.sent, cloneBidiFrame(frame))
	return nil
}

func (t *memoryBidiSessionTransport) Receive(
	_ context.Context,
) (BidiFrame, error) {
	if len(t.received) == 0 {
		return BidiFrame{}, errors.New("no frame")
	}
	frame := cloneBidiFrame(t.received[0])
	t.received = t.received[1:]
	return frame, nil
}

func (t *memoryBidiSessionTransport) CloseSend(context.Context) error {
	t.closeSend = true
	return nil
}

func (t *memoryBidiSessionTransport) Close(context.Context) error {
	t.closed = true
	return t.closeErr
}

func (t *memoryBidiSessionTransport) Cancel(
	_ context.Context,
	reason string,
) error {
	t.cancelled = true
	t.cancelReason = reason
	return nil
}

type nonCancellingBidiTransport struct {
	delegate *memoryBidiSessionTransport
}

func (t nonCancellingBidiTransport) Send(
	ctx context.Context,
	frame BidiFrame,
) error {
	return t.delegate.Send(ctx, frame)
}

func (t nonCancellingBidiTransport) Receive(
	ctx context.Context,
) (BidiFrame, error) {
	return t.delegate.Receive(ctx)
}

func (t nonCancellingBidiTransport) CloseSend(ctx context.Context) error {
	return t.delegate.CloseSend(ctx)
}

func (t nonCancellingBidiTransport) Close(ctx context.Context) error {
	return t.delegate.Close(ctx)
}

func newTestBidiSession(
	t *testing.T,
	transport BidiTransport,
	state BidiState,
	maxBuffered int,
) *BidiSession {
	t.Helper()
	session, err := NewBidiSession(transport, BidiSessionOptions{
		SessionID:         "session-1",
		InitialState:      state,
		MaxBufferedFrames: maxBuffered,
	})
	if err != nil {
		t.Fatalf("NewBidiSession: %v", err)
	}
	return session
}

func TestBidiSessionOwnsOrderedReceiptDrivenLifecycle(t *testing.T) {
	transport := &memoryBidiSessionTransport{
		received: []BidiFrame{
			{Kind: BidiFrameReceipt, Sequence: 1, Receipt: map[string]any{"state": "Accepted"}},
			{Kind: BidiFrameBinary, Sequence: 2, StreamID: 7, Data: []byte("reply")},
			{Kind: BidiFrameReceipt, Sequence: 3, Receipt: map[string]any{"state": "Completed"}},
			{Kind: BidiFrameDone},
		},
	}
	session := newTestBidiSession(t, transport, BidiOpening, 2)

	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive admission: %v", err)
	}
	if session.State() != BidiOpen {
		t.Fatalf("state after admission = %s, want Open", session.State())
	}

	first, err := NewBidiBinaryFrame(1, 7, []byte("one"), 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewBidiBinaryFrame(2, 7, []byte("two"), 20)
	if err != nil {
		t.Fatal(err)
	}
	third, err := NewBidiBinaryFrame(3, 7, []byte("three"), 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []BidiFrame{first, second, third} {
		if err := session.Send(context.Background(), frame); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	if got := session.SentFrames(); len(got) != 2 ||
		got[0].Sequence != 2 ||
		got[1].Sequence != 3 {
		t.Fatalf("bounded sent history = %#v", got)
	}

	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive content: %v", err)
	}
	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive terminal: %v", err)
	}
	if session.State() != BidiTerminal {
		t.Fatalf("state after terminal receipt = %s", session.State())
	}
	terminal, err := session.TerminalFrame()
	if err != nil {
		t.Fatalf("TerminalFrame: %v", err)
	}
	if terminal.Sequence != 3 {
		t.Fatalf("terminal sequence = %d", terminal.Sequence)
	}
	if done, err := session.Receive(context.Background()); err != nil ||
		done.Kind != BidiFrameDone {
		t.Fatalf("Receive done = %#v, %v", done, err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !transport.closed || session.State() != BidiClosed {
		t.Fatalf("closed=%v state=%s", transport.closed, session.State())
	}
}

func TestBidiSessionWaitsForTerminalReceiptAfterBothDirectionsClose(t *testing.T) {
	transport := &memoryBidiSessionTransport{
		received: []BidiFrame{
			{
				Kind:     BidiFrameControl,
				Sequence: 1,
				Control:  map[string]any{"kind": "eof"},
			},
			{
				Kind:     BidiFrameReceipt,
				Sequence: 2,
				Receipt:  map[string]any{"state": "Completed"},
			},
		},
	}
	session := newTestBidiSession(t, transport, BidiOpen, 4)

	outcome, err := session.CloseSend(context.Background())
	if err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	if outcome.State() != BidiHalfClosedLocal || outcome.Terminal() {
		t.Fatalf("close-send outcome = %#v", outcome)
	}
	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive remote close: %v", err)
	}
	if session.State() != BidiDraining {
		t.Fatalf("state = %s, want Draining", session.State())
	}
	if err := session.Close(context.Background()); err == nil {
		t.Fatal("Close accepted before terminal receipt")
	}
	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive terminal: %v", err)
	}
	if session.State() != BidiTerminal {
		t.Fatalf("state = %s, want Terminal", session.State())
	}
}

func TestBidiSessionCancellationIsExplicitAndNonTerminal(t *testing.T) {
	transport := &memoryBidiSessionTransport{
		received: []BidiFrame{
			{
				Kind:     BidiFrameReceipt,
				Sequence: 1,
				Receipt:  map[string]any{"state": "Cancelled"},
			},
		},
	}
	session := newTestBidiSession(t, transport, BidiOpen, 4)

	outcome, err := session.Cancel(context.Background(), "caller stop")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !transport.cancelled ||
		transport.cancelReason != "caller stop" ||
		outcome.Terminal() ||
		outcome.State() != BidiCancelRequested {
		t.Fatalf("cancel outcome = %#v transport=%#v", outcome, transport)
	}
	if err := session.Close(context.Background()); err == nil {
		t.Fatal("Close accepted before cancellation receipt")
	}
	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive cancellation receipt: %v", err)
	}
	if session.State() != BidiCancelled {
		t.Fatalf("state = %s, want Cancelled", session.State())
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close after cancellation receipt: %v", err)
	}
}

func TestBidiSessionRejectsCancellationWithoutTransportCapability(t *testing.T) {
	delegate := &memoryBidiSessionTransport{}
	session := newTestBidiSession(
		t,
		nonCancellingBidiTransport{delegate: delegate},
		BidiOpen,
		4,
	)
	if _, err := session.Cancel(context.Background(), "stop"); err == nil {
		t.Fatal("Cancel accepted a transport without cancellation capability")
	}
	if delegate.cancelled || session.State() != BidiOpen {
		t.Fatalf("unsupported cancellation mutated state: %#v %s", delegate, session.State())
	}
}

func TestBidiSessionRejectsOutOfOrderFrames(t *testing.T) {
	transport := &memoryBidiSessionTransport{
		received: []BidiFrame{
			{Kind: BidiFrameBinary, Sequence: 2},
			{Kind: BidiFrameBinary, Sequence: 1},
		},
	}
	session := newTestBidiSession(t, transport, BidiOpen, 4)
	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive first: %v", err)
	}
	if _, err := session.Receive(context.Background()); err == nil {
		t.Fatal("out-of-order frame accepted")
	}
	if session.State() != BidiFailed {
		t.Fatalf("state = %s, want Failed", session.State())
	}
}

func TestBidiSessionRejectsDuplicateTerminalReceipt(t *testing.T) {
	transport := &memoryBidiSessionTransport{
		received: []BidiFrame{
			{
				Kind:     BidiFrameReceipt,
				Sequence: 1,
				Receipt:  map[string]any{"state": "Completed"},
			},
			{
				Kind:     BidiFrameReceipt,
				Sequence: 2,
				Receipt:  map[string]any{"state": "Completed"},
			},
		},
	}
	session := newTestBidiSession(t, transport, BidiOpen, 4)
	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive terminal: %v", err)
	}
	if _, err := session.Receive(context.Background()); err == nil {
		t.Fatal("duplicate terminal receipt accepted")
	}
	if session.State() != BidiTerminal {
		t.Fatalf("state = %s, want Terminal", session.State())
	}
	terminal, err := session.TerminalFrame()
	if err != nil || terminal.Sequence != 1 {
		t.Fatalf("terminal frame = %#v, %v", terminal, err)
	}
}

func TestBidiSessionTransportCloseFailureDoesNotRewriteTerminalReceipt(t *testing.T) {
	transport := &memoryBidiSessionTransport{
		received: []BidiFrame{
			{
				Kind:     BidiFrameReceipt,
				Sequence: 1,
				Receipt:  map[string]any{"state": "Completed"},
			},
		},
		closeErr: errors.New("close failed"),
	}
	session := newTestBidiSession(t, transport, BidiOpen, 4)
	if _, err := session.Receive(context.Background()); err != nil {
		t.Fatalf("Receive terminal: %v", err)
	}
	if err := session.Close(context.Background()); err == nil {
		t.Fatal("Close accepted transport failure")
	}
	if session.State() != BidiTerminal {
		t.Fatalf("state = %s, want Terminal", session.State())
	}
}
