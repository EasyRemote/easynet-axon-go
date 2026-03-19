// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/dendrite_bridge_capability_request.go
// Description: Typed capability request models and wire-payload builders shared by CGO and stub bridges.
//
// Protocol Responsibility:
// - Encodes capability publish/install/deploy/update request payloads into helper JSON maps.
// - Keeps optional proof constraint fields explicit and minimizes wire payload size.
//
// Implementation Approach:
// - Uses typed request structs with small map-builder helpers.
// - Omits optional empty/zero-value proof fields to keep semantics consistent across SDK languages.
//
// Usage Contract:
// - Required identity/routing fields must be provided by callers.
// - Optional proof fields are transmitted only when explicitly meaningful (non-empty string, non-zero size).
//
// Architectural Position:
// - Shared SDK request-model layer under Dendrite bridge transport calls.
// - Pure data shaping; no transport execution logic.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"fmt"
	"strings"
)

const digestPrefix = "sha256:"

func validateDigestPrefix(fieldName string, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if !strings.HasPrefix(trimmed, digestPrefix) || len(trimmed) == len(digestPrefix) {
		return fmt.Errorf("%s must start with 'sha256:' (e.g. sha256:<hex>)", fieldName)
	}
	return nil
}

type PublishCapabilityRequest struct {
	TenantID             string
	PackageID            string
	CapabilityName       string
	Version              string
	Digest               string
	SignatureBase64      string
	Tags                 []string
	Requirements         map[string]string
	Metadata             map[string]string
	PayloadURI           string
	PayloadSizeBytes     *int64
	PackageBytesBase64   string
	SignatureFingerprint string
	PackageFingerprint   string
	PublisherKeyVersion  int
}

func (r PublishCapabilityRequest) validate() error {
	if err := requireNonBlank("tenant_id", r.TenantID); err != nil {
		return err
	}
	if err := requireNonBlank("package_id", r.PackageID); err != nil {
		return err
	}
	if err := requireNonBlank("capability_name", r.CapabilityName); err != nil {
		return err
	}
	if err := requireNonBlank("version", r.Version); err != nil {
		return err
	}
	if err := validateDigestPrefix("digest", r.Digest); err != nil {
		return err
	}
	if err := requireNonBlank("signature_base64", r.SignatureBase64); err != nil {
		return err
	}
	if strings.TrimSpace(r.PackageBytesBase64) == "" &&
		strings.TrimSpace(r.PayloadURI) == "" {
		return fmt.Errorf("payload_uri or package_bytes_base64 is required")
	}
	if r.PayloadSizeBytes != nil && *r.PayloadSizeBytes < 0 {
		return fmt.Errorf("payload_size_bytes must be >= 0")
	}
	return nil
}

func (r PublishCapabilityRequest) helperPayload() (map[string]any, error) {
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	requirements := r.Requirements
	if requirements == nil {
		requirements = map[string]string{}
	}
	metadata := r.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	payload := map[string]any{
		"tenant_id":        strings.TrimSpace(r.TenantID),
		"package_id":       strings.TrimSpace(r.PackageID),
		"capability_name":  strings.TrimSpace(r.CapabilityName),
		"version":          strings.TrimSpace(r.Version),
		"signature_base64": strings.TrimSpace(r.SignatureBase64),
		"tags":             tags,
		"requirements":     requirements,
		"metadata":         metadata,
	}
	putOptionalTrimmedString(payload, "digest", r.Digest)
	putOptionalTrimmedString(payload, "payload_uri", r.PayloadURI)
	if err := putOptionalPayloadSize(payload, "payload_size_bytes", r.PayloadSizeBytes); err != nil {
		return nil, err
	}
	putOptionalTrimmedString(payload, "package_bytes_base64", r.PackageBytesBase64)
	putOptionalTrimmedString(payload, "signature_fingerprint", r.SignatureFingerprint)
	putOptionalTrimmedString(payload, "package_fingerprint", r.PackageFingerprint)
	if r.PublisherKeyVersion > 0 {
		payload["publisher_key_version"] = r.PublisherKeyVersion
	}
	return payload, nil
}

type InstallCapabilityRequest struct {
	TenantID              string
	NodeID                string
	PackageID             string
	Version               string
	Digest                string
	RequireConsent        *bool
	AllowTransferredCode  *bool
	ExecutionMode         string
	InstallTimeoutSeconds *int
	PayloadDigest         string
	PayloadSizeBytes      *int64
	SignatureFingerprint  string
	PackageFingerprint    string
}

func (r InstallCapabilityRequest) validate() error {
	if err := requireNonBlank("tenant_id", r.TenantID); err != nil {
		return err
	}
	if err := requireNonBlank("node_id", r.NodeID); err != nil {
		return err
	}
	if err := requireNonBlank("package_id", r.PackageID); err != nil {
		return err
	}
	if err := requireNonBlank("version", r.Version); err != nil {
		return err
	}
	if err := validateDigestPrefix("digest", r.Digest); err != nil {
		return err
	}
	if err := validateDigestPrefix("payload_digest", r.PayloadDigest); err != nil {
		return err
	}
	if strings.TrimSpace(r.Digest) == "" && strings.TrimSpace(r.PayloadDigest) == "" {
		return fmt.Errorf("digest or payload_digest is required")
	}
	if r.PayloadSizeBytes != nil && *r.PayloadSizeBytes < 0 {
		return fmt.Errorf("payload_size_bytes must be >= 0")
	}
	return nil
}

func (r InstallCapabilityRequest) helperPayload() (map[string]any, error) {
	payload := map[string]any{
		"tenant_id":  strings.TrimSpace(r.TenantID),
		"node_id":    strings.TrimSpace(r.NodeID),
		"package_id": strings.TrimSpace(r.PackageID),
		"version":    strings.TrimSpace(r.Version),
	}
	putOptionalTrimmedString(payload, "digest", r.Digest)
	if r.RequireConsent != nil {
		payload["require_consent"] = *r.RequireConsent
	}
	if r.AllowTransferredCode != nil {
		payload["allow_transferred_code"] = *r.AllowTransferredCode
	}
	if r.ExecutionMode != "" {
		payload["execution_mode"] = strings.TrimSpace(r.ExecutionMode)
	}
	if r.InstallTimeoutSeconds != nil {
		payload["install_timeout_seconds"] = *r.InstallTimeoutSeconds
	}
	putOptionalTrimmedString(payload, "payload_digest", r.PayloadDigest)
	if err := putOptionalPayloadSize(payload, "payload_size_bytes", r.PayloadSizeBytes); err != nil {
		return nil, err
	}
	putOptionalTrimmedString(payload, "signature_fingerprint", r.SignatureFingerprint)
	putOptionalTrimmedString(payload, "package_fingerprint", r.PackageFingerprint)
	return payload, nil
}

type DeployMCPListDirRequest struct {
	TenantID           string
	NodeID             string
	TargetPath         string
	CommandTemplate    string
	PackageID          string
	CapabilityName     string
	ToolName           string
	Version            string
	Digest             string
	SignatureBase64    string
	PackageBytesBase64 string
	InputSchema        map[string]any
	OutputSchema       map[string]any
	Tags               []string
}

func (r *DeployMCPListDirRequest) applyDefaults() {
	if strings.TrimSpace(r.TargetPath) == "" {
		r.TargetPath = "/client"
	}
}

func (r DeployMCPListDirRequest) validate() error {
	if err := requireNonBlank("tenant_id", r.TenantID); err != nil {
		return err
	}
	if err := requireNonBlank("node_id", r.NodeID); err != nil {
		return err
	}
	if err := requireNonBlank("target_path", r.TargetPath); err != nil {
		return err
	}
	if err := requireNonBlank("command_template", r.CommandTemplate); err != nil {
		return err
	}
	if err := validateDigestPrefix("digest", r.Digest); err != nil {
		return err
	}
	return nil
}

func (r DeployMCPListDirRequest) helperPayload() (map[string]any, error) {
	payload := map[string]any{
		"tenant_id":        strings.TrimSpace(r.TenantID),
		"node_id":          strings.TrimSpace(r.NodeID),
		"target_path":      strings.TrimSpace(r.TargetPath),
		"command_template": strings.TrimSpace(r.CommandTemplate),
	}
	putOptionalTrimmedString(payload, "package_id", r.PackageID)
	putOptionalTrimmedString(payload, "capability_name", r.CapabilityName)
	putOptionalTrimmedString(payload, "tool_name", r.ToolName)
	putOptionalTrimmedString(payload, "version", r.Version)
	putOptionalTrimmedString(payload, "digest", r.Digest)
	putOptionalTrimmedString(payload, "signature_base64", r.SignatureBase64)
	putOptionalTrimmedString(payload, "package_bytes_base64", r.PackageBytesBase64)
	if r.InputSchema != nil {
		payload["input_schema"] = r.InputSchema
	}
	if r.OutputSchema != nil {
		payload["output_schema"] = r.OutputSchema
	}
	if len(r.Tags) > 0 {
		payload["tags"] = r.Tags
	}
	return payload, nil
}

type UpdateMCPListDirRequest struct {
	TenantID           string
	NodeID             string
	ExistingInstallID  string
	DeactivateOld      *bool
	UninstallOld       *bool
	ForceUninstall     *bool
	DeactivateReason   string
	TargetPath         string
	CommandTemplate    string
	PackageID          string
	CapabilityName     string
	ToolName           string
	Version            string
	Digest             string
	SignatureBase64    string
	PackageBytesBase64 string
}

func (r *UpdateMCPListDirRequest) applyDefaults() {
	if strings.TrimSpace(r.TargetPath) == "" {
		r.TargetPath = "/client"
	}
}

func (r UpdateMCPListDirRequest) validate() error {
	if err := requireNonBlank("tenant_id", r.TenantID); err != nil {
		return err
	}
	if err := requireNonBlank("node_id", r.NodeID); err != nil {
		return err
	}
	if err := requireNonBlank("target_path", r.TargetPath); err != nil {
		return err
	}
	if err := requireNonBlank("command_template", r.CommandTemplate); err != nil {
		return err
	}
	if err := validateDigestPrefix("digest", r.Digest); err != nil {
		return err
	}
	return nil
}

func (r UpdateMCPListDirRequest) helperPayload() (map[string]any, error) {
	payload := map[string]any{
		"tenant_id":        strings.TrimSpace(r.TenantID),
		"node_id":          strings.TrimSpace(r.NodeID),
		"target_path":      strings.TrimSpace(r.TargetPath),
		"command_template": strings.TrimSpace(r.CommandTemplate),
	}
	if r.DeactivateOld != nil {
		payload["deactivate_old"] = *r.DeactivateOld
	} else {
		payload["deactivate_old"] = true // default to true, matching Java/Node/Python
	}
	if r.UninstallOld != nil {
		payload["uninstall_old"] = *r.UninstallOld
	} else {
		payload["uninstall_old"] = true // default to true
	}
	if r.ForceUninstall != nil {
		payload["force_uninstall"] = *r.ForceUninstall
	} else {
		payload["force_uninstall"] = false
	}
	putOptionalTrimmedString(payload, "existing_install_id", r.ExistingInstallID)
	putOptionalTrimmedString(payload, "deactivate_reason", r.DeactivateReason)
	putOptionalTrimmedString(payload, "package_id", r.PackageID)
	putOptionalTrimmedString(payload, "capability_name", r.CapabilityName)
	putOptionalTrimmedString(payload, "tool_name", r.ToolName)
	putOptionalTrimmedString(payload, "version", r.Version)
	putOptionalTrimmedString(payload, "digest", r.Digest)
	putOptionalTrimmedString(payload, "signature_base64", r.SignatureBase64)
	putOptionalTrimmedString(payload, "package_bytes_base64", r.PackageBytesBase64)
	return payload, nil
}

type UninstallCapabilityRequest struct {
	TenantID         string
	NodeID           string
	InstallID        string
	DeactivateFirst  *bool
	DeactivateReason string
	Force            *bool
}

func (r *UninstallCapabilityRequest) validate() error {
	if err := requireNonBlank("tenant_id", r.TenantID); err != nil {
		return err
	}
	if err := requireNonBlank("node_id", r.NodeID); err != nil {
		return err
	}
	if err := requireNonBlank("install_id", r.InstallID); err != nil {
		return err
	}
	return nil
}

func (r *UninstallCapabilityRequest) applyDefaults() {
	if r.DeactivateFirst == nil {
		v := true
		r.DeactivateFirst = &v
	}
	if r.Force == nil {
		v := false
		r.Force = &v
	}
}

func (r *UninstallCapabilityRequest) helperPayload() (map[string]any, error) {
	r.applyDefaults()
	if err := r.validate(); err != nil {
		return nil, err
	}
	payload := map[string]any{
		"tenant_id":        strings.TrimSpace(r.TenantID),
		"node_id":          strings.TrimSpace(r.NodeID),
		"install_id":       strings.TrimSpace(r.InstallID),
		"deactivate_first": *r.DeactivateFirst,
		"force":            *r.Force,
	}
	putOptionalTrimmedString(payload, "deactivate_reason", r.DeactivateReason)
	return payload, nil
}

func putOptionalTrimmedString(payload map[string]any, key string, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		payload[key] = trimmed
	}
}

// putOptionalPayloadSize adds key only when value is positive; zero is treated as absent.
// Returns an error if the value is negative.
func putOptionalPayloadSize(payload map[string]any, key string, value *int64) error {
	if value == nil {
		return nil
	}
	if *value < 0 {
		return fmt.Errorf("%s must be >= 0", key)
	}
	if *value > 0 {
		payload[key] = *value
	}
	// zero is treated as absent
	return nil
}

func requireNonBlank(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}
