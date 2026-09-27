// I-12 message_inbox_bounded
package industrial

import (
	"context"
	"testing"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_message_inbox_bounded(t *testing.T) {
	inbox := inv.NewMessageInbox("inv_bounded", 2)
	if _, e := inbox.Deliver(context.Background(), []byte("a"), "", ""); e != nil {
		t.Fatal(e)
	}
	if _, e := inbox.Deliver(context.Background(), []byte("b"), "", ""); e != nil {
		t.Fatal(e)
	}
	_, e := inbox.Deliver(context.Background(), []byte("c"), "", "")
	if e == nil || e.Kind != inv.KindResourceExhausted {
		t.Fatalf("expected resource_exhausted, got %v", e)
	}
	if e.RetryAfterMs <= 0 {
		t.Fatal("retry_after_ms not set")
	}
}
