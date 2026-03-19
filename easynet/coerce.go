// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/coerce.go
// Description: Go SDK type-coercion helper set for consistent conversion of dynamic payload values.
//
// Protocol Responsibility:
// - Provides shared coercion primitives used by Go SDK case/tooling code when handling loosely typed maps and JSON data.
// - Reduces per-feature parsing drift by standardizing bool/int/string/map conversion behavior in one module.
//
// Implementation Approach:
// - Implements deterministic conversion paths with fallback semantics for nil, numeric, string, and map inputs.
// - Includes pointer helpers and map-normalization functions to keep call-site code compact and predictable.
//
// Usage Contract:
// - Consumers should rely on these helpers when decoding generic `any` payloads from bridge/runtime responses.
// - Fallback-return variants must be used where strict schema validation is not guaranteed upstream.
//
// Architectural Position:
// - Core utility layer in the Go SDK beneath orchestration/case modules and above raw payload handling sites.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.
package easynet

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// BoolPtr returns a pointer to the given bool.
func BoolPtr(v bool) *bool { return &v }

// IntPtr returns a pointer to the given int.
func IntPtr(v int) *int { return &v }

// AsString extracts a trimmed string from an any value.
// Returns empty string for nil or non-string types.
func AsString(raw any) string {
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// AsStringOrDefault extracts a trimmed string, returning fallback when empty.
func AsStringOrDefault(raw any, fallback string) string {
	value := AsString(raw)
	if value == "" {
		return fallback
	}
	return value
}

// AsBool extracts a bool from an any value.
// Recognizes bool, numeric non-zero (int, int64, float64), and
// case-insensitive string "true"/"1"/"yes"/"on".
func AsBool(raw any) bool {
	if raw == nil {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return n != 0
		}
		return false
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

// AsBoolOrDefault extracts a bool from an any value with a default.
func AsBoolOrDefault(raw any, defaultVal bool) bool {
	if raw == nil {
		return defaultVal
	}
	return AsBool(raw)
}

// AsInt extracts an int from an any value, returning fallback on failure.
// Supports int, int64, float64, float32, json.Number, and string.
func AsInt(raw any, fallback int) int {
	if raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		parsed, err := v.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

// AsMap extracts a map[string]any from an any value.
func AsMap(raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return nil
}

// AsMapOrEmpty extracts a map[string]any, returning an empty map (never nil)
// for non-map inputs.
func AsMapOrEmpty(raw any) map[string]any {
	if m := AsMap(raw); m != nil {
		return m
	}
	return map[string]any{}
}

// AsStringMap extracts a map[string]string from a generic map.
func AsStringMap(raw any) map[string]string {
	m := AsMap(raw)
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = AsString(v)
	}
	return result
}
