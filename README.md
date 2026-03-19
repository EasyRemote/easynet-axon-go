<p align="center">
  <a href="https://github.com/EasyRemote"><img src="https://avatars.githubusercontent.com/u/213722898?s=200&v=4" width="200" height="200" alt="EasyRemote"></a>
</p>

<h1 align="center">sdk-go</h1>

<p align="center">
  Go SDK for <strong>EasyNet Axon</strong> — the Capability Control Plane for agent-native distributed execution.
</p>

<p align="center">
  <a href='https://github.com/EasyRemote/EasyNet-Axon'><img src='https://img.shields.io/badge/EasyNet-Axon-00d9ff?style=for-the-badge&labelColor=0f172a'></a>
  <a href='https://github.com/EasyRemote/EasyNet-Axon/blob/main/LICENSE'><img src='https://img.shields.io/badge/License-Apache_2.0-f97316?style=for-the-badge&labelColor=0f172a'></a>
</p>
<p align="center">
  <img src='https://img.shields.io/badge/Go-1.22+-00add8?style=for-the-badge&logo=go&logoColor=white&labelColor=0f172a'>
  <img src='https://img.shields.io/badge/Platform-macOS_|_Linux_|_Windows-22c55e?style=for-the-badge&labelColor=0f172a'>
</p>

---

## What is this?

This is the Go surface for **Axon**, a protocol-level control plane that treats agent capabilities as first-class network objects. Every ability carries its own schema, trust posture, scheduling contract, and tenant isolation rules — enforced atomically at invocation time.

Axon collapses tenant isolation, rate limiting, policy evaluation, node selection, concurrency admission, and circuit breaking into a **single atomic decision** against the same state snapshot. No race window between policy check and routing.

The SDK ships a native Dendrite bridge binary (loaded via cgo + `dlopen`/`dlsym`) that provides protocol-complete Axon gRPC calls without gRPC codegen in Go.

## Install

```bash
go get easynet.run/axon/sdk/go
```

## Quick Start

### Expose an ability

```go
easynet.Ability("easynet:///r/org/reg/agent.quote-bot/abilities/order.quote@1?tenant_id=tenant-test").
  Handle(func(ctx context.Context, in easynet.Payload) (easynet.Payload, error) {
    qty := 1
    if v, ok := in["qty"].(int); ok {
      qty = v
    }
    return easynet.Payload{"sku": in["sku"], "price": 19.9 * float64(qty)}, nil
  }).
  Expose()
```

### Invoke an ability

```go
res, _ := easynet.NewClient(nil).
  Tenant("tenant-test").
  Ability("easynet:///r/org/reg/agent.quote-bot/abilities/order.quote@1?tenant_id=tenant-test").
  Call(ctx, easynet.Payload{"sku": "A1", "qty": 2})
```

Use `CallAny()` or `CallRaw()` for stream/media-oriented non-object payloads.

### Bootstrap a local runtime behind NAT

```go
srv, err := easynet.StartServerWithOptions(easynet.StartServerOptions{
  Hub:       "axon://hub.easynet.run:50084",
  HubTenant: "tenant-test",
  HubLabel:  "alice-macbook",
})
defer srv.Stop()
```

No public IP required — the local runtime connects outbound to the Hub.

## Capabilities

### Core Protocol

- **Fluent builders** — `Tenant()` → `Principal()` → `Ability()` → `Call()` are immutable, chainable, and context-aware.
- **Native Dendrite bridge** — `DendriteBridge` loads the platform C ABI via cgo; all Axon gRPC shapes (unary, server-stream, client-stream, bidi-stream) available without gRPC codegen.
- **Semantic builder** — `SemanticBridge` for catalog-aware invocation with functional options (`WithAbilityTimeoutMs`, `WithMetadata`).
- **Subject binding** — `Principal(...)` scopes invocation to a subject identity with automatic URI visibility mapping.

### Ability Lifecycle

Full lifecycle management — not just invocation:

- `CreateAbility()` / `ExportAbility()` — define and register abilities with schemas
- `DeployToNode()` — install + activate on target nodes
- `ListAbilities()` / `InvokeAbility()` / `UninstallAbility()`
- `DiscoverNodes()` / `ExecuteCommand()` / `DisconnectDevice()` / `DrainDevice()`

### MCP & A2A Protocols

- **MCP server** — `StdioMcpServer` hosts JSON-RPC 2.0 tool endpoints over stdio.
- **MCP operations** — deploy, list, call, and update MCP tools on remote nodes.
- **A2A agent protocol** — `ListA2aAgents()`, `SendA2aTask()` for inter-agent discovery and task dispatch.
- **Tool adapter** — convert abilities to OpenAI/Anthropic tool definitions.

### Voice & Media Signaling

First-class voice call lifecycle and transport negotiation (cgo build-tagged):

- Call management: create, join, leave, end, watch events, report metrics
- Transport sessions: create, set description, add ICE candidates, refresh lease
- Media path updates

### Orchestration

High-level capability management for server-side workflows:

```go
orch := easynet.NewOrchestrator(
  easynet.WithEndpoint("http://127.0.0.1:50051"),
  easynet.WithTenant("tenant-test"),
)
defer orch.Close()

node, _ := orch.SelectNode("", "")
lifecycle, _ := orch.PublishInstallActivate(
  node["node_id"].(string), packageRef, capName, bundle, execPolicy,
)
```

### Federation

- `StartServerWithOptions()` spawns a local Axon runtime and joins the Hub — all traffic is outbound.
- Federated node discovery and cross-network invocation dispatch.

## License

Apache-2.0 — see [LICENSE](https://github.com/EasyRemote/EasyNet-Axon/blob/main/LICENSE).

## Author

[Silan Hu](https://github.com/Qingbolan) · [silan.hu@u.nus.edu](mailto:silan.hu@u.nus.edu)
