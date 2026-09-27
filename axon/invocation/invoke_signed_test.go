package axon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
)

func silanAgent() AgentIdentity {
	return NewAgentIdentity("easynet:///r/silan/agents/t@1", ProfileStrictV2)
}

func openaiAgent() AgentIdentity {
	return NewAgentIdentity("easynet:///r/openai/agents/t@1", ProfileStrictV2)
}

func signedSubject() SubjectIdentity {
	return NewSubjectIdentity("easynet:///r/silan/resource/silan.papers/p", ProfileStrictV2)
}

const signedAbilityURA = "easynet:///r/openai/ability/authority.demo.echo"
const signedAbilityRef = signedAbilityURA + "@descriptor.v1#" + testDescriptorHashHex + "!invoke"
const unknownSignedAbilityRef = "easynet:///r/openai/ability/authority.demo.missing@descriptor.v1#" + testDescriptorHashHex + "!invoke"

func echoRuntime(t *testing.T, key ed25519.PrivateKey) *LocalRuntime {
	t.Helper()
	runtime := mustTestRuntime(t, key)
	mustRegisterTestAbility(t, runtime, signedAbilityURA, func(_ context.Context, context *AbilityContext) ([]byte, *AxonError) {
		return context.Payload, nil
	})
	return runtime
}

func mustDescriptorBound(t *testing.T, envelope InvocationEnvelope) DescriptorBoundEnvelope {
	t.Helper()
	bound, err := NewDescriptorBoundEnvelope(envelope)
	if err != nil {
		t.Fatalf("descriptor-bound envelope: %v", err)
	}
	return bound
}

func mustDescriptorBoundDraft(t *testing.T, envelope InvocationEnvelope) DescriptorBoundInvocationDraft {
	t.Helper()
	draft, err := NewDescriptorBoundInvocationDraft(mustDescriptorBound(t, envelope))
	if err != nil {
		t.Fatalf("descriptor-bound draft: %v", err)
	}
	return draft
}

func mustDescriptorBoundBytes(t *testing.T, envelope InvocationEnvelope) []byte {
	t.Helper()
	canonical, err := mustDescriptorBoundDraft(t, envelope).CanonicalBytes()
	if err != nil {
		t.Fatalf("descriptor-bound canonical bytes: %v", err)
	}
	return canonical
}

func mustSignedRequest(
	t *testing.T,
	key ed25519.PrivateKey,
	subject SubjectIdentity,
	ability string,
	payload []byte,
	causal CausalContext,
	nonce [16]byte,
) DescriptorBoundInvocationRequest {
	t.Helper()
	envelope := mustDescriptorBound(t, InvocationEnvelope{
		Caller:          silanAgent(),
		Callee:          openaiAgent(),
		Subject:         subject,
		Ability:         ability,
		ArgsDigest:      Sha256(payload),
		InvocationNonce: nonce,
		CausalContext:   causal,
	})
	request, err := SignDescriptorBoundInvocationRequest(CallModeRPC, envelope, key, payload, "")
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func invokeSignedRequest(
	t *testing.T,
	runtime *LocalRuntime,
	request DescriptorBoundInvocationRequest,
) (*InvocationHandle, SignedEnvelope, *AxonError) {
	t.Helper()
	return runtime.InvokeDescriptorBoundRequest(context.Background(), request)
}

func terminalReceipt(t *testing.T, handle *InvocationHandle) SignedInvocationReceipt {
	t.Helper()
	receipts := handle.SnapshotReceipts()
	if len(receipts) == 0 {
		t.Fatalf("runtime must emit receipt chain")
	}
	return receipts[len(receipts)-1]
}

func assertNonZeroHash(t *testing.T, hash [32]byte, name string) {
	t.Helper()
	if hash == ([32]byte{}) {
		t.Fatalf("%s must be non-zero", name)
	}
}

func TestDescriptorBoundRequestExplicitSubjectAndNonceVerifies(t *testing.T) {
	key := testSigningKey(t)
	runtime := echoRuntime(t, key)
	subject := signedSubject()
	var nonce [16]byte
	for index := range nonce {
		nonce[index] = 0xab
	}
	request := mustSignedRequest(t, key, subject, signedAbilityRef, []byte("hi"), CausalNoneCtx(), nonce)
	canonical, err := request.CanonicalBytes()
	if err != nil {
		t.Fatalf("request canonical bytes: %v", err)
	}
	requestDraft, err := NewDescriptorBoundInvocationDraft(request.Envelope())
	if err != nil {
		t.Fatalf("request draft: %v", err)
	}
	requestDraftCanonical, err := requestDraft.CanonicalBytes()
	if err != nil {
		t.Fatalf("request draft canonical bytes: %v", err)
	}
	if !bytes.Equal(canonical, requestDraftCanonical) {
		t.Fatalf("request canonical bytes must match descriptor-bound envelope bytes")
	}
	if err := requestDraft.VerifyCallerSignature(
		request.Signature(),
		&staticResolver{key: key.Public().(ed25519.PublicKey)},
	); err != nil {
		t.Fatalf("draft-owned signature verification: %v", err)
	}

	handle, signed, axonErr := invokeSignedRequest(t, runtime, request)
	if axonErr != nil {
		t.Fatalf("invoke descriptor-bound request: %v", axonErr)
	}
	_ = handle.Wait(context.Background())

	stored, ok := runtime.AxiomEnvelopeOf(handle.InvocationID())
	if !ok {
		t.Fatal("envelope not stored")
	}
	if stored.Envelope.Subject != subject || stored.Envelope.InvocationNonce != nonce {
		t.Fatal("stored envelope diverged")
	}
	if err := mustDescriptorBoundDraft(t, signed.Envelope).VerifyCallerSignature(
		signed.Signature,
		&staticResolver{key: key.Public().(ed25519.PublicKey)},
	); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !bytes.Equal(mustDescriptorBoundBytes(t, signed.Envelope), mustDescriptorBoundBytes(t, stored.Envelope)) {
		t.Fatal("canonical bytes diverged after store")
	}

	facts := terminalReceipt(t, handle).AxiomBinding().ProofFacts
	if facts.SubjectRef == nil || facts.SubjectRef.URA != subject.URA {
		t.Fatalf("subject proof fact mismatch: %+v", facts.SubjectRef)
	}
	if facts.DescriptorVersion != "descriptor.v1" || facts.RuntimeEnv != "canonical-go-test-runtime-v1" {
		t.Fatalf("descriptor proof facts mismatch: %+v", facts)
	}
	if facts.InputHash != Sha256([]byte("hi")) || facts.OutputHash != Sha256([]byte("hi")) {
		t.Fatal("input/output hash mismatch")
	}
	assertNonZeroHash(t, facts.SchemaHash, "schema_hash")
	assertNonZeroHash(t, facts.ImplHash, "impl_hash")
	if facts.AuthorityProof.Binding == nil ||
		facts.AuthorityProof.Binding.Binding == nil ||
		facts.AuthorityProof.Binding.Binding.Authority.URA != silanAgent().URA {
		t.Fatalf("authority binding mismatch: %+v", facts.AuthorityProof.Binding)
	}
}

func TestDescriptorBoundEnvelopeRejectsMissingSubject(t *testing.T) {
	envelope := InvocationEnvelope{
		Caller:          silanAgent(),
		Callee:          openaiAgent(),
		Ability:         signedAbilityRef,
		ArgsDigest:      Sha256(nil),
		InvocationNonce: FreshNonce(),
		CausalContext:   CausalNoneCtx(),
	}
	if _, err := NewDescriptorBoundEnvelope(envelope); err == nil {
		t.Fatal("missing subject must not construct a descriptor-bound envelope")
	}
}

func TestDescriptorBoundRequestRejectsUnknownAbility(t *testing.T) {
	key := testSigningKey(t)
	runtime := echoRuntime(t, key)
	request := mustSignedRequest(t, key, signedSubject(), unknownSignedAbilityRef, nil, CausalNoneCtx(), FreshNonce())
	_, _, err := invokeSignedRequest(t, runtime, request)
	if err == nil || !strings.Contains(err.Error(), "unknown_ability") {
		t.Fatalf("expected unknown ability, got %v", err)
	}
}

func TestDescriptorBoundRequestRejectsPayloadDigestMismatch(t *testing.T) {
	key := testSigningKey(t)
	runtime := echoRuntime(t, key)
	request := mustSignedRequest(t, key, signedSubject(), signedAbilityRef, []byte("signed"), CausalNoneCtx(), FreshNonce())
	request.payload = []byte("tampered")
	_, _, err := invokeSignedRequest(t, runtime, request)
	if err == nil || err.Reason != ReasonEnvelopeIncomplete || err.Message != "args_digest_payload_mismatch" {
		t.Fatalf("expected payload mismatch, got %v", err)
	}
}

func TestDescriptorBoundRequestRejectsWrongResolverKey(t *testing.T) {
	callerKey := testSigningKey(t)
	var otherSeed [32]byte
	otherSeed[0] = 0x7f
	otherKey, _ := SigningKeyFromBytes(otherSeed[:])
	runtime := echoRuntime(t, otherKey)
	request := mustSignedRequest(t, callerKey, signedSubject(), signedAbilityRef, nil, CausalNoneCtx(), FreshNonce())
	_, _, err := invokeSignedRequest(t, runtime, request)
	if err == nil || err.Reason != ReasonCallerSignatureInvalid {
		t.Fatalf("expected signature rejection, got %v", err)
	}
}

func TestDescriptorBoundRequestRejectsReplay(t *testing.T) {
	key := testSigningKey(t)
	runtime := echoRuntime(t, key)
	request := mustSignedRequest(t, key, signedSubject(), signedAbilityRef, nil, CausalNoneCtx(), FreshNonce())
	handle, _, err := invokeSignedRequest(t, runtime, request)
	if err != nil {
		t.Fatal(err)
	}
	_ = handle.Wait(context.Background())
	if _, _, err = invokeSignedRequest(t, runtime, request); err == nil || err.Reason != ReasonNonceReplay {
		t.Fatalf("expected nonce replay, got %v", err)
	}
}

func TestDescriptorBoundRequestBindsSubjectAndCausalContext(t *testing.T) {
	key := testSigningKey(t)
	runtime := echoRuntime(t, key)
	request := mustSignedRequest(t, key, signedSubject(), signedAbilityRef, nil, CausalNoneCtx(), FreshNonce())
	_, signed, err := invokeSignedRequest(t, runtime, request)
	if err != nil {
		t.Fatal(err)
	}

	tampered := signed.Envelope
	tampered.Subject = NewSubjectIdentity("easynet:///r/mallory/resource/evil", ProfileStrictV2)
	if err := mustDescriptorBoundDraft(t, tampered).VerifyCallerSignature(
		signed.Signature,
		&staticResolver{key: key.Public().(ed25519.PublicKey)},
	); err == nil {
		t.Fatal("tampered subject must fail verification")
	}
}

func TestDescriptorBoundRequestRejectsUnsupportedCallMode(t *testing.T) {
	key := testSigningKey(t)
	runtime := echoRuntime(t, key)
	request := mustSignedRequest(t, key, signedSubject(), signedAbilityRef, nil, CausalNoneCtx(), FreshNonce())
	request.callMode = CallModeStream
	_, _, err := invokeSignedRequest(t, runtime, request)
	if err == nil || err.Reason != "descriptor_bound_request_mode_unsupported" {
		t.Fatalf("expected mode rejection, got %v", err)
	}
}

func TestDescriptorBoundRequestOwnsMutableInputs(t *testing.T) {
	key := testSigningKey(t)
	payload := []byte("payload")
	envelope := mustDescriptorBound(t, InvocationEnvelope{
		Caller:          silanAgent(),
		Callee:          openaiAgent(),
		Subject:         signedSubject(),
		Ability:         signedAbilityRef,
		ArgsDigest:      Sha256(payload),
		InvocationNonce: FreshNonce(),
		CausalContext: CausalListCtx([]ReceiptRef{{
			ReceiptURA: "easynet:///r/test/receipt/parent",
		}}),
	})
	signature, err := mustDescriptorBoundDraft(t, envelope.Envelope()).SignCallerSignature(key, "")
	if err != nil {
		t.Fatal(err)
	}
	spec := &SupervisorSpec{Tags: map[string]string{"owner": "caller"}}
	request := NewDescriptorBoundInvocationRequest(CallModeRPC, envelope, signature, payload).WithSupervisor(spec)

	payload[0] = 'X'
	signature.Signature[0] ^= 0xff
	spec.Tags["owner"] = "mutated"
	gotPayload := request.Payload()
	gotSignature := request.Signature()
	gotSpec := request.SupervisorSpec()
	if string(gotPayload) != "payload" || gotSignature.Signature[0] == signature.Signature[0] {
		t.Fatal("request retained caller-owned payload or signature storage")
	}
	if gotSpec == nil || gotSpec.Tags["owner"] != "caller" {
		t.Fatal("request retained caller-owned supervisor storage")
	}

	gotPayload[0] = 'Y'
	gotSignature.Signature[0] ^= 0xff
	gotSpec.Tags["owner"] = "accessor"
	if string(request.Payload()) != "payload" || request.SupervisorSpec().Tags["owner"] != "caller" {
		t.Fatal("request accessor exposed mutable internal storage")
	}
}

func TestNewLocalRuntimeRequiresResolver(t *testing.T) {
	key := testSigningKey(t)
	provider := mustTestRuntime(t, key).receiptProvider
	runtime, err := NewLocalRuntime(nil, provider)
	if runtime != nil || err == nil || err.Reason != ReasonCallerSignatureInvalid || err.Message != "key_resolver_required" {
		t.Fatalf("expected resolver-required construction failure, got runtime=%v err=%v", runtime, err)
	}
}

func TestNewLocalRuntimeRequiresReceiptProvider(t *testing.T) {
	key := testSigningKey(t)
	runtime, err := NewLocalRuntime(
		&fixedKeyResolver{key: key.Public().(ed25519.PublicKey)},
		nil,
	)
	if runtime != nil || err == nil || err.Reason != "canonical_receipt_provider_not_configured" {
		t.Fatalf("expected provider-required construction failure, got runtime=%v err=%v", runtime, err)
	}
}

func TestAxiomEnvelopeOfUnknownIsAbsent(t *testing.T) {
	key := testSigningKey(t)
	runtime := echoRuntime(t, key)
	if _, ok := runtime.AxiomEnvelopeOf("never-created"); ok {
		t.Fatal("expected absent envelope")
	}
}
