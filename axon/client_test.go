package axon

import (
	"context"
	"strings"
	"testing"
)

const (
	testAbilityRef = "easynet:///r/tenant-test/ability/alice.stream.binary@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke"
	testCallerURA  = "easynet:///r/tenant-test/agent/alice.sdk"
)

type captureTransport struct {
	resourceURA string
	payload     Payload
	options     CallOptions
}

func (t *captureTransport) Call(
	_ context.Context,
	resourceURA string,
	payload Payload,
	options CallOptions,
) (Payload, error) {
	t.resourceURA = resourceURA
	t.payload = payload
	t.options = options
	return Payload{"ok": true}, nil
}

type captureRawTransport struct {
	captureTransport
	raw Payload
}

func (t *captureRawTransport) CallRaw(
	_ context.Context,
	resourceURA string,
	payload Payload,
	options CallOptions,
) (Payload, error) {
	t.resourceURA = resourceURA
	t.payload = payload
	t.options = options
	return t.raw, nil
}

func TestClientFluentReturnsImmutableCopies(t *testing.T) {
	transport := &captureTransport{}
	base := NewClient(transport)
	client := base.
		Ability("easynet:///r/tenant-test/ability/seller.quote-bot.order.quote@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke")

	if _, err := client.Call(context.Background(), Payload{"sku": "A1"}); err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if _, err := base.Call(context.Background(), Payload{"sku": "A1"}); err == nil {
		t.Fatalf("expected base client to remain unchanged")
	}

	if transport.resourceURA == "" {
		t.Fatalf("resource ura not propagated")
	}
}

func TestClientFluentBranchingRemainsImmutable(t *testing.T) {
	transport := &captureTransport{}
	base := NewClient(transport)
	derivedA := base.
		Ability("easynet:///r/tenant-test/ability/seller.quote-bot.order.quote@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke")
	derivedB := base.
		Ability("easynet:///r/tenant-test/ability/seller.quote-bot.inventory.get@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke")

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

func TestClientPropagatesSubjectSelectionOnly(t *testing.T) {
	transport := &captureTransport{}
	client := NewClient(transport).
		Ability(testAbilityRef).
		Principal("alice")

	if _, err := client.Call(context.Background(), Payload{"document": "draft"}); err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if transport.options.PrincipalID != "alice" {
		t.Fatalf("principal selection not propagated: %#v", transport.options)
	}
}

func TestClientCallAnySupportsNonObjectResultJSON(t *testing.T) {
	transport := &captureRawTransport{
		raw: Payload{
			"result_json": []any{"chunk-1", "chunk-2"},
		},
	}
	client := NewClient(transport).
		Ability("easynet:///r/tenant-test/ability/seller.stream.binary@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke")

	result, err := client.CallAny(context.Background(), Payload{"format": "binary"})
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
		Ability("easynet:///r/tenant-test/ability/seller.stream.binary@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke")

	if _, err := client.CallAny(context.Background(), Payload{"format": "binary"}); err == nil {
		t.Fatalf("expected callAny to reject missing result_json")
	}
}

func TestClientRequiresAbilityDescriptorRefBeforeCall(t *testing.T) {
	transport := &captureTransport{}
	client := NewClient(transport).
		Ability("easynet:///r/tenant-test/ability/seller.quote")

	if _, err := client.Call(context.Background(), Payload{"sku": "A1"}); err == nil {
		t.Fatalf("expected unversioned ability to fail")
	}
}

func TestClientRequiresExplicitTransport(t *testing.T) {
	client := NewClient(nil).
		Ability(testAbilityRef)

	_, err := client.Call(context.Background(), Payload{})
	if err == nil {
		t.Fatalf("expected nil transport client to fail")
	}
	if !strings.Contains(err.Error(), "explicit signed transport") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubjectURAForInvocationMapsShortPrincipalToSDKResourceSubject(t *testing.T) {
	subject, err := SubjectURAForInvocation(
		"alice",
		testAbilityRef,
		testCallerURA,
	)
	if err != nil {
		t.Fatalf("unexpected principal mapping error: %v", err)
	}
	if subject.Raw != "easynet:///r/tenant-test/resource/sdk.principal/alice" {
		t.Fatalf("unexpected subject ura mapping: %q", subject.Raw)
	}
}

func TestSubjectURAForInvocationPreservesExplicitCanonicalSubject(t *testing.T) {
	subject, err := SubjectURAForInvocation(
		"easynet:///r/tenant-test/resource/alice.payload",
		testAbilityRef,
		testCallerURA,
	)
	if err != nil {
		t.Fatalf("unexpected principal mapping error: %v", err)
	}
	if subject.Raw != "easynet:///r/tenant-test/resource/alice.payload" {
		t.Fatalf("explicit subject ura should be preserved, got %q", subject.Raw)
	}
}

func TestSubjectURAForInvocationDefaultsToSigningCaller(t *testing.T) {
	subject, err := SubjectURAForInvocation("", testAbilityRef, testCallerURA)
	if err != nil {
		t.Fatalf("unexpected default subject mapping error: %v", err)
	}
	if subject.Raw != testCallerURA {
		t.Fatalf("missing principal should use signing caller subject, got %q", subject.Raw)
	}
}

func TestSubjectURAForInvocationRejectsLegacyPrivateSubjectSyntax(t *testing.T) {
	if _, err := SubjectURAForInvocation(
		"easynet:prv:resource:agent.alice.payload",
		testAbilityRef,
		testCallerURA,
	); err == nil {
		t.Fatalf("expected legacy private subject syntax to fail")
	}
}

func TestSubjectURAForInvocationRejectsInvalidShortPrincipal(t *testing.T) {
	if _, err := SubjectURAForInvocation(
		"alice payload",
		testAbilityRef,
		testCallerURA,
	); err == nil {
		t.Fatalf("expected invalid short principal to fail")
	}
}

func TestSidecarTransportRequiresSigningForOrdinaryInvocation(t *testing.T) {
	transport := &SidecarTransport{}
	_, err := transport.CallRaw(context.Background(), testAbilityRef, Payload{}, CallOptions{})
	if err == nil {
		t.Fatalf("expected unsigned transport to fail before ordinary invocation")
	}
	if !strings.Contains(err.Error(), "requires signing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractResultPayloadRequiresObjectResultJSON(t *testing.T) {
	out, err := extractResultPayload(Payload{"result_json": map[string]any{"ok": true}})
	if err != nil {
		t.Fatalf("unexpected extract error: %v", err)
	}
	if out["ok"] != true {
		t.Fatalf("unexpected extracted payload: %#v", out)
	}
	if _, err := extractResultPayload(Payload{"result_json": []any{"chunk"}}); err == nil {
		t.Fatalf("expected non-object result_json to fail for Call")
	}
}
