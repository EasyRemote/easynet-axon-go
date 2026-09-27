// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_signed_invoke_stub.go
// Description: No-cgo stub of the authenticated Invocation surface.
//              Mirrors `dendrite_bridge_signed_invoke_cgo.go` so the
//              package compiles on builds that disable cgo, with the
//              same error policy as the rest of the stub bridge:
//              every call returns the canonical
//              "dendrite bridge requires cgo" DendriteError.
//
// Protocol Responsibility:
// - Preserves the SDK API surface (`OpenSignedClient`,
//   `(*DendriteBridge).InvokeAbilitySigned`) under non-cgo builds so
//   downstream packages that depend on the typed signed-invocation
//   path keep compiling. Behaviourally these calls cannot succeed —
//   AXIOM caller-signing requires the cgo-loaded native bridge.
//
// Implementation Approach:
// - One-line implementations returning the package-shared
//   `errDendriteUnsupported` value so the failure mode is uniform
//   across every stubbed entry point.
// - The validation logic in `signed_invoke_request.go` (build-tag
//   neutral) is still reachable in stub builds; callers that want
//   to fast-fail on bad request shapes without ever crossing cgo
//   can call `SignedInvokeRequest{...}.helperPayload()` directly
//   even under the stub, which is occasionally useful in tests.
//
// Architectural Position:
// - No-cgo build path. The cgo counterpart is
//   `dendrite_bridge_signed_invoke_cgo.go`.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build !cgo

package axon

import (
	"errors"
	"fmt"
	"strings"
)

const (
	signingAlgorithmEd25519 = "ed25519"
	uraProfileStrictV2      = "axon-strict-v2"
)

// SigningConfig — see `dendrite_bridge_signed_invoke_cgo.go` for the
// canonical documentation. Fields are kept identical so request-
// constructing code shared across cgo and non-cgo builds compiles
// against the same struct shape.
type SigningConfig struct {
	Seed          []byte
	CallerURA     string
	CallerProfile string
}

// NewSigningConfig validates seed length + caller URA even under the
// stub build, so unit tests that exercise request construction (and
// only construction) can still use `NewSigningConfig` for parity
// with cgo callers.
func NewSigningConfig(seed []byte, callerURA string) (SigningConfig, error) {
	if len(seed) != 32 {
		return SigningConfig{}, fmt.Errorf("signing seed must be exactly 32 bytes, got %d", len(seed))
	}
	if strings.TrimSpace(callerURA) == "" {
		return SigningConfig{}, errors.New("signing caller_ura must be non-empty")
	}
	cp := make([]byte, 32)
	copy(cp, seed)
	return SigningConfig{
		Seed:          cp,
		CallerURA:     strings.TrimSpace(callerURA),
		CallerProfile: uraProfileStrictV2,
	}, nil
}

// OpenSignedClient — stub. Returns errDendriteUnsupported because
// the authenticated path requires the cgo-loaded native bridge.
func (b *DendriteBridge) OpenSignedClient(_ string, _ SigningConfig, _ int) (uint64, error) {
	_ = b
	return 0, errDendriteUnsupported
}

// OpenSignedClientWithOptions requires the native bridge.
func (b *DendriteBridge) OpenSignedClientWithOptions(_ string, _ SigningConfig, _ DendriteClientOptions) (uint64, error) {
	return 0, errDendriteUnsupported
}

// InvokeAbilitySigned — stub. Returns errDendriteUnsupported.
func (b *DendriteBridge) InvokeAbilitySigned(_ uint64, _ SignedInvokeRequest) (map[string]any, error) {
	_ = b
	return nil, errDendriteUnsupported
}

// ServerStreamOpenResult mirrors the cgo type.
type ServerStreamOpenResult struct {
	StreamHandle uint64
	RawPayload   map[string]any
}

// InvokeAbilityStreamSigned — stub. Returns errDendriteUnsupported.
func (b *DendriteBridge) InvokeAbilityStreamSigned(
	_ uint64,
	_ SignedInvokeStreamRequest,
) (uint64, *ServerStreamOpenResult, error) {
	_ = b
	return 0, nil, errDendriteUnsupported
}
