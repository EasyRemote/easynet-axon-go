// Axon Go native transport request adapters.
//
// File: sdk/go/axon/dendrite_bridge_signed_invoke_cgo.go
// Responsibility: encode the existing Go session-signing request API.
// Current native contract: sessions carry transport configuration only. Native
// rejects the signing field; it neither stores caller keys nor signs invocations.
// The methods below therefore do not establish a supported signed native path.
// Caller-signed canonical requests are owned by the invocation package; a complete
// native projection from that package remains to be implemented.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

//go:build cgo

package axon

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// signingAlgorithmEd25519 is the only accepted signing algorithm at
// the bridge boundary. Mirrors `signing::SIGNING_ALGORITHM_ED25519`
// in the Rust bridge. Hard-coded here so callers cannot accidentally
// supply an algorithm string the bridge will reject after a costly
// round-trip.
const signingAlgorithmEd25519 = "ed25519"

// uraProfileStrictV2 is the only accepted URA profile per
// RFC 001 §3.1. Pinned to match
// `signing::URA_PROFILE_STRICT_V2` on the Rust side.
const uraProfileStrictV2 = "axon-strict-v2"

// SigningConfig encodes the unsupported session-signing request shape.
// Current native artifacts reject this configuration; no native key-custody or
// authenticated-session guarantee follows from constructing it.
type SigningConfig struct {
	// Seed is the 32-byte material encoded in the rejected signing request.
	Seed []byte

	// CallerURA is the requested caller identity; no session is established.
	CallerURA string

	// CallerProfile is the URA encoding profile selector. RFC 001
	// §3.1 pins it to "axon-strict-v2"; supplying any other value
	// is rejected.
	CallerProfile string
}

// NewSigningConfig validates the Go request shape. Validation does not imply
// that the current native bridge supports session signing.
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

// validate runs the language-native preflight that mirrors the
// bridge's `SigningConfig::new` checks. Bridge-side rejection codes
// for the same conditions are surfaced through the open call's
// `error.code` field as BRIDGE_BAD_REQUEST.
func (s SigningConfig) validate() error {
	if len(s.Seed) != 32 {
		return fmt.Errorf("signing seed must be exactly 32 bytes, got %d", len(s.Seed))
	}
	if strings.TrimSpace(s.CallerURA) == "" {
		return errors.New("signing caller_ura must be non-empty")
	}
	if strings.TrimSpace(s.CallerProfile) != uraProfileStrictV2 {
		return fmt.Errorf(
			"signing caller_profile must be %q, got %q",
			uraProfileStrictV2, s.CallerProfile,
		)
	}
	return nil
}

// signingBlock serializes the existing configuration. The current native open
// request rejects the resulting signing field.
func (s SigningConfig) signingBlock() map[string]any {
	return map[string]any{
		"algorithm":      signingAlgorithmEd25519,
		"seed_base64":    base64.StdEncoding.EncodeToString(s.Seed),
		"caller_ura":     strings.TrimSpace(s.CallerURA),
		"caller_profile": strings.TrimSpace(s.CallerProfile),
	}
}

// OpenSignedClient sends a session-signing request.
// Current native artifacts reject it as an unknown signing field. This method
// does not establish an authenticated session with the current bridge.
func (b *DendriteBridge) OpenSignedClient(
	endpoint string,
	signing SigningConfig,
	connectTimeoutMs int,
) (uint64, error) {
	return b.OpenSignedClientWithOptions(endpoint, signing, DendriteClientOptions{ConnectTimeoutMs: connectTimeoutMs})
}

// OpenSignedClientWithOptions retains the existing signing request and adds
// explicit transport trust. Native contract errors are returned unchanged.
func (b *DendriteBridge) OpenSignedClientWithOptions(endpoint string, signing SigningConfig, options DendriteClientOptions) (uint64, error) {
	if err := signing.validate(); err != nil {
		return 0, DendriteError{Code: ErrCodeBridge, Message: err.Error()}
	}
	if strings.TrimSpace(endpoint) == "" {
		return 0, DendriteError{Code: ErrCodeBridge, Message: "endpoint must be non-empty"}
	}
	payload := options.openPayload(endpoint)
	payload["signing"] = signing.signingBlock()
	return b.openClientPayload(payload)
}

// InvokeAbilitySigned submits the existing incomplete request projection.
// It lacks a complete externally signed envelope and is not a supported signed
// invocation path for the current native bridge. The bridge does not fill in
// caller authority, sign, or generate a nonce on its behalf.
func (b *DendriteBridge) InvokeAbilitySigned(
	handle uint64,
	req SignedInvokeRequest,
) (map[string]any, error) {
	if b.sym.descriptorBoundInvoke == nil {
		return nil, DendriteError{
			Code: ErrCodeSymbolNotFound,
			Message: "dendrite bridge symbol not available: " +
				"axon_dendrite_descriptor_bound_invoke_json " +
				"(rebuild against a canonical-runtime native lib)",
		}
	}
	payload, err := req.helperPayload()
	if err != nil {
		return nil, DendriteError{Code: ErrCodeBridge, Message: err.Error()}
	}
	resp, err := b.callDescriptorBoundInvoke(handle, payload)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(resp.Payload))
	for k, v := range resp.Payload {
		if k == "ok" {
			continue
		}
		out[k] = v
	}
	return out, nil
}
