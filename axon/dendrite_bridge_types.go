// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_types.go
// Description: Shared types for DendriteBridge used by both CGO and stub implementations.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package axon

// Canonical DendriteError code constants.
//
// These match the taxonomy defined in sdk/rust/src/error.rs::AxonError::code().
// Keep the two lists in sync — the wire protocol encodes codes as strings, so
// a missing constant here is a silent cross-SDK divergence, not a compile
// error. Product-specific provider errors remain downstream of this bridge.
const (
	// Input validation or local configuration error — raised before any
	// network call when arguments fail local checks.
	ErrCodeValidation = "VALIDATION"

	// Native library symbol could not be loaded (dlsym / GetProcAddress).
	ErrCodeSymbolNotFound = "SYMBOL_NOT_FOUND"

	// Bridge initialization or connection to the Axon runtime failed.
	ErrCodeBridge = "BRIDGE"

	// Remote invocation failed.
	ErrCodeInvocation = "INVOCATION"

	// Streaming transport was interrupted before completion.
	ErrCodeStream = "STREAM"

	// A governance policy rejected the operation (distinct from VALIDATION:
	// VALIDATION is local, POLICY_DENIED is enforced by the control plane).
	ErrCodePolicyDenied = "POLICY_DENIED"

	// Operation completed with partial failures.
	ErrCodePartialSuccess = "PARTIAL_SUCCESS"

	// JSON serialization or deserialization failed.
	ErrCodeJSON = "JSON"

	// File or IO operation failed.
	ErrCodeIO = "IO"
)

// Sentinel errors for direct use with errors.Is.
//
// Each sentinel carries the canonical code and an empty message; callers can
// wrap them with fmt.Errorf("%w: %s", ErrXxx, detail) or construct a
// DendriteError{Code: ErrCodeXxx, Message: detail} directly.
var (
	ErrValidation     = DendriteError{Code: ErrCodeValidation}
	ErrSymbolNotFound = DendriteError{Code: ErrCodeSymbolNotFound}
	ErrBridge         = DendriteError{Code: ErrCodeBridge}
	ErrInvocation     = DendriteError{Code: ErrCodeInvocation}
	ErrStream         = DendriteError{Code: ErrCodeStream}
	ErrPolicyDenied   = DendriteError{Code: ErrCodePolicyDenied}
	ErrPartialSuccess = DendriteError{Code: ErrCodePartialSuccess}
	ErrJSON           = DendriteError{Code: ErrCodeJSON}
	ErrIO             = DendriteError{Code: ErrCodeIO}
)

// DendriteError is the error type returned by DendriteBridge operations.
//
// Code holds the canonical error code for cross-SDK mapping (see the
// ErrCode* constants above). Message carries the human-readable detail.
// An empty Code means the error predates taxonomy mapping and should be
// treated as ErrCodeBridge by defensive callers.
type DendriteError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	Source  string `json:"source,omitempty"`
	// InvocationResponseJSON is unverified native response evidence. The raw
	// JSON preserves integer precision and keeps DendriteError comparable.
	// Empty means no evidence. Verify receipt signatures before trusting it.
	InvocationResponseJSON string `json:"invocation_response_json,omitempty"`
}

func (e DendriteError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

// Is supports errors.Is matching by Code. Two DendriteError values are
// considered "the same error" when their codes match; Message is treated as
// detail, not identity. This lets callers write:
//
//	if errors.Is(err, axon.ErrBridge) { ... }
//
// against errors produced anywhere in the SDK without string-matching.
func (e DendriteError) Is(target error) bool {
	t, ok := target.(DendriteError)
	if !ok {
		return false
	}
	return t.Code != "" && t.Code == e.Code
}

// ProtocolInvokeRequest describes a raw protocol invocation.
type ProtocolInvokeRequest struct {
	Service             string
	RPC                 string
	Path                string
	RequestBase64       string
	RequestChunksBase64 []string
	Metadata            map[string]string
	TimeoutMs           int
	MaxChunks           int
	MaxRequestChunks    int
	MaxResponseChunks   int
}

// StreamNextResult holds the result of a StreamNext call.
type StreamNextResult struct {
	Chunk   []byte
	Done    bool
	Timeout bool
}
