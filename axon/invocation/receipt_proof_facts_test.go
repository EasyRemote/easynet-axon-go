package axon

import (
	"strings"
	"testing"
)

func receiptFixtureProofFacts(
	t testingT,
	callee AgentIdentity,
	subject SubjectIdentity,
	causal CausalContext,
	payloadDigest [32]byte,
	outputHash [32]byte,
	authority AuthorityBinding,
) ReceiptProofFacts {
	t.Helper()
	subjectRef := EntityRef{
		Kind:    EntityRefResource,
		URA:     subject.URA,
		Profile: subject.Profile,
	}
	authorityCopy := AuthorityOrBootstrapFromBinding(authority)
	authorityProof := InvocationAuthorityProof{
		ProofType:     "go-receipt-fixture",
		Binding:       &authorityCopy,
		ProofHash:     AuthorityOrBootstrapProofHash(authorityCopy),
		Issuer:        &callee,
		AdmissionHook: "axon.go.receipt_fixture.v1",
	}
	if err := ValidateAuthorityProofHash(authorityProof); err != nil {
		t.Fatalf("fixture authority proof: %v", err)
	}
	return NewReceiptProofFacts(
		&subjectRef,
		"descriptor.review.v1",
		Sha256([]byte("schema.review.v1")),
		Sha256([]byte("impl.review.v1")),
		"go-receipt-fixture;descriptor_proof=bound",
		authorityProof,
		payloadDigest,
		outputHash,
		parentReceiptsFromCausal(causal),
	)
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

func tryReconstructReceiptProofFacts(facts ReceiptProofFacts) (ReceiptProofFacts, error) {
	return TryNewReceiptProofFacts(
		facts.SubjectRef,
		facts.DescriptorVersion,
		facts.SchemaHash,
		facts.ImplHash,
		facts.RuntimeEnv,
		facts.AuthorityProof,
		facts.InputHash,
		facts.OutputHash,
		facts.ParentReceipts,
	)
}

func validReceiptProofFactsForConstructionTest(t *testing.T) ReceiptProofFacts {
	t.Helper()
	callee := NewAgentIdentity("easynet:///r/acme/agent/acme.callee", ProfileStrictV2)
	subject := NewSubjectIdentity(
		"easynet:///r/acme/resource/acme.document",
		ProfileStrictV2,
	)
	authority := SelfAuthority("easynet:///r/acme/agent/acme.caller")
	return receiptFixtureProofFacts(
		t,
		callee,
		subject,
		CausalNoneCtx(),
		Sha256([]byte("input")),
		Sha256([]byte("output")),
		authority,
	)
}

func assertReceiptProofFactsError(t *testing.T, facts ReceiptProofFacts, reason string) {
	t.Helper()
	_, err := tryReconstructReceiptProofFacts(facts)
	if err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("expected %s, got %v", reason, err)
	}
}

func TestReceiptProofFactsRejectIncompleteConstruction(t *testing.T) {
	valid := validReceiptProofFactsForConstructionTest(t)

	missingSubject := valid
	missingSubject.SubjectRef = nil
	assertReceiptProofFactsError(t, missingSubject, "subject_ref_required")

	missingVersion := valid
	missingVersion.DescriptorVersion = " "
	assertReceiptProofFactsError(t, missingVersion, "descriptor_version_required")

	missingRuntime := valid
	missingRuntime.RuntimeEnv = ""
	assertReceiptProofFactsError(t, missingRuntime, "runtime_env_required")
}

func TestReceiptProofFactsRejectSemanticZeroHashes(t *testing.T) {
	valid := validReceiptProofFactsForConstructionTest(t)
	cases := []struct {
		name   string
		reason string
		mutate func(*ReceiptProofFacts)
	}{
		{
			name:   "schema",
			reason: "schema_hash_semantic_zero",
			mutate: func(facts *ReceiptProofFacts) { facts.SchemaHash = [32]byte{} },
		},
		{
			name:   "implementation",
			reason: "impl_hash_semantic_zero",
			mutate: func(facts *ReceiptProofFacts) { facts.ImplHash = [32]byte{} },
		},
		{
			name:   "input",
			reason: "input_hash_semantic_zero",
			mutate: func(facts *ReceiptProofFacts) { facts.InputHash = [32]byte{} },
		},
		{
			name:   "output",
			reason: "output_hash_semantic_zero",
			mutate: func(facts *ReceiptProofFacts) { facts.OutputHash = [32]byte{} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invalid := valid
			tc.mutate(&invalid)
			assertReceiptProofFactsError(t, invalid, tc.reason)
		})
	}
}

func TestReceiptProofFactsRejectIncompleteAuthorityProof(t *testing.T) {
	valid := validReceiptProofFactsForConstructionTest(t)
	cases := []struct {
		name   string
		reason string
		mutate func(*InvocationAuthorityProof)
	}{
		{
			name:   "type",
			reason: "authority_proof_type_required",
			mutate: func(proof *InvocationAuthorityProof) { proof.ProofType = "" },
		},
		{
			name:   "binding",
			reason: "authority_proof_binding_required",
			mutate: func(proof *InvocationAuthorityProof) { proof.Binding = nil },
		},
		{
			name:   "incomplete_binding",
			reason: "authority_principal_required",
			mutate: func(proof *InvocationAuthorityProof) {
				binding := AuthorityOrBootstrapFromBinding(SelfAuthority(" "))
				proof.Binding = &binding
			},
		},
		{
			name:   "issuer",
			reason: "authority_proof_issuer_required",
			mutate: func(proof *InvocationAuthorityProof) { proof.Issuer = nil },
		},
		{
			name:   "admission_hook",
			reason: "authority_proof_admission_hook_required",
			mutate: func(proof *InvocationAuthorityProof) { proof.AdmissionHook = "" },
		},
		{
			name:   "semantic_zero_hash",
			reason: "authority_proof_hash_semantic_zero",
			mutate: func(proof *InvocationAuthorityProof) { proof.ProofHash = [32]byte{} },
		},
		{
			name:   "mismatched_hash",
			reason: "authority_proof_hash_mismatch",
			mutate: func(proof *InvocationAuthorityProof) { proof.ProofHash = [32]byte{0x44} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invalid := valid
			tc.mutate(&invalid.AuthorityProof)
			assertReceiptProofFactsError(t, invalid, tc.reason)
		})
	}
}

func TestReceiptProofFactsRejectInvalidParentsAndForgedCompleteState(t *testing.T) {
	valid := validReceiptProofFactsForConstructionTest(t)
	invalidParent := valid
	invalidParent.ParentReceipts = []ReceiptRef{{
		ReceiptHash: [32]byte{},
		ReceiptURA:  "",
	}}
	assertReceiptProofFactsError(t, invalidParent, "parent_receipt_semantically_invalid")

	forged := valid
	forged.OutputHash = [32]byte{}
	forged.complete = true
	if _, err := TryCanonicalReceiptProofFacts(forged); err == nil ||
		!strings.Contains(err.Error(), "output_hash_semantic_zero") {
		t.Fatalf("forged complete state must fail canonical encoding, got %v", err)
	}
}
