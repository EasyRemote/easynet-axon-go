// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/helpers.go
// Description: Type conversion and utility helpers for remote-control handlers.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

import (
	"encoding/json"
	"strings"

	"easynet.run/axon/sdk/go/easynet"
)

// asMap delegates to easynet.AsMapOrEmpty (never returns nil).
func asMap(raw any) map[string]any {
	return easynet.AsMapOrEmpty(raw)
}

// asBool delegates to easynet.AsBool (canonical version now supports
// int64, json.Number, and case-insensitive strings).
func asBool(raw any) bool {
	return easynet.AsBool(raw)
}

// asBoolOrDefault delegates to easynet.AsBoolOrDefault.
func asBoolOrDefault(raw any, fallback bool) bool {
	return easynet.AsBoolOrDefault(raw, fallback)
}

// asInt delegates to easynet.AsInt with zero fallback.
func asInt(raw any) int {
	return easynet.AsInt(raw, 0)
}

// asString delegates to easynet.AsString (canonical version now trims).
func asString(raw any) string {
	return easynet.AsString(raw)
}

// asStringOrDefault delegates to easynet.AsStringOrDefault.
func asStringOrDefault(raw any, fallback string) string {
	return easynet.AsStringOrDefault(raw, fallback)
}

func normalizeTags(raw any, fallback []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(fallback))
	for _, value := range fallback {
		tag := strings.TrimSpace(value)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	values, ok := raw.([]any)
	if !ok {
		return out
	}
	for _, value := range values {
		tag := asString(value)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func sanitizeIDFragment(raw string) string {
	out := strings.Builder{}
	for _, ch := range strings.ToLower(strings.TrimSpace(raw)) {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
			out.WriteRune(ch)
		} else {
			out.WriteRune('-')
		}
	}
	value := strings.Trim(out.String(), "-")
	if value == "" {
		return "ability"
	}
	return value
}

func shellSingleQuote(raw string) string {
	return "'" + strings.ReplaceAll(raw, "'", "'\"'\"'") + "'"
}

func mergeMaps(dst map[string]any, sources ...map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for _, source := range sources {
		for key, value := range source {
			dst[key] = value
		}
	}
	return dst
}

func toJSON(value map[string]any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func resolveTenant(raw any, fallback string) string {
	if value := asString(raw); value != "" && value != "<nil>" {
		return value
	}
	return fallback
}
