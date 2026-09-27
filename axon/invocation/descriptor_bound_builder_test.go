package axon

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"
)

func TestDescriptorBoundInvocationBuilderOwnsCanonicalEnvelope(t *testing.T) {
	payload := []byte(`{"city":"Singapore"}`)
	draft := mustDescriptorBoundBuilderDraft(t, payload)

	envelope := draft.Envelope().Envelope()
	if envelope.Ability != descriptorBoundBuilderRef() {
		t.Fatalf("descriptor ref = %q", envelope.Ability)
	}
	if envelope.ArgsDigest != Sha256(payload) {
		t.Fatalf("args digest = %x, want %x", envelope.ArgsDigest, Sha256(payload))
	}
	canonical, err := draft.CanonicalBytes()
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	if len(canonical) == 0 {
		t.Fatal("canonical bytes are empty")
	}

	payload[0] = '!'
	if got := draft.Payload(); string(got) != `{"city":"Singapore"}` {
		t.Fatalf("draft payload mutated through caller input: %q", got)
	}
}

func TestEnvelopeOnlyDraftCannotBindRuntimeRequest(t *testing.T) {
	payload := []byte(`{"city":"Singapore"}`)
	payloadBound := mustDescriptorBoundBuilderDraft(t, payload)
	envelopeOnly, err := NewDescriptorBoundInvocationDraft(payloadBound.Envelope())
	if err != nil {
		t.Fatalf("NewDescriptorBoundInvocationDraft: %v", err)
	}
	signature := CallerSignature{
		Algorithm: "ed25519",
		Signature: bytes.Repeat([]byte{0x5a}, ed25519.SignatureSize),
		KeyIDHint: "caller-key",
	}
	if _, err := envelopeOnly.BindCallerSignature(CallModeRPC, signature); err == nil ||
		!strings.Contains(err.Error(), "descriptor_bound_payload_required") {
		t.Fatalf("envelope-only draft must reject request binding, got %v", err)
	}
}

func TestDescriptorBoundWireProjectionUsesOnePublicRouteForEveryCallMode(t *testing.T) {
	draft := mustDescriptorBoundBuilderDraft(t, []byte(`{"city":"Singapore"}`))
	signature := CallerSignature{
		Algorithm: "ed25519",
		Signature: bytes.Repeat([]byte{0x5a}, ed25519.SignatureSize),
		KeyIDHint: "caller-key",
	}
	builder := NewDescriptorBoundWireProjectionBuilder()

	for _, callMode := range []CallMode{CallModeRPC, CallModeStream, CallModeBidi} {
		request, err := draft.BindCallerSignature(callMode, signature)
		if err != nil {
			t.Fatalf("BindCallerSignature(%s): %v", callMode, err)
		}
		projection, err := builder.Build(request, DescriptorBoundWireOptions{
			ContentType:    "application/json",
			TimeoutSeconds: 2,
			Metadata:       map[string]string{"trace_id": "builder-test"},
		})
		if err != nil {
			t.Fatalf("Build(%s): %v", callMode, err)
		}
		if projection.RouteName() != "er.weather" {
			t.Fatalf("%s route = %q, want er.weather", callMode, projection.RouteName())
		}
		if projection.DescriptorRef() != descriptorBoundBuilderRef() {
			t.Fatalf("%s descriptor ref = %q", callMode, projection.DescriptorRef())
		}
		if !strings.HasPrefix(projection.Envelope().RequestID(), "req-") ||
			len(projection.Envelope().RequestID()) != len("req-")+32 {
			t.Fatalf("%s request id = %q", callMode, projection.Envelope().RequestID())
		}
		if projection.ContentEncoding() != WireContentEncodingIdentity {
			t.Fatalf("%s content encoding = %q", callMode, projection.ContentEncoding())
		}
	}
}

func TestDescriptorBoundWireProjectionRejectsInvalidDispatchFacts(t *testing.T) {
	draft := mustDescriptorBoundBuilderDraft(t, []byte("payload"))
	validSignature := CallerSignature{
		Algorithm: "ed25519",
		Signature: bytes.Repeat([]byte{0x7b}, ed25519.SignatureSize),
	}
	builder := NewDescriptorBoundWireProjectionBuilder()

	tests := []struct {
		name    string
		request DescriptorBoundInvocationRequest
		options DescriptorBoundWireOptions
	}{
		{
			name: "invalid signature",
			request: NewDescriptorBoundInvocationRequest(
				CallModeRPC,
				draft.Envelope(),
				CallerSignature{Algorithm: "ed25519", Signature: []byte("short")},
				draft.Payload(),
			),
			options: validWireOptions(),
		},
		{
			name: "payload digest mismatch",
			request: NewDescriptorBoundInvocationRequest(
				CallModeRPC,
				draft.Envelope(),
				validSignature,
				[]byte("different"),
			),
			options: validWireOptions(),
		},
		{
			name: "invalid call mode",
			request: NewDescriptorBoundInvocationRequest(
				CallMode("unknown"),
				draft.Envelope(),
				validSignature,
				draft.Payload(),
			),
			options: validWireOptions(),
		},
		{
			name: "missing content type",
			request: NewDescriptorBoundInvocationRequest(
				CallModeRPC,
				draft.Envelope(),
				validSignature,
				draft.Payload(),
			),
			options: DescriptorBoundWireOptions{TimeoutSeconds: 1},
		},
		{
			name: "invalid timeout",
			request: NewDescriptorBoundInvocationRequest(
				CallModeRPC,
				draft.Envelope(),
				validSignature,
				draft.Payload(),
			),
			options: DescriptorBoundWireOptions{ContentType: "application/json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := builder.Build(test.request, test.options); err == nil {
				t.Fatal("Build accepted invalid dispatch facts")
			}
		})
	}
}

func TestDescriptorBoundWireProjectionRejectsDescriptorOwnedByDifferentCallee(t *testing.T) {
	draft, err := NewDescriptorBoundInvocationBuilder().
		WithCaller(NewAgentIdentity("easynet:///r/example/agent/alice", ProfileStrictV2)).
		WithCallee(NewAgentIdentity("easynet:///r/example/device/dev-b", ProfileStrictV2)).
		WithSubject(NewSubjectIdentity("easynet:///r/example/device/dev-b", ProfileStrictV2)).
		WithDescriptorRef(descriptorBoundBuilderRef()).
		WithNonce([16]byte{1}).
		WithCausalContext(CausalNoneCtx()).
		WithPayload([]byte("payload")).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	request, err := draft.BindCallerSignature(CallModeRPC, CallerSignature{
		Algorithm: "ed25519",
		Signature: bytes.Repeat([]byte{0x3c}, ed25519.SignatureSize),
	})
	if err != nil {
		t.Fatalf("BindCallerSignature: %v", err)
	}
	if _, err := NewDescriptorBoundWireProjectionBuilder().Build(request, validWireOptions()); err == nil {
		t.Fatal("wire projection accepted descriptor owned by another callee")
	}
}

func TestDescriptorBoundWireProjectionIsImmutable(t *testing.T) {
	draft := mustDescriptorBoundBuilderDraft(t, []byte("payload"))
	signature := CallerSignature{
		Algorithm: "ed25519",
		Signature: bytes.Repeat([]byte{0x6d}, ed25519.SignatureSize),
		KeyIDHint: "caller-key",
	}
	request, err := draft.BindCallerSignature(CallModeRPC, signature)
	if err != nil {
		t.Fatalf("BindCallerSignature: %v", err)
	}
	metadata := map[string]string{"trace_id": "immutable"}
	projection, err := NewDescriptorBoundWireProjectionBuilder().Build(
		request,
		DescriptorBoundWireOptions{
			ContentType:    "application/json",
			TimeoutSeconds: 1,
			Metadata:       metadata,
		},
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	metadata["trace_id"] = "mutated"
	payload := projection.Payload()
	payload[0] = '!'
	canonical := projection.CanonicalBytes()
	canonical[0] ^= 0xff
	projectedSignature := projection.Envelope().CallerSignature()
	projectedSignature.Signature[0] ^= 0xff

	expectedCanonical, err := draft.CanonicalBytes()
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	if got := projection.Metadata()["trace_id"]; got != "immutable" {
		t.Fatalf("metadata mutated through input: %q", got)
	}
	if got := string(projection.Payload()); got != "payload" {
		t.Fatalf("payload mutated through accessor: %q", got)
	}
	if !bytes.Equal(projection.CanonicalBytes(), expectedCanonical) {
		t.Fatal("canonical bytes mutated through accessor")
	}
	if got := projection.Envelope().CallerSignature().Signature[0]; got != 0x6d {
		t.Fatalf("caller signature mutated through accessor: %x", got)
	}
}

func mustDescriptorBoundBuilderDraft(t *testing.T, payload []byte) DescriptorBoundInvocationDraft {
	t.Helper()
	draft, err := NewDescriptorBoundInvocationBuilder().
		WithCaller(NewAgentIdentity("easynet:///r/example/user/alice", ProfileStrictV2)).
		WithCallee(NewAgentIdentity("easynet:///r/example/agent/device.dev-a.ability-management", ProfileStrictV2)).
		WithSubject(NewSubjectIdentity("easynet:///r/example/device/dev-a", ProfileStrictV2)).
		WithDescriptorRef(descriptorBoundBuilderRef()).
		WithNonce([16]byte{1, 2, 3, 4}).
		WithCausalContext(CausalNoneCtx()).
		WithPayload(payload).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return draft
}

func descriptorBoundBuilderRef() string {
	return "easynet:///r/example/ability/system-agent.dev-a.ability-management.er.weather@1.0.0#" +
		strings.Repeat("ab", 32) + "!invoke"
}

func validWireOptions() DescriptorBoundWireOptions {
	return DescriptorBoundWireOptions{
		ContentType:    "application/json",
		TimeoutSeconds: 1,
	}
}
