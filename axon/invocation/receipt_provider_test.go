package axon

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sync"
	"testing"
)

type receiptProviderFixture struct {
	provider *DefaultCanonicalReceiptProvider
	context  *BoundReceiptContext
	key      ed25519.PrivateKey
}

func newReceiptProviderFixture(
	t *testing.T,
	resolve ReceiptSigningAuthorityResolver,
) receiptProviderFixture {
	t.Helper()
	key := testSigningKey(t)
	caller := silanAgent()
	callee := openaiAgent()
	subject := signedSubject()
	payload := []byte("{}")
	envelope, err := NewDescriptorBoundEnvelope(InvocationEnvelope{
		Caller:          caller,
		Callee:          callee,
		Subject:         subject,
		Ability:         receiptVerbDescriptorRef,
		ArgsDigest:      Sha256(payload),
		InvocationNonce: FreshNonce(),
		CausalContext:   CausalNoneCtx(),
	})
	if err != nil {
		t.Fatal(err)
	}
	authorityBinding := AuthorityOrBootstrapFromBinding(SelfAuthority(caller.URA))
	proofBinding := cloneAuthorityOrBootstrap(authorityBinding)
	issuer := callee
	policy, err := NewVerifiedAdmissionPolicy(
		envelope,
		authorityBinding,
		InvocationAuthorityProof{
			ProofType:     "receipt-provider-test-admission",
			Binding:       &proofBinding,
			ProofHash:     AuthorityOrBootstrapProofHash(authorityBinding),
			Issuer:        &issuer,
			AdmissionHook: "test.go.receipt_provider.admission.v1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolve == nil {
		resolve = ReceiptSigningAuthorityResolverFunc(func(
			resolvedCallee AgentIdentity,
		) (ReceiptSigningAuthority, error) {
			return NewEd25519ReceiptSigningAuthority(
				resolvedCallee,
				key,
				"receipt-provider-test",
			)
		})
	}
	provider, err := NewDefaultCanonicalReceiptProvider(
		AdmissionPolicyVerifierFunc(func(
			DescriptorBoundEnvelope,
		) (VerifiedAdmissionPolicy, error) {
			return policy, nil
		}),
		resolve,
	)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := newVerifiedDescriptorBoundAdmission(envelope, policy)
	if err != nil {
		t.Fatal(err)
	}
	binding := mustProviderBinding(
		t,
		receiptVerbDescriptorRef,
		Sha256([]byte("receipt-provider-test-schema")),
		Sha256([]byte("receipt-provider-test-impl")),
		"canonical-go-receipt-provider-test-runtime-v1",
		func(_ context.Context, _ *AbilityContext) ([]byte, *AxonError) {
			return nil, nil
		},
	)
	descriptor, err := resolvedDescriptorEvidence(admission, binding)
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := registeredImplementationEvidence(binding)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := provider.Bind(
		"inv-receipt-provider-test",
		admission,
		descriptor,
		implementation,
	)
	if err != nil {
		t.Fatal(err)
	}
	return receiptProviderFixture{provider: provider, context: bound, key: key}
}

type failingReceiptSigningAuthority struct {
	callee AgentIdentity
	key    ed25519.PrivateKey
}

func (a *failingReceiptSigningAuthority) CalleeIdentity() AgentIdentity {
	return a.callee
}

func (a *failingReceiptSigningAuthority) SignerIdentity() AgentIdentity {
	return a.callee
}

func (a *failingReceiptSigningAuthority) HostAttestation() []byte {
	return nil
}

func (a *failingReceiptSigningAuthority) VerifyingKey() ed25519.PublicKey {
	return a.key.Public().(ed25519.PublicKey)
}

func (a *failingReceiptSigningAuthority) SignAndVerify(
	_ []byte,
) (CalleeSignature, error) {
	return CalleeSignature{}, ErrInternal("injected_signing_failure")
}

func TestCanonicalReceiptProviderDoesNotAppendSigningFailure(t *testing.T) {
	key := testSigningKey(t)
	fixture := newReceiptProviderFixture(
		t,
		ReceiptSigningAuthorityResolverFunc(func(
			callee AgentIdentity,
		) (ReceiptSigningAuthority, error) {
			return &failingReceiptSigningAuthority{callee: callee, key: key}, nil
		}),
	)
	if _, err := fixture.provider.AppendSignedReceipt(
		fixture.context,
		ReceiptAppendInput{
			ReceiptType:     "progress",
			State:           StateRunning,
			TimestampUnixMs: 1,
			Payload:         []byte("output"),
		},
	); err == nil || err.Error() == "" {
		t.Fatalf("expected signing failure, got %v", err)
	}
	if receipts := fixture.provider.SnapshotSignedReceipts(
		fixture.context.InvocationID(),
	); len(receipts) != 0 {
		t.Fatalf("signing failure appended %d receipts", len(receipts))
	}
}

func TestCanonicalReceiptProviderSerializesConcurrentAppendsPerInvocation(
	t *testing.T,
) {
	fixture := newReceiptProviderFixture(t, nil)
	const count = 32
	var wait sync.WaitGroup
	errors := make(chan error, count)
	wait.Add(count)
	for index := 0; index < count; index++ {
		go func(index int) {
			defer wait.Done()
			_, err := fixture.provider.AppendSignedReceipt(
				fixture.context,
				ReceiptAppendInput{
					ReceiptType:     "progress",
					State:           StateRunning,
					TimestampUnixMs: int64(index + 1),
					Payload:         []byte(fmt.Sprintf("output-%d", index)),
				},
			)
			errors <- err
		}(index)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	receipts := fixture.provider.SnapshotSignedReceipts(
		fixture.context.InvocationID(),
	)
	if len(receipts) != count {
		t.Fatalf("receipt count = %d, want %d", len(receipts), count)
	}
	if check := VerifyReceiptChain(receipts); !check.OK {
		t.Fatalf("concurrent chain invalid: %+v", check)
	}
}

func TestCanonicalReceiptProviderLatchesTerminalAfterVerifiedAppend(t *testing.T) {
	fixture := newReceiptProviderFixture(t, nil)
	if _, err := fixture.provider.AppendSignedReceipt(
		fixture.context,
		ReceiptAppendInput{
			ReceiptType:     "completed",
			State:           StateCompleted,
			TimestampUnixMs: 1,
			Payload:         []byte("done"),
			CleanupComplete: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.provider.AppendSignedReceipt(
		fixture.context,
		ReceiptAppendInput{
			ReceiptType:     "progress",
			State:           StateRunning,
			TimestampUnixMs: 2,
		},
	); err == nil || err.Error() == "" {
		t.Fatalf("terminal chain accepted another receipt: %v", err)
	}
}

func TestValidateReceiptFactSemanticsRejectsMissingAndZeroEvidence(t *testing.T) {
	fixture := newReceiptProviderFixture(t, nil)
	output := []byte("output")
	valid := fixture.context.proofFactsForOutput(output)
	if err := ValidateReceiptFactSemantics(fixture.context, valid, output); err != nil {
		t.Fatal(err)
	}

	missingSubject := cloneReceiptProofFacts(valid)
	missingSubject.SubjectRef = nil
	if err := ValidateReceiptFactSemantics(
		fixture.context,
		missingSubject,
		output,
	); err == nil {
		t.Fatal("missing subject_ref was accepted")
	}

	zeroOutput := cloneReceiptProofFacts(valid)
	zeroOutput.OutputHash = zeroHash32
	if err := ValidateReceiptFactSemantics(
		fixture.context,
		zeroOutput,
		output,
	); err == nil {
		t.Fatal("zero output hash was accepted")
	}

	spuriousParent := cloneReceiptProofFacts(valid)
	spuriousParent.ParentReceipts = []ReceiptRef{{
		ReceiptHash: [32]byte{1},
		ReceiptURA:  "easynet:///r/test/receipt/spurious",
	}}
	if err := ValidateReceiptFactSemantics(
		fixture.context,
		spuriousParent,
		output,
	); err == nil {
		t.Fatal("spurious parent receipt was accepted")
	}
}
