// Axon canonical Go SDK
// =========================
//
// File: sdk/go/axon/signed_invoke_request_test.go
// Description: Unit tests for the typed `SignedInvokeRequest` Go
//              surface. Pins the wire-shape contract against the
//              Rust-side `SignedInvokeRequest` deserialiser without
//              requiring cgo. Covers: per-form causal_context
//              encoding, exactly-one payload validation, nonce
//              constraints, identity preflight rules, optional-
//              field omission, defensive metadata copy.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package axon

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// helperPayloadJSON is a small re-marshalling helper used by the
// wire-shape tests. We re-serialise the helperPayload result through
// the standard library so the assertions can read fields by JSON
// path. We do NOT compare against a pre-baked JSON string because
// Go map ordering is non-deterministic; key-presence + per-key value
// assertions are the stable form.
func helperPayloadJSON(t *testing.T, req SignedInvokeRequest) map[string]any {
	t.Helper()
	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload returned error: %v", err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	return decoded
}

func minimalValidRequest() SignedInvokeRequest {
	return SignedInvokeRequest{
		Callee: SignedAgentIdentity{
			URA:     "easynet:///r/acme/authority",
			Profile: "axon-strict-v2",
		},
		Subject: SignedAgentIdentity{
			URA:     "easynet:///r/acme/device/dev-1",
			Profile: "axon-strict-v2",
		},
		Ability:     "easynet:///r/acme/ability/authority.runtime.forward@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke",
		PayloadJSON: map[string]any{"filter": map[string]any{}},
	}
}

func TestSignedInvokeRequest_MinimalValid(t *testing.T) {
	req := minimalValidRequest()
	got := helperPayloadJSON(t, req)

	if got["ability"] != "easynet:///r/acme/ability/authority.runtime.forward@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke" {
		t.Errorf("ability = %v, want easynet:///r/acme/ability/authority.runtime.forward@1.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke", got["ability"])
	}
	callee, ok := got["callee"].(map[string]any)
	if !ok {
		t.Fatalf("callee not a map: %T", got["callee"])
	}
	if callee["ura"] != "easynet:///r/acme/authority" {
		t.Errorf("callee.ura = %v", callee["ura"])
	}
	if callee["profile"] != "axon-strict-v2" {
		t.Errorf("callee.profile = %v", callee["profile"])
	}
	subject, ok := got["subject"].(map[string]any)
	if !ok {
		t.Fatalf("subject missing or wrong type: %T", got["subject"])
	}
	if subject["ura"] != "easynet:///r/acme/device/dev-1" {
		t.Errorf("subject.ura = %v", subject["ura"])
	}
	if subject["profile"] != "axon-strict-v2" {
		t.Errorf("subject.profile = %v", subject["profile"])
	}
	if _, hasSubjectRef := got["subject_ref"]; hasSubjectRef {
		t.Errorf("subject_ref should be absent from signed bridge wire")
	}
	if _, hasDescriptorVersion := got["descriptor_version"]; hasDescriptorVersion {
		t.Errorf("descriptor_version should be absent from signed bridge wire")
	}
	if _, hasNonce := got["nonce_base64"]; hasNonce {
		t.Errorf("nonce_base64 should be omitted when empty (bridge generates fresh)")
	}
	if _, hasCausal := got["causal_context"]; hasCausal {
		t.Errorf("causal_context should be omitted when None")
	}
	if _, hasTimeout := got["timeout_ms"]; hasTimeout {
		t.Errorf("timeout_ms should be omitted when 0")
	}
	if _, hasMetadata := got["metadata"]; hasMetadata {
		t.Errorf("metadata should be omitted when empty")
	}
}

func TestSignedInvokeRequest_AbilityDescriptorRefTrimmed(t *testing.T) {
	req := minimalValidRequest()
	req.Ability = " easynet:///r/acme/ability/authority.runtime.forward@2.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke "

	got := helperPayloadJSON(t, req)
	if got["ability"] != "easynet:///r/acme/ability/authority.runtime.forward@2.0.0#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!invoke" {
		t.Errorf("ability = %v", got["ability"])
	}
}

func TestSignedInvokeRequest_RejectsUnversionedAbility(t *testing.T) {
	req := minimalValidRequest()
	req.Ability = "easynet:///r/acme/ability/authority.runtime.forward"
	if _, err := req.helperPayload(); err == nil || !strings.Contains(err.Error(), "AbilityDescriptorRef") {
		t.Fatalf("missing ability descriptor ref error = %v", err)
	}
}

func TestSignedInvokeRequest_RejectsMissingSubject(t *testing.T) {
	req := minimalValidRequest()
	req.Subject = SignedAgentIdentity{}
	if _, err := req.helperPayload(); err == nil || !strings.Contains(err.Error(), "subject.ura") {
		t.Fatalf("missing subject error = %v", err)
	}
}

func TestSignedInvokeRequest_RejectsBothPayloadForms(t *testing.T) {
	req := minimalValidRequest()
	req.PayloadBase64 = base64.StdEncoding.EncodeToString([]byte("{}"))

	_, err := req.helperPayload()
	if err == nil {
		t.Fatalf("expected error when both PayloadJSON and PayloadBase64 are set")
	}
	if !strings.Contains(err.Error(), "exactly one of") {
		t.Errorf("error message should name the rule, got: %v", err)
	}
}

func TestSignedInvokeRequest_RejectsNeitherPayloadForm(t *testing.T) {
	req := minimalValidRequest()
	req.PayloadJSON = nil

	_, err := req.helperPayload()
	if err == nil {
		t.Fatalf("expected error when neither PayloadJSON nor PayloadBase64 is set")
	}
}

func TestSignedInvokeRequest_PayloadBase64Path(t *testing.T) {
	req := minimalValidRequest()
	req.PayloadJSON = nil
	req.PayloadBase64 = base64.StdEncoding.EncodeToString([]byte(`{"k":"v"}`))

	got := helperPayloadJSON(t, req)
	if _, hasJSON := got["payload_json"]; hasJSON {
		t.Errorf("payload_json should be absent when PayloadBase64 is used")
	}
	if got["payload_base64"] != req.PayloadBase64 {
		t.Errorf("payload_base64 round-trip mismatch")
	}
}

func TestSignedInvokeRequest_EmitsExplicitSubject(t *testing.T) {
	req := minimalValidRequest()
	req.Subject = StrictSignedIdentity("easynet:///r/acme/agent/silan.echo")

	got := helperPayloadJSON(t, req)
	subject, ok := got["subject"].(map[string]any)
	if !ok {
		t.Fatalf("subject missing or wrong type: %T", got["subject"])
	}
	if subject["ura"] != "easynet:///r/acme/agent/silan.echo" {
		t.Errorf("subject.ura mismatch: %v", subject["ura"])
	}
	if subject["profile"] != "axon-strict-v2" {
		t.Errorf("subject.profile mismatch: %v", subject["profile"])
	}
}

func TestSignedInvokeRequest_NonceBase64_Valid(t *testing.T) {
	req := minimalValidRequest()
	// Sixteen non-zero bytes: simplest legal nonce.
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = byte(i + 1)
	}
	req.NonceBase64 = base64.StdEncoding.EncodeToString(nonce)

	got := helperPayloadJSON(t, req)
	if got["nonce_base64"] != req.NonceBase64 {
		t.Errorf("nonce_base64 round-trip mismatch")
	}
}

func TestSignedInvokeRequest_NonceBase64_WrongLength(t *testing.T) {
	req := minimalValidRequest()
	req.NonceBase64 = base64.StdEncoding.EncodeToString([]byte{1, 2, 3})

	_, err := req.helperPayload()
	if err == nil || !strings.Contains(err.Error(), "16 bytes") {
		t.Fatalf("expected length error, got: %v", err)
	}
}

func TestSignedInvokeRequest_NonceBase64_AllZero(t *testing.T) {
	req := minimalValidRequest()
	req.NonceBase64 = base64.StdEncoding.EncodeToString(make([]byte, 16))

	_, err := req.helperPayload()
	if err == nil || !strings.Contains(err.Error(), "all-zero") {
		t.Fatalf("expected all-zero rejection, got: %v", err)
	}
}

func TestSignedInvokeRequest_NonceBase64_NotBase64(t *testing.T) {
	req := minimalValidRequest()
	req.NonceBase64 = "!!!not-base64!!!"

	_, err := req.helperPayload()
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("expected base64 error, got: %v", err)
	}
}

func TestSignedInvokeRequest_CausalScalar(t *testing.T) {
	req := minimalValidRequest()
	hash := strings.Repeat("ab", 32) // 64 lowercase hex chars = 32 bytes
	req.CausalContext = CausalScalar(SignedCausalRef{
		ReceiptHashHex: hash,
		ReceiptURA:     "easynet:///r/acme/receipt/01RCP-1",
	})

	got := helperPayloadJSON(t, req)
	cc, ok := got["causal_context"].(map[string]any)
	if !ok {
		t.Fatalf("causal_context missing: %T", got["causal_context"])
	}
	if cc["form"] != "scalar" {
		t.Errorf("form = %v, want scalar", cc["form"])
	}
	if cc["receipt_hash_hex"] != hash {
		t.Errorf("receipt_hash_hex round-trip mismatch")
	}
	if cc["receipt_ura"] != "easynet:///r/acme/receipt/01RCP-1" {
		t.Errorf("receipt_ura round-trip mismatch")
	}
}

func TestSignedInvokeRequest_CausalScalar_RejectsBadHex(t *testing.T) {
	req := minimalValidRequest()
	req.CausalContext = CausalScalar(SignedCausalRef{
		ReceiptHashHex: "ZZZZ", // not hex
		ReceiptURA:     "easynet:///r/acme/receipt/01RCP-1",
	})

	_, err := req.helperPayload()
	if err == nil {
		t.Fatalf("expected error on non-hex receipt hash")
	}
}

func TestSignedInvokeRequest_CausalScalar_RejectsWrongHexLength(t *testing.T) {
	req := minimalValidRequest()
	req.CausalContext = CausalScalar(SignedCausalRef{
		ReceiptHashHex: strings.Repeat("ab", 16), // 32 chars = 16 bytes; want 32
		ReceiptURA:     "easynet:///r/acme/receipt/01RCP-1",
	})

	_, err := req.helperPayload()
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("expected 32-byte rejection, got: %v", err)
	}
}

func TestSignedInvokeRequest_CausalScalar_RejectsUppercaseHex(t *testing.T) {
	req := minimalValidRequest()
	req.CausalContext = CausalScalar(SignedCausalRef{
		ReceiptHashHex: strings.Repeat("AB", 32),
		ReceiptURA:     "easynet:///r/acme/receipt/01RCP-1",
	})

	_, err := req.helperPayload()
	if err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Fatalf("expected lowercase rejection, got: %v", err)
	}
}

func TestSignedInvokeRequest_CausalList(t *testing.T) {
	req := minimalValidRequest()
	hashA := strings.Repeat("aa", 32)
	hashB := strings.Repeat("bb", 32)
	req.CausalContext = CausalList([]SignedCausalRef{
		{ReceiptHashHex: hashA, ReceiptURA: "easynet:///r/acme/receipt/01RCP-A"},
		{ReceiptHashHex: hashB, ReceiptURA: "easynet:///r/acme/receipt/01RCP-B"},
	})

	got := helperPayloadJSON(t, req)
	cc, ok := got["causal_context"].(map[string]any)
	if !ok {
		t.Fatalf("causal_context missing: %T", got["causal_context"])
	}
	if cc["form"] != "list" {
		t.Errorf("form = %v, want list", cc["form"])
	}
	prior, ok := cc["prior"].([]any)
	if !ok || len(prior) != 2 {
		t.Fatalf("prior wrong shape: %T length %d", cc["prior"], len(prior))
	}
	first := prior[0].(map[string]any)
	if first["receipt_hash_hex"] != hashA {
		t.Errorf("prior[0].receipt_hash_hex mismatch")
	}
}

func TestSignedInvokeRequest_CausalList_RejectsEmpty(t *testing.T) {
	req := minimalValidRequest()
	req.CausalContext = CausalList([]SignedCausalRef{})

	_, err := req.helperPayload()
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("expected non-empty list rejection, got: %v", err)
	}
}

func TestSignedInvokeRequest_CausalMerkle(t *testing.T) {
	req := minimalValidRequest()
	root := strings.Repeat("cd", 32)
	req.CausalContext = CausalMerkle(root, "easynet:///r/acme/proof/01PRF-1")

	got := helperPayloadJSON(t, req)
	cc, ok := got["causal_context"].(map[string]any)
	if !ok {
		t.Fatalf("causal_context missing: %T", got["causal_context"])
	}
	if cc["form"] != "merkle" {
		t.Errorf("form = %v, want merkle", cc["form"])
	}
	if cc["root_hex"] != root {
		t.Errorf("root_hex round-trip mismatch")
	}
	if cc["proof_ura"] != "easynet:///r/acme/proof/01PRF-1" {
		t.Errorf("proof_ura round-trip mismatch")
	}
}

func TestSignedInvokeRequest_CausalNone_OmittedFromWire(t *testing.T) {
	req := minimalValidRequest()
	req.CausalContext = CausalNone()
	got := helperPayloadJSON(t, req)
	if _, has := got["causal_context"]; has {
		t.Errorf("CausalNone should be omitted; bridge treats absent and form=none identically")
	}
}

func TestSignedInvokeRequest_TimeoutEmittedOnlyWhenPositive(t *testing.T) {
	req := minimalValidRequest()
	req.TimeoutMs = 5000
	got := helperPayloadJSON(t, req)
	tm, ok := got["timeout_ms"].(float64) // JSON numbers decode as float64
	if !ok || int(tm) != 5000 {
		t.Errorf("timeout_ms = %v, want 5000", got["timeout_ms"])
	}

	req.TimeoutMs = 0
	got = helperPayloadJSON(t, req)
	if _, has := got["timeout_ms"]; has {
		t.Errorf("timeout_ms should be omitted when 0")
	}

	req.TimeoutMs = -1
	got = helperPayloadJSON(t, req)
	if _, has := got["timeout_ms"]; has {
		t.Errorf("timeout_ms should be omitted when negative")
	}
}

func TestSignedInvokeRequest_MetadataDefensiveCopy(t *testing.T) {
	req := minimalValidRequest()
	req.Metadata = map[string]string{
		"x-easynet-delegation": "fake-proof",
		"x-trace-id":           "trace-123",
	}

	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("helperPayload error: %v", err)
	}
	md, ok := payload["metadata"].(map[string]string)
	if !ok {
		t.Fatalf("metadata wrong type: %T", payload["metadata"])
	}
	// Mutating the original after the call MUST NOT affect what
	// landed in the payload. This is the defensive-copy contract.
	req.Metadata["x-trace-id"] = "trace-MUTATED"
	if md["x-trace-id"] != "trace-123" {
		t.Errorf("metadata copy was not defensive: payload changed when source mutated")
	}
}

func TestSignedInvokeRequest_RejectsEmptyAbility(t *testing.T) {
	req := minimalValidRequest()
	req.Ability = "   "

	_, err := req.helperPayload()
	if err == nil {
		t.Fatalf("expected error for blank ability")
	}
}

func TestSignedInvokeRequest_RejectsEmptyCalleeURA(t *testing.T) {
	req := minimalValidRequest()
	req.Callee.URA = ""

	_, err := req.helperPayload()
	if err == nil {
		t.Fatalf("expected error for blank callee.ura")
	}
}

func TestSignedInvokeRequest_RejectsEmptyCalleeProfile(t *testing.T) {
	req := minimalValidRequest()
	req.Callee.Profile = ""

	_, err := req.helperPayload()
	if err == nil {
		t.Fatalf("expected error for blank callee.profile")
	}
}

func TestSignedCausalContext_FormAccessor(t *testing.T) {
	cases := []struct {
		name string
		c    SignedCausalContext
		want string
	}{
		{"none", CausalNone(), "none"},
		{
			"scalar",
			CausalScalar(SignedCausalRef{
				ReceiptHashHex: strings.Repeat("ab", 32),
				ReceiptURA:     "easynet:///r/acme/receipt/x",
			}),
			"scalar",
		},
		{
			"list",
			CausalList([]SignedCausalRef{{
				ReceiptHashHex: strings.Repeat("ab", 32),
				ReceiptURA:     "easynet:///r/acme/receipt/x",
			}}),
			"list",
		},
		{
			"merkle",
			CausalMerkle(strings.Repeat("ab", 32), "easynet:///r/acme/proof/x"),
			"merkle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Form(); got != tc.want {
				t.Errorf("Form() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSignedCausalContext_IsNoneCoversZeroValue(t *testing.T) {
	var zero SignedCausalContext
	if !zero.IsNone() {
		t.Errorf("zero-value SignedCausalContext should report IsNone() = true")
	}
	if !CausalNone().IsNone() {
		t.Errorf("CausalNone().IsNone() should be true")
	}
	scalar := CausalScalar(SignedCausalRef{
		ReceiptHashHex: strings.Repeat("ab", 32),
		ReceiptURA:     "easynet:///r/acme/receipt/x",
	})
	if scalar.IsNone() {
		t.Errorf("Scalar form must not report IsNone()")
	}
}

func TestSigningConfig_SeedLengthRule(t *testing.T) {
	if _, err := NewSigningConfig(make([]byte, 31), "easynet:///r/acme/agent/01BAK"); err == nil {
		t.Errorf("31-byte seed should be rejected")
	}
	if _, err := NewSigningConfig(make([]byte, 33), "easynet:///r/acme/agent/01BAK"); err == nil {
		t.Errorf("33-byte seed should be rejected")
	}
	if _, err := NewSigningConfig(make([]byte, 32), "easynet:///r/acme/agent/01BAK"); err != nil {
		t.Errorf("32-byte seed should be accepted, got: %v", err)
	}
}

func TestSigningConfig_RequiresCallerURA(t *testing.T) {
	if _, err := NewSigningConfig(make([]byte, 32), "   "); err == nil {
		t.Errorf("blank caller URA should be rejected")
	}
}

func TestSigningConfig_DefensiveCopiesSeed(t *testing.T) {
	src := make([]byte, 32)
	for i := range src {
		src[i] = byte(i + 1)
	}
	cfg, err := NewSigningConfig(src, "easynet:///r/acme/agent/01BAK")
	if err != nil {
		t.Fatalf("NewSigningConfig: %v", err)
	}
	src[0] = 0xFF
	if cfg.Seed[0] == 0xFF {
		t.Errorf("NewSigningConfig must copy seed, not alias the caller's slice")
	}
}

func TestSigningConfig_PinsProfileToStrictV2(t *testing.T) {
	cfg, err := NewSigningConfig(make([]byte, 32), "easynet:///r/acme/agent/01BAK")
	if err != nil {
		t.Fatalf("NewSigningConfig: %v", err)
	}
	if cfg.CallerProfile != "axon-strict-v2" {
		t.Errorf("CallerProfile = %q, want axon-strict-v2", cfg.CallerProfile)
	}
}
