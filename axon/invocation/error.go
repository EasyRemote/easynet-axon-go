// Package axon implements Axon's invocation runtime model in Go.
//
// Mirrors the Python and Rust SDKs in semantics. See
// `document/concepts/CONCEPT_MODEL.md`, `sdk/SDK_INTERFACE_SPEC.md`,
// `sdk/INDUSTRIAL_TEST_MATRIX.md`.
package axon

import "fmt"

// AxonErrorKind — the seven canonical error classes.
type AxonErrorKind string

const (
	KindCancelled         AxonErrorKind = "cancelled"
	KindDeadlineExceeded  AxonErrorKind = "deadline_exceeded"
	KindUnavailable       AxonErrorKind = "unavailable"
	KindInvalidArgument   AxonErrorKind = "invalid_argument"
	KindResourceExhausted AxonErrorKind = "resource_exhausted"
	KindPermissionDenied  AxonErrorKind = "permission_denied"
	KindInternal          AxonErrorKind = "internal"
)

// AxonError is the canonical Invocation-level error. Uniform across SDKs.
type AxonError struct {
	Kind         AxonErrorKind
	Reason       string
	Message      string
	InvocationID string
	RetryAfterMs int64 // 0 = unset
	CauseChain   []string
}

func (e *AxonError) Error() string {
	s := string(e.Kind)
	if e.Reason != "" {
		s += " reason=" + e.Reason
	}
	if e.InvocationID != "" {
		s += " invocation_id=" + e.InvocationID
	}
	if e.RetryAfterMs != 0 {
		s += fmt.Sprintf(" retry_after_ms=%d", e.RetryAfterMs)
	}
	if e.Message != "" {
		s += fmt.Sprintf(" message=%q", e.Message)
	}
	return s
}

// Retriable reports whether this error kind carries a retry hint.
func (e *AxonError) Retriable() bool {
	return e.Kind == KindUnavailable || e.Kind == KindResourceExhausted
}

// WithInvocationID returns a clone with the invocation id set.
func (e *AxonError) WithInvocationID(id string) *AxonError {
	c := *e
	c.InvocationID = id
	return &c
}

// WithRetryAfterMs returns a clone with retry_after_ms set.
func (e *AxonError) WithRetryAfterMs(ms int64) *AxonError {
	c := *e
	c.RetryAfterMs = ms
	return &c
}

// ErrCancelled and the six siblings are convenience constructors.
func ErrCancelled(reason string) *AxonError {
	return &AxonError{Kind: KindCancelled, Reason: reason}
}
func ErrDeadlineExceeded(reason string) *AxonError {
	return &AxonError{Kind: KindDeadlineExceeded, Reason: reason}
}
func ErrUnavailable(reason string) *AxonError {
	return &AxonError{Kind: KindUnavailable, Reason: reason}
}
func ErrInvalidArgument(reason string) *AxonError {
	return &AxonError{Kind: KindInvalidArgument, Reason: reason}
}
func ErrResourceExhausted(reason string) *AxonError {
	return &AxonError{Kind: KindResourceExhausted, Reason: reason}
}
func ErrPermissionDenied(reason string) *AxonError {
	return &AxonError{Kind: KindPermissionDenied, Reason: reason}
}
func ErrInternal(reason string) *AxonError {
	return &AxonError{Kind: KindInternal, Reason: reason}
}

// MapProtoCode maps a numeric gRPC-aligned error code to the taxonomy.
// Unknown codes map to (KindInternal, "unknown_error_code").
func MapProtoCode(code int32) (AxonErrorKind, string) {
	switch code {
	case 1:
		return KindCancelled, ""
	case 3:
		return KindInvalidArgument, ""
	case 4:
		return KindDeadlineExceeded, ""
	case 7:
		return KindPermissionDenied, ""
	case 8:
		return KindResourceExhausted, ""
	case 13:
		return KindInternal, ""
	case 14:
		return KindUnavailable, ""
	default:
		return KindInternal, "unknown_error_code"
	}
}
