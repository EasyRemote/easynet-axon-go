package axon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type forwardingRequest struct{ nativeTestRequest }

func (forwardingRequest) DescriptorBoundAbility() string { return testAbilityRef }

type forwardingTransport struct {
	captureTransport
	request DescriptorBoundForwardingRequest
	options DescriptorBoundNativeOptions
	calls   int
	failure error
}

func (t *forwardingTransport) InvokeDescriptorBound(_ context.Context, request DescriptorBoundForwardingRequest, options DescriptorBoundNativeOptions) (map[string]any, error) {
	t.request, t.options = request, options
	t.calls++
	return nil, t.failure
}
func TestCompleteRequestForwardingBindingsAndFailure(t *testing.T) {
	request := &forwardingRequest{}
	failure := DendriteError{Code: "BUSINESS_ERROR", Message: "refused", InvocationResponseJSON: `{"state":"FAILED"}`}
	transport := &forwardingTransport{failure: failure}
	options := DescriptorBoundNativeOptions{RequestID: "test", ContentType: "application/octet-stream", Metadata: map[string]string{"trace": "caller"}}
	client := NewClient(transport).Ability(testAbilityRef)
	_, err := client.InvokeDescriptorBound(context.Background(), request, options)
	if err != failure || transport.request != request || transport.options.Metadata["trace"] != "caller" || transport.calls != 1 {
		t.Fatal("forwarding changed request or failure")
	}
	for _, invalid := range []*Client{client.Principal("alice"), client.Ability(strings.Replace(testAbilityRef, "stream.binary", "other", 1)), NewClient(&captureTransport{})} {
		if _, err := invalid.InvokeDescriptorBound(context.Background(), request, options); err == nil {
			t.Fatal("unsupported override accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.InvokeDescriptorBound(ctx, request, options); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if transport.calls != 1 {
		t.Fatal("invalid request reached transport")
	}
}
func TestCompleteRequestForwardingRejectsBeforeNativeOpen(t *testing.T) {
	request := &forwardingRequest{}
	for _, transport := range []*SidecarTransport{{Signing: &SigningConfig{}, LibraryPath: "/must-not-open"}, {LibraryPath: "/must-not-open"}} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := transport.InvokeDescriptorBound(ctx, request, DescriptorBoundNativeOptions{}); err == nil {
			t.Fatal("invalid call accepted")
		}
		if transport.bridge != nil {
			t.Fatal("opened native before rejection")
		}
	}
	transport := &SidecarTransport{LibraryPath: "/must-not-open"}
	if _, err := transport.InvokeDescriptorBound(context.Background(), request, DescriptorBoundNativeOptions{TimeoutMs: -1}); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatal(err)
	}
}
func TestInvocationTimeoutBounds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if value, err := invocationTimeout(ctx, 30000); err != nil || value < 1 || value > 500 {
		t.Fatalf("deadline bound: %d %v", value, err)
	}
	if value, err := invocationTimeout(context.Background(), 0); err != nil || value != DefaultTimeoutMs {
		t.Fatalf("default: %d %v", value, err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := invocationTimeout(expired, 42); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
