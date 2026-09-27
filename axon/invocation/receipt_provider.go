package axon

import (
	"crypto/ed25519"
	"reflect"
	"strings"
	"sync"
)

// VerifiedAdmissionPolicy is the complete, validated output of provider-owned
// authorization. Its evidence cannot be replaced after construction.
type VerifiedAdmissionPolicy struct {
	authorityBinding AuthorityOrBootstrap
	authorityProof   InvocationAuthorityProof
}

// NewVerifiedAdmissionPolicy validates policy evidence against the exact
// descriptor-bound envelope.
func NewVerifiedAdmissionPolicy(
	envelope DescriptorBoundEnvelope,
	authorityBinding AuthorityOrBootstrap,
	authorityProof InvocationAuthorityProof,
) (VerifiedAdmissionPolicy, error) {
	plain := envelope.Envelope()
	if err := validateAuthorityBinding(
		plain.Caller,
		plain.Subject,
		plain.Ability,
		authorityBinding,
	); err != nil {
		return VerifiedAdmissionPolicy{}, err
	}
	if err := validateAuthorityProof(authorityProof, authorityBinding, plain.Callee); err != nil {
		return VerifiedAdmissionPolicy{}, err
	}
	return VerifiedAdmissionPolicy{
		authorityBinding: cloneAuthorityOrBootstrap(authorityBinding),
		authorityProof:   cloneAuthorityProof(authorityProof),
	}, nil
}

// AdmissionPolicyVerifier is the product-neutral policy seam consumed by the
// canonical provider.
type AdmissionPolicyVerifier interface {
	Verify(envelope DescriptorBoundEnvelope) (VerifiedAdmissionPolicy, error)
}

// AdmissionPolicyVerifierFunc adapts a function to AdmissionPolicyVerifier.
type AdmissionPolicyVerifierFunc func(DescriptorBoundEnvelope) (VerifiedAdmissionPolicy, error)

// Verify implements AdmissionPolicyVerifier.
func (f AdmissionPolicyVerifierFunc) Verify(
	envelope DescriptorBoundEnvelope,
) (VerifiedAdmissionPolicy, error) {
	return f(envelope)
}

type admissionVerified struct{}

type receiptContextBound struct{}

// VerifiedDescriptorBoundAdmission is created only after signature, replay,
// payload, and provider policy admission have all succeeded.
type VerifiedDescriptorBoundAdmission struct {
	state          admissionVerified
	axiomBinding   AxiomBinding
	subjectRef     EntityRef
	inputHash      [32]byte
	parentReceipts []ReceiptRef
	authorityProof InvocationAuthorityProof
}

func newVerifiedDescriptorBoundAdmission(
	envelope DescriptorBoundEnvelope,
	policy VerifiedAdmissionPolicy,
) (VerifiedDescriptorBoundAdmission, error) {
	plain := envelope.Envelope()
	subjectRef, err := EntityRefForSubject(plain.Subject)
	if err != nil {
		return VerifiedDescriptorBoundAdmission{}, err
	}
	return VerifiedDescriptorBoundAdmission{
		state: admissionVerified{},
		axiomBinding: AxiomBinding{
			Caller:           plain.Caller,
			Callee:           plain.Callee,
			Subject:          plain.Subject,
			InvocationNonce:  plain.InvocationNonce,
			Causal:           cloneCausalContext(plain.CausalContext),
			PayloadDigest:    plain.ArgsDigest,
			AbilityBinding:   plain.Ability,
			AuthorityBinding: cloneAuthorityOrBootstrap(policy.authorityBinding),
		},
		subjectRef:     subjectRef,
		inputHash:      plain.ArgsDigest,
		parentReceipts: enumerableParentReceipts(plain.CausalContext),
		authorityProof: cloneAuthorityProof(policy.authorityProof),
	}, nil
}

// ResolvedDescriptorEvidence is catalog evidence for the exact admitted
// descriptor version.
type ResolvedDescriptorEvidence struct {
	descriptorVersion string
	schemaHash        [32]byte
}

func resolvedDescriptorEvidence(
	admission VerifiedDescriptorBoundAdmission,
	binding ProviderBinding,
) (ResolvedDescriptorEvidence, error) {
	admitted, err := ParseAbilityDescriptorRef(admission.axiomBinding.AbilityBinding)
	if err != nil {
		return ResolvedDescriptorEvidence{}, err
	}
	descriptor := binding.descriptor
	if admitted.Raw != descriptor.descriptorRef.Raw {
		return ResolvedDescriptorEvidence{}, ErrInvalidArgument("resolved_descriptor_ref_mismatch")
	}
	if admitted.Version != descriptor.descriptorRef.Version {
		return ResolvedDescriptorEvidence{}, ErrInvalidArgument("resolved_descriptor_version_mismatch")
	}
	if admitted.DescriptorHash != descriptor.descriptorRef.DescriptorHash {
		return ResolvedDescriptorEvidence{}, ErrInvalidArgument("resolved_descriptor_hash_mismatch")
	}
	if admitted.AdmissionAction != descriptor.descriptorRef.AdmissionAction {
		return ResolvedDescriptorEvidence{}, ErrInvalidArgument("resolved_descriptor_action_mismatch")
	}
	if descriptor.schemaHash == zeroHash32 {
		return ResolvedDescriptorEvidence{}, ErrInvalidArgument("resolved_descriptor_schema_hash_missing")
	}
	return ResolvedDescriptorEvidence{
		descriptorVersion: descriptor.descriptorRef.Version,
		schemaHash:        descriptor.schemaHash,
	}, nil
}

// RegisteredImplementationEvidence is registry evidence for the executable
// selected for one invocation.
type RegisteredImplementationEvidence struct {
	implHash   [32]byte
	runtimeEnv string
}

func registeredImplementationEvidence(
	binding ProviderBinding,
) (RegisteredImplementationEvidence, error) {
	implementation := binding.implementation
	if implementation.implHash == zeroHash32 {
		return RegisteredImplementationEvidence{}, ErrInvalidArgument("registered_implementation_hash_missing")
	}
	if strings.TrimSpace(implementation.runtimeEnv) == "" {
		return RegisteredImplementationEvidence{}, ErrInvalidArgument("registered_runtime_env_missing")
	}
	return RegisteredImplementationEvidence{
		implHash:   implementation.implHash,
		runtimeEnv: implementation.runtimeEnv,
	}, nil
}

// BoundReceiptContext is immutable provider-bound evidence for every receipt
// in one invocation.
type BoundReceiptContext struct {
	state             receiptContextBound
	invocationID      string
	axiomBinding      AxiomBinding
	subjectRef        EntityRef
	descriptorVersion string
	schemaHash        [32]byte
	implHash          [32]byte
	runtimeEnv        string
	authorityProof    InvocationAuthorityProof
	inputHash         [32]byte
	parentReceipts    []ReceiptRef
}

func bindReceiptContext(
	invocationID string,
	admission VerifiedDescriptorBoundAdmission,
	descriptor ResolvedDescriptorEvidence,
	implementation RegisteredImplementationEvidence,
) (*BoundReceiptContext, error) {
	if strings.TrimSpace(invocationID) == "" {
		return nil, ErrInvalidArgument("invocation_id_required")
	}
	context := &BoundReceiptContext{
		state:             receiptContextBound{},
		invocationID:      invocationID,
		axiomBinding:      cloneAxiomBinding(admission.axiomBinding),
		subjectRef:        admission.subjectRef,
		descriptorVersion: descriptor.descriptorVersion,
		schemaHash:        descriptor.schemaHash,
		implHash:          implementation.implHash,
		runtimeEnv:        implementation.runtimeEnv,
		authorityProof:    cloneAuthorityProof(admission.authorityProof),
		inputHash:         admission.inputHash,
		parentReceipts:    append([]ReceiptRef(nil), admission.parentReceipts...),
	}
	probe := []byte("bound-receipt-context")
	facts := context.proofFactsForOutput(probe)
	if err := ValidateReceiptFactSemantics(context, facts, probe); err != nil {
		return nil, err
	}
	return context, nil
}

// InvocationID returns the invocation identity bound to this context.
func (c *BoundReceiptContext) InvocationID() string {
	return c.invocationID
}

// Callee returns the exact callee whose receipt authority must sign.
func (c *BoundReceiptContext) Callee() AgentIdentity {
	return c.axiomBinding.Callee
}

func (c *BoundReceiptContext) proofFactsForOutput(output []byte) ReceiptProofFacts {
	return NewReceiptProofFacts(
		&c.subjectRef,
		c.descriptorVersion,
		c.schemaHash,
		c.implHash,
		c.runtimeEnv,
		cloneAuthorityProof(c.authorityProof),
		c.inputHash,
		Sha256(output),
		c.parentReceipts,
	)
}

// ReceiptSigningAuthority is the private-key boundary for one exact callee.
// Implementations must sign and self-verify before returning.
type ReceiptSigningAuthority interface {
	CalleeIdentity() AgentIdentity
	SignerIdentity() AgentIdentity
	HostAttestation() []byte
	VerifyingKey() ed25519.PublicKey
	SignAndVerify(canonicalReceipt []byte) (CalleeSignature, error)
}

// ReceiptSigningAuthorityResolver resolves the authority for one exact callee.
type ReceiptSigningAuthorityResolver interface {
	Resolve(callee AgentIdentity) (ReceiptSigningAuthority, error)
}

// ReceiptSigningAuthorityResolverFunc adapts a function to the resolver.
type ReceiptSigningAuthorityResolverFunc func(AgentIdentity) (ReceiptSigningAuthority, error)

// Resolve implements ReceiptSigningAuthorityResolver.
func (f ReceiptSigningAuthorityResolverFunc) Resolve(
	callee AgentIdentity,
) (ReceiptSigningAuthority, error) {
	return f(callee)
}

// Ed25519ReceiptSigningAuthority retains private key material outside
// LocalRuntime and returns only a self-verified signature.
type Ed25519ReceiptSigningAuthority struct {
	callee          AgentIdentity
	signer          AgentIdentity
	signingKey      ed25519.PrivateKey
	hostAttestation []byte
	keyIDHint       string
}

// NewEd25519ReceiptSigningAuthority constructs a self-signing callee
// authority.
func NewEd25519ReceiptSigningAuthority(
	callee AgentIdentity,
	signingKey ed25519.PrivateKey,
	keyIDHint string,
) (*Ed25519ReceiptSigningAuthority, error) {
	if strings.TrimSpace(callee.URA) == "" {
		return nil, ErrInvalidArgument("receipt_authority_callee_required")
	}
	if len(signingKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidArgument("receipt_authority_private_key_invalid")
	}
	return &Ed25519ReceiptSigningAuthority{
		callee:     callee,
		signer:     callee,
		signingKey: append(ed25519.PrivateKey(nil), signingKey...),
		keyIDHint:  keyIDHint,
	}, nil
}

// NewHostedEd25519ReceiptSigningAuthority constructs a host authority for a
// hosted callee.
func NewHostedEd25519ReceiptSigningAuthority(
	callee AgentIdentity,
	signer AgentIdentity,
	hostAttestation []byte,
	signingKey ed25519.PrivateKey,
	keyIDHint string,
) (*Ed25519ReceiptSigningAuthority, error) {
	if err := ValidateHostedAttestationAuthority(callee.URA, signer.URA); err != nil {
		return nil, err
	}
	if len(signingKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidArgument("receipt_authority_private_key_invalid")
	}
	if len(hostAttestation) != ed25519.SignatureSize {
		return nil, ErrInvalidArgument("host_attestation_wrong_length")
	}
	return &Ed25519ReceiptSigningAuthority{
		callee:          callee,
		signer:          signer,
		signingKey:      append(ed25519.PrivateKey(nil), signingKey...),
		hostAttestation: append([]byte(nil), hostAttestation...),
		keyIDHint:       keyIDHint,
	}, nil
}

// CalleeIdentity implements ReceiptSigningAuthority.
func (a *Ed25519ReceiptSigningAuthority) CalleeIdentity() AgentIdentity {
	return a.callee
}

// SignerIdentity implements ReceiptSigningAuthority.
func (a *Ed25519ReceiptSigningAuthority) SignerIdentity() AgentIdentity {
	return a.signer
}

// HostAttestation implements ReceiptSigningAuthority.
func (a *Ed25519ReceiptSigningAuthority) HostAttestation() []byte {
	return append([]byte(nil), a.hostAttestation...)
}

// VerifyingKey implements ReceiptSigningAuthority.
func (a *Ed25519ReceiptSigningAuthority) VerifyingKey() ed25519.PublicKey {
	key := a.signingKey.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), key...)
}

// SignAndVerify implements ReceiptSigningAuthority.
func (a *Ed25519ReceiptSigningAuthority) SignAndVerify(
	canonicalReceipt []byte,
) (CalleeSignature, error) {
	signature := ed25519.Sign(a.signingKey, canonicalReceipt)
	if !ed25519.Verify(a.VerifyingKey(), canonicalReceipt, signature) {
		return CalleeSignature{}, ErrInternal("receipt_signature_self_verification_failed")
	}
	return CalleeSignature{
		Algorithm: "ed25519",
		Signature: append([]byte(nil), signature...),
		KeyIDHint: a.keyIDHint,
	}, nil
}

// ReceiptAppendInput is runtime execution output. It intentionally excludes
// proof facts and signatures because only the provider may derive them.
type ReceiptAppendInput struct {
	ReceiptType        string
	State              InvocationState
	TimestampUnixMs    int64
	Payload            []byte
	PayloadContentType string
	CleanupComplete    bool
	Reason             string
	ChildInvocationID  string
	Usage              InvocationUsage
}

// CanonicalReceiptProvider owns policy binding, signing authority resolution,
// fact validation, and atomic receipt append.
type CanonicalReceiptProvider interface {
	VerifyAdmissionPolicy(
		envelope DescriptorBoundEnvelope,
	) (VerifiedAdmissionPolicy, error)
	Bind(
		invocationID string,
		admission VerifiedDescriptorBoundAdmission,
		descriptor ResolvedDescriptorEvidence,
		implementation RegisteredImplementationEvidence,
	) (*BoundReceiptContext, error)
	AppendSignedReceipt(
		context *BoundReceiptContext,
		input ReceiptAppendInput,
	) (SignedInvocationReceipt, error)
	ValidateReceiptFactSemantics(
		context *BoundReceiptContext,
		facts ReceiptProofFacts,
		output []byte,
	) error
	SnapshotSignedReceipts(invocationID string) []SignedInvocationReceipt
}

// DefaultCanonicalReceiptProvider is the canonical in-memory provider.
// Registry mutation uses one short global lock; each invocation owns an
// independent append reservation so authority calls never execute under a
// provider lock.
type DefaultCanonicalReceiptProvider struct {
	policyVerifier    AdmissionPolicyVerifier
	authorityResolver ReceiptSigningAuthorityResolver

	mu       sync.Mutex
	contexts map[string]*BoundReceiptContext
	chains   map[string]*receiptChainState
}

// NewDefaultCanonicalReceiptProvider constructs a provider with explicit
// policy and signing dependencies.
func NewDefaultCanonicalReceiptProvider(
	policyVerifier AdmissionPolicyVerifier,
	authorityResolver ReceiptSigningAuthorityResolver,
) (*DefaultCanonicalReceiptProvider, error) {
	if policyVerifier == nil {
		return nil, ErrInvalidArgument("admission_policy_verifier_required")
	}
	if authorityResolver == nil {
		return nil, ErrInvalidArgument("receipt_signing_authority_resolver_required")
	}
	return &DefaultCanonicalReceiptProvider{
		policyVerifier:    policyVerifier,
		authorityResolver: authorityResolver,
		contexts:          make(map[string]*BoundReceiptContext),
		chains:            make(map[string]*receiptChainState),
	}, nil
}

// VerifyAdmissionPolicy validates the verifier output again at the provider
// boundary.
func (p *DefaultCanonicalReceiptProvider) VerifyAdmissionPolicy(
	envelope DescriptorBoundEnvelope,
) (VerifiedAdmissionPolicy, error) {
	policy, err := p.policyVerifier.Verify(envelope)
	if err != nil {
		return VerifiedAdmissionPolicy{}, err
	}
	return NewVerifiedAdmissionPolicy(
		envelope,
		policy.authorityBinding,
		policy.authorityProof,
	)
}

// Bind installs one immutable receipt context. Rebinding an invocation is
// rejected.
func (p *DefaultCanonicalReceiptProvider) Bind(
	invocationID string,
	admission VerifiedDescriptorBoundAdmission,
	descriptor ResolvedDescriptorEvidence,
	implementation RegisteredImplementationEvidence,
) (*BoundReceiptContext, error) {
	context, err := bindReceiptContext(invocationID, admission, descriptor, implementation)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.contexts[invocationID]; exists {
		return nil, ErrInvalidArgument("receipt_context_already_bound")
	}
	p.contexts[invocationID] = context
	p.chains[invocationID] = newReceiptChainState()
	return context, nil
}

// AppendSignedReceipt performs canonicalization, exact-authority signing,
// independent self-verification, and append as one provider-owned transition.
func (p *DefaultCanonicalReceiptProvider) AppendSignedReceipt(
	context *BoundReceiptContext,
	input ReceiptAppendInput,
) (SignedInvocationReceipt, error) {
	if context == nil {
		return emptySignedReceipt(), ErrInvalidArgument("receipt_context_not_bound")
	}
	p.mu.Lock()
	boundContext := p.contexts[context.invocationID]
	chain := p.chains[context.invocationID]
	p.mu.Unlock()
	if boundContext != context || chain == nil {
		return emptySignedReceipt(), ErrInvalidArgument("receipt_context_not_bound")
	}
	if strings.TrimSpace(input.ReceiptType) == "" {
		return emptySignedReceipt(), ErrInvalidArgument("receipt_type_required")
	}
	if input.State == StateUnspecified || string(input.State) != strings.ToUpper(string(input.State)) {
		return emptySignedReceipt(), ErrInvalidArgument("receipt_state_invalid")
	}
	if input.State.IsTerminal() && !input.CleanupComplete {
		return emptySignedReceipt(), ErrInternal("terminal_receipt_requires_cleanup_complete")
	}
	var terminal *executionTerminal
	if input.State.IsTerminal() {
		terminalState := executionTerminal{input: input}
		terminal = &terminalState
	}

	payload := append([]byte(nil), input.Payload...)
	facts := context.proofFactsForOutput(payload)
	if err := p.ValidateReceiptFactSemantics(context, facts, payload); err != nil {
		return emptySignedReceipt(), err
	}
	reservation, err := chain.reserveAppend()
	if err != nil {
		return emptySignedReceipt(), err
	}
	committed := false
	defer func() {
		if !committed {
			chain.abortAppend()
		}
	}()

	authority, err := p.authorityResolver.Resolve(context.Callee())
	if err != nil {
		return emptySignedReceipt(), err
	}
	hosted, err := validateSigningAuthority(context.Callee(), authority)
	if err != nil {
		return emptySignedReceipt(), err
	}

	binding := cloneAxiomBinding(context.axiomBinding)
	binding.PayloadDigest = Sha256(payload)
	binding.ProofFacts = cloneReceiptProofFacts(facts)
	binding.Hosted = hosted
	draft := newReceiptRecord(receiptRecordInput{
		Index:              reservation.index,
		InvocationID:       context.invocationID,
		ReceiptType:        input.ReceiptType,
		State:              string(input.State),
		TimestampUnixMs:    input.TimestampUnixMs,
		PrevReceiptHash:    reservation.prevHash,
		Payload:            payload,
		PayloadContentType: input.PayloadContentType,
		CleanupComplete:    input.CleanupComplete,
		Reason:             input.Reason,
		ChildInvocationID:  input.ChildInvocationID,
		AxiomBinding:       binding,
		Usage:              input.Usage,
	})
	canonicalized := receiptCanonicalized{record: draft, terminal: terminal}
	signature, err := authority.SignAndVerify(canonicalized.canonicalBytes())
	if err != nil {
		return emptySignedReceipt(), err
	}
	signed := receiptSigned{canonicalized: canonicalized, signature: signature}
	verified, err := signed.selfVerify(authority)
	if err != nil {
		return emptySignedReceipt(), err
	}
	signedReceipt := verified.signedReceipt()
	appended, err := chain.commitAppend(
		reservation,
		signedReceipt,
		input.State.IsTerminal(),
	)
	if err != nil {
		return emptySignedReceipt(), err
	}
	committed = true
	return appended.receipt, nil
}

// ValidateReceiptFactSemantics implements CanonicalReceiptProvider.
func (p *DefaultCanonicalReceiptProvider) ValidateReceiptFactSemantics(
	context *BoundReceiptContext,
	facts ReceiptProofFacts,
	output []byte,
) error {
	return ValidateReceiptFactSemantics(context, facts, output)
}

// SnapshotSignedReceipts returns immutable receipt values.
func (p *DefaultCanonicalReceiptProvider) SnapshotSignedReceipts(
	invocationID string,
) []SignedInvocationReceipt {
	p.mu.Lock()
	chain := p.chains[invocationID]
	p.mu.Unlock()
	if chain == nil {
		return nil
	}
	return chain.snapshot()
}

type receiptChainState struct {
	mu        sync.Mutex
	cond      *sync.Cond
	appending bool
	terminal  bool
	receipts  []SignedInvocationReceipt
}

func newReceiptChainState() *receiptChainState {
	chain := &receiptChainState{}
	chain.cond = sync.NewCond(&chain.mu)
	return chain
}

type receiptAppendReservation struct {
	index    uint64
	prevHash [32]byte
}

func (c *receiptChainState) reserveAppend() (receiptAppendReservation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.appending {
		c.cond.Wait()
	}
	if c.terminal {
		return receiptAppendReservation{}, ErrInvalidArgument("receipt_chain_terminal")
	}
	c.appending = true
	reservation := receiptAppendReservation{
		index:    uint64(len(c.receipts)),
		prevHash: ZeroHash,
	}
	if len(c.receipts) > 0 {
		reservation.prevHash = c.receipts[len(c.receipts)-1].SelfHash()
	}
	return reservation, nil
}

func (c *receiptChainState) abortAppend() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.appending {
		return
	}
	c.appending = false
	c.cond.Broadcast()
}

func (c *receiptChainState) commitAppend(
	reservation receiptAppendReservation,
	receipt SignedInvocationReceipt,
	terminal bool,
) (signedReceiptAppended, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.appending ||
		uint64(len(c.receipts)) != reservation.index ||
		(len(c.receipts) == 0 && reservation.prevHash != ZeroHash) ||
		(len(c.receipts) > 0 &&
			c.receipts[len(c.receipts)-1].SelfHash() != reservation.prevHash) {
		return signedReceiptAppended{}, ErrInternal("receipt_sequence_reservation_lost")
	}
	c.receipts = append(c.receipts, receipt)
	c.terminal = terminal
	c.appending = false
	c.cond.Broadcast()
	return signedReceiptAppended{receipt: receipt}, nil
}

func (c *receiptChainState) snapshot() []SignedInvocationReceipt {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SignedInvocationReceipt(nil), c.receipts...)
}

type executionTerminal struct {
	input ReceiptAppendInput
}

type receiptCanonicalized struct {
	record   receiptRecord
	terminal *executionTerminal
}

func (s receiptCanonicalized) canonicalBytes() []byte {
	return canonicalSerialise(&s.record)
}

type receiptSigned struct {
	canonicalized receiptCanonicalized
	signature     CalleeSignature
}

func (s receiptSigned) selfVerify(
	authority ReceiptSigningAuthority,
) (signatureSelfVerified, error) {
	if err := validateCalleeSignatureStructure(s.signature); err != nil {
		return signatureSelfVerified{}, err
	}
	canonical := s.canonicalized.canonicalBytes()
	if !ed25519.Verify(authority.VerifyingKey(), canonical, s.signature.Signature) {
		return signatureSelfVerified{}, ErrInternal("receipt_signature_self_verification_failed")
	}
	record := cloneReceiptRecord(s.canonicalized.record)
	record.CalleeSignature = cloneCalleeSignature(s.signature)
	record.SelfHash = computeReceiptHash(&record)
	return signatureSelfVerified{record: record}, nil
}

type signatureSelfVerified struct {
	record receiptRecord
}

func (s signatureSelfVerified) signedReceipt() SignedInvocationReceipt {
	return SignedInvocationReceipt{receipt: cloneReceiptRecord(s.record)}
}

type signedReceiptAppended struct {
	receipt SignedInvocationReceipt
}

// ValidateReceiptFactSemantics rejects missing, zero, synthesized, or
// mismatched canonical receipt facts.
func ValidateReceiptFactSemantics(
	context *BoundReceiptContext,
	facts ReceiptProofFacts,
	output []byte,
) error {
	if context == nil {
		return ErrInvalidArgument("receipt_context_required")
	}
	if facts.SubjectRef == nil {
		return ErrInvalidArgument("receipt_subject_ref_missing")
	}
	if *facts.SubjectRef != context.subjectRef || strings.TrimSpace(facts.SubjectRef.URA) == "" {
		return ErrInvalidArgument("receipt_subject_ref_semantically_invalid")
	}
	if strings.TrimSpace(facts.DescriptorVersion) == "" ||
		facts.DescriptorVersion != context.descriptorVersion {
		return ErrInvalidArgument("receipt_descriptor_version_semantically_invalid")
	}
	if facts.SchemaHash == zeroHash32 {
		return ErrInvalidArgument("receipt_schema_hash_unbound")
	}
	if facts.SchemaHash != context.schemaHash {
		return ErrInvalidArgument("receipt_schema_hash_semantically_invalid")
	}
	if facts.ImplHash == zeroHash32 {
		return ErrInvalidArgument("receipt_impl_hash_unbound")
	}
	if facts.ImplHash != context.implHash {
		return ErrInvalidArgument("receipt_impl_hash_semantically_invalid")
	}
	if strings.TrimSpace(facts.RuntimeEnv) == "" || facts.RuntimeEnv != context.runtimeEnv {
		return ErrInvalidArgument("receipt_runtime_env_missing")
	}
	if facts.InputHash == zeroHash32 {
		return ErrInvalidArgument("receipt_input_hash_unbound")
	}
	if facts.InputHash != context.inputHash {
		return ErrInvalidArgument("receipt_input_hash_semantically_invalid")
	}
	expectedOutputHash := Sha256(output)
	if facts.OutputHash == zeroHash32 || facts.OutputHash != expectedOutputHash {
		return ErrInvalidArgument("receipt_output_hash_semantically_invalid")
	}
	if err := validateAuthorityBinding(
		context.axiomBinding.Caller,
		context.axiomBinding.Subject,
		context.axiomBinding.AbilityBinding,
		context.axiomBinding.AuthorityBinding,
	); err != nil {
		return err
	}
	if err := validateAuthorityProof(
		facts.AuthorityProof,
		context.axiomBinding.AuthorityBinding,
		context.axiomBinding.Callee,
	); err != nil {
		return err
	}
	if !reflect.DeepEqual(facts.AuthorityProof, context.authorityProof) {
		return ErrInvalidArgument("receipt_authority_proof_semantically_invalid")
	}
	for _, parent := range facts.ParentReceipts {
		if parent.ReceiptHash == zeroHash32 || strings.TrimSpace(parent.ReceiptURA) == "" {
			return ErrInvalidArgument("receipt_parent_reference_semantically_invalid")
		}
	}
	if !receiptRefsEqual(facts.ParentReceipts, context.parentReceipts) {
		return ErrInvalidArgument("receipt_parent_receipts_semantically_invalid")
	}
	return nil
}

func validateSigningAuthority(
	callee AgentIdentity,
	authority ReceiptSigningAuthority,
) (*HostedAttestation, error) {
	if authority == nil {
		return nil, ErrInvalidArgument("receipt_signing_authority_required")
	}
	if authority.CalleeIdentity() != callee {
		return nil, ErrInvalidArgument("receipt_signing_authority_callee_mismatch")
	}
	signer := authority.SignerIdentity()
	key := authority.VerifyingKey()
	if len(key) != ed25519.PublicKeySize {
		return nil, ErrInvalidArgument("receipt_signing_authority_key_invalid")
	}
	attestation := authority.HostAttestation()
	if signer == callee {
		if len(attestation) != 0 {
			return nil, ErrInvalidArgument("self_signing_authority_has_host_attestation")
		}
		return nil, nil
	}
	if err := VerifyHostAttestation(callee.URA, signer.URA, attestation, key); err != nil {
		return nil, err
	}
	return &HostedAttestation{
		SignerBinding:   signer,
		HostAttestation: append([]byte(nil), attestation...),
	}, nil
}

func validateCalleeSignatureStructure(signature CalleeSignature) error {
	if signature.Algorithm != "ed25519" {
		return ErrInvalidArgument("receipt_signature_algorithm_invalid")
	}
	if len(signature.Signature) != ed25519.SignatureSize {
		return ErrInvalidArgument("receipt_signature_length_invalid")
	}
	return nil
}

// validateAuthorityBinding dispatches to the shallow structural gate for
// whichever plane the binding carries. Mirrors the Rust
// validate_authority_binding.
func validateAuthorityBinding(
	caller AgentIdentity,
	subject SubjectIdentity,
	ability string,
	binding AuthorityOrBootstrap,
) error {
	if binding.IsBootstrap {
		if binding.Bootstrap == nil {
			return ErrInvalidArgument("authority_binding_missing")
		}
		return validateBootstrapBindingFields(caller, ability, *binding.Bootstrap)
	}
	if binding.Binding == nil {
		return ErrInvalidArgument("authority_binding_missing")
	}
	return validateAuthorityBindingFields(caller, subject, *binding.Binding)
}

func validateBootstrapBindingFields(
	caller AgentIdentity,
	admittedAbility string,
	bootstrap BootstrapBinding,
) error {
	if bootstrap.PrincipalURA != caller.URA ||
		bootstrap.Ability != admittedAbility ||
		strings.TrimSpace(bootstrap.Realm) == "" {
		return ErrInvalidArgument("authority_bootstrap_binding_mismatch")
	}
	return nil
}

// validateAuthorityBindingFields is the shallow structural gate at
// receipt-construction time. Mirrors the frozen compatibility matrix in
// document/rfcs/001-authority-binding-relation-evidence.md — deep
// semantic verification (signature checks, expiry, scope matching)
// happens later in ProveAuthority; this only rejects bindings that
// already contradict the envelope's public tuple facts.
func validateAuthorityBindingFields(
	caller AgentIdentity,
	subject SubjectIdentity,
	binding AuthorityBinding,
) error {
	switch {
	case binding.Relation == AuthorityRelationSelf && binding.EvidenceKind == AuthorityEvidenceIdentity:
		if strings.TrimSpace(binding.Authority.URA) == "" || binding.Authority.URA != caller.URA {
			return ErrInvalidArgument("authority_self_principal_mismatch")
		}
	case binding.Relation == AuthorityRelationDelegatedBy && binding.EvidenceKind == AuthorityEvidenceDelegation:
		// No cross-check against envelope.subject here — DelegatedBy's
		// authority is who the caller acts for, an independent
		// dimension from the envelope subject (see RFC doc field
		// provenance archaeology). The caller-match check belongs to
		// signature verification (the claim binds envelope.caller as
		// delegatee), not this shallow gate.
		evidence := binding.Delegation
		if strings.TrimSpace(binding.Authority.URA) == "" ||
			evidence == nil ||
			strings.TrimSpace(evidence.Issuer.URA) == "" ||
			strings.TrimSpace(evidence.Audience) == "" ||
			len(evidence.Scopes) == 0 ||
			len(evidence.Signature) == 0 {
			return ErrInvalidArgument("authority_delegation_binding_mismatch")
		}
	case binding.Relation == AuthorityRelationSessionOf && binding.EvidenceKind == AuthorityEvidenceSession:
		evidence := binding.Session
		if strings.TrimSpace(binding.Authority.URA) == "" ||
			binding.Authority.URA != subject.URA ||
			evidence == nil ||
			strings.TrimSpace(evidence.Issuer.URA) == "" ||
			strings.TrimSpace(evidence.SessionID) == "" ||
			len(evidence.Scopes) == 0 ||
			len(evidence.Audiences) == 0 ||
			len(evidence.Signature) == 0 {
			return ErrInvalidArgument("authority_session_incomplete")
		}
	case binding.Relation == AuthorityRelationCredentialOf && binding.EvidenceKind == AuthorityEvidenceAttestation:
		return ErrInvalidArgument("authority_credential_of_reserved")
	default:
		return ErrInvalidArgument("authority_relation_evidence_mismatch")
	}
	return nil
}

func validateAuthorityProof(
	proof InvocationAuthorityProof,
	binding AuthorityOrBootstrap,
	callee AgentIdentity,
) error {
	if strings.TrimSpace(proof.ProofType) == "" {
		return ErrInvalidArgument("receipt_authority_proof_type_missing")
	}
	if proof.Binding == nil || !reflect.DeepEqual(*proof.Binding, binding) {
		return ErrInvalidArgument("receipt_authority_proof_binding_mismatch")
	}
	if proof.Issuer == nil {
		return ErrInvalidArgument("receipt_authority_proof_issuer_missing")
	}
	if *proof.Issuer != callee || strings.TrimSpace(proof.Issuer.URA) == "" {
		return ErrInvalidArgument("receipt_authority_proof_issuer_mismatch")
	}
	if strings.TrimSpace(proof.AdmissionHook) == "" {
		return ErrInvalidArgument("receipt_authority_proof_admission_hook_missing")
	}
	expectedHash := AuthorityProofExpectedHash(proof)
	if expectedHash == zeroHash32 || proof.ProofHash == zeroHash32 || proof.ProofHash != expectedHash {
		return ErrInvalidArgument("receipt_authority_proof_hash_invalid")
	}
	return nil
}

func enumerableParentReceipts(causal CausalContext) []ReceiptRef {
	switch causal.Form {
	case CausalScalar:
		if causal.Scalar == nil {
			return nil
		}
		return []ReceiptRef{*causal.Scalar}
	case CausalList:
		return append([]ReceiptRef(nil), causal.List...)
	default:
		return nil
	}
}

func receiptRefsEqual(left []ReceiptRef, right []ReceiptRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneAuthorityBinding(binding AuthorityBinding) AuthorityBinding {
	copy := binding
	if binding.Delegation != nil {
		delegation := *binding.Delegation
		delegation.Scopes = append([]string(nil), binding.Delegation.Scopes...)
		delegation.Signature = append([]byte(nil), binding.Delegation.Signature...)
		copy.Delegation = &delegation
	}
	if binding.Session != nil {
		session := *binding.Session
		session.Scopes = append([]string(nil), binding.Session.Scopes...)
		session.Audiences = append([]string(nil), binding.Session.Audiences...)
		session.Signature = append([]byte(nil), binding.Session.Signature...)
		copy.Session = &session
	}
	return copy
}

func cloneAuthorityOrBootstrap(binding AuthorityOrBootstrap) AuthorityOrBootstrap {
	copy := binding
	if binding.Binding != nil {
		cloned := cloneAuthorityBinding(*binding.Binding)
		copy.Binding = &cloned
	}
	if binding.Bootstrap != nil {
		bootstrap := *binding.Bootstrap
		copy.Bootstrap = &bootstrap
	}
	return copy
}

func cloneAuthorityProof(proof InvocationAuthorityProof) InvocationAuthorityProof {
	copy := proof
	if proof.Binding != nil {
		binding := cloneAuthorityOrBootstrap(*proof.Binding)
		copy.Binding = &binding
	}
	copy.ProofPayload = append([]byte(nil), proof.ProofPayload...)
	if proof.Issuer != nil {
		issuer := *proof.Issuer
		copy.Issuer = &issuer
	}
	if proof.Signature != nil {
		signature := cloneCalleeSignature(*proof.Signature)
		copy.Signature = &signature
	}
	return copy
}

func cloneReceiptProofFacts(facts ReceiptProofFacts) ReceiptProofFacts {
	copy := facts
	if facts.SubjectRef != nil {
		subject := *facts.SubjectRef
		copy.SubjectRef = &subject
	}
	copy.AuthorityProof = cloneAuthorityProof(facts.AuthorityProof)
	copy.ParentReceipts = append([]ReceiptRef(nil), facts.ParentReceipts...)
	return copy
}

func cloneAxiomBinding(binding AxiomBinding) AxiomBinding {
	copy := binding
	copy.Causal = cloneCausalContext(binding.Causal)
	copy.AuthorityBinding = cloneAuthorityOrBootstrap(binding.AuthorityBinding)
	copy.ProofFacts = cloneReceiptProofFacts(binding.ProofFacts)
	if binding.Hosted != nil {
		hosted := *binding.Hosted
		hosted.HostAttestation = append([]byte(nil), binding.Hosted.HostAttestation...)
		copy.Hosted = &hosted
	}
	return copy
}

func cloneCalleeSignature(signature CalleeSignature) CalleeSignature {
	copy := signature
	copy.Signature = append([]byte(nil), signature.Signature...)
	return copy
}

var zeroHash32 [32]byte
