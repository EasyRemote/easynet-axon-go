package axon

// RFC 001 §8 — axiom-cross-language-verify-parity (Go producer).
//
// Go emits a signed receipt bundle on disk in the shared JSON format,
// then spawns an independently implemented verifier against it. Exit 0 +
// "chain verified" ⇒ Go's bundle is accepted by a third-party-language
// verifier. Mirrors sdk/python/tests/test_cross_language_verify.py.
//
// Binary lookup:
//  1. AXON_VERIFY_BIN supplies the verifier explicitly.
//  2. <repo>/sdk/rust/target/debug/axon-verify is the local fallback.
//  3. Tests skip when neither path is available.

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func findVerifyBin(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("AXON_VERIFY_BIN"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env
		}
	}
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	candidate := filepath.Clean(filepath.Join(
		filepath.Dir(sourcePath),
		"..", "..", "..", "rust", "target", "debug", "axon-verify",
	))
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// ── Helpers — shared producer logic ─────────────────────────────────

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeKey(t *testing.T, bundle, alias string, sk ed25519.PrivateKey) {
	t.Helper()
	keyDir := filepath.Join(bundle, "keys")
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pub := sk.Public().(ed25519.PublicKey)
	if err := os.WriteFile(filepath.Join(keyDir, alias+".ed25519"), pub, 0o644); err != nil {
		t.Fatal(err)
	}
}

func strictProofFacts(env InvocationEnvelope, outputHash [32]byte) ReceiptProofFacts {
	descriptor, err := ParseAbilityDescriptorRef(env.Ability)
	if err != nil {
		panic(err)
	}
	authority := AuthorityOrBootstrapFromBinding(SelfAuthority(env.Caller.URA))
	authorityCopy := cloneAuthorityOrBootstrap(authority)
	issuer := env.Callee
	return NewReceiptProofFacts(
		&EntityRef{
			Kind:    EntityRefResource,
			URA:     env.Subject.URA,
			Profile: env.Subject.Profile,
		},
		descriptor.Version,
		Sha256([]byte("schema."+env.Ability)),
		Sha256([]byte("impl."+env.Ability)),
		"go-test",
		InvocationAuthorityProof{
			ProofType:     "go-wire-test-verified-admission",
			Binding:       &authorityCopy,
			ProofHash:     AuthorityOrBootstrapProofHash(authority),
			Issuer:        &issuer,
			AdmissionHook: "test.go.wire.admission.v1",
		},
		env.ArgsDigest,
		outputHash,
		parentReceiptsFromCausal(env.CausalContext),
	)
}

// emitTwoReceiptChain writes admitted → completed receipts for one
// invocation and returns the terminal self-hash so the next invocation
// can link to it via causal_context.scalar.
func emitTwoReceiptChain(
	t *testing.T,
	bundle string,
	invID string,
	env InvocationEnvelope,
	callerSk, calleeSk ed25519.PrivateKey,
) [32]byte {
	t.Helper()
	bound, err := NewDescriptorBoundEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := NewDescriptorBoundInvocationDraft(bound)
	if err != nil {
		t.Fatal(err)
	}
	callerSig, err := draft.SignCallerSignature(callerSk, "")
	if err != nil {
		t.Fatal(err)
	}
	invJSON := NewDescriptorBoundInvocationJSON(invID, bound, callerSig)
	writeJSON(t, filepath.Join(bundle, "invocations", invID+".json"), invJSON)

	authorityBinding := AuthorityOrBootstrapFromBinding(SelfAuthority(env.Caller.URA))
	proofBinding := cloneAuthorityOrBootstrap(authorityBinding)
	issuer := env.Callee
	policy, err := NewVerifiedAdmissionPolicy(
		bound,
		authorityBinding,
		InvocationAuthorityProof{
			ProofType:     "cross-language-verified-admission",
			Binding:       &proofBinding,
			ProofHash:     AuthorityOrBootstrapProofHash(authorityBinding),
			Issuer:        &issuer,
			AdmissionHook: "test.go.cross_language.admission.v1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := newVerifiedDescriptorBoundAdmission(bound, policy)
	if err != nil {
		t.Fatal(err)
	}
	binding := mustProviderBinding(
		t,
		env.Ability,
		Sha256([]byte("schema."+env.Ability)),
		Sha256([]byte("impl."+env.Ability)),
		"canonical-go-cross-language-runtime-v1",
		func(context.Context, *AbilityContext) ([]byte, *AxonError) { return nil, nil },
	)
	descriptor, err := resolvedDescriptorEvidence(admission, binding)
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := registeredImplementationEvidence(binding)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewDefaultCanonicalReceiptProvider(
		AdmissionPolicyVerifierFunc(func(
			DescriptorBoundEnvelope,
		) (VerifiedAdmissionPolicy, error) {
			return policy, nil
		}),
		ReceiptSigningAuthorityResolverFunc(func(
			callee AgentIdentity,
		) (ReceiptSigningAuthority, error) {
			return NewEd25519ReceiptSigningAuthority(callee, calleeSk, "cross-language")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptContext, err := provider.Bind(invID, admission, descriptor, implementation)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := provider.AppendSignedReceipt(receiptContext, ReceiptAppendInput{
		ReceiptType:     "admitted",
		State:           StateAdmitted,
		TimestampUnixMs: 1_700_000_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := provider.AppendSignedReceipt(receiptContext, ReceiptAppendInput{
		ReceiptType:     "completed",
		State:           StateCompleted,
		TimestampUnixMs: 1_700_000_001_000,
		CleanupComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []SignedInvocationReceipt{admitted, completed} {
		projection, err := ProjectSignedInvocationReceipt(receipt)
		if err != nil {
			t.Fatal(err)
		}
		writeJSON(
			t,
			filepath.Join(
				bundle,
				"receipts",
				invID,
				fmt.Sprintf("%d.json", receipt.Index()),
			),
			projection,
		)
	}

	return completed.SelfHash()
}

func receiptJSONFromBody(r ReceiptBody, sig CalleeSignature) ReceiptJSON {
	if r.ProofFacts.SubjectRef == nil {
		panic("receipt test body requires subject_ref")
	}
	authority, err := AuthorityOrBootstrapJSONFromBinding(r.AuthorityBinding)
	if err != nil {
		panic(err)
	}
	proof, err := AuthorityProofFromProof(r.ProofFacts.AuthorityProof)
	if err != nil {
		panic(err)
	}
	return ReceiptJSON{
		Index:              r.Index,
		InvocationID:       r.InvocationID,
		ReceiptType:        r.ReceiptType,
		State:              r.State,
		TimestampUnixMs:    r.TimestampUnixMs,
		PrevReceiptHashHex: hexStr(r.PrevReceiptHash[:]),
		PayloadSha256Hex:   hexStr(r.PayloadDigest[:]),
		Reason:             r.Reason,
		CleanupComplete:    r.CleanupComplete,
		CallerBinding:      IdentityFromAgent(r.CallerBinding),
		CalleeBinding:      IdentityFromAgent(r.CalleeBinding),
		SubjectBinding:     IdentityFromSubject(r.SubjectBinding),
		InvocationNonceHex: hexStr(r.InvocationNonce[:]),
		CausalBinding:      CausalFromCtx(r.CausalBinding),
		AbilityBinding:     r.AbilityBinding,
		AuthorityBinding:   authority,
		UsageTokensIn:      JSONU64(r.Usage.TokensIn),
		UsageTokensOut:     JSONU64(r.Usage.TokensOut),
		UsageDurationMs:    JSONU64(r.Usage.DurationMs),
		UsageExternalCalls: JSONU32(r.Usage.ExternalCalls),
		SubjectRef:         EntityRefToJSON(*r.ProofFacts.SubjectRef),
		DescriptorVersion:  r.ProofFacts.DescriptorVersion,
		SchemaHashHex:      hexStr(r.ProofFacts.SchemaHash[:]),
		ImplHashHex:        hexStr(r.ProofFacts.ImplHash[:]),
		RuntimeEnv:         r.ProofFacts.RuntimeEnv,
		AuthorityProof:     proof,
		InputHashHex:       hexStr(r.ProofFacts.InputHash[:]),
		OutputHashHex:      hexStr(r.ProofFacts.OutputHash[:]),
		ParentReceipts:     receiptRefsJSON(r.ProofFacts.ParentReceipts),
		CalleeSignatureHex: hexStr(sig.Signature),
		CalleeSignatureAlg: sig.Algorithm,
	}
}

func receiptRefsJSON(refs []ReceiptRef) []ReceiptRefJSON {
	out := make([]ReceiptRefJSON, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ReceiptRefJSON{
			ReceiptHashHex: hexStr(ref.ReceiptHash[:]),
			ReceiptURA:     ref.ReceiptURA,
		})
	}
	return out
}

func hexStr(b []byte) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[2*i] = hexchars[c>>4]
		out[2*i+1] = hexchars[c&0x0f]
	}
	return string(out)
}

// buildBundle produces a two-invocation bundle (scalar causal link).
func buildBundle(t *testing.T, bundle string) {
	t.Helper()
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}

	seed1 := make([]byte, 32)
	for i := range seed1 {
		seed1[i] = 1
	}
	seed2 := make([]byte, 32)
	for i := range seed2 {
		seed2[i] = 2
	}
	sk1 := ed25519.NewKeyFromSeed(seed1)
	sk2 := ed25519.NewKeyFromSeed(seed2)
	writeKey(t, bundle, "silan", sk1)
	writeKey(t, bundle, "openai", sk2)

	silan := NewAgentIdentity("easynet:///r/silan/agents/writer@1", ProfileStrictV2)
	openai := NewAgentIdentity("easynet:///r/openai/agents/reviewer@1", ProfileStrictV2)
	paper := NewSubjectIdentity("easynet:///r/silan/resource/silan.papers/p", ProfileStrictV2)

	var nonceA [16]byte
	for i := range nonceA {
		nonceA[i] = 0xAA
	}
	envA := InvocationEnvelope{
		Caller: silan, Callee: openai, Subject: paper,
		Ability: "easynet:///r/openai/ability/authority.review.paper@descriptor.review.v1#" + testDescriptorHashHex + "!invoke", ArgsDigest: Sha256(nil),
		InvocationNonce: nonceA, CausalContext: CausalNoneCtx(),
	}
	aTerminalHash := emitTwoReceiptChain(t, bundle, "inv-a", envA, sk1, sk2)

	var nonceB [16]byte
	for i := range nonceB {
		nonceB[i] = 0xBB
	}
	envB := InvocationEnvelope{
		Caller: silan, Callee: openai, Subject: paper,
		Ability: "easynet:///r/openai/ability/authority.summarize.paper@descriptor.summarize.v1#" + testDescriptorHashHex + "!invoke", ArgsDigest: Sha256(nil),
		InvocationNonce: nonceB,
		CausalContext: CausalScalarCtx(ReceiptRef{
			ReceiptHash: aTerminalHash,
			ReceiptURA:  "bundle:///receipts/inv-a/1",
		}),
	}
	_ = emitTwoReceiptChain(t, bundle, "inv-b", envB, sk1, sk2)
}

// ── Tests ───────────────────────────────────────────────────────────

func TestGoBundleAcceptedByRustVerify(t *testing.T) {
	bin := findVerifyBin(t)
	if bin == "" {
		t.Skip("axon-verify not found; set AXON_VERIFY_BIN or build sdk/rust")
	}
	tmp := t.TempDir()
	bundle := filepath.Join(tmp, "bundle")
	buildBundle(t, bundle)

	cmd := exec.Command(bin, bundle)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		stderr := ""
		if e, ok := err.(*exec.ExitError); ok {
			stderr = string(e.Stderr)
			ee = e
		}
		_ = ee
		t.Fatalf("verify failed: err=%v\nstdout=%s\nstderr=%s", err, out, stderr)
	}
	stdout := string(out)
	if !strings.Contains(stdout, "chain verified") {
		t.Fatalf("missing 'chain verified' in stdout: %q", stdout)
	}
	if !strings.Contains(stdout, "4 receipts") {
		t.Fatalf("expected 4 receipts: %q", stdout)
	}
	if !strings.Contains(stdout, "2 invocations") {
		t.Fatalf("expected 2 invocations: %q", stdout)
	}
}

func TestGoBundleRejectedAfterTamper(t *testing.T) {
	bin := findVerifyBin(t)
	if bin == "" {
		t.Skip("axon-verify not found; set AXON_VERIFY_BIN or build sdk/rust")
	}
	tmp := t.TempDir()
	bundle := filepath.Join(tmp, "bundle")
	buildBundle(t, bundle)

	receiptPath := filepath.Join(bundle, "receipts", "inv-a", "1.json")
	raw, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	sig := obj["callee_signature_hex"].(string)
	flip := "0"
	if sig[len(sig)-1] == '0' {
		flip = "1"
	}
	obj["callee_signature_hex"] = sig[:len(sig)-1] + flip
	tampered, _ := json.MarshalIndent(obj, "", "  ")
	if err := os.WriteFile(receiptPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, bundle)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("tampered bundle should fail verify; stdout=%s", out)
	}
	combined := string(out)
	if !strings.Contains(combined, "FAIL") {
		t.Fatalf("expected FAIL in output: %q", combined)
	}
}

// silence unused
var _ = fmt.Sprintf
