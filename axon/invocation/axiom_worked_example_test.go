package axon

// PR2 §1.3 — Go vector-driver test for the authenticated-invocation
// worked example.
//
// Mirror of sdk/rust/tests/axiom_worked_example.rs. The contract is
// identical and the test structure is a 1:1 port — same four test
// functions, same assertion classes, same loader pattern — so that
// subsequent language ports (Python / Node / Java / Swift) have one
// unambiguous template to follow.
//
// Contract (repeated here for reviewers):
//
//   - Single source of truth. This test reads
//     sdk/conformance/cases/axiom/axiom-worked-example-authenticated.json
//     and asserts against its `expected` block. NO hard-coded reference
//     values live in this file. Deleting or altering the JSON breaks
//     this test first.
//
//   - Two assertion classes:
//       (1) positive reproduction — the four pinned outputs
//           (args_digest, caller_pubkey, canonical_invocation_sha256,
//           caller_signature) MUST match byte-for-byte
//       (2) metadata-exclusion invariant — the canonical byte output
//           MUST be stable regardless of any non-axiom metadata
//           (Go's InvocationEnvelope has no metadata field, so the
//           guarantee is structural; the test asserts the structural
//           fact holds).
//
// Go-specific hazards this test deliberately guards against:
//
//   - []byte / string implicit conversion: every comparison uses
//     bytes.Equal / explicit hex.EncodeToString; no `string(b) == …`
//     short-circuits.
//   - base64/hex round-trip: every decoded value is compared against
//     the reference via its canonical hex/base64 form, not against a
//     freshly-encoded version (which could normalise padding or case).
//   - fixed-length arrays vs slices: Go's [32]byte vs []byte is
//     material for Ed25519 keys and args digests — the loader
//     explicitly copies into fixed-size arrays.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// ── JSON shape (mirrors axiom-worked-example-authenticated.json) ─────

type workedVectorInputs struct {
	SigningSeedHex     string             `json:"signing_seed_hex"`
	Caller             workedVectorID     `json:"caller"`
	Callee             workedVectorID     `json:"callee"`
	Subject            workedVectorID     `json:"subject"`
	Ability            string             `json:"ability"`
	PayloadBase64      string             `json:"payload_base64"`
	InvocationNonceHex string             `json:"invocation_nonce_hex"`
	CausalContext      workedVectorCausal `json:"causal_context"`
}

type workedVectorID struct {
	URA     string `json:"ura"`
	Profile string `json:"profile"`
}

// Only the "none" form is referenced by this vector; other forms would
// fail parse with a clear error, which is intentional — any drift that
// changes the inputs beyond what this vector covers is a spec change.
type workedVectorCausal struct {
	Form string `json:"form"`
}

type workedVectorExpected struct {
	ArgsDigestHex             string `json:"args_digest_hex"`
	CallerPubkeyHex           string `json:"caller_pubkey_hex"`
	CanonicalInvocationSha256 string `json:"canonical_invocation_sha256"`
	CallerSignatureBase64     string `json:"caller_signature_base64"`
}

type workedVectorMetadataExcluded struct {
	MetadataProbe json.RawMessage `json:"metadata_probe"`
}

type workedVectorInvariants struct {
	MetadataExcluded workedVectorMetadataExcluded `json:"metadata_excluded"`
}

type workedVector struct {
	ID         string                 `json:"id"`
	Inputs     workedVectorInputs     `json:"inputs"`
	Expected   workedVectorExpected   `json:"expected"`
	Invariants workedVectorInvariants `json:"invariants"`
}

// ── Loader ────────────────────────────────────────────────────────────

func workedVectorPath(t *testing.T) string {
	t.Helper()
	return conformancePath(t, "axiom", "axiom-worked-example-authenticated.json")
}

func loadWorkedVector(t *testing.T) workedVector {
	t.Helper()
	raw, err := os.ReadFile(workedVectorPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var v workedVector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vector: %v", err)
	}
	return v
}

// parseHex32 / parseHex16: fixed-size copies. These guard the Go-
// specific hazard that `hex.DecodeString` returns a []byte of any
// length; the [N]byte targets the canonical-bytes encoder expects
// silently truncate or pad if we're not explicit.
func parseHex32(t *testing.T, s string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode %q: %v", s, err)
	}
	if len(b) != 32 {
		t.Fatalf("hex %q decoded to %d bytes, want 32", s, len(b))
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

func parseHex16(t *testing.T, s string) [16]byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode %q: %v", s, err)
	}
	if len(b) != 16 {
		t.Fatalf("hex %q decoded to %d bytes, want 16", s, len(b))
	}
	var out [16]byte
	copy(out[:], b)
	return out
}

func buildWorkedEnvelope(t *testing.T, in workedVectorInputs) InvocationEnvelope {
	t.Helper()
	callerProfile, err := ParseUraProfile(in.Caller.Profile)
	if err != nil {
		t.Fatalf("caller profile: %v", err)
	}
	calleeProfile, err := ParseUraProfile(in.Callee.Profile)
	if err != nil {
		t.Fatalf("callee profile: %v", err)
	}
	subjectProfile, err := ParseUraProfile(in.Subject.Profile)
	if err != nil {
		t.Fatalf("subject profile: %v", err)
	}
	payload, err := base64.StdEncoding.DecodeString(in.PayloadBase64)
	if err != nil {
		t.Fatalf("payload base64: %v", err)
	}
	if in.CausalContext.Form != "none" {
		t.Fatalf("this vector requires causal_context.form=\"none\"; got %q", in.CausalContext.Form)
	}
	return InvocationEnvelope{
		Caller:          NewAgentIdentity(in.Caller.URA, callerProfile),
		Callee:          NewAgentIdentity(in.Callee.URA, calleeProfile),
		Subject:         NewSubjectIdentity(in.Subject.URA, subjectProfile),
		Ability:         in.Ability,
		ArgsDigest:      Sha256(payload),
		InvocationNonce: parseHex16(t, in.InvocationNonceHex),
		CausalContext:   CausalNoneCtx(),
	}
}

// workedCanonicalBytes returns the DESCRIPTOR-BOUND canonical bytes — the
// admission/proof boundary this vector pins. The vector's Ability is an
// AbilityDescriptorRef and the subject derives to an EntityRef, so the
// descriptor-bound envelope constructs successfully.
func workedCanonicalBytes(t *testing.T, env InvocationEnvelope) []byte {
	t.Helper()
	bound, err := NewDescriptorBoundEnvelope(env)
	if err != nil {
		t.Fatalf("worked example must be a descriptor-bound envelope: %v", err)
	}
	draft, err := NewDescriptorBoundInvocationDraft(bound)
	if err != nil {
		t.Fatalf("worked example must build a descriptor-bound draft: %v", err)
	}
	canon, err := draft.CanonicalBytes()
	if err != nil {
		t.Fatalf("descriptor-bound canonical bytes: %v", err)
	}
	return canon
}

// workedSign signs the descriptor-bound canonical bytes.
func workedSign(t *testing.T, sk ed25519.PrivateKey, env InvocationEnvelope) CallerSignature {
	t.Helper()
	bound, err := NewDescriptorBoundEnvelope(env)
	if err != nil {
		t.Fatalf("worked example must be a descriptor-bound envelope: %v", err)
	}
	draft, err := NewDescriptorBoundInvocationDraft(bound)
	if err != nil {
		t.Fatalf("worked example must build a descriptor-bound draft: %v", err)
	}
	sig, err := draft.SignCallerSignature(sk, "")
	if err != nil {
		t.Fatalf("descriptor-bound sign: %v", err)
	}
	return sig
}

// ── Assertions ────────────────────────────────────────────────────────

// Class 1 — positive reproduction of all four pinned outputs.
//
// Every comparison goes through hex.EncodeToString / base64.StdEncoding
// before string-equality, so that []byte-vs-string quirks and
// lowercase/uppercase hex-normalisation bugs surface here instead of at
// cross-language runtime.
func TestWorkedExample_ReproducesAllFourPinnedOutputs(t *testing.T) {
	v := loadWorkedVector(t)
	if v.ID != "axiom-worked-example-authenticated" {
		t.Fatalf("wrong vector id %q — this test is loading the wrong file", v.ID)
	}

	env := buildWorkedEnvelope(t, v.Inputs)
	seedBytes := parseHex32(t, v.Inputs.SigningSeedHex)
	sk, err := SigningKeyFromBytes(seedBytes[:])
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}

	// Output 1/4 — args_digest_hex: pure SHA-256(payload).
	gotArgsDigest := hex.EncodeToString(env.ArgsDigest[:])
	if gotArgsDigest != v.Expected.ArgsDigestHex {
		t.Fatalf("args_digest drift\n  got:  %s\n  want: %s",
			gotArgsDigest, v.Expected.ArgsDigestHex)
	}

	// Output 2/4 — caller_pubkey_hex: Ed25519 derivation from seed.
	// Go's ed25519.PrivateKey.Public() returns the 32-byte raw key
	// wrapped in ed25519.PublicKey ([]byte alias); take it out
	// explicitly to dodge implicit-conversion hazards.
	pub, ok := sk.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("unexpected public-key type from ed25519.PrivateKey.Public()")
	}
	if len(pub) != 32 {
		t.Fatalf("ed25519 pubkey length %d, want 32", len(pub))
	}
	gotPubkey := hex.EncodeToString(pub)
	if gotPubkey != v.Expected.CallerPubkeyHex {
		t.Fatalf("caller_pubkey drift (RFC 8032 §5.1.5)\n  got:  %s\n  want: %s",
			gotPubkey, v.Expected.CallerPubkeyHex)
	}

	// Output 3/4 — canonical_invocation_sha256: descriptor-bound §4.1 layout.
	canon := workedCanonicalBytes(t, env)
	canonHash := Sha256(canon)
	gotCanonHash := hex.EncodeToString(canonHash[:])
	if gotCanonHash != v.Expected.CanonicalInvocationSha256 {
		t.Fatalf("canonical_invocation_sha256 drift — §4.1 descriptor-bound layout changed\n  got:  %s\n  want: %s",
			gotCanonHash, v.Expected.CanonicalInvocationSha256)
	}

	// Output 4/4 — caller_signature_base64: deterministic Ed25519.
	sig := workedSign(t, sk, env)
	gotSigB64 := base64.StdEncoding.EncodeToString(sig.Signature)
	if gotSigB64 != v.Expected.CallerSignatureBase64 {
		t.Fatalf("caller_signature drift (canonical bytes or ed25519 signing changed)\n  got:  %s\n  want: %s",
			gotSigB64, v.Expected.CallerSignatureBase64)
	}
}

// Class 2 — metadata exclusion invariant.
//
// Go's InvocationEnvelope struct has no Metadata field — metadata
// lives at transport level (gRPC headers / request messages). The
// structural guarantee: canonical bytes depend solely on the 7-tuple.
// This test proves the guarantee holds by building two envelopes with
// identical axiom fields and showing canonical output is byte-identical
// and matches the pinned hash.
//
// The vector's invariants.metadata_excluded.metadata_probe is loaded
// and asserted present, so the JSON contract stays honoured even
// though Go's type system already enforces the structural invariant.
func TestWorkedExample_MetadataExclusionInvariantHolds(t *testing.T) {
	v := loadWorkedVector(t)

	// Presence of metadata_probe in the vector is itself part of the
	// contract — assert it loaded.
	if len(v.Invariants.MetadataExcluded.MetadataProbe) == 0 {
		t.Fatalf("vector must provide invariants.metadata_excluded.metadata_probe")
	}
	// Verify it's a JSON object, not a null or scalar.
	var probeObj map[string]any
	if err := json.Unmarshal(v.Invariants.MetadataExcluded.MetadataProbe, &probeObj); err != nil {
		t.Fatalf("metadata_probe must be a JSON object: %v", err)
	}
	if len(probeObj) == 0 {
		t.Fatalf("metadata_probe must be a non-empty object")
	}

	envA := buildWorkedEnvelope(t, v.Inputs)
	envB := buildWorkedEnvelope(t, v.Inputs)
	canonA := workedCanonicalBytes(t, envA)
	canonB := workedCanonicalBytes(t, envB)
	if !bytes.Equal(canonA, canonB) {
		t.Fatalf("two envelopes with identical inputs produced different canonical bytes")
	}

	// Reproducibility check — canonical bytes still match the pinned
	// anchor, so this test is not accidentally decoupled from the
	// positive-reproduction test above.
	gotHash := hex.EncodeToString(func() []byte { h := Sha256(canonA); return h[:] }())
	if gotHash != v.Expected.CanonicalInvocationSha256 {
		t.Fatalf("canonical_invocation_sha256 drift detected via invariant test\n  got:  %s\n  want: %s",
			gotHash, v.Expected.CanonicalInvocationSha256)
	}
}

// Class 2 (extension) — Ed25519 signing is deterministic for fixed
// inputs. Two signs of the same envelope with the same key MUST
// produce byte-identical signatures (RFC 8032 Ed25519 property).
// Asserting it here catches pathological library behaviour early.
func TestWorkedExample_SignatureIsDeterministic(t *testing.T) {
	v := loadWorkedVector(t)
	env := buildWorkedEnvelope(t, v.Inputs)
	seedBytes := parseHex32(t, v.Inputs.SigningSeedHex)
	sk, err := SigningKeyFromBytes(seedBytes[:])
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	sig1 := workedSign(t, sk, env)
	sig2 := workedSign(t, sk, env)
	if !bytes.Equal(sig1.Signature, sig2.Signature) {
		t.Fatalf("Ed25519 signing is deterministic per RFC 8032; two signatures diverged")
	}
}

// Failure-mode test — mutating a loaded input MUST flip
// canonical_invocation_sha256. This proves the test is alive; if
// someone accidentally turns the assertion above into a tautology,
// this catches it.
func TestWorkedExample_MutatedInputBreaksCanonicalHash(t *testing.T) {
	v := loadWorkedVector(t)
	env := buildWorkedEnvelope(t, v.Inputs)
	// Flip one bit in the nonce.
	env.InvocationNonce[0] ^= 0x01
	canon := workedCanonicalBytes(t, env)
	h := Sha256(canon)
	got := hex.EncodeToString(h[:])
	if got == v.Expected.CanonicalInvocationSha256 {
		t.Fatalf("mutating the nonce did not change canonical_invocation_sha256 — test is dead code")
	}
}
