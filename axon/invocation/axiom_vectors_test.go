package axon

// Go driver for the shared axiom conformance vectors.
//
// Reads sdk/conformance/cases/axiom/*.json and asserts the same
// verdicts as the Rust and Python drivers. Any encoder divergence
// manifests as a pin failure on the affected axiom vector.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type vectorFile struct {
	ID                string         `json:"id"`
	Title             string         `json:"title"`
	Invariant         string         `json:"invariant"`
	Expect            map[string]any `json:"expect"`
	DocumentationOnly bool           `json:"documentation_only"`
	BindingConstants  struct {
		HkdfSalt string `json:"hkdf_salt_utf8"`
		InfoUp   string `json:"hkdf_info_caller_to_callee_utf8"`
		InfoDown string `json:"hkdf_info_callee_to_caller_utf8"`
	} `json:"binding_constants"`
	AnchorVectorHkdf struct {
		Inputs struct {
			SignatureHex string `json:"envelope_signature_bytes_hex"`
			NonceUTF8    string `json:"invocation_nonce_utf8"`
		} `json:"inputs"`
	} `json:"anchor_vector_hkdf"`
	AnchorVectorFrameMac struct {
		Inputs struct {
			KeyHex      string `json:"key_bytes_hex"`
			Sequence    uint64 `json:"sequence_u64_be"`
			PrevMacHex  string `json:"prev_mac_bytes_hex"`
			PayloadUTF8 string `json:"canonical_payload_bytes_utf8"`
		} `json:"inputs"`
	} `json:"anchor_vector_frame_mac"`
	WedgeProperty struct {
		Inputs struct {
			KeyHex               string   `json:"key_bytes_hex"`
			AnchorHex            string   `json:"anchor_bytes_hex"`
			PayloadsUTF8         []string `json:"payloads_utf8"`
			TamperedPayloadsUTF8 []string `json:"tampered_payloads_utf8"`
		} `json:"inputs"`
	} `json:"wedge_property"`
	// Inputs is polymorphic across vectors; we re-decode specific keys
	// per dispatch as raw json.RawMessage.
	RawInputs       json.RawMessage   `json:"inputs"`
	RawInputsA      json.RawMessage   `json:"inputs_a"`
	RawInputsB      json.RawMessage   `json:"inputs_b"`
	RawInputsStrict json.RawMessage   `json:"inputs_strict"`
	RawInputsWeb    json.RawMessage   `json:"inputs_websafe"`
	Mutations       map[string]string `json:"mutations"`
}

type inputJ struct {
	Caller              IdentityJSON      `json:"caller"`
	Callee              IdentityJSON      `json:"callee"`
	Subject             IdentityJSON      `json:"subject"`
	Ability             string            `json:"ability"`
	ArgsDigestHex       string            `json:"args_digest_hex"`
	InvocationNonceHex  string            `json:"invocation_nonce_hex"`
	CausalContext       CausalJSON        `json:"causal_context"`
	CallerSecretHex     string            `json:"caller_secret_hex"`
	CalleeSecretHex     string            `json:"callee_secret_hex"`
	Tamper              map[string]string `json:"tamper"`
	ProfileSwapOnVerify map[string]string `json:"profile_swap_on_verify"`
}

func (i inputJ) envelope(t *testing.T) InvocationEnvelope {
	t.Helper()
	caller, err := i.Caller.ToAgent()
	if err != nil {
		t.Fatalf("caller parse: %v", err)
	}
	callee, err := i.Callee.ToAgent()
	if err != nil {
		t.Fatalf("callee parse: %v", err)
	}
	subject, err := i.Subject.ToSubject()
	if err != nil {
		t.Fatalf("subject parse: %v", err)
	}
	args, err := hexTo32(i.ArgsDigestHex)
	if err != nil {
		t.Fatalf("args_digest: %v", err)
	}
	nonce, err := hexTo16(i.InvocationNonceHex)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	ctx, err := i.CausalContext.ToCtx()
	if err != nil {
		t.Fatalf("causal: %v", err)
	}
	return InvocationEnvelope{
		Caller: caller, Callee: callee, Subject: subject,
		Ability: i.Ability, ArgsDigest: args, InvocationNonce: nonce,
		CausalContext: ctx,
	}
}

func (i inputJ) descriptorBoundEnvelope(t *testing.T) DescriptorBoundEnvelope {
	t.Helper()
	env, err := NewDescriptorBoundEnvelope(i.envelope(t))
	if err != nil {
		t.Fatalf("descriptor-bound envelope: %v", err)
	}
	return env
}

func descriptorBoundEnvelope(t *testing.T, env InvocationEnvelope) DescriptorBoundEnvelope {
	t.Helper()
	bound, err := NewDescriptorBoundEnvelope(env)
	if err != nil {
		t.Fatalf("descriptor-bound envelope: %v", err)
	}
	return bound
}

func descriptorBoundDraft(t *testing.T, env InvocationEnvelope) DescriptorBoundInvocationDraft {
	t.Helper()
	draft, err := NewDescriptorBoundInvocationDraft(descriptorBoundEnvelope(t, env))
	if err != nil {
		t.Fatalf("descriptor-bound draft: %v", err)
	}
	return draft
}

func descriptorBoundBytes(t *testing.T, env InvocationEnvelope) []byte {
	t.Helper()
	bytes, err := descriptorBoundDraft(t, env).CanonicalBytes()
	if err != nil {
		t.Fatalf("descriptor-bound bytes: %v", err)
	}
	return bytes
}

func signDescriptorBoundVector(t *testing.T, sk ed25519.PrivateKey, env InvocationEnvelope, keyIDHint string) CallerSignature {
	t.Helper()
	sig, err := descriptorBoundDraft(t, env).SignCallerSignature(sk, keyIDHint)
	if err != nil {
		t.Fatalf("descriptor-bound sign: %v", err)
	}
	return sig
}

func verifyDescriptorBoundVector(t *testing.T, env InvocationEnvelope, sig CallerSignature, resolver KeyResolver) error {
	t.Helper()
	return descriptorBoundDraft(t, env).VerifyCallerSignature(sig, resolver)
}

func parseInput(t *testing.T, raw json.RawMessage) inputJ {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("missing inputs block")
	}
	var out inputJ
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse input: %v", err)
	}
	return out
}

func vectorsDirs(t *testing.T) []string {
	t.Helper()
	return []string{conformancePath(t, "axiom")}
}

func listVectors(t *testing.T) []string {
	var out []string
	for _, dir := range vectorsDirs(t) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".json" {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

func mustExpectString(t *testing.T, vf vectorFile, key string) string {
	t.Helper()
	out, ok := vf.Expect[key].(string)
	if !ok || out == "" {
		t.Fatalf("missing expect.%s", key)
	}
	return out
}

func mustExpectBool(t *testing.T, vf vectorFile, key string) bool {
	t.Helper()
	out, ok := vf.Expect[key].(bool)
	if !ok {
		t.Fatalf("missing expect.%s", key)
	}
	return out
}

func sha256Hex(b []byte) string {
	h := Sha256(b)
	return hex.EncodeToString(h[:])
}

// ── Fixed resolver (shared with axiom_test.go via package scope) ──────

type staticResolver struct{ key ed25519.PublicKey }

func (r *staticResolver) Resolve(_ string) (ed25519.PublicKey, error) { return r.key, nil }

// ── Dispatch ──────────────────────────────────────────────────────────

func runVector(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// First decode only the documentation_only flag; doc-only vectors may
	// have polymorphic `expect` shapes (e.g. array-typed) that would fail
	// the strict map[string]any decode below.
	var meta struct {
		ID                string `json:"id"`
		DocumentationOnly bool   `json:"documentation_only"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("parse meta %s: %v", path, err)
	}
	if meta.DocumentationOnly {
		t.Skipf("documentation-only: %s", meta.ID)
		return
	}
	var vf vectorFile
	if err := json.Unmarshal(raw, &vf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	switch vf.ID {
	case "axiom-e2-caller-sig-required":
		runE2(t, vf)
	case "axiom-e3-nonce-replay-discriminated":
		runE3(t, vf)
	case "axiom-e4-subject-distinct-from-callee":
		runE4(t, vf)
	case "axiom-e5-causal-none-root":
		runE5(t, vf)
	case "axiom-e6-causal-scalar":
		runE6(t, vf)
	case "axiom-e7-causal-list-fanin":
		runE7(t, vf)
	case "axiom-identity-composite-required":
		runIdentityComposite(t, vf)
	case "axiom-profile-binding":
		runProfileBinding(t, vf)
	case "axiom-subject-survives-callee-swap":
		runSubjectSurvives(t, vf)
	case "axiom-descriptor-bound-subject-ref":
		runDescriptorBoundSubjectRef(t, vf)
	case "axiom-bidi-frame-chain":
		runBidiFrameChain(t, vf)
	case "axiom-worked-example-authenticated":
		// PR2 §1.1 vector. Uses the inputs/expected/invariants schema
		// and is driven by axiom_worked_example_test.go instead of
		// this generic walker. Skip here rather than fail.
		t.Skipf("driven by axiom_worked_example_test.go: %s", vf.ID)
	default:
		t.Fatalf("unknown vector id: %s", vf.ID)
	}
}

func runBidiFrameChain(t *testing.T, vf vectorFile) {
	sig := mustHex(vf.AnchorVectorHkdf.Inputs.SignatureHex)
	ikm := append(append([]byte{}, sig...), []byte(vf.AnchorVectorHkdf.Inputs.NonceUTF8)...)
	up := hkdfSha256(ikm, []byte(vf.BindingConstants.HkdfSalt), []byte(vf.BindingConstants.InfoUp), 32)
	down := hkdfSha256(ikm, []byte(vf.BindingConstants.HkdfSalt), []byte(vf.BindingConstants.InfoDown), 32)
	if got, want := hex.EncodeToString(up), mustExpectString(t, vf, "hkdf_up_key_hex"); got != want {
		t.Fatalf("bidi HKDF up key\n  got: %s\n  want: %s", got, want)
	}
	if got, want := hex.EncodeToString(down), mustExpectString(t, vf, "hkdf_down_key_hex"); got != want {
		t.Fatalf("bidi HKDF down key\n  got: %s\n  want: %s", got, want)
	}

	tag := bidiFrameMac(
		mustHex(vf.AnchorVectorFrameMac.Inputs.KeyHex),
		vf.AnchorVectorFrameMac.Inputs.Sequence,
		mustHex(vf.AnchorVectorFrameMac.Inputs.PrevMacHex),
		[]byte(vf.AnchorVectorFrameMac.Inputs.PayloadUTF8),
	)
	if got, want := hex.EncodeToString(tag), mustExpectString(t, vf, "frame_mac_tag_hex"); got != want {
		t.Fatalf("bidi frame mac\n  got: %s\n  want: %s", got, want)
	}

	honest := bidiChain(
		mustHex(vf.WedgeProperty.Inputs.KeyHex),
		mustHex(vf.WedgeProperty.Inputs.AnchorHex),
		utf8Payloads(vf.WedgeProperty.Inputs.PayloadsUTF8),
	)
	tampered := bidiChain(
		mustHex(vf.WedgeProperty.Inputs.KeyHex),
		mustHex(vf.WedgeProperty.Inputs.AnchorHex),
		utf8Payloads(vf.WedgeProperty.Inputs.TamperedPayloadsUTF8),
	)
	same := 0
	for i := range honest {
		if bytes.Equal(honest[i], tampered[i]) {
			same++
		}
	}
	if got, want := same, int(vf.Expect["wedge_unchanged_prefix_count"].(float64)); got != want {
		t.Fatalf("bidi wedge unchanged prefix = %d, want %d", got, want)
	}
	if got, want := len(honest)-same, int(vf.Expect["wedge_changed_suffix_count"].(float64)); got != want {
		t.Fatalf("bidi wedge changed suffix = %d, want %d", got, want)
	}
}

func hkdfSha256(ikm, salt, info []byte, length int) []byte {
	extract := hmac.New(sha256.New, salt)
	extract.Write(ikm)
	prk := extract.Sum(nil)
	out := make([]byte, 0, length)
	block := []byte{}
	for counter := byte(1); len(out) < length; counter++ {
		expand := hmac.New(sha256.New, prk)
		expand.Write(block)
		expand.Write(info)
		expand.Write([]byte{counter})
		block = expand.Sum(nil)
		out = append(out, block...)
	}
	return out[:length]
}

func bidiFrameMac(key []byte, sequence uint64, prevMac []byte, canonicalPayload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	var seq [8]byte
	for i := 7; i >= 0; i-- {
		seq[i] = byte(sequence)
		sequence >>= 8
	}
	mac.Write(seq[:])
	mac.Write(prevMac)
	mac.Write(canonicalPayload)
	return mac.Sum(nil)
}

func bidiChain(key []byte, anchor []byte, payloads [][]byte) [][]byte {
	prev := anchor
	tags := make([][]byte, 0, len(payloads))
	for i, payload := range payloads {
		tag := bidiFrameMac(key, uint64(i+1), prev, payload)
		tags = append(tags, tag)
		prev = tag
	}
	return tags
}

func utf8Payloads(payloads []string) [][]byte {
	out := make([][]byte, 0, len(payloads))
	for _, payload := range payloads {
		out = append(out, []byte(payload))
	}
	return out
}

func runDescriptorBoundSubjectRef(t *testing.T, vf vectorFile) {
	inp := parseInput(t, vf.RawInputs)
	env := inp.descriptorBoundEnvelope(t)
	raw := env.Envelope()
	subjectRef, err := EntityRefForSubject(raw.Subject)
	if err != nil {
		t.Fatalf("subject_ref derive: %v", err)
	}

	entityBytes := CanonicalEntityRef(subjectRef)
	if got, want := hex.EncodeToString(entityBytes), mustExpectString(t, vf, "canonical_entity_ref_hex"); got != want {
		t.Fatalf("descriptor-bound entity_ref bytes\n  got: %s\n  want: %s", got, want)
	}
	if got, want := sha256Hex(entityBytes), mustExpectString(t, vf, "canonical_entity_ref_sha256"); got != want {
		t.Fatalf("descriptor-bound entity_ref sha256\n  got: %s\n  want: %s", got, want)
	}

	baseDraft, err := NewDescriptorBoundInvocationDraft(env)
	if err != nil {
		t.Fatalf("descriptor-bound draft: %v", err)
	}
	base, err := baseDraft.CanonicalBytes()
	if err != nil {
		t.Fatalf("descriptor-bound invocation bytes: %v", err)
	}
	if got, want := sha256Hex(base), mustExpectString(t, vf, "canonical_descriptor_bound_invocation_sha256"); got != want {
		t.Fatalf("descriptor-bound invocation sha256\n  got: %s\n  want: %s", got, want)
	}

	abilitySwappedEnvelope := raw
	abilitySwappedEnvelope.Ability = vf.Mutations["ability"]
	abilitySwapped, err := NewDescriptorBoundEnvelope(abilitySwappedEnvelope)
	if err != nil {
		t.Fatalf("ability mutation descriptor-bound envelope: %v", err)
	}
	abilityDraft, err := NewDescriptorBoundInvocationDraft(abilitySwapped)
	if err != nil {
		t.Fatalf("ability mutation descriptor-bound draft: %v", err)
	}
	abilityBytes, err := abilityDraft.CanonicalBytes()
	if err != nil {
		t.Fatalf("ability mutation descriptor-bound bytes: %v", err)
	}
	if got, want := sha256Hex(abilityBytes), mustExpectString(t, vf, "ability_ref_swap_descriptor_bound_invocation_sha256"); got != want {
		t.Fatalf("descriptor-bound ability ref swap sha256\n  got: %s\n  want: %s", got, want)
	}
	if got, want := string(abilityBytes) != string(base), mustExpectBool(t, vf, "ability_ref_changes_descriptor_bound_invocation"); got != want {
		t.Fatalf("ability ref projection change flag = %v, want %v", got, want)
	}
}

func runE2(t *testing.T, vf vectorFile) {
	inp := parseInput(t, vf.RawInputs)
	env := inp.envelope(t)
	sk, _ := SigningKeyFromBytes(mustHex(inp.CallerSecretHex))
	vk := sk.Public().(ed25519.PublicKey)
	sig := signDescriptorBoundVector(t, sk, env, "")

	if inp.Tamper["kind"] != "swap_nonce" {
		t.Fatalf("unknown tamper kind: %s", inp.Tamper["kind"])
	}
	n, err := hexTo16(inp.Tamper["new_invocation_nonce_hex"])
	if err != nil {
		t.Fatal(err)
	}
	tampered := env
	tampered.InvocationNonce = n
	err = verifyDescriptorBoundVector(t, tampered, sig, &staticResolver{key: vk})
	if err == nil {
		t.Fatalf("e2 expected failure after tamper")
	}
	expect, _ := vf.Expect["expected_error_contains"].(string)
	if !strings.Contains(err.Error(), expect) {
		t.Fatalf("e2 error mismatch: %v (want contains %s)", err, expect)
	}
}

func runE3(t *testing.T, vf vectorFile) {
	a := parseInput(t, vf.RawInputsA).envelope(t)
	b := parseInput(t, vf.RawInputsB).envelope(t)
	if string(descriptorBoundBytes(t, a)) == string(descriptorBoundBytes(t, b)) {
		t.Fatalf("e3 nonce must enter canonical bytes")
	}
}

func runE4(t *testing.T, vf vectorFile) {
	inp := parseInput(t, vf.RawInputs)
	env := inp.envelope(t)
	if env.Callee.URA == env.Subject.URA {
		t.Fatalf("e4 expects subject != callee")
	}
	sk, _ := SigningKeyFromBytes(mustHex(inp.CallerSecretHex))
	vk := sk.Public().(ed25519.PublicKey)
	sig := signDescriptorBoundVector(t, sk, env, "")
	if err := verifyDescriptorBoundVector(t, env, sig, &staticResolver{key: vk}); err != nil {
		t.Fatalf("e4 verify: %v", err)
	}
}

func runE5(t *testing.T, vf vectorFile) {
	var inp struct {
		CausalContext CausalJSON `json:"causal_context"`
	}
	if err := json.Unmarshal(vf.RawInputs, &inp); err != nil {
		t.Fatal(err)
	}
	ctx, err := inp.CausalContext.ToCtx()
	if err != nil {
		t.Fatal(err)
	}
	b := CanonicalCausalContext(ctx)
	want, _ := vf.Expect["canonical_causal_bytes_hex"].(string)
	if hex.EncodeToString(b) != want {
		t.Fatalf("e5 bytes: got=%s want=%s", hex.EncodeToString(b), want)
	}
	if int(vf.Expect["canonical_causal_length"].(float64)) != len(b) {
		t.Fatalf("e5 length: got=%d", len(b))
	}
}

func runE6(t *testing.T, vf vectorFile) {
	var inp struct {
		CausalContext CausalJSON `json:"causal_context"`
	}
	_ = json.Unmarshal(vf.RawInputs, &inp)
	ctx, err := inp.CausalContext.ToCtx()
	if err != nil {
		t.Fatal(err)
	}
	b := CanonicalCausalContext(ctx)
	first, _ := vf.Expect["canonical_causal_first_byte_hex"].(string)
	if hex.EncodeToString(b[:1]) != first {
		t.Fatalf("e6 first byte: got=%s want=%s", hex.EncodeToString(b[:1]), first)
	}
	start, _ := vf.Expect["canonical_causal_bytes_start_with"].(string)
	if !strings.HasPrefix(hex.EncodeToString(b), start) {
		t.Fatalf("e6 prefix: got=%s want_prefix=%s", hex.EncodeToString(b), start)
	}
}

func runE7(t *testing.T, vf vectorFile) {
	var a, b struct {
		CausalContext CausalJSON `json:"causal_context"`
	}
	if err := json.Unmarshal(vf.RawInputsA, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(vf.RawInputsB, &b); err != nil {
		t.Fatal(err)
	}
	aCtx, _ := a.CausalContext.ToCtx()
	bCtx, _ := b.CausalContext.ToCtx()
	ab := CanonicalCausalContext(aCtx)
	bb := CanonicalCausalContext(bCtx)
	if string(ab) == string(bb) {
		t.Fatalf("e7 list order must bind into bytes")
	}
	first, _ := vf.Expect["canonical_a_first_byte_hex"].(string)
	if hex.EncodeToString(ab[:1]) != first {
		t.Fatalf("e7 first byte: got=%s want=%s", hex.EncodeToString(ab[:1]), first)
	}
}

func runIdentityComposite(t *testing.T, vf vectorFile) {
	strict := parseInput(t, vf.RawInputsStrict).envelope(t)
	web := parseInput(t, vf.RawInputsWeb).envelope(t)
	if string(descriptorBoundBytes(t, strict)) == string(descriptorBoundBytes(t, web)) {
		t.Fatalf("profile must be structurally bound into canonical bytes")
	}
}

func runProfileBinding(t *testing.T, vf vectorFile) {
	inp := parseInput(t, vf.RawInputs)
	env := inp.envelope(t)
	sk, _ := SigningKeyFromBytes(mustHex(inp.CallerSecretHex))
	vk := sk.Public().(ed25519.PublicKey)
	sig := signDescriptorBoundVector(t, sk, env, "")

	newProfile := inp.ProfileSwapOnVerify["caller"]
	p, err := ParseUraProfile(newProfile)
	if err != nil {
		t.Fatal(err)
	}
	tampered := env
	tampered.Caller.Profile = p
	err = verifyDescriptorBoundVector(t, tampered, sig, &staticResolver{key: vk})
	if err == nil {
		t.Fatalf("profile-binding expected failure")
	}
	want, _ := vf.Expect["expected_error_contains"].(string)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("profile-binding: %v (want contains %s)", err, want)
	}
}

func runSubjectSurvives(t *testing.T, vf vectorFile) {
	a := parseInput(t, vf.RawInputsA).envelope(t)
	b := parseInput(t, vf.RawInputsB).envelope(t)
	if a.Subject.URA != b.Subject.URA || a.Subject.Profile != b.Subject.Profile {
		t.Fatalf("subject must be byte-identical across A and B")
	}
	if a.Callee.URA == b.Callee.URA {
		t.Fatalf("callee URAs must differ in this vector")
	}
	if string(descriptorBoundBytes(t, a)) == string(descriptorBoundBytes(t, b)) {
		t.Fatalf("overall canonical bytes must differ")
	}
}

// ── Top-level runner ─────────────────────────────────────────────────

func TestAxiomVectorsAllPass(t *testing.T) {
	paths := listVectors(t)
	if len(paths) < 10 {
		t.Fatalf("expected ≥10 vectors, got %d", len(paths))
	}
	for _, p := range paths {
		name := filepath.Base(p)
		t.Run(name, func(t *testing.T) {
			runVector(t, p)
		})
	}
}

func TestPubkeyMapParses(t *testing.T) {
	m, err := ParsePubkeyMap("silan=./a.ed25519,openai=./b.ed25519")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ByAlias) != 2 {
		t.Fatalf("expected 2 aliases, got %d", len(m.ByAlias))
	}
	if _, ok := m.ByAlias["silan"]; !ok {
		t.Fatalf("silan missing")
	}
}

func TestPubkeyMapRejectsMalformed(t *testing.T) {
	_, err := ParsePubkeyMap("silan")
	if err == nil {
		t.Fatalf("expected error on malformed spec")
	}
	if !strings.Contains(err.Error(), "bad_pubkey_spec") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Silence unused-var warning in release builds (the var is in bundle.go
// guard comment).
var _ = fmt.Sprintf
