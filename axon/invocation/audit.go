package axon

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// ZeroHash is the sentinel prev_receipt_hash for receipts[0].
var ZeroHash = [32]byte{}

// AxiomBinding is the immutable invocation evidence copied into every receipt.
// Signature ownership is deliberately absent: only SignedInvocationReceipt
// carries a provider-verified callee signature.
type AxiomBinding struct {
	Caller           AgentIdentity
	Callee           AgentIdentity
	Subject          SubjectIdentity
	InvocationNonce  [16]byte
	Causal           CausalContext
	PayloadDigest    [32]byte
	AbilityBinding   string
	AuthorityBinding AuthorityOrBootstrap
	Hosted           *HostedAttestation
	ProofFacts       ReceiptProofFacts
}

type receiptRecord struct {
	Index              uint64
	InvocationID       string
	ReceiptType        string
	State              string
	TimestampUnixMs    int64
	PrevReceiptHash    [32]byte
	SelfHash           [32]byte
	Payload            []byte
	PayloadContentType string
	CleanupComplete    bool
	Reason             string
	ChildInvocationID  string
	AxiomBinding       AxiomBinding
	Usage              InvocationUsage
	CalleeSignature    CalleeSignature
}

type receiptRecordInput struct {
	Index              uint64
	InvocationID       string
	ReceiptType        string
	State              string
	TimestampUnixMs    int64
	PrevReceiptHash    [32]byte
	Payload            []byte
	PayloadContentType string
	CleanupComplete    bool
	Reason             string
	ChildInvocationID  string
	AxiomBinding       AxiomBinding
	Usage              InvocationUsage
}

func newReceiptRecord(input receiptRecordInput) receiptRecord {
	record := receiptRecord{
		Index:              input.Index,
		InvocationID:       input.InvocationID,
		ReceiptType:        input.ReceiptType,
		State:              input.State,
		TimestampUnixMs:    input.TimestampUnixMs,
		PrevReceiptHash:    input.PrevReceiptHash,
		Payload:            append([]byte(nil), input.Payload...),
		PayloadContentType: input.PayloadContentType,
		CleanupComplete:    input.CleanupComplete,
		Reason:             input.Reason,
		ChildInvocationID:  input.ChildInvocationID,
		AxiomBinding:       cloneAxiomBinding(input.AxiomBinding),
		Usage:              input.Usage,
	}
	record.SelfHash = computeReceiptHash(&record)
	return record
}

// SignedInvocationReceipt is an opaque immutable receipt accepted only after
// the exact signing authority has self-verified its signature.
type SignedInvocationReceipt struct {
	receipt receiptRecord
}

// UnverifiedReceipt is a structurally decoded wire receipt. It must be
// cryptographically verified before it can enter trusted APIs.
type UnverifiedReceipt struct {
	receipt receiptRecord
}

// Verify resolves and verifies the wire receipt signer before returning an
// opaque signed receipt.
func (r UnverifiedReceipt) Verify(resolver KeyResolver) (SignedInvocationReceipt, error) {
	record := cloneReceiptRecord(r.receipt)
	if err := verifyReceiptRecord(record, resolver); err != nil {
		return emptySignedReceipt(), err
	}
	return acceptVerifiedReceipt(record), nil
}

func verifyReceiptRecord(record receiptRecord, resolver KeyResolver) error {
	if err := validateReceiptRecordSemantics(record); err != nil {
		return err
	}
	if err := validateCalleeSignatureStructure(record.CalleeSignature); err != nil {
		return err
	}
	if err := VerifyReceiptSignatureWithHosted(
		receiptBody(&record),
		record.AxiomBinding.Hosted,
		record.CalleeSignature,
		resolver,
	); err != nil {
		return err
	}
	if record.SelfHash != computeReceiptHash(&record) {
		return ErrInvalidArgument("self_hash_mismatch")
	}
	return nil
}

func validateReceiptRecordSemantics(record receiptRecord) error {
	binding := record.AxiomBinding
	if err := validateAuthorityBinding(
		binding.Caller,
		binding.Subject,
		binding.AbilityBinding,
		binding.AuthorityBinding,
	); err != nil {
		return err
	}
	if err := validateAuthorityProof(
		binding.ProofFacts.AuthorityProof,
		binding.AuthorityBinding,
		binding.Callee,
	); err != nil {
		return err
	}
	if binding.ProofFacts.SubjectRef == nil {
		return ErrInvalidArgument("receipt_subject_ref_missing")
	}
	expectedSubject, err := EntityRefForSubject(binding.Subject)
	if err != nil {
		return err
	}
	if *binding.ProofFacts.SubjectRef != expectedSubject {
		return ErrInvalidArgument("receipt_subject_ref_semantically_invalid")
	}
	descriptor, err := ParseAbilityDescriptorRef(binding.AbilityBinding)
	if err != nil {
		return err
	}
	if binding.ProofFacts.DescriptorVersion != descriptor.Version {
		return ErrInvalidArgument("receipt_descriptor_version_semantically_invalid")
	}
	if binding.ProofFacts.SchemaHash == zeroHash32 ||
		binding.ProofFacts.ImplHash == zeroHash32 ||
		binding.ProofFacts.InputHash == zeroHash32 ||
		binding.ProofFacts.OutputHash == zeroHash32 {
		return ErrInvalidArgument("receipt_proof_fact_semantically_zero")
	}
	if strings.TrimSpace(binding.ProofFacts.RuntimeEnv) == "" {
		return ErrInvalidArgument("receipt_runtime_env_missing")
	}
	if !receiptRefsEqual(
		binding.ProofFacts.ParentReceipts,
		enumerableParentReceipts(binding.Causal),
	) {
		return ErrInvalidArgument("receipt_parent_receipts_semantically_invalid")
	}
	return nil
}

func canonicalSerialise(record *receiptRecord) []byte {
	return CanonicalReceiptBytesWithHosted(
		receiptBody(record),
		record.AxiomBinding.Hosted,
	)
}

func computeReceiptHash(record *receiptRecord) [32]byte {
	return sha256.Sum256(canonicalSerialise(record))
}

func receiptBody(record *receiptRecord) ReceiptBody {
	binding := record.AxiomBinding
	return ReceiptBody{
		Index:            record.Index,
		InvocationID:     record.InvocationID,
		ReceiptType:      record.ReceiptType,
		State:            record.State,
		TimestampUnixMs:  record.TimestampUnixMs,
		PrevReceiptHash:  record.PrevReceiptHash,
		PayloadDigest:    binding.PayloadDigest,
		Reason:           record.Reason,
		CleanupComplete:  record.CleanupComplete,
		CallerBinding:    binding.Caller,
		CalleeBinding:    binding.Callee,
		SubjectBinding:   binding.Subject,
		InvocationNonce:  binding.InvocationNonce,
		CausalBinding:    binding.Causal,
		AbilityBinding:   binding.AbilityBinding,
		AuthorityBinding: binding.AuthorityBinding,
		Usage:            record.Usage,
		ProofFacts:       binding.ProofFacts,
	}
}

func cloneReceiptRecord(record receiptRecord) receiptRecord {
	copy := record
	copy.Payload = append([]byte(nil), record.Payload...)
	copy.AxiomBinding = cloneAxiomBinding(record.AxiomBinding)
	copy.CalleeSignature = cloneCalleeSignature(record.CalleeSignature)
	return copy
}

// Index returns the monotonic chain index.
func (r SignedInvocationReceipt) Index() uint64 { return r.receipt.Index }

// InvocationID returns the invocation identity.
func (r SignedInvocationReceipt) InvocationID() string { return r.receipt.InvocationID }

// ReceiptType returns the lifecycle event type.
func (r SignedInvocationReceipt) ReceiptType() string { return r.receipt.ReceiptType }

// State returns the canonical lifecycle state.
func (r SignedInvocationReceipt) State() string { return r.receipt.State }

// TimestampUnixMs returns the receipt timestamp.
func (r SignedInvocationReceipt) TimestampUnixMs() int64 {
	return r.receipt.TimestampUnixMs
}

// PrevReceiptHash returns the previous chain hash.
func (r SignedInvocationReceipt) PrevReceiptHash() [32]byte {
	return r.receipt.PrevReceiptHash
}

// SelfHash returns the canonical receipt hash.
func (r SignedInvocationReceipt) SelfHash() [32]byte { return r.receipt.SelfHash }

// Payload returns a defensive copy of the terminal or event payload.
func (r SignedInvocationReceipt) Payload() []byte {
	return append([]byte(nil), r.receipt.Payload...)
}

// PayloadContentType returns the payload media type.
func (r SignedInvocationReceipt) PayloadContentType() string {
	return r.receipt.PayloadContentType
}

// CleanupComplete reports whether cleanup completed before this receipt.
func (r SignedInvocationReceipt) CleanupComplete() bool {
	return r.receipt.CleanupComplete
}

// Reason returns the lifecycle reason.
func (r SignedInvocationReceipt) Reason() string { return r.receipt.Reason }

// ChildInvocationID returns the optional child invocation identity.
func (r SignedInvocationReceipt) ChildInvocationID() string {
	return r.receipt.ChildInvocationID
}

// AxiomBinding returns a defensive copy of the signed binding.
func (r SignedInvocationReceipt) AxiomBinding() AxiomBinding {
	return cloneAxiomBinding(r.receipt.AxiomBinding)
}

// Usage returns the signed usage counters.
func (r SignedInvocationReceipt) Usage() InvocationUsage { return r.receipt.Usage }

// CalleeSignature returns a defensive copy of the verified signature.
func (r SignedInvocationReceipt) CalleeSignature() CalleeSignature {
	return cloneCalleeSignature(r.receipt.CalleeSignature)
}

// CanonicalBytes returns the exact bytes covered by the callee signature.
func (r SignedInvocationReceipt) CanonicalBytes() []byte {
	return canonicalSerialise(&r.receipt)
}

// ChainCheckResult is the result of a signed receipt chain walk.
type ChainCheckResult struct {
	OK          bool
	BrokenIndex int64
	Detail      string
}

// VerifyReceiptChain verifies index, hash, and previous-hash continuity over
// provider-accepted receipts.
func VerifyReceiptChain(receipts []SignedInvocationReceipt) ChainCheckResult {
	if len(receipts) == 0 {
		return ChainCheckResult{OK: true, BrokenIndex: -1, Detail: "empty"}
	}
	first := receipts[0]
	if first.Index() != 0 {
		return ChainCheckResult{
			OK: false, BrokenIndex: 0,
			Detail: fmt.Sprintf("first receipt has non-zero index %d", first.Index()),
		}
	}
	if first.PrevReceiptHash() != ZeroHash {
		return ChainCheckResult{
			OK: false, BrokenIndex: 0,
			Detail: "first receipt's prev_receipt_hash must be zero",
		}
	}
	prev := computeReceiptHash(&first.receipt)
	if first.SelfHash() != prev {
		return ChainCheckResult{OK: false, BrokenIndex: 0, Detail: "self_hash mismatch at index 0"}
	}
	for position, receipt := range receipts[1:] {
		index := int64(position + 1)
		if receipt.Index() != uint64(index) {
			return ChainCheckResult{
				OK: false, BrokenIndex: index,
				Detail: fmt.Sprintf("non-monotonic index at position %d", index),
			}
		}
		if receipt.PrevReceiptHash() != prev {
			return ChainCheckResult{
				OK: false, BrokenIndex: index,
				Detail: fmt.Sprintf("prev_receipt_hash mismatch at index %d", index),
			}
		}
		current := computeReceiptHash(&receipt.receipt)
		if receipt.SelfHash() != current {
			return ChainCheckResult{
				OK: false, BrokenIndex: index,
				Detail: fmt.Sprintf("self_hash mismatch at index %d", index),
			}
		}
		prev = current
	}
	return ChainCheckResult{OK: true, BrokenIndex: -1}
}
