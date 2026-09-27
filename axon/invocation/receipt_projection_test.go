package axon

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
)

func TestInvocationReceiptJSONToDomainPreservesFullFields(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	caller := NewAgentIdentity("easynet:///r/acme/agent/alice.caller", ProfileStrictV2)
	callee := NewAgentIdentity("easynet:///r/acme/agent/alice.worker", ProfileStrictV2)
	subject := NewSubjectIdentity("easynet:///r/acme/resource/alice.documents/report", ProfileStrictV2)
	envelope := InvocationEnvelope{
		Caller:          caller,
		Callee:          callee,
		Subject:         subject,
		Ability:         "easynet:///r/acme/ability/alice.worker.documents.summarize@1.0.0#" + testDescriptorHashHex + "!invoke",
		ArgsDigest:      Sha256(payload),
		InvocationNonce: [16]byte{1, 2, 3},
		CausalContext:   CausalNoneCtx(),
	}
	body := ReceiptBody{
		Index:            0,
		InvocationID:     "inv-projection",
		ReceiptType:      "completed",
		State:            "COMPLETED",
		TimestampUnixMs:  1_710_000_000_000,
		PayloadDigest:    Sha256(payload),
		CleanupComplete:  true,
		CallerBinding:    caller,
		CalleeBinding:    callee,
		SubjectBinding:   subject,
		InvocationNonce:  envelope.InvocationNonce,
		CausalBinding:    envelope.CausalContext,
		AbilityBinding:   envelope.Ability,
		AuthorityBinding: AuthorityOrBootstrapFromBinding(SelfAuthority(caller.URA)),
		Usage:            InvocationUsage{TokensIn: 4, TokensOut: 2, DurationMs: 9, ExternalCalls: 1},
		ProofFacts:       strictProofFacts(envelope, Sha256(payload)),
	}
	base := receiptJSONFromBody(body, CalleeSignature{
		Algorithm: "ed25519",
		Signature: make([]byte, ed25519.SignatureSize),
		KeyIDHint: "callee-key-1",
	})
	payloadHex := hex.EncodeToString(payload)
	host := IdentityFromAgent(NewAgentIdentity("easynet:///r/acme/device/host-1", ProfileStrictV2))
	projection := InvocationReceiptJSON{
		Receipt:                  base,
		PayloadHex:               &payloadHex,
		PayloadContentType:       "application/json",
		ChildInvocationID:        "child-1",
		CalleeSignatureKeyIDHint: "callee-key-1",
		SignerBinding:            &host,
		HostAttestationHex:       "010203",
	}

	raw, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ParseInvocationReceiptJSON(raw)
	if err != nil {
		t.Fatalf("parse full receipt projection: %v", err)
	}
	if string(receipt.receipt.Payload) != string(payload) || receipt.receipt.PayloadContentType != "application/json" {
		t.Fatalf("payload projection lost: %q %q", receipt.receipt.Payload, receipt.receipt.PayloadContentType)
	}
	if receipt.receipt.ChildInvocationID != "child-1" {
		t.Fatalf("child invocation id lost: %q", receipt.receipt.ChildInvocationID)
	}
	if receipt.receipt.CalleeSignature.KeyIDHint != "callee-key-1" {
		t.Fatalf("signature key hint lost: %+v", receipt.receipt.CalleeSignature)
	}
	if receipt.receipt.AxiomBinding.Hosted == nil || len(receipt.receipt.AxiomBinding.Hosted.HostAttestation) != 3 {
		t.Fatalf("hosted attestation lost: %+v", receipt.receipt.AxiomBinding.Hosted)
	}
	if receipt.receipt.SelfHash != computeReceiptHash(&receipt.receipt) {
		t.Fatal("absent self_hash must be computed by wire decoder")
	}
}

func TestInvocationReceiptJSONPreservesSuppliedTamperedSelfHash(t *testing.T) {
	projection := receiptProjectionForTamperTest(t)
	tamperedBytes := make([]byte, 32)
	for index := range tamperedBytes {
		tamperedBytes[index] = 0xaa
	}
	tampered := hex.EncodeToString(tamperedBytes)
	projection.SelfHashHex = &tampered
	receipt, err := projection.ToUnverifiedReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if receipt.receipt.SelfHash == computeReceiptHash(&receipt.receipt) {
		t.Fatal("supplied self hash was normalised away")
	}
	if _, err := receipt.Verify(&fixedResolver{key: make(ed25519.PublicKey, ed25519.PublicKeySize)}); err == nil {
		t.Fatal("tampered supplied self hash must not verify")
	}
}

func receiptProjectionForTamperTest(t *testing.T) InvocationReceiptJSON {
	t.Helper()
	payload := []byte("payload")
	caller := NewAgentIdentity("easynet:///r/acme/agent/alice.caller", ProfileStrictV2)
	callee := NewAgentIdentity("easynet:///r/acme/agent/alice.worker", ProfileStrictV2)
	subject := NewSubjectIdentity("easynet:///r/acme/resource/alice.documents/report", ProfileStrictV2)
	envelope := InvocationEnvelope{
		Caller: caller, Callee: callee, Subject: subject,
		Ability:    "easynet:///r/acme/ability/alice.worker.documents.summarize@1.0.0#" + testDescriptorHashHex + "!invoke",
		ArgsDigest: Sha256(payload), InvocationNonce: [16]byte{4}, CausalContext: CausalNoneCtx(),
	}
	body := ReceiptBody{
		Index: 0, InvocationID: "inv-tamper", ReceiptType: "accepted", State: "ACCEPTED",
		PayloadDigest: Sha256(payload), CallerBinding: caller, CalleeBinding: callee,
		SubjectBinding: subject, InvocationNonce: envelope.InvocationNonce,
		CausalBinding: envelope.CausalContext, AbilityBinding: envelope.Ability,
		AuthorityBinding: AuthorityOrBootstrapFromBinding(SelfAuthority(caller.URA)), ProofFacts: strictProofFacts(envelope, Sha256(payload)),
	}
	base := receiptJSONFromBody(body, CalleeSignature{
		Algorithm: "ed25519",
		Signature: make([]byte, ed25519.SignatureSize),
	})
	payloadHex := hex.EncodeToString(payload)
	return InvocationReceiptJSON{Receipt: base, PayloadHex: &payloadHex}
}

func TestReceiptJSONStablePublicFieldSet(t *testing.T) {
	want := []string{
		"Index", "InvocationID", "ReceiptType", "State", "TimestampUnixMs",
		"PrevReceiptHashHex", "PayloadSha256Hex", "Reason", "CleanupComplete",
		"CallerBinding", "CalleeBinding", "SubjectBinding", "InvocationNonceHex",
		"CausalBinding", "AbilityBinding", "AuthorityBinding", "UsageTokensIn",
		"UsageTokensOut", "UsageDurationMs", "UsageExternalCalls", "SubjectRef",
		"DescriptorVersion", "SchemaHashHex", "ImplHashHex", "RuntimeEnv",
		"AuthorityProof", "InputHashHex", "OutputHashHex", "ParentReceipts",
		"CalleeSignatureHex", "CalleeSignatureAlg",
	}
	typeOf := reflect.TypeOf(ReceiptJSON{})
	got := make([]string, 0, typeOf.NumField())
	for index := 0; index < typeOf.NumField(); index++ {
		got = append(got, typeOf.Field(index).Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReceiptJSON public fields changed\n got: %v\nwant: %v", got, want)
	}

	stable := receiptProjectionForTamperTest(t).Receipt
	raw, err := json.Marshal(stable)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, extension := range []string{
		"self_hash_hex", "payload_hex", "payload_content_type",
		"child_invocation_id", "callee_signature_key_id_hint",
		"signer_binding", "host_attestation_hex",
	} {
		if _, exists := document[extension]; exists {
			t.Fatalf("stable ReceiptJSON leaked additive field %q", extension)
		}
	}
}
