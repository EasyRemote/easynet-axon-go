package axon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// InboundMessage — a message delivered to a running Invocation.
type InboundMessage struct {
	MessageID        string
	AcceptedSequence uint64
	Payload          []byte
	ContentType      string
	AcceptedAtUnixMs int64
}

// MessageAck — runtime-side ack for SendInvocationMessage.
type MessageAck struct {
	MessageID        string
	AcceptedSequence uint64
	Deduplicated     bool
	InboxDepth       int
}

// DefaultInboxCapacity is the canonical bounded inbound-frame capacity.
const DefaultInboxCapacity = canonicalBidiCapacity

// DefaultDedupWindow — 7 days, matching the other SDKs.
const DefaultDedupWindow = 7 * 24 * time.Hour

// MessageInbox implements FIFO / idempotent / bounded inbound delivery.
type MessageInbox struct {
	input        *inboxInput
	InvocationID string

	mu             sync.Mutex
	capacity       int
	overflowReason string
	dedupWindow    time.Duration
	seen           map[string]seenEntry
	nextSequence   uint64
	queue          chan *InboundMessage
	observers      []func(*InboundMessage)
}

type seenEntry struct {
	sequence   uint64
	acceptedAt time.Time
}

// NewMessageInbox constructs a new inbox.
func NewMessageInbox(invocationID string, capacity int) *MessageInbox {
	return newMessageInbox(invocationID, capacity, "inbox_full")
}

func newMessageInbox(
	invocationID string,
	capacity int,
	overflowReason string,
) *MessageInbox {
	if capacity <= 0 {
		capacity = DefaultInboxCapacity
	}
	return &MessageInbox{
		input:          newInboxInput(),
		InvocationID:   invocationID,
		capacity:       capacity,
		overflowReason: overflowReason,
		dedupWindow:    DefaultDedupWindow,
		seen:           make(map[string]seenEntry),
		queue:          make(chan *InboundMessage, capacity),
	}
}

// RegisterObserver — fire callback on every successful deliver.
// Used by the runtime core to emit `message_received` events.
func (i *MessageInbox) RegisterObserver(cb func(*InboundMessage)) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.observers = append(i.observers, cb)
}

// Depth returns the current queued-message count.
func (i *MessageInbox) Depth() int {
	return len(i.queue)
}

// Deliver a message. Returns (ack, nil) on success;
// (nil, AxonError{Kind: ResourceExhausted}) when the inbox is full.
func (i *MessageInbox) Deliver(
	_ context.Context,
	payload []byte,
	messageID string,
	contentType string,
) (*MessageAck, *AxonError) {
	i.mu.Lock()
	now := time.Now()
	// Sweep stale dedup entries.
	for mid, e := range i.seen {
		if now.Sub(e.acceptedAt) > i.dedupWindow {
			delete(i.seen, mid)
		}
	}

	if messageID != "" {
		if prior, ok := i.seen[messageID]; ok {
			depth := len(i.queue)
			i.mu.Unlock()
			return &MessageAck{
				MessageID:        messageID,
				AcceptedSequence: prior.sequence,
				Deduplicated:     true,
				InboxDepth:       depth,
			}, nil
		}
	}

	if !i.input.begin() {
		i.mu.Unlock()
		return nil, ErrUnavailable("inbox_closed")
	}
	defer i.input.finish()

	assigned := messageID
	if assigned == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		assigned = "msg_" + hex.EncodeToString(b[:])
	}
	if len(i.queue) >= i.capacity {
		i.mu.Unlock()
		return nil, ErrResourceExhausted(i.overflowReason).WithRetryAfterMs(100)
	}
	seq := i.nextSequence
	i.nextSequence++
	msg := &InboundMessage{
		MessageID:        assigned,
		AcceptedSequence: seq,
		Payload:          append([]byte(nil), payload...),
		ContentType:      contentType,
		AcceptedAtUnixMs: now.UnixMilli(),
	}
	i.seen[assigned] = seenEntry{sequence: seq, acceptedAt: now}
	observers := make([]func(*InboundMessage), len(i.observers))
	copy(observers, i.observers)

	for _, cb := range observers {
		cb(msg)
	}
	i.queue <- msg
	depth := len(i.queue)
	i.mu.Unlock()
	return &MessageAck{
		MessageID:        assigned,
		AcceptedSequence: seq,
		Deduplicated:     false,
		InboxDepth:       depth,
	}, nil
}

// CloseInput seals admission without waiting for a receiver or observer.
func (i *MessageInbox) CloseInput()       { i.input.close() }
func (i *MessageInbox) InputClosed() bool { return i.input.closed() }

// Recv pulls FIFO input; nil after close/drain, timeout or context cancel.
func (i *MessageInbox) Recv(ctx context.Context, timeout time.Duration) *InboundMessage {
	if timeout <= 0 {
		select {
		case msg := <-i.queue:
			return msg
		case <-i.input.drained:
			select {
			case msg := <-i.queue:
				return msg
			default:
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case msg := <-i.queue:
		return msg
	case <-t.C:
		return nil
	case <-i.input.drained:
		select {
		case msg := <-i.queue:
			return msg
		default:
			return nil
		}
	case <-ctx.Done():
		return nil
	}
}
