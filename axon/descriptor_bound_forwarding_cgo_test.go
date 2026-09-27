//go:build cgo

package axon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type observedForwardingRequest struct {
	forwardingRequest
	observe func()
}

func (r observedForwardingRequest) DescriptorBoundNativePayload() (map[string]any, error) {
	r.observe()
	return r.forwardingRequest.DescriptorBoundNativePayload()
}
func TestForwardingNativePreservesEvidenceAfterDispatchCancellation(t *testing.T) {
	library, opened := nativeOpenCapture(t)
	capture := filepath.Join(t.TempDir(), "invoke.jsonl")
	t.Setenv("AXON_TEST_INVOKE_CAPTURE", capture)
	evidence := `{"state":"FAILED","result":{"number":9007199254740993}}`
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":false,"error":{"code":"BUSINESS_ERROR","message":"refused","source":"runtime","invocation_response":`+evidence+`}}`)
	sidecar := &SidecarTransport{LibraryPath: library}
	defer sidecar.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := observedForwardingRequest{forwardingRequest: forwardingRequest{nativeTestRequest{payload: map[string]any{"mode": "unary", "payload_base64": "AP8="}}}, observe: func() {
		if sidecar.mu.TryLock() {
			sidecar.mu.Unlock()
			t.Fatal("forwarding does not protect native lifetime")
		}
		cancel() // Cancellation after dispatch must not discard native business evidence.
	}}
	_, err := NewClient(sidecar).Ability(testAbilityRef).InvokeDescriptorBound(ctx, request, DescriptorBoundNativeOptions{RequestID: "forward", ContentType: "application/octet-stream", TimeoutMs: 30000})
	var failure DendriteError
	if !errors.As(err, &failure) || failure.InvocationResponseJSON != evidence {
		t.Fatalf("lost native evidence: %v", err)
	}
	sessions := opened()
	if len(sessions) != 1 {
		t.Fatal("unexpected session count")
	}
	if _, exists := sessions[0]["signing"]; exists {
		t.Fatal("forwarding submitted session signing")
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err = json.Unmarshal(data, &sent); err != nil {
		t.Fatal(err)
	}
	timeout := sent["timeout_ms"].(float64)
	if timeout != 30000 || sent["payload_base64"] != "AP8=" {
		t.Fatal("request timeout or caller payload changed")
	}
}

// Initialization and request dispatch are separate cancellation boundaries.
// Trigger real context cancellation at the first Err check after session open,
// independently of machine speed, without delaying or changing native code.
type cancelAfterNativeOpenContext struct {
	context.Context
	sidecar *SidecarTransport
	cancel  context.CancelFunc
}

func (c cancelAfterNativeOpenContext) Err() error {
	if c.sidecar.handle != 0 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestForwardingCancellationAfterInitializationPreventsDispatch(t *testing.T) {
	library, opened := nativeOpenCapture(t)
	capture := filepath.Join(t.TempDir(), "must-not-dispatch.jsonl")
	t.Setenv("AXON_TEST_INVOKE_CAPTURE", capture)
	t.Setenv("AXON_TEST_INVOKE_RESPONSE", `{"ok":true}`)
	sidecar := &SidecarTransport{LibraryPath: library}
	defer sidecar.Close()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := cancelAfterNativeOpenContext{Context: base, sidecar: sidecar, cancel: cancel}
	request := observedForwardingRequest{forwardingRequest: forwardingRequest{nativeTestRequest{payload: map[string]any{"mode": "unary"}}}, observe: func() { t.Fatal("cancelled request reached projection") }}
	_, err := NewClient(sidecar).Ability(testAbilityRef).InvokeDescriptorBound(ctx, request, DescriptorBoundNativeOptions{RequestID: "cancel-before-dispatch", ContentType: "application/octet-stream"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if len(opened()) != 1 {
		t.Fatal("did not exercise initialization boundary")
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("cancelled request reached native invocation")
	}
}
