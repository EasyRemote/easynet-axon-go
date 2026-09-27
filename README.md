# Axon Go SDK

The Go SDK implements Axon's canonical runtime model. It provides generic
descriptor-bound invocation, provider binding, typed content and bidirectional
sessions, lifecycle state, and signed execution receipts.

Application packages own their catalogs, policy, provider configuration, and
domain lifecycle. Those concerns are not part of this module.

## Native signed-call support boundary

The current native bridge accepts transport-only session configuration and
requires a complete externally signed invocation on each call. It does not
accept signing seeds, sign invocations, or generate invocation nonces.

Go's existing `OpenSignedClient` sends a `signing` field and is rejected by
current native artifacts. `InvokeAbilitySigned` / `SignedInvokeRequest` omit the
complete caller/signature envelope and are not a supported signed native path.
Use `DendriteBridge.InvokeDescriptorBound(handle, request, options)` with a
canonical `invocation.DescriptorBoundInvocationRequest` and a transport-only
`OpenClient` session. The request supplies the caller-owned signature and nonce;
the bridge does not create authority. `DescriptorBoundNativeOptions` requires
`RequestID` and `ContentType`; zero `TimeoutMs` uses 30 seconds, negative values
are rejected. The unary path rejects stream/bidi requests, local parent or
supervisor controls, and payload/digest mismatches. Without CGO it returns an
explicit unsupported error.

The returned native JSON and `DendriteError.InvocationResponseJSON` on failure
are unverified evidence. Verify receipts using the expected signing authority
before trusting them. Projection and captured-FFI tests cover this carrier. The explicit Rust
`go_native_execution` integration launches an external compiled Go consumer
against the shared canonical runtime: invalid signature and replay are denied;
success and business-failure admission/terminal receipts verify using a fixed
public key, and tampered receipts fail verification. The Go probe is under
`axon/invocation/testdata/native-execution/`. This is a local candidate test with
fixture identities and policy, not a production trust deployment or full-chain
verification.

## Install

```bash
go get axon.run/sdk/go/axon
```

The neutral packages are:

- `axon.run/sdk/go/axon`
- `axon.run/sdk/go/axon/invocation`

## Runtime Model

The local execution boundary has four explicit parts:

- `AbilityDescriptor`: immutable descriptor identity and schema evidence.
- `AbilityImpl`: immutable executable evidence.
- `ProviderBinding`: atomic descriptor, implementation, and handler binding.
- `LocalRuntime`: descriptor-bound admission, execution lifecycle, and receipt
  publication.

There is no process-local facade registry. Providers bind directly to the
runtime that owns admission and execution.

```go
descriptor, err := invocation.NewAbilityDescriptor(
	descriptorRef,
	invocation.Sha256([]byte("canonical input schema")),
)
if err != nil {
	return err
}

implementation, err := invocation.NewAbilityImpl(
	descriptorRef,
	invocation.Sha256(implementationBytes),
	"go-runtime-v1",
)
if err != nil {
	return err
}

binding, err := invocation.NewProviderBinding(
	descriptor,
	implementation,
	func(
		ctx context.Context,
		ability *invocation.AbilityContext,
	) ([]byte, *invocation.AxonError) {
		return handle(ctx, ability.Payload)
	},
)
if err != nil {
	return err
}
if axonErr := runtime.BindProvider(binding); axonErr != nil {
	return axonErr
}
```

`DescriptorBoundInvocationRequest` is the only public `LocalRuntime` admission
input. The caller signs the request; the runtime verifies it before execution.

```go
request, err := invocation.SignDescriptorBoundInvocationRequest(
	invocation.CallModeRPC,
	envelope,
	callerKey,
	payload,
	"caller-key-v1",
)
if err != nil {
	return err
}

handle, accepted, axonErr := runtime.InvokeDescriptorBoundRequest(ctx, request)
if axonErr != nil {
	return axonErr
}
_ = accepted
handle.Wait(ctx)
```

## Canonical Lifecycle

`LocalRuntime` owns one explicit invocation state machine:

```text
UNSPECIFIED -> ACCEPTED -> ADMITTED -> DISPATCHED -> RUNNING
RUNNING -> COMPLETED | FAILED | TIMED_OUT | CANCELLED
```

Runtime-wide root and child admission is bounded at 256 active invocations.
Output and inbound message buffers are bounded at 64 frames. Parent
cancellation and deadlines commit child terminal evidence before the parent
terminal receipt, and every terminal receipt crosses the supervisor cleanup
barrier.

Inspect provider-owned limits and live state through:

```go
limits := runtime.LifecycleLimits()
snapshot := handle.LifecycleSnapshot()
```

Durable continuation is opt-in and provider-backed. Bind a generic
`RecoveryContinuation`, construct the runtime with `LocalRuntimeOptions`, and
checkpoint only through `AbilityContext.CheckpointRecovery`. A replacement
runtime leases suspended records and resumes the same invocation identity:

```go
continuation, err := invocation.NewRecoveryContinuation(resume, initialCheckpoint)
if err != nil {
	return err
}
binding, err = binding.WithRecovery(continuation)
if err != nil {
	return err
}

runtime, axonErr := invocation.NewLocalRuntimeWithOptions(
	keyResolver,
	receiptProvider,
	invocation.LocalRuntimeOptions{Persistence: persistentLog},
)
```

## Receipts

`CanonicalReceiptProvider` owns receipt construction, signing,
self-verification, and append publication. The runtime accepts a downstream
admission-policy verifier and receipt-signing authority resolver; it does not
generate production authority.

```go
provider, err := invocation.NewDefaultCanonicalReceiptProvider(
	admissionPolicyVerifier,
	invocation.ReceiptSigningAuthorityResolverFunc(func(
		callee invocation.AgentIdentity,
	) (invocation.ReceiptSigningAuthority, error) {
		return invocation.NewEd25519ReceiptSigningAuthority(
			callee,
			calleeKey,
			"runtime-key-v1",
		)
	}),
)
if err != nil {
	return err
}

runtime, axonErr := invocation.NewLocalRuntime(keyResolver, provider)
if axonErr != nil {
	return axonErr
}
```

Published `SignedInvocationReceipt` values support:

- `Verify`: signature and canonical-byte integrity.
- `Trace`: direct causal placement.
- `ProveAuthority`: admitted authority evidence.

Use `ParseInvocationReceiptJSON` for wire input. It returns an
`UnverifiedReceipt` that must be verified before it can become a trusted
receipt.

The complete flow is in
[`examples/authority_receipt/main.go`](examples/authority_receipt/main.go).

## Content And Sessions

`ContentEnvelope` carries typed invocation content. `BidiSession`,
`StreamDescriptor`, `BinaryChunk`, and session authority contracts provide the
generic full-duplex runtime surface. Applications define domain codecs and
session policy downstream.

## Native Bridge

`DendriteBridge` loads the Axon native bridge through cgo. Configure resolution
with:

- `AXON_DENDRITE_BRIDGE_LIB`
- `AXON_DENDRITE_BRIDGE_HOME`
- `AXON_DENDRITE_BRIDGE_PLATFORM`
- `AXON_DENDRITE_BRIDGE_SOURCE=local`
- `AXON_DENDRITE_BRIDGE_DEBUG=1`
- `SDK_VERSION`
- `SDK_TARGET`

The bridge exposes generic unary, server-streaming, client-streaming,
bidirectional-streaming, protocol catalog, and descriptor-bound invocation
operations.

For HTTPS, configure the deployment's public CA bundle explicitly before using
`SidecarTransport`:

```go
caBytes, err := os.ReadFile("deployment-ca.pem")
if err != nil {
    return err
}
caPEM := string(caBytes)
transport := &axon.SidecarTransport{
    Endpoint: "https://runtime.example.org:50051",
    TLSCAPEM: &caPEM,
}
defer transport.Close()
```

Direct bridge users can pass `DendriteClientOptions` to `OpenClientWithOptions`.
`nil` omits the CA field; explicit empty or invalid values reach native validation
and fail. First connection and automatic reconnect use the same options. Do not
mutate transport configuration while it is in use. The native bridge must contain
source change `5f739034`; this does not assert published-package availability.
The native layer checks the endpoint hostname and CA certificates, with no
plaintext retry or ambient trust-store fallback. Transport TLS trust is separate
from Invocation authority and receipt verification.

## Local Runtime Process

`StartServerWithOptions` connects to an existing runtime or starts a local
`axon-runtime` process. Use `AXON_ENDPOINT` for an existing endpoint and
`AXON_RUNTIME_BIN` for an explicit local binary.

```go
server, err := axon.StartServerWithOptions(axon.StartServerOptions{
	Endpoint: os.Getenv("AXON_ENDPOINT"),
})
if err != nil {
	return err
}
defer server.Stop()
```

## Verification

From this directory:

```bash
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
```

## License

Apache-2.0. See the repository `LICENSE`.


### Testing an installed module candidate

Canonical conformance vectors are included under
`axon/invocation/testdata/conformance/`. An extracted module can run `go test ./...`
without a sibling checkout or external fixture directory. Source updates use
`scripts/checks/sync_sdk_test_fixtures.py --write` from the repository root;
CI and packaging require its read-only `--check` to pass. The generator maintains
Rust and Go copies from one canonical inventory and rolls back a partial update.

Set `AXON_VERIFY_BIN` to an independently built Rust `axon-verify` executable to
run the accepted-bundle and tampered-bundle cross-language checks. Without it,
those two integration tests report skips.

## Event reader ownership

A stream returned by `InvocationHandle.Events(offset)` has one cursor owner.
`Close` is idempotent and may run concurrently with a pending `Next`; it wakes
that read without cancelling the provider. A private closed channel joins the
core event wait and caller context, so no per-read goroutine is needed. Keep
`Next` and `CurrentOffset` on the cursor-owning goroutine; concurrent reads of a
single iterator are not promised. Do not copy an EventStream value after use.

The race-tested `TestEventStreamCloseWakesReadWithoutCancellingProvider` checks
16 concurrent close callers, a pending read, provider independence and reader
goroutine cleanup. This proves local source behavior, not an installed module,
network reconnect or process-crash recovery.


## Convenience transport support boundary

The existing `SidecarTransport` ability/payload convenience path still uses an
incomplete signed helper and session signing configuration. It is not a
supported native signed-call path with current artifacts. Use the complete
canonical-request bridge entrypoint described above. A caller must explicitly
bind the executor and subject; an ability URI does not supply executor identity.
See the [native entrypoint evidence matrix](../conformance/native-entrypoint-evidence.md)
for tested entrypoints, source call paths and the remaining migration criteria.

### Forwarding complete signed requests

`Client.InvokeDescriptorBound(ctx, request, options)` and
`SidecarTransport.InvokeDescriptorBound` accept the canonical invocation request
through `DescriptorBoundForwardingRequest`. Configure the SidecarTransport with
endpoint/library/TLS options and no `Signing`. The caller constructs and signs
its explicit callee, subject, nonce and causal context before forwarding.

```go
sidecar := &axon.SidecarTransport{Endpoint: endpoint, LibraryPath: libraryPath}
defer sidecar.Close()
response, err := axon.NewClient(sidecar).InvokeDescriptorBound(ctx, request,
    axon.DescriptorBoundNativeOptions{
        RequestID: "document-review", ContentType: "application/octet-stream",
        TimeoutMs: 3000,
    })
```

The bound `Ability(...)`, if present, must match the signed request. A configured
`Principal(...)` is rejected because it cannot override the signed subject.
Custom transports implement the optional `DescriptorBoundTransport` interface.
The method returns the complete native response and preserves native error
receipt evidence for independent verification; it does not retry invocations.
Context deadlines cap the native timeout at dispatch. Cancellation after dispatch
cannot interrupt the synchronous C call; returned business evidence is retained.
Close waits for an active forwarding call before unloading its native library.
Existing `Call`/`CallRaw` ability/payload helpers retain the unsupported boundary
above. Streaming, automatic reconnection and deployment trust need their own
acceptance evidence.

### Complete signed server-stream transport

`DendriteBridge.StreamDescriptorBound(handle, request, options)` accepts the
caller-owned `DescriptorBoundNativeRequest` interface used by unary, with
`DescriptorBoundNativeStreamOptions` (embedded unary options, `ChunkTimeoutMs`,
`ChunkBufferSize`). Zero uses finite defaults; negative stream options are
rejected before FFI. The returned handle is consumed by `StreamNext` and must
be disposed with `StreamClose`, including after early exit. Open response
facts are preserved in `ServerStreamOpenResult.RawPayload`. Construct the canonical
request with `invocation.CallModeStream`; unary requires `CallModeRPC`. The
transport rejects missing or mismatched modes before FFI instead of changing
the request mode. Mode selection does not change the signed seven-tuple bytes.

CGO capture tests verify projection and failure evidence. Without CGO the
method explicitly reports unsupported. The external Go probe also executes
Stream requests through a manifest-pinned native bridge against canonical
LocalRuntime, returning raw chunks to an independently pinned Rust receipt
verifier. Success, business failure, wrong signature and replay are covered.
The isolated local archive passed alongside unary bridge/Client regressions;
coordinated candidate stream acceptance remains pending. Raw protobuf chunks
and endpoint receipts do not themselves prove a complete stream transcript.


### Signed bidi cleanup

Always defer `BidiStream.Dispose()` after opening a signed bidi session.
`Close()` and `SendEOF()` half-close sending and permit further receiving;
`Dispose()` performs final native cleanup, including after an early exit or error.
A terminal receipt is returned once with `Terminal == true`; later reads return
`BidiFrameDone` without accessing the released native handle. Receipt projection
and lifecycle tests do not establish a complete native signed bidi entrypoint;
the existing ability convenience opener remains subject to the support boundary
at the beginning of this document.


### Complete signed bidi transport

`DendriteBridge.BidiDescriptorBound(handle, request, options)` accepts a canonical
request constructed with `invocation.CallModeBidi` and returns `BidiStream` plus
`BidiOpenResult`. Defer `Dispose`; use `Send`/`SendEOF` and `Recv` for duplex flow.
`DescriptorBoundNativeBidiOptions` embeds server-stream options and adds
`RequestBufferSize` and `Streams`. Both buffer capacities default to 64; negative
bounds, wrong mode, empty stream content type and invalid ordering reject before
native allocation. Caller signature and binary opening payload remain unchanged.
CGO capture and canonical signature tests cover this carrier. An external Go
consumer from an isolated local archive also passes real native bidi execution
against canonical LocalRuntime, including independent receipt verification.
Coordinated candidate bidi acceptance remains pending. Without CGO it explicitly
reports unsupported.
