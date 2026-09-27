package industrial

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"

	inv "axon.run/sdk/go/axon/invocation"
)

const industrialDescriptorHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type industrialKeyResolver struct {
	key ed25519.PublicKey
}

func (r *industrialKeyResolver) Resolve(_ string) (ed25519.PublicKey, error) {
	return r.key, nil
}

func industrialSigningKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	var seed [32]byte
	for index := range seed {
		seed[index] = 0x6b
	}
	key, err := inv.SigningKeyFromBytes(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newIndustrialRuntime(t *testing.T) (*inv.LocalRuntime, ed25519.PrivateKey) {
	t.Helper()
	key := industrialSigningKey(t)
	provider, err := inv.NewDefaultCanonicalReceiptProvider(
		inv.AdmissionPolicyVerifierFunc(func(
			envelope inv.DescriptorBoundEnvelope,
		) (inv.VerifiedAdmissionPolicy, error) {
			plain := envelope.Envelope()
			binding := inv.AuthorityOrBootstrapFromBinding(inv.SelfAuthority(plain.Caller.URA))
			bindingCopy := binding
			issuer := plain.Callee
			proof := inv.InvocationAuthorityProof{
				ProofType:     "industrial-verified-admission",
				Binding:       &bindingCopy,
				ProofHash:     inv.AuthorityOrBootstrapProofHash(binding),
				Issuer:        &issuer,
				AdmissionHook: "test.go.industrial.admission.v1",
			}
			return inv.NewVerifiedAdmissionPolicy(envelope, binding, proof)
		}),
		inv.ReceiptSigningAuthorityResolverFunc(func(
			callee inv.AgentIdentity,
		) (inv.ReceiptSigningAuthority, error) {
			return inv.NewEd25519ReceiptSigningAuthority(callee, key, "go-industrial-test")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime, axonErr := inv.NewLocalRuntime(
		&industrialKeyResolver{key: key.Public().(ed25519.PublicKey)},
		provider,
	)
	if axonErr != nil {
		t.Fatal(axonErr)
	}
	return runtime, key
}

func industrialDescriptorRef(name string) string {
	return industrialAbilityURA(name) + "@descriptor.v1#" + industrialDescriptorHash + "!invoke"
}

func registerIndustrialAbility(runtime *inv.LocalRuntime, name string, ability inv.AbilityFn) {
	descriptorRef := industrialDescriptorRef(name)
	descriptor, err := inv.NewAbilityDescriptor(
		descriptorRef,
		inv.Sha256([]byte("schema."+descriptorRef)),
	)
	if err != nil {
		panic(err)
	}
	implementation, err := inv.NewAbilityImpl(
		descriptorRef,
		inv.Sha256([]byte("impl."+descriptorRef)),
		"canonical-go-industrial-test-runtime-v1",
	)
	if err != nil {
		panic(err)
	}
	binding, err := inv.NewProviderBinding(descriptor, implementation, ability)
	if err != nil {
		panic(err)
	}
	if axonErr := runtime.BindProvider(binding); axonErr != nil {
		panic(axonErr)
	}
}

func industrialAbilityURA(name string) string {
	return "easynet:///r/test/ability/authority.industrial." + strings.ReplaceAll(name, "_", ".")
}

func industrialRequest(
	key ed25519.PrivateKey,
	name string,
	payload []byte,
	parentInvocationID string,
	spec *inv.SupervisorSpec,
) (inv.DescriptorBoundInvocationRequest, error) {
	ability := industrialDescriptorRef(name)
	envelope, err := inv.NewDescriptorBoundEnvelope(inv.InvocationEnvelope{
		Caller: inv.NewAgentIdentity(
			"easynet:///r/test/agents/industrial.caller@1",
			inv.ProfileStrictV2,
		),
		Callee: inv.NewAgentIdentity(
			"easynet:///r/test/agents/industrial.runtime@1",
			inv.ProfileStrictV2,
		),
		Subject: inv.NewSubjectIdentity(
			"easynet:///r/test/resource/industrial.subject",
			inv.ProfileStrictV2,
		),
		Ability:         ability,
		ArgsDigest:      inv.Sha256(payload),
		InvocationNonce: inv.FreshNonce(),
		CausalContext:   inv.CausalNoneCtx(),
	})
	if err != nil {
		return inv.DescriptorBoundInvocationRequest{}, err
	}
	request, err := inv.SignDescriptorBoundInvocationRequest(inv.CallModeRPC, envelope, key, payload, "")
	if err != nil {
		return inv.DescriptorBoundInvocationRequest{}, err
	}
	return request.WithParentInvocationID(parentInvocationID).WithSupervisor(spec), nil
}

func invokeIndustrialRuntime(
	ctx context.Context,
	runtime *inv.LocalRuntime,
	key ed25519.PrivateKey,
	name string,
	payload []byte,
	parentInvocationID string,
	spec *inv.SupervisorSpec,
) (*inv.InvocationHandle, *inv.AxonError) {
	request, err := industrialRequest(key, name, payload, parentInvocationID, spec)
	if err != nil {
		return nil, inv.ErrInternal("industrial_request_build_failed:" + err.Error())
	}
	handle, _, axonErr := runtime.InvokeDescriptorBoundRequest(ctx, request)
	return handle, axonErr
}

func invokeIndustrial(
	t *testing.T,
	ctx context.Context,
	runtime *inv.LocalRuntime,
	key ed25519.PrivateKey,
	name string,
	payload []byte,
	parentInvocationID string,
	spec *inv.SupervisorSpec,
) (*inv.InvocationHandle, *inv.AxonError) {
	t.Helper()
	return invokeIndustrialRuntime(ctx, runtime, key, name, payload, parentInvocationID, spec)
}
