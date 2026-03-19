// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/descriptor.go
// Description: Skill package descriptor: build, parse, and serialize for MCP deployment.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"easynet.run/axon/sdk/go/easynet"
)

type AbilityPackageDescriptor struct {
	AbilityName     string
	PackageID       string
	CapabilityName  string
	ToolName        string
	Description     string
	Version         string
	Tags            []string
	Metadata        map[string]string
	SignatureBase64  string
	PackageBytes    string
	Digest          string
}

func (d AbilityPackageDescriptor) toToolPayload() map[string]any {
	out := map[string]any{
		"ability_name":         d.AbilityName,
		"package_id":         d.PackageID,
		"capability_name":    d.CapabilityName,
		"tool_name":          d.ToolName,
		"description":        d.Description,
		"version":            d.Version,
		"tags":               d.Tags,
		"metadata":           d.Metadata,
		"signature_base64":   d.SignatureBase64,
		"package_bytes_base64": d.PackageBytes,
	}
	if d.Digest != "" {
		out["digest"] = d.Digest
	}
	return out
}

func (d AbilityPackageDescriptor) toDeployDescriptor() easynet.DeployAbilityPackageDescriptor {
	return easynet.DeployAbilityPackageDescriptor{
		PackageID:          d.PackageID,
		CapabilityName:     d.CapabilityName,
		Version:            d.Version,
		Digest:             d.Digest,
		SignatureBase64:    d.SignatureBase64,
		Tags:               d.Tags,
		Metadata:           d.Metadata,
		PackageBytesBase64:  d.PackageBytes,
		InstallTimeoutSec:   defaultInstallTimeoutSeconds,
		ExecutionMode:       defaultExecutionMode,
	}
}

func parseDescriptor(raw any) (AbilityPackageDescriptor, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return AbilityPackageDescriptor{}, fmt.Errorf("package must be an object")
	}
	metadataObj, ok := obj["metadata"].(map[string]any)
	if !ok {
		return AbilityPackageDescriptor{}, fmt.Errorf("package.metadata must be an object of string values")
	}
	metadata := map[string]string{}
	for key, value := range metadataObj {
		metadata[key] = asString(value)
	}
	required := []string{"ability_name", "package_id", "capability_name", "tool_name", "description", "version", "signature_base64", "package_bytes_base64"}
	for _, key := range required {
		if asString(obj[key]) == "" {
			return AbilityPackageDescriptor{}, fmt.Errorf("package.%s is required", key)
		}
	}
	return AbilityPackageDescriptor{
		AbilityName:    asString(obj["ability_name"]),
		PackageID:      asString(obj["package_id"]),
		CapabilityName: asString(obj["capability_name"]),
		ToolName:       asString(obj["tool_name"]),
		Description:    asString(obj["description"]),
		Version:        asString(obj["version"]),
		Tags:           normalizeTags(obj["tags"], []string{"mcp", "ability", "gallery"}),
		Metadata:       metadata,
		SignatureBase64: asString(obj["signature_base64"]),
		PackageBytes:   asString(obj["package_bytes_base64"]),
		Digest:         asString(obj["digest"]),
	}, nil
}

func parseOrBuildDescriptor(args map[string]any, fallbackSignature string) (AbilityPackageDescriptor, error) {
	if raw, ok := args["package"]; ok && raw != nil {
		return parseDescriptor(raw)
	}
	return buildDescriptor(args, fallbackSignature)
}

func buildDescriptor(args map[string]any, fallbackSignature string) (AbilityPackageDescriptor, error) {
	abilityName := asString(args["ability_name"])
	if abilityName == "" {
		return AbilityPackageDescriptor{}, fmt.Errorf("ability_name is required")
	}
	commandTemplate := asString(args["command_template"])
	if commandTemplate == "" {
		return AbilityPackageDescriptor{}, fmt.Errorf("command_template is required")
	}

	now := time.Now().UnixMilli()
	token := sanitizeIDFragment(abilityName)
	version := asStringOrDefault(args["version"], defaultVersion)
	toolName := asStringOrDefault(args["tool_name"], fmt.Sprintf("ability_%s", token))
	packageID := asStringOrDefault(args["package_id"], fmt.Sprintf("pkg.ability.%s.%d", token, now))
	capabilityName := asStringOrDefault(args["capability_name"], fmt.Sprintf("ability_%s", token))
	description := asStringOrDefault(args["description"], fmt.Sprintf("Ability %s", abilityName))
	signature := asStringOrDefault(args["signature_base64"], fallbackSignature)
	if signature == "" {
		return AbilityPackageDescriptor{}, fmt.Errorf("signature_base64 is required")
	}

	inputSchema := asMap(args["input_schema"])
	if len(inputSchema) == 0 {
		inputSchema = defaultInputSchema()
	}
	outputSchema := asMap(args["output_schema"])
	if len(outputSchema) == 0 {
		outputSchema = defaultOutputSchema()
	}
	metadata := map[string]string{
		"mcp.tool_name":     toolName,
		"mcp.description":   description,
		"mcp.input_schema":  toJSON(inputSchema),
		"mcp.output_schema": toJSON(outputSchema),
		"axon.exec.command": commandTemplate,
		"ability.name":      abilityName,
		"ability.version":   version,
	}
	packagePayload := map[string]any{
		"kind":               "axon.ability.package.v1",
		"ability_name":         abilityName,
		"package_id":         packageID,
		"capability_name":    capabilityName,
		"tool_name":          toolName,
		"description":        description,
		"version":            version,
		"command_template":   commandTemplate,
		"input_schema":       inputSchema,
		"output_schema":      outputSchema,
		"created_at_unix_ms": now,
	}
	raw, err := json.Marshal(packagePayload)
	if err != nil {
		return AbilityPackageDescriptor{}, fmt.Errorf("invalid package payload: %w", err)
	}
	return AbilityPackageDescriptor{
		AbilityName:    abilityName,
		PackageID:      packageID,
		CapabilityName: capabilityName,
		ToolName:       toolName,
		Description:    description,
		Version:        version,
		Tags:           normalizeTags(args["tags"], []string{"mcp", "ability", "gallery"}),
		Metadata:       metadata,
		SignatureBase64: signature,
		PackageBytes:   base64.StdEncoding.EncodeToString(raw),
		Digest:         asString(args["digest"]),
	}, nil
}

func defaultInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"entries": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}
}

func defaultOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"entries": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}
}

// defaultCommandTemplate is an AI agent command execution preset -- wraps a
// shell command in a python3 subprocess skill package.
func defaultCommandTemplate(command string) string {
	quoted, err := json.Marshal(command)
	if err != nil {
		quoted = []byte(`"` + strings.ReplaceAll(command, `"`, `\"`) + `"`)
	}
	body := strings.Join([]string{
		"import json,subprocess",
		fmt.Sprintf("cmd = %s", quoted),
		"proc = subprocess.run(['/bin/sh', '-c', cmd], text=True, capture_output=True)",
		"combined = (proc.stdout + proc.stderr).strip()",
		"print(json.dumps({'entries': [combined], 'command': cmd, 'exit_code': proc.returncode, 'stdout': proc.stdout, 'stderr': proc.stderr}))",
	}, "; ")
	return "python3 -c " + shellSingleQuote(string(body))
}
