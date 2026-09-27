// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_signed_invoke_bidi_stub.go
// Description: No-cgo stub of the signed InvokeBidi surface.
//              Mirrors `dendrite_bridge_signed_invoke_bidi_cgo.go`
//              so the package compiles when cgo is disabled.
//              All entry points return `errDendriteUnsupported`;
//              AXIOM-signed bidi requires the cgo-loaded native
//              bridge (the bridge owns the per-direction HMAC
//              chain).
//
// Architectural Position:
// - No-cgo build path. Cgo counterpart:
//   `dendrite_bridge_signed_invoke_bidi_cgo.go`.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build !cgo

package axon

// SignedInvokeBidiOpenRequest mirrors the cgo type so non-cgo
// callers (test code, request-construction unit tests) can use
// the same struct shape.
type SignedInvokeBidiOpenRequest struct {
	SignedInvokeRequest
	ArgsContentType   string
	ChunkTimeoutMs    int
	ChunkBufferSize   int
	RequestBufferSize int
	Streams           []StreamDescriptor
}

// StreamDescriptor mirrors the cgo type.
type StreamDescriptor struct {
	StreamID    uint32
	ContentType string
	CodecParams string
	Ordering    string
}

// BidiOpenResult mirrors the cgo type.
type BidiOpenResult struct {
	StreamHandle uint64
	RequestID    string
	RawPayload   map[string]any
}

// BidiFrameKind mirrors the cgo type.
type BidiFrameKind string

const (
	BidiFrameBinary  BidiFrameKind = "binary_chunk"
	BidiFrameReceipt BidiFrameKind = "receipt"
	BidiFrameControl BidiFrameKind = "control"
	BidiFrameDone    BidiFrameKind = "done"
	BidiFrameTimeout BidiFrameKind = "timeout"
)

// BidiFrame mirrors the cgo type.
type BidiFrame struct {
	Kind     BidiFrameKind
	Sequence uint64
	StreamID uint32
	Data     []byte
	PTS      uint64
	Receipt  map[string]any
	Terminal bool
	Control  map[string]any
}

// BidiStream mirrors the cgo type. Every method returns
// `errDendriteUnsupported` because the bridge-side HMAC chain
// machinery requires cgo.
type BidiStream struct {
	streamHandle uint64
}

// StreamHandle returns the embedded handle. Useful for tests
// that construct a `BidiStream{streamHandle: N}` and assert on
// the value without touching the FFI.
func (s *BidiStream) StreamHandle() uint64 { return s.streamHandle }

// Send — stub. Returns errDendriteUnsupported.
func (s *BidiStream) Send(_ uint32, _ []byte, _ uint64) error {
	return errDendriteUnsupported
}

// SendControl — stub.
func (s *BidiStream) SendControl(_ map[string]any) error {
	return errDendriteUnsupported
}

// SendPtyResize — stub.
func (s *BidiStream) SendPtyResize(_, _ uint32) error {
	return errDendriteUnsupported
}

// SendPtySignal — stub.
func (s *BidiStream) SendPtySignal(_ int32) error {
	return errDendriteUnsupported
}

// SendMediaTimestamp — stub.
func (s *BidiStream) SendMediaTimestamp(_ uint32, _ uint64) error {
	return errDendriteUnsupported
}

// SendEOF — stub.
func (s *BidiStream) SendEOF() error {
	return errDendriteUnsupported
}

// Recv — stub.
func (s *BidiStream) Recv(_ int) (BidiFrame, error) {
	return BidiFrame{}, errDendriteUnsupported
}

// Close — stub.
func (s *BidiStream) Close() error {
	return errDendriteUnsupported
}

// InvokeAbilityBidiSigned — stub. Returns errDendriteUnsupported.
func (b *DendriteBridge) InvokeAbilityBidiSigned(
	_ uint64,
	_ SignedInvokeBidiOpenRequest,
) (*BidiStream, *BidiOpenResult, error) {
	_ = b
	return nil, nil, errDendriteUnsupported
}

// Dispose is unsupported without CGO.
func (s *BidiStream) Dispose() error { return errDendriteUnsupported }
