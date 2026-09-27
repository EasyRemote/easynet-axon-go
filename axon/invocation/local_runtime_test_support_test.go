package axon

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
)

func testSigningKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	var seed [32]byte
	for index := range seed {
		seed[index] = 0x5a
	}
	key, err := SigningKeyFromBytes(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustTestRuntime(t *testing.T, key ed25519.PrivateKey) *LocalRuntime {
	return mustTestRuntimeWithOptions(t, key, LocalRuntimeOptions{})
}

func mustTestRuntimeWithOptions(
	t *testing.T,
	key ed25519.PrivateKey,
	options LocalRuntimeOptions,
) *LocalRuntime {
	t.Helper()
	provider, providerErr := NewDefaultCanonicalReceiptProvider(
		AdmissionPolicyVerifierFunc(func(
			envelope DescriptorBoundEnvelope,
		) (VerifiedAdmissionPolicy, error) {
			plain := envelope.Envelope()
			binding := AuthorityOrBootstrapFromBinding(SelfAuthority(plain.Caller.URA))
			bindingCopy := cloneAuthorityOrBootstrap(binding)
			issuer := plain.Callee
			proof := InvocationAuthorityProof{
				ProofType:     "test-verified-admission",
				Binding:       &bindingCopy,
				ProofHash:     AuthorityOrBootstrapProofHash(binding),
				Issuer:        &issuer,
				AdmissionHook: "test.go.canonical_receipt_provider.admission.v1",
			}
			return NewVerifiedAdmissionPolicy(envelope, binding, proof)
		}),
		ReceiptSigningAuthorityResolverFunc(func(
			callee AgentIdentity,
		) (ReceiptSigningAuthority, error) {
			return NewEd25519ReceiptSigningAuthority(callee, key, "go-test")
		}),
	)
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	runtime, err := NewLocalRuntimeWithOptions(
		&fixedKeyResolver{key: key.Public().(ed25519.PublicKey)},
		provider,
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func mustRegisterTestAbility(
	t *testing.T,
	runtime *LocalRuntime,
	abilityURA string,
	handler AbilityFn,
) {
	t.Helper()
	descriptorRef := testDescriptorRef(abilityURA)
	binding := mustProviderBinding(
		t,
		descriptorRef,
		Sha256([]byte("schema."+descriptorRef)),
		Sha256([]byte("impl."+descriptorRef)),
		"canonical-go-test-runtime-v1",
		handler,
	)
	if axonErr := runtime.BindProvider(binding); axonErr != nil {
		t.Fatal(axonErr)
	}
}

func mustProviderBinding(
	t *testing.T,
	descriptorRef string,
	schemaHash [32]byte,
	implHash [32]byte,
	runtimeEnv string,
	handler AbilityFn,
) ProviderBinding {
	t.Helper()
	descriptor, err := NewAbilityDescriptor(descriptorRef, schemaHash)
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := NewAbilityImpl(descriptorRef, implHash, runtimeEnv)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := NewProviderBinding(descriptor, implementation, handler)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func testDescriptorRef(abilityURA string) string {
	return testAbilityURA(abilityURA) + "@descriptor.v1#" + testDescriptorHashHex + "!invoke"
}

func testAbilityURA(ability string) string {
	if strings.HasPrefix(ability, "easynet:///") {
		return ability
	}
	return "easynet:///r/test/ability/authority.test." + strings.ReplaceAll(ability, "_", ".")
}

func mustTestRequest(
	t *testing.T,
	key ed25519.PrivateKey,
	abilityURA string,
	payload []byte,
	parentInvocationID string,
	spec *SupervisorSpec,
) DescriptorBoundInvocationRequest {
	t.Helper()
	envelope, err := NewDescriptorBoundEnvelope(InvocationEnvelope{
		Caller:          silanAgent(),
		Callee:          openaiAgent(),
		Subject:         signedSubject(),
		Ability:         testDescriptorRef(abilityURA),
		ArgsDigest:      Sha256(payload),
		InvocationNonce: FreshNonce(),
		CausalContext:   CausalNoneCtx(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := SignDescriptorBoundInvocationRequest(CallModeRPC, envelope, key, payload, "")
	if err != nil {
		t.Fatal(err)
	}
	return request.WithParentInvocationID(parentInvocationID).WithSupervisor(spec)
}

func mustTestInvoke(
	t *testing.T,
	runtime *LocalRuntime,
	key ed25519.PrivateKey,
	abilityURA string,
	payload []byte,
	parentInvocationID string,
	spec *SupervisorSpec,
) *InvocationHandle {
	t.Helper()
	handle, _, err := runtime.InvokeDescriptorBoundRequest(
		context.Background(),
		mustTestRequest(t, key, abilityURA, payload, parentInvocationID, spec),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}
