// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/dendrite_bridge_types_test.go
// Description: Guards the cross-SDK error taxonomy surface.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package axon

import (
	"errors"
	"testing"
)

// canonicalCodes is the generic bridge taxonomy exposed by the canonical Go
// SDK. Provider-specific state and protocol errors remain downstream.
var canonicalCodes = []string{
	"VALIDATION",
	"SYMBOL_NOT_FOUND",
	"BRIDGE",
	"INVOCATION",
	"STREAM",
	"POLICY_DENIED",
	"PARTIAL_SUCCESS",
	"JSON",
	"IO",
}

func TestErrorCodeConstantsCoverCanonicalTaxonomy(t *testing.T) {
	exposed := map[string]string{
		ErrCodeValidation:     "ErrCodeValidation",
		ErrCodeSymbolNotFound: "ErrCodeSymbolNotFound",
		ErrCodeBridge:         "ErrCodeBridge",
		ErrCodeInvocation:     "ErrCodeInvocation",
		ErrCodeStream:         "ErrCodeStream",
		ErrCodePolicyDenied:   "ErrCodePolicyDenied",
		ErrCodePartialSuccess: "ErrCodePartialSuccess",
		ErrCodeJSON:           "ErrCodeJSON",
		ErrCodeIO:             "ErrCodeIO",
	}
	if len(exposed) != len(canonicalCodes) {
		t.Fatalf("exposed codes count=%d, canonical=%d", len(exposed), len(canonicalCodes))
	}
	for _, code := range canonicalCodes {
		if _, ok := exposed[code]; !ok {
			t.Errorf("canonical code %q has no Go constant", code)
		}
	}
}

func TestSentinelErrorsIsByCode(t *testing.T) {
	// Every sentinel must be matchable by errors.Is against a freshly
	// constructed DendriteError carrying the same code.
	cases := []struct {
		sentinel DendriteError
		built    DendriteError
	}{
		{ErrValidation, DendriteError{Code: ErrCodeValidation, Message: "bad input"}},
		{ErrStream, DendriteError{Code: ErrCodeStream, Message: "interrupted"}},
		{ErrPolicyDenied, DendriteError{Code: ErrCodePolicyDenied, Message: "forbidden"}},
		{ErrJSON, DendriteError{Code: ErrCodeJSON, Message: "decode"}},
		{ErrIO, DendriteError{Code: ErrCodeIO, Message: "enospc"}},
		{ErrSymbolNotFound, DendriteError{Code: ErrCodeSymbolNotFound, Message: "axon_open"}},
		{ErrBridge, DendriteError{Code: ErrCodeBridge, Message: "disconnected"}},
		{ErrInvocation, DendriteError{Code: ErrCodeInvocation, Message: "remote failed"}},
		{ErrPartialSuccess, DendriteError{Code: ErrCodePartialSuccess, Message: "2/5 failed"}},
	}
	for _, c := range cases {
		var err error = c.built
		if !errors.Is(err, c.sentinel) {
			t.Errorf("errors.Is returned false for code %q", c.sentinel.Code)
		}
	}
}

func TestSentinelErrorsDoNotCollapseAcrossCodes(t *testing.T) {
	if errors.Is(DendriteError{Code: ErrCodeBridge}, ErrInvocation) {
		t.Error("BRIDGE error incorrectly matched INVOCATION sentinel")
	}
	if errors.Is(DendriteError{Code: ErrCodeInvocation}, ErrBridge) {
		t.Error("INVOCATION error incorrectly matched BRIDGE sentinel")
	}
}

func TestDendriteErrorEmptyMessageFallsBackToCode(t *testing.T) {
	// Error() should still be human-readable when only the code is set
	// (sentinels are constructed that way).
	if got := ErrBridge.Error(); got != "BRIDGE" {
		t.Errorf("ErrBridge.Error() = %q, want %q", got, "BRIDGE")
	}
}
