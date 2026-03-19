// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/dendrite_bridge_capability_request_test.go
// Description: Source file for Go SDK bridge and capability helper implementation; keeps behavior explicit and interoperable across language/runtime boundaries.
//
// Protocol Responsibility:
// - Implements Go SDK bridge and capability helper implementation contracts required by current Axon service and SDK surfaces.
// - Preserves stable request/response semantics and error mapping for dendrite_bridge_capability_request_test.go call paths.
//
// Implementation Approach:
// - Uses small typed helpers and explicit control flow to avoid hidden side effects.
// - Keeps protocol translation and transport details close to this module boundary.
//
// Usage Contract:
// - Callers should provide valid tenant/resource/runtime context before invoking exported APIs.
// - Errors should be treated as typed protocol/runtime outcomes rather than silently ignored.
//
// Architectural Position:
// - Part of the Go SDK bridge and capability helper implementation layer.
// - Should not embed unrelated orchestration logic outside this file's responsibility.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"strings"
	"testing"
)

func boolPtrT(v bool) *bool { return &v }
func intPtrT(v int) *int    { return &v }
func int64Ptr(v int64) *int64 {
	return &v
}

func TestInstallCapabilityRequestPayloadOmitsUnsetProofFields(t *testing.T) {
	req := InstallCapabilityRequest{
		TenantID:              "tenant-a",
		NodeID:                "node-a",
		PackageID:             "pkg-a",
		Version:               "1.0.0",
		Digest:                "sha256:abc",
		RequireConsent:        boolPtrT(false),
		AllowTransferredCode:  boolPtrT(true),
		ExecutionMode:         "sandbox_first",
		InstallTimeoutSeconds: intPtrT(120),
		PayloadDigest:         "   ",
		PayloadSizeBytes:      int64Ptr(0),
		SignatureFingerprint:  "",
		PackageFingerprint:    "\t",
	}

	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := payload["payload_digest"]; ok {
		t.Fatalf("payload_digest must be omitted for blank value")
	}
	if _, ok := payload["payload_size_bytes"]; ok {
		t.Fatalf("payload_size_bytes must be omitted for zero value")
	}
	if _, ok := payload["signature_fingerprint"]; ok {
		t.Fatalf("signature_fingerprint must be omitted for blank value")
	}
	if _, ok := payload["package_fingerprint"]; ok {
		t.Fatalf("package_fingerprint must be omitted for blank value")
	}
}

func TestPublishCapabilityRequestPayloadOmitsUnsetOptionalFields(t *testing.T) {
	req := PublishCapabilityRequest{
		TenantID:            "tenant-a",
		PackageID:           "pkg-a",
		CapabilityName:      "cap-a",
		Version:             "1.0.0",
		Digest:              "sha256:abc",
		SignatureBase64:     "c2ln",
		Tags:                nil,
		Requirements:        nil,
		Metadata:            nil,
		PayloadURI:          "  ",
		PayloadSizeBytes:    int64Ptr(0),
		PackageBytesBase64:  "",
		PublisherKeyVersion: 0,
	}

	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := payload["payload_uri"]; ok {
		t.Fatalf("payload_uri must be omitted for blank value")
	}
	if _, ok := payload["payload_size_bytes"]; ok {
		t.Fatalf("payload_size_bytes must be omitted for zero value")
	}
	if _, ok := payload["package_bytes_base64"]; ok {
		t.Fatalf("package_bytes_base64 must be omitted for blank value")
	}
	if _, ok := payload["publisher_key_version"]; ok {
		t.Fatalf("publisher_key_version must be omitted when <= 0")
	}
}

func TestInstallCapabilityRequestPayloadIncludesExplicitProofFields(t *testing.T) {
	req := InstallCapabilityRequest{
		TenantID:              "tenant-a",
		NodeID:                "node-a",
		PackageID:             "pkg-a",
		Version:               "1.0.0",
		Digest:                "sha256:abc",
		RequireConsent:        boolPtrT(false),
		AllowTransferredCode:  boolPtrT(true),
		ExecutionMode:         "sandbox_first",
		InstallTimeoutSeconds: intPtrT(120),
		PayloadDigest:         "sha256:proof",
		PayloadSizeBytes:      int64Ptr(42),
		SignatureFingerprint:  "sig:abc",
		PackageFingerprint:    "pkg:abc",
	}

	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["payload_digest"] != "sha256:proof" {
		t.Fatalf("unexpected payload_digest: %v", payload["payload_digest"])
	}
	if payload["payload_size_bytes"] != int64(42) {
		t.Fatalf("unexpected payload_size_bytes: %v", payload["payload_size_bytes"])
	}
	if payload["signature_fingerprint"] != "sig:abc" {
		t.Fatalf("unexpected signature_fingerprint: %v", payload["signature_fingerprint"])
	}
	if payload["package_fingerprint"] != "pkg:abc" {
		t.Fatalf("unexpected package_fingerprint: %v", payload["package_fingerprint"])
	}
}

func TestInstallCapabilityRequestValidateRejectsNegativeProofSize(t *testing.T) {
	req := InstallCapabilityRequest{
		TenantID:         "tenant-a",
		NodeID:           "node-a",
		PackageID:        "pkg-a",
		Version:          "1.0.0",
		Digest:           "sha256:abc",
		PayloadSizeBytes: int64Ptr(-1),
	}
	err := req.validate()
	if err == nil {
		t.Fatalf("expected validate to reject negative payload_size_bytes")
	}
	if !strings.Contains(err.Error(), "payload_size_bytes") {
		t.Fatalf("expected error to mention payload_size_bytes, got: %v", err)
	}
}

func TestPublishCapabilityRequestValidateRejectsNegativeProofSize(t *testing.T) {
	req := PublishCapabilityRequest{
		TenantID:         "tenant-a",
		PackageID:        "pkg-a",
		CapabilityName:   "cap-a",
		Version:          "1.0.0",
		SignatureBase64:  "c2ln",
		Digest:           "sha256:abc",
		PayloadURI:       "payload://bucket/object",
		PayloadSizeBytes: int64Ptr(-1),
	}
	err := req.validate()
	if err == nil {
		t.Fatalf("expected validate to reject negative payload_size_bytes")
	}
	if !strings.Contains(err.Error(), "payload_size_bytes") {
		t.Fatalf("expected error to mention payload_size_bytes, got: %v", err)
	}
}

func TestPublishCapabilityRequestValidateRequiresPayloadSource(t *testing.T) {
	req := PublishCapabilityRequest{
		TenantID:         "tenant-a",
		PackageID:        "pkg-a",
		CapabilityName:   "cap-a",
		Version:          "1.0.0",
		Digest:           "",
		SignatureBase64:  "c2ln",
		PayloadURI:       "https://example.com/payload.json",
		PayloadSizeBytes: int64Ptr(1),
	}
	if err := req.validate(); err != nil {
		t.Fatalf("expected valid publish request, got %v", err)
	}

	req.PayloadURI = ""
	if err := req.validate(); err == nil {
		t.Fatalf("expected validate to reject missing payload_uri/package_bytes_base64")
	}

	req.PackageBytesBase64 = "cGF5bG9hZA=="
	if err := req.validate(); err != nil {
		t.Fatalf("expected package_bytes_base64 to satisfy payload source requirement, got %v", err)
	}
}

func TestPublishCapabilityRequestValidateRejectsDigestOnlyPayloadSource(t *testing.T) {
	req := PublishCapabilityRequest{
		TenantID:        "tenant-a",
		PackageID:       "pkg-a",
		CapabilityName:  "cap-a",
		Version:         "1.0.0",
		Digest:          "sha256:abc",
		SignatureBase64: "c2ln",
		PayloadURI:      "",
	}
	if err := req.validate(); err == nil {
		t.Fatalf("expected validate to reject digest-only publish request without payload source")
	}
}

func TestInstallCapabilityRequestValidateRequiresDigestOrPayloadDigest(t *testing.T) {
	req := InstallCapabilityRequest{
		TenantID:              "tenant-a",
		NodeID:                "node-a",
		PackageID:             "pkg-a",
		Version:               "1.0.0",
		Digest:                "",
		ExecutionMode:         "sandbox_first",
		InstallTimeoutSeconds: intPtrT(30),
	}
	if err := req.validate(); err == nil {
		t.Fatalf("expected validate to reject missing digest and payload_digest")
	}

	req.PayloadDigest = "sha256:proof"
	if err := req.validate(); err != nil {
		t.Fatalf("expected payload_digest to satisfy digest requirement, got %v", err)
	}
}

func TestPublishCapabilityRequestValidateRejectsDigestWithoutSha256Prefix(t *testing.T) {
	req := PublishCapabilityRequest{
		TenantID:        "tenant-a",
		PackageID:       "pkg-a",
		CapabilityName:  "cap-a",
		Version:         "1.0.0",
		Digest:          "abc123",
		SignatureBase64: "c2ln",
		PayloadURI:      "https://example.com/payload.json",
	}
	err := req.validate()
	if err == nil {
		t.Fatalf("expected validate to reject digest without sha256 prefix")
	}
	if !strings.Contains(err.Error(), "sha256:") {
		t.Fatalf("expected error to mention sha256 prefix, got: %v", err)
	}
}

func TestInstallCapabilityRequestValidateRejectsPayloadDigestWithoutSha256Prefix(t *testing.T) {
	req := InstallCapabilityRequest{
		TenantID:      "tenant-a",
		NodeID:        "node-a",
		PackageID:     "pkg-a",
		Version:       "1.0.0",
		PayloadDigest: "proof",
	}
	err := req.validate()
	if err == nil {
		t.Fatalf("expected validate to reject payload_digest without sha256 prefix")
	}
	if !strings.Contains(err.Error(), "sha256:") {
		t.Fatalf("expected error to mention sha256 prefix, got: %v", err)
	}
}

func TestDeployRequestValidateRejectsDigestWithoutSha256Prefix(t *testing.T) {
	req := DeployMCPListDirRequest{
		TenantID:        "tenant-a",
		NodeID:          "node-a",
		TargetPath:      "/client",
		CommandTemplate: "bash echo.sh",
		Digest:          "abc123",
	}
	if err := req.validate(); err == nil {
		t.Fatalf("expected deploy validate to reject digest without sha256 prefix")
	}
}

func TestUpdateRequestValidateRejectsDigestWithoutSha256Prefix(t *testing.T) {
	req := UpdateMCPListDirRequest{
		TenantID:        "tenant-a",
		NodeID:          "node-a",
		TargetPath:      "/client",
		CommandTemplate: "bash echo.sh",
		Digest:          "abc123",
	}
	if err := req.validate(); err == nil {
		t.Fatalf("expected update validate to reject digest without sha256 prefix")
	}
}

func TestPutOptionalProofSizeReturnsErrorForNegative(t *testing.T) {
	payload := map[string]any{}
	negative := int64(-1)

	err := putOptionalPayloadSize(payload, "payload_size_bytes", &negative)
	if err == nil {
		t.Fatalf("expected error for negative value")
	}
	if !strings.Contains(err.Error(), "payload_size_bytes") {
		t.Fatalf("expected error to mention payload_size_bytes, got: %v", err)
	}
	if _, ok := payload["payload_size_bytes"]; ok {
		t.Fatalf("payload_size_bytes must not be set when error is returned")
	}
}

func TestPutOptionalProofSizeOmitsZeroAndIncludesPositive(t *testing.T) {
	payload := map[string]any{}
	zero := int64(0)
	positive := int64(7)

	if err := putOptionalPayloadSize(payload, "payload_size_bytes", &zero); err != nil {
		t.Fatalf("unexpected error for zero: %v", err)
	}
	if _, ok := payload["payload_size_bytes"]; ok {
		t.Fatalf("payload_size_bytes must be omitted for zero value")
	}

	if err := putOptionalPayloadSize(payload, "payload_size_bytes", &positive); err != nil {
		t.Fatalf("unexpected error for positive: %v", err)
	}
	if got, ok := payload["payload_size_bytes"]; !ok || got != int64(7) {
		t.Fatalf("expected payload_size_bytes=7, got %v", got)
	}
}

func TestPutOptionalProofSizeNilIsNoOp(t *testing.T) {
	payload := map[string]any{}
	if err := putOptionalPayloadSize(payload, "payload_size_bytes", nil); err != nil {
		t.Fatalf("unexpected error for nil: %v", err)
	}
	if _, ok := payload["payload_size_bytes"]; ok {
		t.Fatalf("payload_size_bytes must be omitted for nil value")
	}
}

func TestDeployAndUpdateRequestApplyDefaults(t *testing.T) {
	deploy := DeployMCPListDirRequest{}
	deploy.applyDefaults()
	if deploy.TargetPath != "/client" {
		t.Fatalf("deploy default target path mismatch: %q", deploy.TargetPath)
	}

	update := UpdateMCPListDirRequest{}
	update.applyDefaults()
	if update.TargetPath != "/client" {
		t.Fatalf("update default target path mismatch: %q", update.TargetPath)
	}
}

func TestDeployAndUpdateRequestValidate(t *testing.T) {
	deploy := DeployMCPListDirRequest{
		TenantID:        "tenant-a",
		NodeID:          "node-a",
		TargetPath:      "",
		CommandTemplate: "python3 -c 'print(1)'",
	}
	deploy.applyDefaults()
	if err := deploy.validate(); err != nil {
		t.Fatalf("expected deploy request to validate, got %v", err)
	}

	update := UpdateMCPListDirRequest{
		TenantID:        "tenant-a",
		NodeID:          "node-a",
		TargetPath:      "",
		CommandTemplate: "python3 -c 'print(1)'",
	}
	update.applyDefaults()
	if err := update.validate(); err != nil {
		t.Fatalf("expected update request to validate, got %v", err)
	}

	update.TenantID = ""
	if err := update.validate(); err == nil {
		t.Fatalf("expected update request to reject blank tenant_id")
	}
}

func TestDeployMCPListDirRequestPayloadIncludesTrimmedFields(t *testing.T) {
	req := DeployMCPListDirRequest{
		TenantID:           "tenant-a",
		NodeID:             "node-a",
		TargetPath:         "",
		CommandTemplate:    "bash echo.sh",
		PackageID:          "  pkg-a  ",
		CapabilityName:     "cap-a",
		ToolName:           "tool-a",
		Version:            "1.0.0",
		Digest:             "sha256:abc",
		SignatureBase64:    "c2ln",
		PackageBytesBase64: "cGF5bG9hZA==",
	}
	req.applyDefaults()
	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["package_id"] != "pkg-a" {
		t.Fatalf("expected trimmed package_id, got %v", payload["package_id"])
	}
	if payload["target_path"] != "/client" {
		t.Fatalf("expected target_path /client, got %v", payload["target_path"])
	}
}

func TestUpdateMCPListDirRequestPayloadDefaultBooleans(t *testing.T) {
	// Verify defaults when booleans are nil (not explicitly set).
	req := UpdateMCPListDirRequest{
		TenantID:        "tenant-a",
		NodeID:          "node-a",
		TargetPath:      "/client",
		CommandTemplate: "bash echo.sh",
	}
	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["deactivate_old"] != true {
		t.Fatalf("expected deactivate_old default true, got %v", payload["deactivate_old"])
	}
	if payload["uninstall_old"] != true {
		t.Fatalf("expected uninstall_old default true, got %v", payload["uninstall_old"])
	}
	if payload["force_uninstall"] != false {
		t.Fatalf("expected force_uninstall default false, got %v", payload["force_uninstall"])
	}

	// Verify explicit overrides take effect.
	req.DeactivateOld = boolPtrT(false)
	req.UninstallOld = boolPtrT(false)
	req.ForceUninstall = boolPtrT(true)
	payload, err = req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["deactivate_old"] != false {
		t.Fatalf("expected deactivate_old override false, got %v", payload["deactivate_old"])
	}
	if payload["uninstall_old"] != false {
		t.Fatalf("expected uninstall_old override false, got %v", payload["uninstall_old"])
	}
	if payload["force_uninstall"] != true {
		t.Fatalf("expected force_uninstall override true, got %v", payload["force_uninstall"])
	}
}

func TestUninstallCapabilityRequestValidateRequiresFields(t *testing.T) {
	req := UninstallCapabilityRequest{}
	if err := req.validate(); err == nil {
		t.Fatalf("expected validate to reject empty request")
	}

	req.TenantID = "tenant-a"
	if err := req.validate(); err == nil {
		t.Fatalf("expected validate to reject missing node_id")
	}

	req.NodeID = "node-a"
	if err := req.validate(); err == nil {
		t.Fatalf("expected validate to reject missing install_id")
	}

	req.InstallID = "install-a"
	if err := req.validate(); err != nil {
		t.Fatalf("expected validate to pass with all required fields, got %v", err)
	}
}

func TestUninstallCapabilityRequestPayloadDefaults(t *testing.T) {
	req := UninstallCapabilityRequest{
		TenantID:  "tenant-a",
		NodeID:    "node-a",
		InstallID: "install-a",
	}
	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["deactivate_first"] != true {
		t.Fatalf("expected deactivate_first default true, got %v", payload["deactivate_first"])
	}
	if payload["force"] != false {
		t.Fatalf("expected force default false, got %v", payload["force"])
	}
	if _, ok := payload["deactivate_reason"]; ok {
		t.Fatalf("deactivate_reason must be omitted when empty")
	}
}

func TestUninstallCapabilityRequestPayloadAllFields(t *testing.T) {
	req := UninstallCapabilityRequest{
		TenantID:         "tenant-a",
		NodeID:           "node-a",
		InstallID:        "install-a",
		DeactivateFirst:  boolPtrT(false),
		DeactivateReason: "upgrade",
		Force:            boolPtrT(true),
	}
	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["tenant_id"] != "tenant-a" {
		t.Fatalf("unexpected tenant_id: %v", payload["tenant_id"])
	}
	if payload["node_id"] != "node-a" {
		t.Fatalf("unexpected node_id: %v", payload["node_id"])
	}
	if payload["install_id"] != "install-a" {
		t.Fatalf("unexpected install_id: %v", payload["install_id"])
	}
	if payload["deactivate_first"] != false {
		t.Fatalf("expected deactivate_first false, got %v", payload["deactivate_first"])
	}
	if payload["deactivate_reason"] != "upgrade" {
		t.Fatalf("expected deactivate_reason upgrade, got %v", payload["deactivate_reason"])
	}
	if payload["force"] != true {
		t.Fatalf("expected force true, got %v", payload["force"])
	}
}

func TestUninstallCapabilityRequestDeactivateReasonTrimming(t *testing.T) {
	req := UninstallCapabilityRequest{
		TenantID:         "tenant-a",
		NodeID:           "node-a",
		InstallID:        "install-a",
		DeactivateReason: "  upgrade  ",
	}
	payload, err := req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["deactivate_reason"] != "upgrade" {
		t.Fatalf("expected trimmed deactivate_reason, got %q", payload["deactivate_reason"])
	}

	// Whitespace-only should be omitted
	req.DeactivateReason = "   "
	payload, err = req.helperPayload()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := payload["deactivate_reason"]; ok {
		t.Fatalf("deactivate_reason must be omitted for whitespace-only value")
	}
}
