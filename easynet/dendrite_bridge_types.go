// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/dendrite_bridge_types.go
// Description: Shared types for DendriteBridge used by both CGO and stub implementations.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.
//
// These types are used by both the cgo and stub implementations.

package easynet

// Canonical DendriteError code constants.
// See sdk/rust/src/error.rs for the full taxonomy.
const (
	ErrCodeValidation     = "VALIDATION"
	ErrCodeBridge         = "BRIDGE"
	ErrCodeInvocation     = "INVOCATION"
	ErrCodePartialSuccess = "PARTIAL_SUCCESS"
)

// DendriteError is the error type returned by DendriteBridge operations.
// Code holds the canonical error code for cross-SDK mapping (e.g. "BRIDGE",
// "INVOCATION", "VALIDATION"). See sdk/rust/src/error.rs for the full taxonomy.
type DendriteError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

func (e DendriteError) Error() string {
	return e.Message
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
