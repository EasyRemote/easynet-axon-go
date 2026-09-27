package axon

import (
	"context"
	"sync"
	"testing"
	"time"
)

type observedReadContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *observedReadContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestEventStreamCloseWakesReadWithoutCancellingProvider(t *testing.T) {
	key := testSigningKey(t)
	runtime := mustTestRuntime(t, key)
	mustRegisterTestAbility(t, runtime, testAbilityURA("parked_reader"), func(ctx context.Context, ability *AbilityContext) ([]byte, *AxonError) {
		<-ctx.Done()
		return nil, nil
	})
	handle := mustTestInvoke(t, runtime, key, "parked_reader", nil, "", nil)
	defer handle.Cancel("test_cleanup")
	ctx, cancel := context.WithCancel(context.Background())
	stream := handle.Events(^uint64(0))
	observed := &observedReadContext{Context: ctx, entered: make(chan struct{})}
	result := make(chan *InvocationEvent, 1)
	done := make(chan struct{})
	go func() { defer close(done); result <- stream.Next(observed) }()
	defer func() {
		cancel()
		stream.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("reader goroutine survived cleanup")
		}
	}()
	select {
	case <-observed.entered:
	case <-time.After(time.Second):
		t.Fatal("reader did not enter fetch")
	}
	var closers sync.WaitGroup
	for i := 0; i < 16; i++ {
		closers.Add(1)
		go func() { defer closers.Done(); stream.Close() }()
	}
	closers.Wait()
	select {
	case event := <-result:
		if event != nil {
			t.Fatal("closed reader delivered an event")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("close left reader pending")
	}
	if handle.IsTerminal() {
		t.Fatal("reader close terminated provider")
	}
	if stream.Next(context.Background()) != nil {
		t.Fatal("closed reader reopened")
	}
}
