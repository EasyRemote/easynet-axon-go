// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_signed_invoke_bidi_stub_test.go
// Description: Tests for the no-cgo stub of the signed InvokeBidi
//              surface. The stub exists so the package compiles
//              on builders without cgo (or with `CGO_ENABLED=0`);
//              every entry point MUST surface
//              `errDendriteUnsupported` rather than silently
//              succeeding or panicking.
//
//              The stub is the kind of code most easily forgotten
//              about — it has no usage in production yet looks
//              live to a reader because the type names match. A
//              quiet, comprehensive test pass keeps it from
//              drifting into a footgun (e.g. a Send that returns
//              nil silently).
//
// Architectural Position:
// - No-cgo build path. Cgo counterpart tests live in
//   `dendrite_bridge_signed_invoke_bidi_cgo_test.go`.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build !cgo

package axon

import (
	"errors"
	"testing"
)

// expectUnsupported asserts the returned error is the canonical
// stub error. We compare via errors.Is so a future wrap with
// %w stays compatible.
func expectUnsupported(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: expected errDendriteUnsupported, got nil", label)
		return
	}
	if !errors.Is(err, errDendriteUnsupported) {
		t.Errorf("%s: expected errDendriteUnsupported, got %v", label, err)
	}
}

func TestStubBidiStreamSendReturnsUnsupported(t *testing.T) {
	s := &BidiStream{streamHandle: 1}
	expectUnsupported(t, "Send", s.Send(0, []byte("data"), 0))
}

func TestStubBidiStreamSendControlReturnsUnsupported(t *testing.T) {
	s := &BidiStream{streamHandle: 1}
	expectUnsupported(t, "SendControl", s.SendControl(map[string]any{"kind": "eof"}))
}

func TestStubBidiStreamSendPtyResizeReturnsUnsupported(t *testing.T) {
	s := &BidiStream{streamHandle: 1}
	expectUnsupported(t, "SendPtyResize", s.SendPtyResize(80, 24))
}

func TestStubBidiStreamSendPtySignalReturnsUnsupported(t *testing.T) {
	s := &BidiStream{streamHandle: 1}
	expectUnsupported(t, "SendPtySignal", s.SendPtySignal(2))
}

func TestStubBidiStreamSendMediaTimestampReturnsUnsupported(t *testing.T) {
	s := &BidiStream{streamHandle: 1}
	expectUnsupported(t, "SendMediaTimestamp", s.SendMediaTimestamp(1, 12345))
}

func TestStubBidiStreamSendEOFReturnsUnsupported(t *testing.T) {
	s := &BidiStream{streamHandle: 1}
	expectUnsupported(t, "SendEOF", s.SendEOF())
}

func TestStubBidiStreamRecvReturnsUnsupported(t *testing.T) {
	s := &BidiStream{streamHandle: 1}
	frame, err := s.Recv(1000)
	expectUnsupported(t, "Recv", err)
	// Frame MUST be zero-value so the caller cannot misread a
	// stale heap value as a real frame.
	if frame.Kind != "" || frame.Sequence != 0 || len(frame.Data) != 0 {
		t.Errorf("stub Recv must return zero-value frame, got %#v", frame)
	}
}

func TestStubBidiStreamCloseReturnsUnsupported(t *testing.T) {
	s := &BidiStream{streamHandle: 1}
	expectUnsupported(t, "Close", s.Close())
}

func TestStubInvokeAbilityBidiSignedReturnsUnsupported(t *testing.T) {
	b := &DendriteBridge{}
	stream, result, err := b.InvokeAbilityBidiSigned(0, SignedInvokeBidiOpenRequest{})
	expectUnsupported(t, "InvokeAbilityBidiSigned", err)
	if stream != nil {
		t.Errorf("stub must return nil stream, got %#v", stream)
	}
	if result != nil {
		t.Errorf("stub must return nil result, got %#v", result)
	}
}

func TestStubBidiStreamHandleAccessor(t *testing.T) {
	s := &BidiStream{streamHandle: 99}
	if got := s.StreamHandle(); got != 99 {
		t.Errorf("StreamHandle = %d; want 99", got)
	}
}

// Type-construction smoke test — the stub structs MUST be usable
// as plain Go values. If a future change adds an unexported field
// of a non-zero-value type this test fails as a heads-up that the
// stub-build users (typically CI / cross-compile contexts) need
// updated zero-value expectations.
func TestStubTypesAreUsableAsZeroValues(t *testing.T) {
	var (
		_ SignedInvokeBidiOpenRequest = SignedInvokeBidiOpenRequest{}
		_ StreamDescriptor            = StreamDescriptor{}
		_ BidiOpenResult              = BidiOpenResult{}
		_ BidiFrame                   = BidiFrame{}
	)
	// Constants exist as compile-time strings; reading them
	// guards against accidental rename.
	if BidiFrameBinary != "binary_chunk" {
		t.Errorf("BidiFrameBinary drifted: %q", BidiFrameBinary)
	}
	if BidiFrameReceipt != "receipt" {
		t.Errorf("BidiFrameReceipt drifted: %q", BidiFrameReceipt)
	}
	if BidiFrameControl != "control" {
		t.Errorf("BidiFrameControl drifted: %q", BidiFrameControl)
	}
	if BidiFrameDone != "done" {
		t.Errorf("BidiFrameDone drifted: %q", BidiFrameDone)
	}
	if BidiFrameTimeout != "timeout" {
		t.Errorf("BidiFrameTimeout drifted: %q", BidiFrameTimeout)
	}
}

func TestStubBidiDisposeUnsupported(t *testing.T) {
	expectUnsupported(t, "Dispose", (&BidiStream{}).Dispose())
}
