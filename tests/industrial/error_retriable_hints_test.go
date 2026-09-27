// I-16 error_retriable_hints
package industrial

import (
	"testing"

	inv "axon.run/sdk/go/axon/invocation"
)

func Test_error_retriable_hints(t *testing.T) {
	u := inv.ErrUnavailable("transient").WithRetryAfterMs(150)
	if !u.Retriable() || u.RetryAfterMs != 150 {
		t.Fatal("unavailable")
	}
	r := inv.ErrResourceExhausted("queue_full").WithRetryAfterMs(50)
	if !r.Retriable() || r.RetryAfterMs != 50 {
		t.Fatal("rex")
	}
	c := inv.ErrCancelled("user")
	if c.Retriable() || c.RetryAfterMs != 0 {
		t.Fatal("cancelled")
	}
	i := inv.ErrInternal("bug")
	if i.Retriable() {
		t.Fatal("internal")
	}
}
