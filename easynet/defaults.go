// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/defaults.go
// Description: Go SDK shared default constants for endpoint, timeout, execution mode, packaging, and MCP protocol behavior.
//
// Protocol Responsibility:
// - Defines canonical default values that keep Go SDK behavior consistent across client, orchestration, and case modules.
// - Acts as a centralized source of truth for protocol and runtime defaults consumed throughout SDK code paths.
//
// Implementation Approach:
// - Stores immutable package-level constants with explicit naming and unit semantics.
// - Separates defaults from call logic so value updates remain auditable and low-risk.
//
// Usage Contract:
// - Use these constants instead of hardcoded literals when building Go SDK requests or runtime configuration.
// - Changing these values can affect compatibility expectations and should be versioned/reviewed carefully.
//
// Architectural Position:
// - Foundational configuration layer in the Go SDK used by higher-level client and orchestration abstractions.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.
package easynet

// Protocol-level default constants shared across the Go SDK.
const (
	// DefaultTimeoutMs is the default RPC/request timeout in milliseconds.
	DefaultTimeoutMs = 30000

	// DefaultMCPToolStreamTimeoutMs is the default per-chunk timeout for MCP
	// tool stream reads.  Long-running streams (e.g. PTY sessions) should set
	// an explicit higher value.
	DefaultMCPToolStreamTimeoutMs = 60000

	// DefaultConnectTimeoutMs is the default connection timeout in milliseconds.
	DefaultConnectTimeoutMs = 5000

	// DefaultExecutionMode is the default sandbox execution mode for installs.
	DefaultExecutionMode = "sandbox_first"

	// DefaultInstallTimeoutSeconds is the default install deadline.
	DefaultInstallTimeoutSeconds = 45

	// DefaultEndpoint is the default Axon runtime endpoint.
	DefaultEndpoint = "http://127.0.0.1:50051"

	// DefaultTenant is the default tenant identifier used in tests/development.
	DefaultTenant = "tenant-test"

	// DefaultUninstallReason is the default reason sent when cleaning up installs.
	DefaultUninstallReason = "sdk cleanup"

	// DefaultMaxChunks is the default maximum number of protocol streaming chunks.
	DefaultMaxChunks = 4096

	// McpProtocolVersion is the default MCP protocol version used by SDK MCP servers.
	McpProtocolVersion = "2026-02-26"
)
