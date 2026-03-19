// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/orchestrator_lifecycle.go
// Description: Capability lifecycle operations — publish/install/activate, deploy skill packages,
//              A2A task dispatch, and install cleanup.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DeployError is a structured error from deploy operations that preserves
// machine-readable detail (install_id, cleanup results) and an optional
// DeployTrace for per-phase timing observability.
type DeployError struct {
	Message string
	Detail  map[string]any
	Trace   *DeployTrace
}

func (e *DeployError) Error() string { return e.Message }

// Lifecycle holds the results of a publish/install/activate workflow.
type Lifecycle struct {
	PackageID      string
	CapabilityName string
	PackageVersion string
	Publish        map[string]any
	Install        map[string]any
	Activate       map[string]any
}

// DeployAbilityPackageDescriptor describes a skill package for deployment.
type DeployAbilityPackageDescriptor struct {
	PackageID          string
	CapabilityName     string
	Version            string
	Digest             string
	SignatureBase64    string
	Tags               []string
	Metadata           map[string]string
	PackageBytesBase64 string
	InstallTimeoutSec  int
	ExecutionMode      string
}

func (d DeployAbilityPackageDescriptor) toMap() map[string]any {
	return map[string]any{
		"package_id":              strings.TrimSpace(d.PackageID),
		"capability_name":         strings.TrimSpace(d.CapabilityName),
		"version":                 strings.TrimSpace(d.Version),
		"digest":                  strings.TrimSpace(d.Digest),
		"signature_base64":        strings.TrimSpace(d.SignatureBase64),
		"package_bytes_base64":    strings.TrimSpace(d.PackageBytesBase64),
		"install_timeout_seconds": d.InstallTimeoutSec,
		"execution_mode":          strings.TrimSpace(d.ExecutionMode),
	}
}

// InstallRef tracks created installs for cleanup.
type InstallRef struct {
	Mode      string
	NodeID    string
	InstallID string
}

// PublishInstallActivate publishes a capability package, installs it to node, and activates it.
func (o *Orchestrator) PublishInstallActivate(
	nodeID string,
	packageID string,
	capabilityName string,
	bundle BundleRef,
	metadata map[string]string,
) (Lifecycle, error) {
	if err := o.Open(); err != nil {
		return Lifecycle{}, err
	}

	signer := o.signer
	if signer == nil {
		signer = deterministicSignatureTokenBase64
	}
	publish, err := o.bridge.PublishCapabilityWithRequest(o.handle, PublishCapabilityRequest{
		TenantID:             o.Tenant,
		PackageID:            packageID,
		CapabilityName:       capabilityName,
		Version:              bundle.Version,
		Digest:               bundle.Digest,
		SignatureBase64:      signer(packageID, capabilityName, bundle.Digest),
		Tags:                 nil,
		Requirements:         nil,
		Metadata:             metadata,
		PayloadURI:           "",
		PayloadSizeBytes:     nil,
		PackageBytesBase64:   bundle.Base64,
		SignatureFingerprint: "",
		PackageFingerprint:   "",
		PublisherKeyVersion:  0,
	})
	if err != nil {
		return Lifecycle{}, err
	}

	publishDigest := bundle.Digest
	if packageRef, ok := publish["package_ref"].(map[string]any); ok {
		if maybe := strings.TrimSpace(fmt.Sprint(packageRef["digest"])); maybe != "" {
			publishDigest = maybe
		}
	}

	// Extract proof fields from publish response for install request.
	var payloadSize *int64
	var payloadDigest string
	var signatureFingerprint string
	var packageFingerprint string
	proof := publish["proof"]
	if m, ok := proof.(map[string]any); ok {
		switch raw := m["payload_size_bytes"].(type) {
		case float64:
			v := int64(raw)
			payloadSize = &v
		case int64:
			v := raw
			payloadSize = &v
		case int:
			v := int64(raw)
			payloadSize = &v
		case json.Number:
			if parsed, e := raw.Int64(); e == nil {
				v := parsed
				payloadSize = &v
			}
		}
		if v := strings.TrimSpace(fmt.Sprint(m["payload_digest"])); v != "" && v != "<nil>" {
			payloadDigest = v
		}
		if v := strings.TrimSpace(fmt.Sprint(m["signature_fingerprint"])); v != "" && v != "<nil>" {
			signatureFingerprint = v
		}
		if v := strings.TrimSpace(fmt.Sprint(m["package_fingerprint"])); v != "" && v != "<nil>" {
			packageFingerprint = v
		}
	}

	install, err := o.bridge.InstallCapabilityWithRequest(o.handle, InstallCapabilityRequest{
		TenantID:              o.Tenant,
		NodeID:                nodeID,
		PackageID:             packageID,
		Version:               bundle.Version,
		Digest:                publishDigest,
		RequireConsent:        BoolPtr(false),
		AllowTransferredCode:  BoolPtr(true),
		ExecutionMode:         DefaultExecutionMode,
		InstallTimeoutSeconds: IntPtr(45),
		PayloadDigest:         payloadDigest,
		PayloadSizeBytes:      payloadSize,
		SignatureFingerprint:  signatureFingerprint,
		PackageFingerprint:    packageFingerprint,
	})
	if err != nil {
		return Lifecycle{}, err
	}

	installID := strings.TrimSpace(fmt.Sprint(install["install_id"]))
	if installID == "" {
		return Lifecycle{}, fmt.Errorf("install_id missing: %v", install)
	}

	activate, err := o.bridge.ActivateCapability(o.handle, o.Tenant, nodeID, installID)
	if err != nil {
		return Lifecycle{}, err
	}

	return Lifecycle{
		PackageID:      packageID,
		CapabilityName: capabilityName,
		PackageVersion: bundle.Version,
		Publish:        publish,
		Install:        install,
		Activate:       activate,
	}, nil
}

// DeployAbilityPackage executes publish/install/activate for an arbitrary descriptor.
// Returns a map shaped for MCP-case consumption while keeping lifecycle details visible.
func (o *Orchestrator) DeployAbilityPackage(
	nodeID string,
	descriptor DeployAbilityPackageDescriptor,
	cleanupOnActivateFailure bool,
) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}

	packageID := strings.TrimSpace(descriptor.PackageID)
	capabilityName := strings.TrimSpace(descriptor.CapabilityName)
	version := strings.TrimSpace(descriptor.Version)
	signature := strings.TrimSpace(descriptor.SignatureBase64)
	digest := strings.TrimSpace(descriptor.Digest)
	pkgBase64 := strings.TrimSpace(descriptor.PackageBytesBase64)
	if packageID == "" {
		return nil, fmt.Errorf("package_id is required")
	}
	if capabilityName == "" {
		return nil, fmt.Errorf("capability_name is required")
	}
	if version == "" {
		return nil, fmt.Errorf("version is required")
	}
	if signature == "" {
		return nil, fmt.Errorf("signature_base64 is required")
	}
	if pkgBase64 == "" {
		return nil, fmt.Errorf("package_bytes_base64 is required")
	}

	installTimeoutSeconds := descriptor.InstallTimeoutSec
	if installTimeoutSeconds <= 0 {
		installTimeoutSeconds = 45
	}
	executionMode := strings.TrimSpace(descriptor.ExecutionMode)
	if executionMode == "" {
		executionMode = DefaultExecutionMode
	}

	tags := descriptor.Tags
	if tags == nil {
		tags = []string{}
	}
	metadata := descriptor.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	publish, err := o.bridge.PublishCapabilityWithRequest(o.handle, PublishCapabilityRequest{
		TenantID:             o.Tenant,
		PackageID:            packageID,
		CapabilityName:       capabilityName,
		Version:              version,
		Digest:               digest,
		SignatureBase64:      signature,
		Tags:                 tags,
		Requirements:         map[string]string{},
		Metadata:             metadata,
		PayloadURI:           "",
		PayloadSizeBytes:     nil,
		PackageBytesBase64:   pkgBase64,
		SignatureFingerprint: "",
		PackageFingerprint:   "",
		PublisherKeyVersion:  0,
	})
	if err != nil {
		return nil, err
	}

	publishDigest := digest
	if rawRef, ok := publish["package_ref"].(map[string]any); ok {
		if rawDigest, ok := rawRef["digest"]; ok {
			if maybe := strings.TrimSpace(fmt.Sprint(rawDigest)); maybe != "" {
				publishDigest = maybe
			}
		}
	}

	install, err := o.bridge.InstallCapabilityWithRequest(o.handle, InstallCapabilityRequest{
		TenantID:              o.Tenant,
		NodeID:                nodeID,
		PackageID:             packageID,
		Version:               version,
		Digest:                publishDigest,
		RequireConsent:        BoolPtr(false),
		AllowTransferredCode:  BoolPtr(true),
		ExecutionMode:         executionMode,
		InstallTimeoutSeconds: IntPtr(installTimeoutSeconds),
		PayloadDigest:         "",
		PayloadSizeBytes:      nil,
		SignatureFingerprint:  "",
		PackageFingerprint:    "",
	})
	if err != nil {
		return nil, err
	}

	installID := strings.TrimSpace(fmt.Sprint(install["install_id"]))
	if installID == "" {
		return nil, fmt.Errorf("deploy_ability_package missing install_id after install")
	}

	activate, err := o.bridge.ActivateCapability(o.handle, o.Tenant, nodeID, installID)
	if err != nil {
		detail := map[string]any{
			"message":    "activate failed",
			"error":      err.Error(),
			"install_id": installID,
		}
		if cleanupOnActivateFailure {
			detail["cleanup"] = o.CleanupInstalls([]InstallRef{
				{Mode: "publish_install_activate", NodeID: nodeID, InstallID: installID},
			})
		}
		return nil, &DeployError{
			Message: fmt.Sprintf("activate failed: %s", err.Error()),
			Detail:  detail,
		}
	}

	result := descriptor.toMap()
	for k, v := range map[string]any{
		"ok":              true,
		"publish":         publish,
		"install":         install,
		"activate":        activate,
		"install_id":      installID,
		"package_id":      packageID,
		"capability_name": capabilityName,
	} {
		result[k] = v
	}
	result["package"] = map[string]any{
		"package_id":       packageID,
		"capability_name":  capabilityName,
		"version":          version,
		"signature_base64": signature,
		"tags":             tags,
	}
	return result, nil
}

// SendA2ATask sends an A2A task to a node for the given skill id and payload.
func (o *Orchestrator) SendA2ATask(nodeID string, skillID string, payload map[string]any) (map[string]any, error) {
	if err := o.Open(); err != nil {
		return nil, err
	}
	return o.bridge.SendA2ATask(o.handle, o.Tenant, nodeID, skillID, payload, "", "", "")
}

// CleanupInstalls attempts uninstall in reverse order; returns a summary map.
// The deactivateReason parameter is passed to uninstall calls; if empty, the orchestrator's default reason is used.
func (o *Orchestrator) CleanupInstalls(created []InstallRef, deactivateReason ...string) map[string]any {
	reason := o.uninstallReason
	if len(deactivateReason) > 0 && strings.TrimSpace(deactivateReason[0]) != "" {
		reason = strings.TrimSpace(deactivateReason[0])
	}
	results := make([]map[string]any, 0, len(created))
	succeeded, failed := 0, 0
	for i := len(created) - 1; i >= 0; i-- {
		item := created[i]
		nodeID := strings.TrimSpace(item.NodeID)
		installID := strings.TrimSpace(item.InstallID)
		result := map[string]any{
			"mode":       strings.TrimSpace(item.Mode),
			"node_id":    nodeID,
			"install_id": installID,
		}
		if nodeID == "" || installID == "" {
			result["ok"] = false
			result["error"] = "missing node_id or install_id"
			failed++
			results = append(results, result)
			continue
		}
		resp, err := o.bridge.UninstallCapability(o.handle, o.Tenant, nodeID, installID, true, reason, false)
		if err != nil {
			result["ok"] = false
			result["error"] = err.Error()
			failed++
		} else {
			result["ok"] = true
			result["response"] = resp
			succeeded++
		}
		results = append(results, result)
	}
	return map[string]any{
		"attempted": len(created),
		"succeeded": succeeded,
		"failed":    failed,
		"ok":        failed == 0,
		"results":   results,
	}
}
