package axon

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type admissionVector struct {
	ID     string `json:"id"`
	Expect struct {
		ReasonStringsLiteral       bool `json:"reason_strings_are_literal_RFC_5_2_text"`
		PipelineOrdering           bool `json:"pipeline_ordering_enforced"`
		ReplayStoreNotPolluted     bool `json:"replay_store_not_polluted_by_early_rejects"`
		DedupKeyIncludesAbility    bool `json:"dedup_key_includes_ability"`
		ResolverRequired           bool `json:"resolver_required"`
		LocalFastAbsent            bool `json:"local_fast_absent"`
		DescriptorRequestSoleInput bool `json:"descriptor_bound_request_is_sole_public_input"`
		CryptoVerifyAlwaysRuns     bool `json:"crypto_verify_always_runs"`
	} `json:"expect"`
}

func loadAdmissionVector(t *testing.T) admissionVector {
	t.Helper()
	path := conformancePath(t, "axiom", "axiom-admission-pipeline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var vector admissionVector
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	return vector
}

func TestAdmissionConformanceVectorRequiresCanonicalAuthorityModel(t *testing.T) {
	vector := loadAdmissionVector(t)
	if vector.ID != "axiom-admission-pipeline" {
		t.Fatalf("unexpected vector %q", vector.ID)
	}
	if !vector.Expect.ResolverRequired || !vector.Expect.LocalFastAbsent {
		t.Fatalf("authority contract not converged: %+v", vector.Expect)
	}
	if !vector.Expect.DescriptorRequestSoleInput || !vector.Expect.CryptoVerifyAlwaysRuns {
		t.Fatalf("runtime input contract not converged: %+v", vector.Expect)
	}
	if !vector.Expect.ReasonStringsLiteral || !vector.Expect.PipelineOrdering ||
		!vector.Expect.ReplayStoreNotPolluted || !vector.Expect.DedupKeyIncludesAbility {
		t.Fatalf("admission invariants incomplete: %+v", vector.Expect)
	}
}

func admSilan() AgentIdentity {
	return NewAgentIdentity("easynet:///r/silan/agents/t@1", ProfileStrictV2)
}

func admOpenai() AgentIdentity {
	return NewAgentIdentity("easynet:///r/openai/agents/t@1", ProfileStrictV2)
}

const admOtherAbilityURA = "easynet:///r/openai/ability/authority.demo.other"
const admOtherAbilityRef = admOtherAbilityURA + "@descriptor.v1#" + testDescriptorHashHex + "!invoke"

func admSampleEnv() InvocationEnvelope {
	var nonce [16]byte
	for index := range nonce {
		nonce[index] = 0x11
	}
	return InvocationEnvelope{
		Caller:          admSilan(),
		Callee:          admOpenai(),
		Subject:         signedSubject(),
		Ability:         signedAbilityRef,
		ArgsDigest:      Sha256(nil),
		InvocationNonce: nonce,
		CausalContext:   CausalNoneCtx(),
	}
}

func admSampleSig(env InvocationEnvelope) (CallerSignature, ed25519.PrivateKey) {
	var seed [32]byte
	for index := range seed {
		seed[index] = 0x42
	}
	key, _ := SigningKeyFromBytes(seed[:])
	draft, err := NewDescriptorBoundInvocationDraft(mustAdmDescriptorBound(env))
	if err != nil {
		panic(err)
	}
	signature, err := draft.SignCallerSignature(key, "")
	if err != nil {
		panic(err)
	}
	return signature, key
}

func mustAdmDescriptorBound(env InvocationEnvelope) DescriptorBoundEnvelope {
	bound, err := NewDescriptorBoundEnvelope(env)
	if err != nil {
		panic(err)
	}
	return bound
}

type fixedKeyResolver struct {
	key ed25519.PublicKey
}

func (f *fixedKeyResolver) Resolve(_ string) (ed25519.PublicKey, error) {
	return f.key, nil
}

func TestValidateEnvelopeRejectsIncompleteFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*InvocationEnvelope)
		detail string
	}{
		{"ability", func(env *InvocationEnvelope) { env.Ability = "" }, "ability_empty"},
		{"caller", func(env *InvocationEnvelope) { env.Caller.URA = "" }, "caller_ura_empty"},
		{"callee", func(env *InvocationEnvelope) { env.Callee.URA = "" }, "callee_ura_empty"},
		{"subject", func(env *InvocationEnvelope) { env.Subject.URA = "" }, "subject_ura_empty"},
		{"nonce", func(env *InvocationEnvelope) { env.InvocationNonce = [16]byte{} }, "invocation_nonce_all_zero"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			env := admSampleEnv()
			test.mutate(&env)
			err := validateEnvelope(env)
			if err == nil || err.Reason != ReasonEnvelopeIncomplete || !strings.Contains(err.Message, test.detail) {
				t.Fatalf("expected %s, got %v", test.detail, err)
			}
		})
	}
}

func TestValidateSignatureStructureIsEd25519Only(t *testing.T) {
	signature, _ := admSampleSig(admSampleEnv())
	if err := validateSignatureStructure(signature); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CallerSignature){
		func(sig *CallerSignature) { sig.Algorithm = "" },
		func(sig *CallerSignature) { sig.Algorithm = "rsa" },
		func(sig *CallerSignature) { sig.Signature = sig.Signature[:5] },
	} {
		candidate := signature
		mutate(&candidate)
		if err := validateSignatureStructure(candidate); err == nil || err.Reason != ReasonCallerSignatureInvalid {
			t.Fatalf("malformed signature accepted: %+v", candidate)
		}
	}
}

func TestRunDescriptorBoundAdmissionRequiresResolverBeforeReplay(t *testing.T) {
	env := admSampleEnv()
	signature, key := admSampleSig(env)
	replay := newNonceReplayStore()
	provider := mustTestRuntime(t, key).receiptProvider
	_, err := verifyDescriptorBoundAdmission(
		mustAdmDescriptorBound(env),
		signature,
		nil,
		replay,
		provider,
		1000,
	)
	if err == nil || err.Reason != ReasonCallerSignatureInvalid || err.Message != "key_resolver_required" {
		t.Fatalf("expected resolver-required rejection, got %v", err)
	}
	if replay.len() != 0 {
		t.Fatal("missing resolver polluted replay store")
	}
}

func TestRunDescriptorBoundAdmissionVerifiesBeforeReplay(t *testing.T) {
	env := admSampleEnv()
	signature, key := admSampleSig(env)
	var wrongSeed [32]byte
	wrongSeed[0] = 0x99
	wrongKey, _ := SigningKeyFromBytes(wrongSeed[:])
	replay := newNonceReplayStore()
	provider := mustTestRuntime(t, key).receiptProvider

	_, err := verifyDescriptorBoundAdmission(
		mustAdmDescriptorBound(env),
		signature,
		&fixedKeyResolver{key: wrongKey.Public().(ed25519.PublicKey)},
		replay,
		provider,
		1000,
	)
	if err == nil || err.Reason != ReasonCallerSignatureInvalid || replay.len() != 0 {
		t.Fatalf("wrong key did not fail closed: err=%v replay=%d", err, replay.len())
	}
	if _, err := verifyDescriptorBoundAdmission(
		mustAdmDescriptorBound(env),
		signature,
		&fixedKeyResolver{key: key.Public().(ed25519.PublicKey)},
		replay,
		provider,
		1000,
	); err != nil {
		t.Fatal(err)
	}
}

func TestReplayStoreDedupIncludesAbilityAndWindow(t *testing.T) {
	replay := newNonceReplayStore().withWindowMs(1)
	var nonce [16]byte
	nonce[0] = 1
	if err := replay.checkAndRecord("caller", "ability-a", nonce, 0); err != nil {
		t.Fatal(err)
	}
	if err := replay.checkAndRecord("caller", "ability-b", nonce, 0); err != nil {
		t.Fatal("ability must participate in dedup key")
	}
	if err := replay.checkAndRecord("caller", "ability-a", nonce, 0); err == nil || err.Reason != ReasonNonceReplay {
		t.Fatalf("expected replay rejection, got %v", err)
	}
	if err := replay.checkAndRecord("caller", "ability-a", nonce, 10); err != nil {
		t.Fatalf("expired nonce should be reusable: %v", err)
	}
}

func admRuntime(t *testing.T, key ed25519.PrivateKey) *LocalRuntime {
	t.Helper()
	runtime := mustTestRuntime(t, key)
	mustRegisterTestAbility(t, runtime, signedAbilityURA, func(_ context.Context, ability *AbilityContext) ([]byte, *AxonError) {
		return ability.Payload, nil
	})
	mustRegisterTestAbility(t, runtime, admOtherAbilityURA, func(_ context.Context, ability *AbilityContext) ([]byte, *AxonError) {
		return ability.Payload, nil
	})
	return runtime
}

func admRequest(
	t *testing.T,
	key ed25519.PrivateKey,
	ability string,
	nonce [16]byte,
) DescriptorBoundInvocationRequest {
	t.Helper()
	return mustSignedRequest(t, key, signedSubject(), ability, nil, CausalNoneCtx(), nonce)
}

func TestRuntimeRejectsAllZeroNonce(t *testing.T) {
	key := testSigningKey(t)
	runtime := admRuntime(t, key)
	request := admRequest(t, key, signedAbilityRef, [16]byte{})
	_, _, err := runtime.InvokeDescriptorBoundRequest(context.Background(), request)
	if err == nil || err.Reason != ReasonEnvelopeIncomplete || err.Message != "invocation_nonce_all_zero" {
		t.Fatalf("expected zero nonce rejection, got %v", err)
	}
}

func TestRuntimeRejectsWrongResolverKey(t *testing.T) {
	callerKey := testSigningKey(t)
	var wrongSeed [32]byte
	wrongSeed[0] = 0x22
	wrongKey, _ := SigningKeyFromBytes(wrongSeed[:])
	runtime := admRuntime(t, wrongKey)
	request := admRequest(t, callerKey, signedAbilityRef, FreshNonce())
	_, _, err := runtime.InvokeDescriptorBoundRequest(context.Background(), request)
	if err == nil || err.Reason != ReasonCallerSignatureInvalid {
		t.Fatalf("expected signature rejection, got %v", err)
	}
}

func TestRuntimeRejectsReplayedNonce(t *testing.T) {
	key := testSigningKey(t)
	runtime := admRuntime(t, key)
	request := admRequest(t, key, signedAbilityRef, FreshNonce())
	handle, _, err := runtime.InvokeDescriptorBoundRequest(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	_ = handle.Wait(context.Background())
	if _, _, err = runtime.InvokeDescriptorBoundRequest(context.Background(), request); err == nil || err.Reason != ReasonNonceReplay {
		t.Fatalf("expected nonce replay, got %v", err)
	}
}

func TestRuntimeDedupIsPerAbility(t *testing.T) {
	key := testSigningKey(t)
	runtime := admRuntime(t, key)
	nonce := FreshNonce()
	if _, _, err := runtime.InvokeDescriptorBoundRequest(context.Background(), admRequest(t, key, signedAbilityRef, nonce)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.InvokeDescriptorBoundRequest(context.Background(), admRequest(t, key, admOtherAbilityRef, nonce)); err != nil {
		t.Fatalf("per-ability dedup regressed: %v", err)
	}
}

func TestRuntimeDedupWindowCanBeReconfiguredWithoutChangingAuthority(t *testing.T) {
	key := testSigningKey(t)
	runtime := admRuntime(t, key)
	runtime.SetAdmissionDedupWindowMs(1)
	nonce := FreshNonce()
	request := admRequest(t, key, signedAbilityRef, nonce)
	if _, _, err := runtime.InvokeDescriptorBoundRequest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, _, err := runtime.InvokeDescriptorBoundRequest(context.Background(), request); err != nil {
		t.Fatalf("window override regressed: %v", err)
	}
}
