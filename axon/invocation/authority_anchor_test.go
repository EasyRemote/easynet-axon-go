package axon

// F07 §A14 authority-binding tail — CORE PARITY anchor test (Go).
//
// RFC 001 amendment "AuthorityBinding: relation/evidence decomposition"
// (document/rfcs/001-authority-binding-relation-evidence.md) changed
// the §A14 authority-tail byte layout (relation/evidence tags replace
// the old flat one-byte discriminator, and the authority identity now
// carries its URA profile). Per that RFC's "Versioning" section, no
// tagged release ever pinned the old per-form byte layout, so these
// anchors are regenerated — not preserved — as part of this change
// (mirrors the Rust reference's own
// strict_receipt_anchor_vectors_match_cross_language_pins, which was
// regenerated in the same change). These pins are self-consistent
// within the Go SDK (they pin Go's own encoder against regressions);
// they are not required to be byte-identical to the Rust reference's
// pins because the two worked examples do not share an ability-binding
// input (Go uses a bare "review" string here; Rust uses a full
// descriptor ref).
//
// Inputs:
//   caller   easynet:///r/silan/agent/silan.alice      axon-strict-v2
//   callee   easynet:///r/openai/agent/openai.reviewer axon-strict-v2
//   subject  easynet:///r/silan/resource/silan.papers/paper axon-strict-v2
//   index=0, invocation_id="inv-worked-example-0001",
//   receipt_type="admitted", state="admitted",
//   timestamp=1_700_000_000_000, prev_hash=[0;32], nonce=[0x22;16],
//   payload_digest=sha256("{}"), reason="", cleanup_complete=false,
//   authority = Self+Identity{authority == caller}.

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// Anchors pinning the Go §A14 relation/evidence encoder against
// regression. Regenerated for the relation/evidence redesign — see the
// package doc comment above.
const (
	receiptWorkedSha256Hex = "f7789a2d7b8cc61160849237a89819f5fc88673bea1486021e8269a8dffe7888"
	receiptFormNoneSha256  = "f7789a2d7b8cc61160849237a89819f5fc88673bea1486021e8269a8dffe7888"
	receiptFormScalarSha   = "b7ae6fdd18a9c364568060a9b1f9cc46b84a6307e6e988455066459e06e790b4"
	receiptFormListSha     = "f1763cdac6274965a716358dd3de45ca6d19084f5ddbcdee904a31bdcd75beb6"
	receiptFormMerkleSha   = "1606989bcc688f452db86783d3ae4ce72612e81da930cfeba051f1197b62185f"
	receiptHostedNoneSha   = "6c19b8a0b82da81359b6b4a3c8253548c3271eaa6d0709e630cbc4a3843d2606"
)

const (
	anchorCallerURA  = "easynet:///r/silan/agent/silan.alice"
	anchorCalleeURA  = "easynet:///r/openai/agent/openai.reviewer"
	anchorSubjectURA = "easynet:///r/silan/resource/silan.papers/paper"
	anchorProfile    = ProfileStrictV2
	anchorInvID      = "inv-worked-example-0001"
	anchorTimestamp  = int64(1_700_000_000_000)
	anchorAbility    = "review"
	anchorRuntimeEnv = "axon-receipt-anchor-v2"
)

func anchorBody(t testingT, causal CausalContext) ReceiptBody {
	t.Helper()
	var nonce [16]byte
	for i := range nonce {
		nonce[i] = 0x22
	}
	caller := NewAgentIdentity(anchorCallerURA, anchorProfile)
	callee := NewAgentIdentity(anchorCalleeURA, anchorProfile)
	subject := NewSubjectIdentity(anchorSubjectURA, anchorProfile)
	payloadDigest := Sha256([]byte("{}"))
	authority := AuthorityOrBootstrapFromBinding(SelfAuthority(anchorCallerURA))
	return ReceiptBody{
		Index:            0,
		InvocationID:     anchorInvID,
		ReceiptType:      "admitted",
		State:            "admitted",
		TimestampUnixMs:  anchorTimestamp,
		PrevReceiptHash:  [32]byte{},
		PayloadDigest:    payloadDigest,
		Reason:           "",
		CleanupComplete:  false,
		CallerBinding:    caller,
		CalleeBinding:    callee,
		SubjectBinding:   subject,
		InvocationNonce:  nonce,
		CausalBinding:    causal,
		AbilityBinding:   anchorAbility,
		AuthorityBinding: authority,
		ProofFacts:       anchorProofFacts(payloadDigest),
	}
}

func anchorProofFacts(inputHash [32]byte) ReceiptProofFacts {
	return NewReceiptProofFacts(
		&EntityRef{
			Kind:    EntityRefResource,
			URA:     anchorSubjectURA,
			Profile: anchorProfile,
		},
		"descriptor.review.v1",
		Sha256([]byte("schema.review.v1")),
		Sha256([]byte("impl.review.v1")),
		anchorRuntimeEnv,
		anchorAuthorityProof(),
		inputHash,
		Sha256(nil),
		nil,
	)
}

func anchorAuthorityProof() InvocationAuthorityProof {
	authority := AuthorityOrBootstrapFromBinding(SelfAuthority(anchorCallerURA))
	issuer := NewAgentIdentity(anchorCalleeURA, anchorProfile)
	return InvocationAuthorityProof{
		ProofType:     "test-verified-admission",
		Binding:       &authority,
		ProofPayload:  nil,
		ProofHash:     AuthorityOrBootstrapProofHash(authority),
		Issuer:        &issuer,
		Signature:     nil,
		AdmissionHook: "test.canonical_receipt_provider.admission.v1",
	}
}

func assertReceiptAnchor(t *testing.T, body ReceiptBody, hosted *HostedAttestation, wantHex, label string) {
	t.Helper()
	h := Sha256(CanonicalReceiptBytesWithHosted(body, hosted))
	got := hex.EncodeToString(h[:])
	if got != wantHex {
		t.Fatalf("§A14 receipt anchor drift [%s]\n  got:  %s\n  want: %s\n"+
			"Go canonical_receipt_bytes does not match the Rust F07 anchor.",
			label, got, wantHex)
	}
}

// CORE PARITY: the None/worked-example form must equal Rust's pin.
func TestAuthorityAnchor_WorkedExampleNoneForm(t *testing.T) {
	assertReceiptAnchor(t, anchorBody(t, CausalNoneCtx()), nil,
		receiptWorkedSha256Hex, "worked-example-none")
	// The form-none pin is intentionally identical to the worked
	// example (same inputs); assert they agree.
	if receiptWorkedSha256Hex != receiptFormNoneSha256 {
		t.Fatalf("worked-example and form-none anchors disagree")
	}
}

func TestAuthorityAnchor_ZeroValueAuthorityIsRejected(t *testing.T) {
	body := anchorBody(t, CausalNoneCtx())
	body.AuthorityBinding = AuthorityOrBootstrap{}
	defer func() {
		if recover() == nil {
			t.Fatal("zero-value AuthorityBinding must be rejected")
		}
	}()
	_ = CanonicalReceiptBytes(body)
}

// CORE PARITY: all four causal forms (authority tail constant = Self_).
func TestAuthorityAnchor_PerCausalForm(t *testing.T) {
	fill := func(b byte) [32]byte {
		var h [32]byte
		for i := range h {
			h[i] = b
		}
		return h
	}
	scalar := CausalScalarCtx(ReceiptRef{
		ReceiptHash: fill(0xAB),
		ReceiptURA:  "easynet:///r/silan/receipts/01R-prev",
	})
	assertReceiptAnchor(t, anchorBody(t, scalar), nil, receiptFormScalarSha, "scalar")

	list := CausalListCtx([]ReceiptRef{
		{ReceiptHash: fill(0xAA), ReceiptURA: "easynet:///r/silan/receipts/01R-A"},
		{ReceiptHash: fill(0xBB), ReceiptURA: "easynet:///r/silan/receipts/01R-B"},
	})
	assertReceiptAnchor(t, anchorBody(t, list), nil, receiptFormListSha, "list")

	merkle := CausalMerkleCtx(fill(0xCD), "easynet:///r/silan/proofs/01PRF-1")
	assertReceiptAnchor(t, anchorBody(t, merkle), nil, receiptFormMerkleSha, "merkle")
}

// CORE PARITY: hosted §A12 tail + §A14 authority tail (None form). The
// authority tail sits AFTER the hosted tail; the hosted SHA differs from
// the self-signed None form.
func TestAuthorityAnchor_HostedNoneForm(t *testing.T) {
	body := anchorBody(t, CausalNoneCtx())
	// Hosted pin overrides the callee + uses SelfAuthority{principal ==
	// caller silan.alice} (admission.rs 1086–1164).
	body.CalleeBinding = NewAgentIdentity("easynet:///r/silan/hosted/llm/claude", anchorProfile)
	body.AuthorityBinding = AuthorityOrBootstrapFromBinding(SelfAuthority(anchorCallerURA))
	var att [64]byte
	for i := range att {
		att[i] = 0x77
	}
	hosted := &HostedAttestation{
		SignerBinding:   NewAgentIdentity("easynet:///r/silan/device/macbook", anchorProfile),
		HostAttestation: att[:],
	}
	assertReceiptAnchor(t, body, hosted, receiptHostedNoneSha, "hosted-none")

	// Cross-form distinctness: hosted ≠ self-signed on the same inputs.
	selfH := Sha256(CanonicalReceiptBytesWithHosted(body, nil))
	hostedH := Sha256(CanonicalReceiptBytesWithHosted(body, hosted))
	if hex.EncodeToString(selfH[:]) == hex.EncodeToString(hostedH[:]) {
		t.Fatalf("hosted form must differ from self-signed form on identical inputs")
	}
}

func TestAuthorityAnchor_UsageTailIsSignedMaterial(t *testing.T) {
	base := anchorBody(t, CausalNoneCtx())
	withUsage := anchorBody(t, CausalNoneCtx())
	withUsage.Usage = InvocationUsage{
		TokensIn:      1832,
		TokensOut:     412,
		DurationMs:    5734,
		ExternalCalls: 0,
	}
	a := CanonicalReceiptBytes(base)
	b := CanonicalReceiptBytes(withUsage)
	hashA := Sha256(a)
	hashB := Sha256(b)
	if hex.EncodeToString(hashA[:]) == hex.EncodeToString(hashB[:]) {
		t.Fatalf("usage must live in the signed region")
	}
	proofBlockLen := 4 + len(CanonicalReceiptProofFacts(withUsage.ProofFacts))
	tail := b[len(b)-proofBlockLen-28 : len(b)-proofBlockLen]
	if got := hex.EncodeToString(tail[0:8]); got != "0000000000000728" {
		t.Fatalf("tokens_in tail mismatch: %s", got)
	}
	if got := hex.EncodeToString(tail[24:28]); got != "00000000" {
		t.Fatalf("external_calls tail mismatch: %s", got)
	}
}

func TestCanonicalProviderBindsUsageBeforeSelfHash(t *testing.T) {
	body := anchorBody(t, CausalNoneCtx())
	usage := InvocationUsage{
		TokensIn:      9007199254740993,
		TokensOut:     412,
		DurationMs:    5734,
		ExternalCalls: 2,
	}
	receipt, _ := appendReceiptForVerbs(
		t,
		testSigningKey(t),
		AxiomBinding{
			Caller:           body.CallerBinding,
			Callee:           body.CalleeBinding,
			Subject:          body.SubjectBinding,
			InvocationNonce:  body.InvocationNonce,
			Causal:           body.CausalBinding,
			PayloadDigest:    body.PayloadDigest,
			AbilityBinding:   receiptVerbDescriptorRef,
			AuthorityBinding: body.AuthorityBinding,
		},
		usage,
	)

	if receipt.Usage() != usage {
		t.Fatalf("usage must be stored before hashing: got %+v want %+v", receipt.Usage(), usage)
	}
	if got := Sha256(receipt.CanonicalBytes()); receipt.SelfHash() != got {
		t.Fatalf("self_hash must cover usage\n  got:  %x\n  want: %x", receipt.SelfHash(), got)
	}
	withoutUsage := receipt.signingBody()
	withoutUsage.Usage = InvocationUsage{}
	if Sha256(CanonicalReceiptBytesWithHosted(
		withoutUsage,
		receipt.receipt.AxiomBinding.Hosted,
	)) == receipt.SelfHash() {
		t.Fatalf("self_hash must change when signed usage changes")
	}
}

func TestReceiptJSON_UsageU64SerializesAsDecimalStrings(t *testing.T) {
	raw := ReceiptJSON{
		Index:              0,
		InvocationID:       "inv-usage-json",
		ReceiptType:        "completed",
		State:              "COMPLETED",
		TimestampUnixMs:    1_700_000_000_000,
		UsageTokensIn:      JSONU64(9_007_199_254_740_993),
		UsageTokensOut:     JSONU64(412),
		UsageDurationMs:    JSONU64(5_734),
		UsageExternalCalls: JSONU32(2),
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal receipt json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal encoded receipt json: %v", err)
	}
	if got["usage_tokens_in"] != "9007199254740993" {
		t.Fatalf("usage_tokens_in must serialize as decimal string, got %#v", got["usage_tokens_in"])
	}
	if got["usage_tokens_out"] != "412" || got["usage_duration_ms"] != "5734" {
		t.Fatalf("usage u64 fields must serialize as strings: %s", encoded)
	}
	if got["usage_external_calls"] != "2" {
		t.Fatalf("usage_external_calls must serialize as decimal string, got %#v", got["usage_external_calls"])
	}

	var parsedU64 JSONU64
	if err := json.Unmarshal([]byte(`"9007199254740993"`), &parsedU64); err != nil {
		t.Fatalf("usage u64 string must parse: %v", err)
	}
	var parsedU32 JSONU32
	if err := json.Unmarshal([]byte(`"2"`), &parsedU32); err != nil {
		t.Fatalf("usage u32 string must parse: %v", err)
	}
	if uint64(parsedU64) != 9_007_199_254_740_993 || uint32(parsedU32) != 2 {
		t.Fatalf("usage string parse mismatch: u64=%d u32=%d", parsedU64, parsedU32)
	}
}

func TestReceiptJSONRejectsMissingUsageField(t *testing.T) {
	// DEC-010: "zero is a fact, absence is not." A receipt that omits a usage
	// counter is malformed and must be rejected, not coerced to zero — keeping
	// the Go read side symmetric with the other SDK decoders.
	var parsed ReceiptJSON
	err := json.Unmarshal([]byte(`{
		"usage_tokens_in": "1",
		"usage_tokens_out": "2",
		"usage_duration_ms": "3"
	}`), &parsed)
	if err == nil || !strings.Contains(err.Error(), "receipt_json_missing_required_field:usage_external_calls") {
		t.Fatalf("expected missing-usage rejection, got %v", err)
	}
}

func TestInvocationJSONRejectsUnknownFields(t *testing.T) {
	var parsed InvocationJSON
	err := json.Unmarshal([]byte(`{"unexpected": true}`), &parsed)
	if err == nil || !strings.Contains(err.Error(), "unknown_invocation_json_field:unexpected") {
		t.Fatalf("expected strict unknown invocation field rejection, got %v", err)
	}
}
