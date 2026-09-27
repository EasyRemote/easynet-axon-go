// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_stub.go
// Description: Source file for Go SDK facade and Dendrite integration; keeps behavior explicit and interoperable across language/runtime boundaries, including tenant/principal invocation context bridging.
//
// Protocol Responsibility:
// - Implements Go SDK facade and Dendrite integration contracts required by current Axon service and SDK surfaces.
// - Preserves stable request/response semantics and error mapping for dendrite_bridge_stub.go call paths.
//
// Implementation Approach:
// - Uses small typed helpers and explicit control flow to avoid hidden side effects.
// - Keeps protocol translation and transport details close to this module boundary.
//
// Usage Contract:
// - Callers should provide valid tenant/resource/runtime context before invoking exported APIs; principal context is optional and maps to canonical subject context.
// - Errors should be treated as typed protocol/runtime outcomes rather than silently ignored.
//
// Architectural Position:
// - Part of the Go SDK facade and Dendrite integration layer.
// - Contains only native bridge loading and transport operations.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build !cgo

package axon

// Shared types (DendriteError, ProtocolInvokeRequest, StreamNextResult)
// are in dendrite_bridge_types.go.

var errDendriteUnsupported = DendriteError{Code: ErrCodeBridge, Message: "dendrite bridge requires cgo; recompile with cgo enabled"}

type DendriteBridge struct{}

func ResolveDendriteLibraryPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	return "", errDendriteUnsupported
}

func OpenDendriteBridge(_ string) (*DendriteBridge, error) {
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) CloseLibrary() error {
	_ = b
	return nil
}

func (b *DendriteBridge) OpenClient(_ string, _ int) (uint64, error) {
	_ = b
	return 0, errDendriteUnsupported
}

// OpenClientWithOptions requires the native bridge.
func (b *DendriteBridge) OpenClientWithOptions(_ string, _ DendriteClientOptions) (uint64, error) {
	return 0, errDendriteUnsupported
}

func (b *DendriteBridge) CloseClient(_ uint64) error {
	_ = b
	return errDendriteUnsupported
}

func (b *DendriteBridge) UnaryCall(_ uint64, _ string, _ []byte, _ map[string]string, _ int) ([]byte, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ServerStreamCall(_ uint64, _ string, _ []byte, _ map[string]string, _ int, _ int) ([][]byte, bool, error) {
	_ = b
	return nil, false, errDendriteUnsupported
}

func (b *DendriteBridge) ClientStreamCall(_ uint64, _ string, _ [][]byte, _ map[string]string, _ int, _ int) ([]byte, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) BidiStreamCall(_ uint64, _ string, _ [][]byte, _ map[string]string, _ int, _ int, _ int) ([][]byte, bool, error) {
	_ = b
	return nil, false, errDendriteUnsupported
}

func (b *DendriteBridge) InvokeAbilityWithSubject(
	_ uint64,
	_ string,
	_ string,
	_ any,
	_ string,
	_ map[string]string,
	_ int,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) InvokeAbilityRawWithSubject(
	_ uint64,
	_ string,
	_ string,
	_ any,
	_ string,
	_ map[string]string,
	_ int,
) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ProtocolCoverage() (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) ProtocolCatalog() (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

func (b *DendriteBridge) InvokeProtocol(_ uint64, _ ProtocolInvokeRequest) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

// Incremental streaming stubs (StreamNextResult is in dendrite_bridge_types.go)

func (b *DendriteBridge) ServerStreamOpen(_ uint64, _ string, _ []byte, _ map[string]string, _ int, _ int, _ int) (uint64, error) {
	_ = b
	return 0, errDendriteUnsupported
}

func (b *DendriteBridge) StreamNext(_ uint64, _ int) (StreamNextResult, error) {
	_ = b
	return StreamNextResult{}, errDendriteUnsupported
}

func (b *DendriteBridge) StreamClose(_ uint64) error {
	_ = b
	return errDendriteUnsupported
}

func (b *DendriteBridge) BidiStreamOpen(_ uint64, _ string, _ []byte, _ [][]byte, _ map[string]string, _ int, _ int, _ int, _ int) (uint64, error) {
	_ = b
	return 0, errDendriteUnsupported
}

func (b *DendriteBridge) BidiStreamSend(_ uint64, _ []byte) (bool, error) {
	_ = b
	return false, errDendriteUnsupported
}

func (b *DendriteBridge) BidiStreamFinishSend(_ uint64) (bool, error) {
	_ = b
	return false, errDendriteUnsupported
}
