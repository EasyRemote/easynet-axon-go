---
name: axon-device-control-go
description: Control remote devices through EasyNet Axon using the Go SDK — discover devices, teach abilities, invoke abilities, manage device connections via MCP stdio server.
compatibility: Requires Go 1.22+, Axon Runtime binary, and DendriteBridge native library
metadata:
  author: easynet-axon
  version: "0.3.0"
  sdk: go
allowed-tools: Bash(*)
---

# Axon Device Control (Go)

Control remote terminal devices through EasyNet Axon Runtime using the Go SDK.

## Quick Start

Build and run the MCP stdio server:

```bash
cd ${CLAUDE_SKILL_DIR}
go build -o axon-mcp-server ./easynet/presets/remote_control/cmd
AXON_BIND=127.0.0.1:19816 ./axon-mcp-server
```

Or run directly:

```bash
AXON_BIND=127.0.0.1:19816 go run ./easynet/presets/remote_control/cmd
```

## Available Tools

### Device Discovery
- **discover_nodes** — Find online devices in the federation
- **list_remote_tools** — List MCP tools available on remote nodes
- **list_abilities** — List locally deployed abilities on a device

### Ability Lifecycle
- **deploy_ability** — Teach a device a new ability (shell command)
- **redeploy_ability** — Redeploy an existing ability by rebuilding its full package
- **uninstall_ability** — Remove a specific ability from a device
- **forget_all** — Remove all abilities from a device (destructive, requires `confirm: true`)
- **build_ability_descriptor** — Build an ability descriptor without deploying
- **export_ability_skill** — Export an ability as a SKILL.md + invoke.sh

### Execution
- **call_remote_tool** — Invoke a learned ability on a remote device
- **call_remote_tool_stream** — Invoke with streaming output
- **execute_command** — One-shot ad-hoc command execution (no learn step)

### Connection Management
- **disconnect_device** — Disconnect a device from the runtime
- **drain_device** — Gracefully drain a device before disconnection

### Packaging
- **package_ability** — Build a full ability package descriptor
- **deploy_ability_package** — Deploy a pre-built package to a device

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `AXON_BIND` | `127.0.0.1:19816` | Axon Runtime gRPC endpoint |
| `AXON_SIGNATURE_BASE64` | (built-in) | Package signing key |
| `DENDRITE_NATIVE_LIB` | (auto-detect) | Path to DendriteBridge native library |
