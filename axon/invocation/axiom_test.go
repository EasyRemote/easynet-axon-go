package axon

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
)

const testDescriptorHashHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func sampleEnv() InvocationEnvelope {
	var args [32]byte
	copy(args[:], mustHex("44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"))
	var nonce [16]byte
	copy(nonce[:], mustHex("11111111111111111111111111111111"))
	return InvocationEnvelope{
		Caller:          NewAgentIdentity("easynet:///r/silan/agents/researcher@1.0", ProfileStrictV2),
		Callee:          NewAgentIdentity("easynet:///r/openai/agents/reviewer@1.0", ProfileStrictV2),
		Subject:         NewSubjectIdentity("easynet:///r/silan/resource/papers.paper@draft-1", ProfileStrictV2),
		Ability:         "easynet:///r/openai/ability/openai.reviewer.review@descriptor.v1#" + testDescriptorHashHex + "!invoke",
		ArgsDigest:      args,
		InvocationNonce: nonce,
		CausalContext:   CausalNoneCtx(),
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func mustDescriptorBoundEnvelope(t *testing.T, env InvocationEnvelope) DescriptorBoundEnvelope {
	t.Helper()
	bound, err := NewDescriptorBoundEnvelope(env)
	if err != nil {
		t.Fatalf("descriptor-bound envelope: %v", err)
	}
	return bound
}

func mustAxiomDescriptorBoundDraft(t *testing.T, env InvocationEnvelope) DescriptorBoundInvocationDraft {
	t.Helper()
	draft, err := NewDescriptorBoundInvocationDraft(mustDescriptorBoundEnvelope(t, env))
	if err != nil {
		t.Fatalf("descriptor-bound draft: %v", err)
	}
	return draft
}

func mustDescriptorBoundInvocationBytes(t *testing.T, env InvocationEnvelope) []byte {
	t.Helper()
	bytes, err := mustAxiomDescriptorBoundDraft(t, env).CanonicalBytes()
	if err != nil {
		t.Fatalf("descriptor-bound canonical bytes: %v", err)
	}
	return bytes
}

func signDescriptorBoundTestInvocation(t *testing.T, sk ed25519.PrivateKey, env InvocationEnvelope, keyIDHint string) CallerSignature {
	t.Helper()
	sig, err := mustAxiomDescriptorBoundDraft(t, env).SignCallerSignature(sk, keyIDHint)
	if err != nil {
		t.Fatalf("descriptor-bound sign: %v", err)
	}
	return sig
}

func verifyDescriptorBoundTestInvocation(t *testing.T, env InvocationEnvelope, sig CallerSignature, resolver KeyResolver) error {
	t.Helper()
	return mustAxiomDescriptorBoundDraft(t, env).VerifyCallerSignature(sig, resolver)
}

// ── 1. Cross-language parity pin for axiom-e1 ──────────────────────────

func TestCanonicalCausalMatchesRustAndPython_NonePin(t *testing.T) {
	b := CanonicalCausalContext(CausalNoneCtx())
	h := Sha256(b)
	got := hex.EncodeToString(h[:])
	const expected = "6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d"
	if got != expected {
		t.Fatalf("causal-none pin mismatch\n  got:      %s\n  expected: %s", got, expected)
	}
}

// ── 2. Structural guarantees ──────────────────────────────────────────

func TestCanonicalInvocationIsDeterministic(t *testing.T) {
	env := sampleEnv()
	a := mustDescriptorBoundInvocationBytes(t, env)
	b := mustDescriptorBoundInvocationBytes(t, env)
	if string(a) != string(b) {
		t.Fatalf("non-deterministic encoder")
	}
	if len(a) == 0 {
		t.Fatalf("empty canonical bytes")
	}
}

func TestAbilityDescriptorRefCanonicalizesComponents(t *testing.T) {
	raw := " easynet:///r/test/ability/authority.review.deep.namespace @ descriptor.v1 # 0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF ! invoke "
	canonical, err := CanonicalAbilityDescriptorRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "easynet:///r/test/ability/authority.review.deep.namespace@descriptor.v1#"+testDescriptorHashHex+"!invoke" {
		t.Fatalf("canonical descriptor ref = %q", canonical)
	}
	abilityURA, err := AbilityURAFromDescriptorRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	if abilityURA != "easynet:///r/test/ability/authority.review.deep.namespace" {
		t.Fatalf("ability ura = %q", abilityURA)
	}
	if _, err := CanonicalAbilityDescriptorRef("easynet:///r/test/ability/authority.review@descriptor@v1"); err == nil || !strings.Contains(err.Error(), "ability_descriptor_ref_malformed") {
		t.Fatalf("expected malformed descriptor ref, got %v", err)
	}
	if _, err := CanonicalAbilityDescriptorRef("easynet:///r/test/ability/authority.review.deep@descriptor.v1"); err == nil || !strings.Contains(err.Error(), "ability_descriptor_ref_digest_missing_or_malformed") {
		t.Fatalf("expected missing digest descriptor ref, got %v", err)
	}
}

func TestNonceIsPartOfSignedBytes(t *testing.T) {
	a := sampleEnv()
	b := sampleEnv()
	b.InvocationNonce[0] ^= 1
	if string(mustDescriptorBoundInvocationBytes(t, a)) == string(mustDescriptorBoundInvocationBytes(t, b)) {
		t.Fatalf("nonce must enter canonical bytes")
	}
}

func TestSubjectIsPartOfSignedBytes(t *testing.T) {
	a := sampleEnv()
	b := sampleEnv()
	b.Subject = NewSubjectIdentity("easynet:///r/other/resource/x", ProfileStrictV2)
	if string(mustDescriptorBoundInvocationBytes(t, a)) == string(mustDescriptorBoundInvocationBytes(t, b)) {
		t.Fatalf("subject must enter canonical bytes")
	}
}

func TestProfileIsPartOfSignedBytes(t *testing.T) {
	a := sampleEnv()
	b := sampleEnv()
	b.Caller.Profile = ProfileWebSafeV2
	if string(mustDescriptorBoundInvocationBytes(t, a)) == string(mustDescriptorBoundInvocationBytes(t, b)) {
		t.Fatalf("profile must enter canonical bytes (anti-profile-confusion)")
	}
}

// ── 3. All four causal forms byte-distinct ────────────────────────────

func TestCausalFormsAreDistinct(t *testing.T) {
	var h32 [32]byte
	for i := range h32 {
		h32[i] = 0xAA
	}
	r := ReceiptRef{ReceiptHash: h32, ReceiptURA: "u"}
	none := CanonicalCausalContext(CausalNoneCtx())
	scalar := CanonicalCausalContext(CausalScalarCtx(r))
	lst := CanonicalCausalContext(CausalListCtx([]ReceiptRef{r}))
	var mroot [32]byte
	for i := range mroot {
		mroot[i] = 0xBB
	}
	merkle := CanonicalCausalContext(CausalMerkleCtx(mroot, "u"))

	if none[0] != 0x00 || scalar[0] != 0x01 || lst[0] != 0x02 || merkle[0] != 0x03 {
		t.Fatalf("tag bytes wrong: %x %x %x %x", none[0], scalar[0], lst[0], merkle[0])
	}
	for i, a := range [][]byte{none, scalar, lst, merkle} {
		for j, b := range [][]byte{none, scalar, lst, merkle} {
			if i != j && string(a) == string(b) {
				t.Fatalf("causal forms collide: %d=%d", i, j)
			}
		}
	}
}

// ── 4. Sign / verify round-trip + negative cases ──────────────────────

type fixedResolver struct{ key ed25519.PublicKey }

func (r *fixedResolver) Resolve(ura string) (ed25519.PublicKey, error) { return r.key, nil }

func TestSignVerifyRoundtrip(t *testing.T) {
	var seed [32]byte
	for i := range seed {
		seed[i] = 0x42
	}
	sk, err := SigningKeyFromBytes(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	vk := sk.Public().(ed25519.PublicKey)
	env := sampleEnv()
	sig := signDescriptorBoundTestInvocation(t, sk, env, "")
	if err := verifyDescriptorBoundTestInvocation(t, env, sig, &fixedResolver{key: vk}); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

func TestTamperedNonceFailsVerification(t *testing.T) {
	var seed [32]byte
	seed[0] = 1
	sk, _ := SigningKeyFromBytes(seed[:])
	vk := sk.Public().(ed25519.PublicKey)
	env := sampleEnv()
	sig := signDescriptorBoundTestInvocation(t, sk, env, "")
	env.InvocationNonce[0] ^= 1
	err := verifyDescriptorBoundTestInvocation(t, env, sig, &fixedResolver{key: vk})
	if err == nil {
		t.Fatalf("tampered nonce should not verify")
	}
	if !strings.Contains(err.Error(), "caller_signature_invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnsupportedAlgorithmRejected(t *testing.T) {
	var seed [32]byte
	sk, _ := SigningKeyFromBytes(seed[:])
	vk := sk.Public().(ed25519.PublicKey)
	env := sampleEnv()
	sig := signDescriptorBoundTestInvocation(t, sk, env, "")
	sig.Algorithm = "rsa-pkcs1"
	err := verifyDescriptorBoundTestInvocation(t, env, sig, &fixedResolver{key: vk})
	if err == nil || !strings.Contains(err.Error(), "unsupported_algorithm") {
		t.Fatalf("expected unsupported_algorithm, got %v", err)
	}
}

func TestFreshNonceIsNonDegenerate(t *testing.T) {
	a := FreshNonce()
	b := FreshNonce()
	if a == b {
		t.Fatalf("two CSPRNG nonces collided")
	}
	allZero := true
	for _, x := range a {
		if x != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatalf("nonce is all-zero")
	}
}

func TestReceiptSignVerifyRoundtrip(t *testing.T) {
	var seed [32]byte
	seed[0] = 9
	sk, _ := SigningKeyFromBytes(seed[:])
	vk := sk.Public().(ed25519.PublicKey)

	env := sampleEnv()
	authority := SelfAuthority(env.Caller.URA)
	r := ReceiptBody{
		Index:            0,
		InvocationID:     "inv-1",
		ReceiptType:      "admitted",
		State:            "ADMITTED",
		TimestampUnixMs:  1,
		PrevReceiptHash:  [32]byte{},
		PayloadDigest:    Sha256(nil),
		Reason:           "",
		CleanupComplete:  false,
		CallerBinding:    env.Caller,
		CalleeBinding:    env.Callee,
		SubjectBinding:   env.Subject,
		InvocationNonce:  env.InvocationNonce,
		CausalBinding:    env.CausalContext,
		AbilityBinding:   env.Ability,
		AuthorityBinding: AuthorityOrBootstrapFromBinding(authority),
		ProofFacts:       receiptFixtureProofFacts(t, env.Callee, env.Subject, env.CausalContext, Sha256(nil), Sha256(nil), authority),
	}
	signingAuthority, err := NewEd25519ReceiptSigningAuthority(env.Callee, sk, "")
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signingAuthority.SignAndVerify(CanonicalReceiptBytes(r))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyReceiptSignature(r, sig, &fixedResolver{key: vk}); err != nil {
		t.Fatalf("receipt verify failed: %v", err)
	}
	// Tamper the causal binding.
	var h [32]byte
	h[0] = 0x99
	r.CausalBinding = CausalScalarCtx(ReceiptRef{ReceiptHash: h, ReceiptURA: "evil"})
	if err := VerifyReceiptSignature(r, sig, &fixedResolver{key: vk}); err == nil {
		t.Fatalf("tampered causal_binding should break receipt verify")
	}
}
