package axon

import (
	"context"
	"testing"
	"time"
)

func TestInputCloseWakesProvider(t *testing.T) {
	key := testSigningKey(t)
	rt := mustTestRuntime(t, key)
	ready := make(chan struct{})
	mustRegisterTestAbility(t, rt, testAbilityURA("input_close"), func(ctx context.Context, a *AbilityContext) ([]byte, *AxonError) {
		close(ready)
		if a.RecvMessage(ctx, 0) != nil {
			return nil, ErrInternal("unexpected_input")
		}
		return []byte("closed"), nil
	})
	h := mustTestInvoke(t, rt, key, "input_close", nil, "", nil)
	defer h.Cancel("test cleanup")
	<-ready
	if err := h.CloseInput(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	state, err := h.WaitResult(ctx)
	if err != nil || state != StateCompleted {
		t.Fatalf("close failed: %v %v", state, err)
	}
}

func TestInputCloseFromObserverDrainsAcceptedMessage(t *testing.T) {
	inbox := NewMessageInbox("observer", 2)
	entered, release := make(chan struct{}), make(chan struct{})
	inbox.RegisterObserver(func(*InboundMessage) { inbox.CloseInput(); close(entered); <-release })
	delivered := make(chan *MessageAck, 1)
	go func() { ack, _ := inbox.Deliver(context.Background(), []byte("one"), "one", ""); delivered <- ack }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("observer close deadlocked")
	}
	read := make(chan *InboundMessage, 1)
	go func() { read <- inbox.Recv(ctx, 0) }()
	select {
	case <-read:
		close(release)
		t.Fatal("EOF preceded accepted publication")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case msg := <-read:
		if msg == nil || string(msg.Payload) != "one" {
			t.Fatal("accepted message lost")
		}
	case <-ctx.Done():
		t.Fatal("receiver stranded")
	}
	ack := <-delivered
	if inbox.Recv(ctx, 0) != nil {
		t.Fatal("input did not drain")
	}
	duplicate, err := inbox.Deliver(ctx, []byte("one"), "one", "")
	if err != nil || !duplicate.Deduplicated || duplicate.AcceptedSequence != ack.AcceptedSequence {
		t.Fatal("dedup changed")
	}
	if _, err := inbox.Deliver(ctx, nil, "two", ""); err == nil || err.Reason != "inbox_closed" {
		t.Fatal("closed input accepted new delivery")
	}
}

func TestInputCloseWakesAllReceivers(t *testing.T) {
	inbox := NewMessageInbox("waiters", 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := make(chan *InboundMessage, 3)
	for i := 0; i < 3; i++ {
		go func() { results <- inbox.Recv(ctx, time.Minute) }()
	}
	inbox.CloseInput()
	inbox.CloseInput()
	for i := 0; i < 3; i++ {
		select {
		case msg := <-results:
			if msg != nil {
				t.Fatal("unexpected input")
			}
		case <-ctx.Done():
			t.Fatal("receiver stranded")
		}
	}
	if ctx.Err() != nil {
		t.Fatal("receivers returned on context expiry instead of input closure")
	}
}

func TestInputCloseEventStreamPreservesBufferedCleanupFailure(t *testing.T) {
	key := testSigningKey(t)
	rt := mustTestRuntime(t, key)
	ready, release := make(chan struct{}), make(chan struct{})
	mustRegisterTestAbility(t, rt, testAbilityURA("buffered_failure"), func(ctx context.Context, a *AbilityContext) ([]byte, *AxonError) {
		a.Supervisor.RegisterCleanup(func() { panic("owned cleanup failure") }, "failure")
		if err := a.EmitProgress([]byte("buffered"), ""); err != nil {
			return nil, err
		}
		close(ready)
		<-release
		return nil, nil
	})
	h := mustTestInvoke(t, rt, key, "buffered_failure", nil, "", nil)
	<-ready
	stream := h.Events(0)
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := h.WaitResult(ctx); err == nil {
		t.Fatal("cleanup failure lost")
	}
	progress := 0
	for event := stream.Next(ctx); event != nil; event = stream.Next(ctx) {
		if event.EventType == "progress" {
			progress++
			if string(event.Payload) != "buffered" {
				t.Fatal("payload changed")
			}
		}
	}
	if ctx.Err() != nil || progress != 1 || stream.Err() == nil || stream.Err().Reason != "supervisor_cleanup_failed" {
		t.Fatal("stream lost buffered progress or cleanup error")
	}
	if stream.Next(ctx) != nil {
		t.Fatal("stream did not drain")
	}
	for _, r := range h.SnapshotReceipts() {
		if r.CleanupComplete() {
			t.Fatal("false cleanup receipt")
		}
	}
}
