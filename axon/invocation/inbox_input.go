package axon

import "sync"

type inboxInputPhase uint8

const (
	inboxInputOpen inboxInputPhase = iota
	inboxInputClosing
	inboxInputClosed
)

// inboxInput owns admission closure independently of the producer mutex.
// EOF is published only after every accepted message has become visible.
type inboxInput struct {
	mu         sync.Mutex
	phase      inboxInputPhase
	publishing int
	drained    chan struct{}
}

func newInboxInput() *inboxInput { return &inboxInput{drained: make(chan struct{})} }
func (i *inboxInput) begin() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.phase != inboxInputOpen {
		return false
	}
	i.publishing++
	return true
}
func (i *inboxInput) finish() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.publishing--
	i.finishClose()
}
func (i *inboxInput) close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.phase != inboxInputOpen {
		return
	}
	i.phase = inboxInputClosing
	i.finishClose()
}
func (i *inboxInput) finishClose() {
	if i.phase == inboxInputClosing && i.publishing == 0 {
		i.phase = inboxInputClosed
		close(i.drained)
	}
}
func (i *inboxInput) closed() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.phase != inboxInputOpen
}
