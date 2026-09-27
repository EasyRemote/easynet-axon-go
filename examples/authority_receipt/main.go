// Command authority_receipt demonstrates the canonical provider-owned receipt
// flow: signed call -> runtime -> signed receipt -> verify -> trace -> authority.
package main

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"

	inv "axon.run/sdk/go/axon/invocation"
)

const descriptorRef = "easynet:///r/openai/ability/openai.reviewer.review@1.0.0#0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef!invoke"

type staticResolver struct {
	keys map[string]ed25519.PublicKey
}

func (r *staticResolver) Resolve(ura string) (ed25519.PublicKey, error) {
	key, ok := r.keys[ura]
	if !ok {
		return nil, inv.ErrInvalidArgument("unknown_agent_key:" + ura)
	}
	return key, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	callerKey, err := signingKey(3)
	if err != nil {
		return err
	}
	calleeKey, err := signingKey(7)
	if err != nil {
		return err
	}
	caller := inv.NewAgentIdentity(
		"easynet:///r/silan/agent/silan.alice",
		inv.ProfileStrictV2,
	)
	callee := inv.NewAgentIdentity(
		"easynet:///r/openai/agent/openai.reviewer",
		inv.ProfileStrictV2,
	)
	subject := inv.NewSubjectIdentity(
		"easynet:///r/silan/resource/silan.papers/paper",
		inv.ProfileStrictV2,
	)
	resolver := &staticResolver{keys: map[string]ed25519.PublicKey{
		caller.URA: callerKey.Public().(ed25519.PublicKey),
		callee.URA: calleeKey.Public().(ed25519.PublicKey),
	}}

	provider, err := inv.NewDefaultCanonicalReceiptProvider(
		inv.AdmissionPolicyVerifierFunc(func(
			envelope inv.DescriptorBoundEnvelope,
		) (inv.VerifiedAdmissionPolicy, error) {
			plain := envelope.Envelope()
			binding := inv.AuthorityOrBootstrapFromBinding(inv.SelfAuthority(plain.Caller.URA))
			proofBinding := binding
			issuer := plain.Callee
			return inv.NewVerifiedAdmissionPolicy(
				envelope,
				binding,
				inv.InvocationAuthorityProof{
					ProofType:     "example-verified-admission",
					Binding:       &proofBinding,
					ProofHash:     inv.AuthorityOrBootstrapProofHash(binding),
					Issuer:        &issuer,
					AdmissionHook: "example.go.authority_receipt.admission.v1",
				},
			)
		}),
		inv.ReceiptSigningAuthorityResolverFunc(func(
			resolvedCallee inv.AgentIdentity,
		) (inv.ReceiptSigningAuthority, error) {
			return inv.NewEd25519ReceiptSigningAuthority(
				resolvedCallee,
				calleeKey,
				"example-callee",
			)
		}),
	)
	if err != nil {
		return err
	}
	runtime, axonErr := inv.NewLocalRuntime(resolver, provider)
	if axonErr != nil {
		return axonErr
	}
	descriptor, err := inv.NewAbilityDescriptor(
		descriptorRef,
		inv.Sha256([]byte("schema.review.v1")),
	)
	if err != nil {
		return err
	}
	implementation, err := inv.NewAbilityImpl(
		descriptorRef,
		inv.Sha256([]byte("impl.review.v1")),
		"canonical-go-example-runtime-v1",
	)
	if err != nil {
		return err
	}
	binding, err := inv.NewProviderBinding(
		descriptor,
		implementation,
		func(_ context.Context, ability *inv.AbilityContext) ([]byte, *inv.AxonError) {
			return ability.Payload, nil
		},
	)
	if err != nil {
		return err
	}
	if axonErr := runtime.BindProvider(binding); axonErr != nil {
		return axonErr
	}

	payload := []byte("{}")
	envelope, err := inv.NewDescriptorBoundEnvelope(inv.InvocationEnvelope{
		Caller:          caller,
		Callee:          callee,
		Subject:         subject,
		Ability:         descriptorRef,
		ArgsDigest:      inv.Sha256(payload),
		InvocationNonce: inv.FreshNonce(),
		CausalContext:   inv.CausalNoneCtx(),
	})
	if err != nil {
		return err
	}
	request, err := inv.SignDescriptorBoundInvocationRequest(
		inv.CallModeRPC,
		envelope,
		callerKey,
		payload,
		"example-caller",
	)
	if err != nil {
		return err
	}
	handle, _, axonErr := runtime.InvokeDescriptorBoundRequest(
		context.Background(),
		request,
	)
	if axonErr != nil {
		return axonErr
	}
	handle.Wait(context.Background())
	receipts := handle.SnapshotReceipts()
	receipt := receipts[len(receipts)-1]
	selfHash := receipt.SelfHash()
	fmt.Printf(
		"receipt: index=%d self_hash=%x...\n",
		receipt.Index(),
		selfHash[:6],
	)

	verified, err := receipt.Verify(resolver)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	fmt.Printf(
		"verify:  ok signer=%s model=%d\n",
		verified.SignerURA,
		verified.SigningModel,
	)

	trace := receipt.Trace()
	if trace.Kind == inv.TraceRoot {
		fmt.Println("trace:   root (no direct parent)")
	}
	proof, err := receipt.ProveAuthority(resolver)
	if err != nil {
		return fmt.Errorf("prove_authority: %w", err)
	}
	fmt.Printf("authority: self principal=%s\n", proof.Principal())
	return nil
}

func signingKey(marker byte) (ed25519.PrivateKey, error) {
	var seed [32]byte
	seed[0] = marker
	return inv.SigningKeyFromBytes(seed[:])
}
