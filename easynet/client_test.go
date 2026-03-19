package easynet

import (
	"context"
	"testing"
)

type captureTransport struct {
	tenantID    string
	resourceURI string
	payload     Payload
}

func (t *captureTransport) Call(
	_ context.Context,
	tenantID string,
	resourceURI string,
	payload Payload,
) (Payload, error) {
	t.tenantID = tenantID
	t.resourceURI = resourceURI
	t.payload = payload
	return Payload{"ok": true}, nil
}

type captureRawTransport struct {
	captureTransport
	raw Payload
}

func (t *captureRawTransport) CallRaw(
	_ context.Context,
	tenantID string,
	resourceURI string,
	payload Payload,
	_ CallOptions,
) (Payload, error) {
	t.tenantID = tenantID
	t.resourceURI = resourceURI
	t.payload = payload
	return t.raw, nil
}

func TestClientFluentReturnsImmutableCopies(t *testing.T) {
	transport := &captureTransport{}
	base := NewClient(transport)
	client := base.
		Tenant("tenant-test").
		Ability("easynet:///r/org/reg/agent.quote-bot/abilities/order.quote@1?tenant_id=tenant-test")

	if _, err := client.Call(context.Background(), Payload{"sku": "A1"}); err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if _, err := base.Call(context.Background(), Payload{"sku": "A1"}); err == nil {
		t.Fatalf("expected base client to remain unchanged")
	}

	if transport.tenantID != "tenant-test" {
		t.Fatalf("tenant not propagated, got %q", transport.tenantID)
	}
	if transport.resourceURI == "" {
		t.Fatalf("resource uri not propagated")
	}
}

func TestClientFluentBranchingRemainsImmutable(t *testing.T) {
	transport := &captureTransport{}
	base := NewClient(transport)
	derivedA := base.
		Tenant("tenant-test").
		Ability("easynet:///r/org/reg/agent.quote-bot/abilities/order.quote@1?tenant_id=tenant-test")
	derivedB := base.
		Tenant("tenant-test").
		Ability("easynet:///r/org/reg/agent.quote-bot/abilities/inventory.get@1?tenant_id=tenant-test")

	if base == derivedA || base == derivedB {
		t.Fatalf("expected derived client to be copied")
	}

	if _, err := base.Call(context.Background(), Payload{"sku": "A1"}); err == nil {
		t.Fatalf("expected base call to fail because base context must remain unchanged")
	}

	if _, err := derivedA.Call(context.Background(), Payload{"sku": "A1"}); err != nil {
		t.Fatalf("derivedA call failed: %v", err)
	}
	if _, err := derivedB.Call(context.Background(), Payload{"sku": "A1"}); err != nil {
		t.Fatalf("derivedB call failed: %v", err)
	}
}

func TestClientCallAnySupportsNonObjectResultJSON(t *testing.T) {
	transport := &captureRawTransport{
		raw: Payload{
			"result_json": []any{"audio/chunk-1", "audio/chunk-2"},
		},
	}
	client := NewClient(transport).
		Tenant("tenant-test").
		Ability("easynet:///r/org/reg/agent.voice/abilities/stream.audio@1?tenant_id=tenant-test")

	result, err := client.CallAny(context.Background(), Payload{"codec": "opus"})
	if err != nil {
		t.Fatalf("callAny failed: %v", err)
	}
	chunks, ok := result.([]any)
	if !ok {
		t.Fatalf("expected array result_json, got %T", result)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
}

func TestClientCallAnyRequiresResultJSONField(t *testing.T) {
	transport := &captureRawTransport{
		raw: Payload{"status": "ok"},
	}
	client := NewClient(transport).
		Tenant("tenant-test").
		Ability("easynet:///r/org/reg/agent.voice/abilities/stream.audio@1?tenant_id=tenant-test")

	if _, err := client.CallAny(context.Background(), Payload{"codec": "opus"}); err == nil {
		t.Fatalf("expected callAny to reject missing result_json")
	}
}

func TestPrincipalToSubjectIDMapsPubVisibilityFromResourceURI(t *testing.T) {
	subjectID := principalToSubjectID(
		"alice",
		"easynet:///r/pub/reg/agent.alice/abilities/voice.stream@1.0.0",
	)
	if subjectID != "easynet:pub:reg:agent.alice" {
		t.Fatalf("unexpected pub subject id mapping: %q", subjectID)
	}
}

func TestPrincipalToSubjectIDMapsOrgVisibilityForNonPubResourceURI(t *testing.T) {
	subjectID := principalToSubjectID(
		"alice",
		"easynet:///r/org/reg/agent.alice/abilities/order.quote@1?tenant_id=tenant-test",
	)
	if subjectID != "easynet:org:reg:agent.alice" {
		t.Fatalf("unexpected org subject id mapping: %q", subjectID)
	}
}

func TestPrincipalToSubjectIDPreservesExplicitSubject(t *testing.T) {
	subjectID := principalToSubjectID(
		"easynet:pub:reg:agent.alice",
		"easynet:///r/org/reg/agent.alice/abilities/order.quote@1?tenant_id=tenant-test",
	)
	if subjectID != "easynet:pub:reg:agent.alice" {
		t.Fatalf("explicit subject id should be preserved, got %q", subjectID)
	}
}
