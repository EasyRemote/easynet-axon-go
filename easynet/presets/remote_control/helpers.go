// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/helpers.go
// Description: Go utility helpers for remote-control argument coercion, schema defaults, and small shared formatting routines.
//
// Protocol Responsibility:
// - Keeps untyped MCP argument parsing predictable across remote-control handlers.
// - Centralizes tiny helper behavior that would otherwise be duplicated across handler code paths.
//
// Implementation Approach:
// - Delegates canonical conversions to shared easynet helpers when possible.
// - Keeps lightweight utility logic near the preset boundary instead of leaking it into orchestrator code.
//
// Usage Contract:
// - Use these helpers when handler code needs consistent coercion from untyped MCP payloads.
// - Avoid embedding business logic here; promote larger behavior into handlers or descriptor modules instead.
//
// Architectural Position:
// - Support utility layer for the Go remote-control preset.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

import (
	"encoding/json"
	"fmt"
	"strings"

	"easynet.run/axon/sdk/go/easynet"
)

// asMap delegates to easynet.AsMapOrEmpty (never returns nil).
func asMap(raw any) map[string]any {
	return easynet.AsMapOrEmpty(raw)
}

// asMapOrNil returns the value as map[string]any if it is one, otherwise nil.
// Use instead of asMap when the caller needs to distinguish "absent" from "empty".
func asMapOrNil(raw any) map[string]any {
	cast, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return cast
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

// normalizeTags merges fallback tags with tags parsed from raw ([]any),
// deduplicating and trimming empty values.
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

// sanitizeIDFragment normalizes a string into a valid identifier fragment
// (lowercase alphanumeric + hyphens).  Returns an error if no valid characters remain.
func sanitizeIDFragment(raw string) (string, error) {
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
		return "", fmt.Errorf("identifier contains no valid characters: %q", raw)
	}
	return value, nil
}

// shellSingleQuote wraps a string in single quotes, escaping embedded quotes
// using the shell idiom '\'' (end-quote, double-quoted quote, resume-quote).
func shellSingleQuote(raw string) string {
	return "'" + strings.ReplaceAll(raw, "'", "'\"'\"'") + "'"
}

// mergeMaps merges one or more source maps into dst (later sources win).
// Creates a new map if dst is nil.
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

// toJSON serializes a map to a JSON string, returning "{}" on error.
func toJSON(value map[string]any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// resolveTenant extracts a tenant ID from an argument, falling back to the
// given default when the value is empty or nil-like.
func resolveTenant(raw any, fallback string) string {
	if value := asString(raw); value != "" && value != "<nil>" {
		return value
	}
	return fallback
}
