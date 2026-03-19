// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/presets/remote_control/config.go
// Description: Runtime configuration and constants for the remote-control preset.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package remotecontrol

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	defaultInvocationStateCompleted = 5
	defaultVersion                  = "1.0.0"
	defaultSignature                = "__AXON_EPHEMERAL_DO_NOT_USE_IN_PROD__"
	defaultInstallTimeoutSeconds    = 45
	defaultExecutionMode            = "sandbox_first"
	defaultConnectTimeoutMs         = 5000
)

// RemoteControlRuntimeConfig captures process-level defaults shared by remote-control operations.
type RemoteControlRuntimeConfig struct {
	Endpoint         string
	Tenant           string
	ConnectTimeoutMs int
	SignatureBase64  string
}

// LoadRemoteControlConfigFromEnv loads runtime config from environment variables.
func LoadRemoteControlConfigFromEnv() RemoteControlRuntimeConfig {
	return RemoteControlRuntimeConfig{
		Endpoint:         envOr("AXON_ENDPOINT", "http://127.0.0.1:50051"),
		Tenant:           envOr("AXON_TENANT", "tenant-test"),
		ConnectTimeoutMs: parsePositiveIntOrFallback(os.Getenv("AXON_CONNECT_TIMEOUT_MS"), defaultConnectTimeoutMs),
		SignatureBase64:  envOr("AXON_DEPLOY_SIGNATURE_BASE64", defaultSignature),
	}
}

// LoadConfigFromEnv keeps previous naming for compatibility.
func LoadConfigFromEnv() RemoteControlRuntimeConfig {
	return LoadRemoteControlConfigFromEnv()
}

// EnsureNativeLibEnv discovers a local native bridge path and updates environment variable.
func EnsureNativeLibEnv() {
	if strings.TrimSpace(os.Getenv("EASYNET_DENDRITE_BRIDGE_LIB")) != "" {
		return
	}
	candidates := []string{
		filepath.Join("core", "runtime-rs", "dendrite-bridge", "target", "release", defaultBridgeLibName()),
		filepath.Join("..", "..", "..", "..", "..", "core", "runtime-rs", "dendrite-bridge", "target", "release", defaultBridgeLibName()),
	}
	for _, candidate := range candidates {
		if isPathToFile(candidate) {
			_ = os.Setenv("EASYNET_DENDRITE_BRIDGE_LIB", candidate)
			return
		}
	}
}

func EnsureNativeBridgeEnv() {
	EnsureNativeLibEnv()
}

func defaultBridgeLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libaxon_dendrite_bridge.dylib"
	case "windows":
		return "axon_dendrite_bridge.dll"
	default:
		return "libaxon_dendrite_bridge.so"
	}
}

func parsePositiveIntOrFallback(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func isPathToFile(raw string) bool {
	info, err := os.Stat(raw)
	return err == nil && !info.IsDir()
}

func randomHex(length int) string {
	data := make([]byte, length)
	_, _ = io.ReadFull(rand.Reader, data)
	return hex.EncodeToString(data)
}
