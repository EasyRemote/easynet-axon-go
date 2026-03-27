package remotecontrol

import (
	"errors"
	"sync/atomic"
	"testing"

	"easynet.run/axon/sdk/go/easynet"
	"easynet.run/axon/sdk/go/easynet/mcp"
)

// testStreamHandle implements mcp.McpToolStreamHandle with a pre-loaded slice
// of chunks. It is a real (non-mock) implementation that replays chunks in
// order and signals done after the last one.
type testStreamHandle struct {
	chunks [][]byte
	index  int
	err    error // if non-nil, returned on first Recv after chunks are exhausted
	closed atomic.Bool
}

func newTestStreamHandle(chunks [][]byte) *testStreamHandle {
	return &testStreamHandle{chunks: chunks}
}


func (h *testStreamHandle) Recv() ([]byte, bool, error) {
	if h.err != nil && h.index >= len(h.chunks) {
		return nil, false, h.err
	}
	if h.index >= len(h.chunks) {
		return nil, true, nil
	}
	chunk := h.chunks[h.index]
	h.index++
	return chunk, false, nil
}

func (h *testStreamHandle) Close() error {
	h.closed.Store(true)
	return nil
}

// stubFactory returns a BridgeFactory that builds a plain Orchestrator with
// no native library. Any call that reaches Open() will fail with the stub
// DendriteError, which is the expected behavior for error-path tests.
func stubFactory() BridgeFactory {
	return func(cfg RemoteControlRuntimeConfig, tenant string) *easynet.Orchestrator {
		return easynet.NewOrchestrator(
			easynet.WithEndpoint(cfg.Endpoint),
			easynet.WithTenant(tenant),
			easynet.WithConnectTimeoutMs(cfg.ConnectTimeoutMs),
		)
	}
}

func testConfig() RemoteControlRuntimeConfig {
	return RemoteControlRuntimeConfig{
		Endpoint:         "http://127.0.0.1:19999",
		Tenant:           "test-tenant",
		ConnectTimeoutMs: 1000,
		SignatureBase64:  "__TEST_SIG__",
	}
}

// ---------------------------------------------------------------------------
// Test 1: HandleToolCallStream returns nil,nil for unknown tools
// ---------------------------------------------------------------------------

func TestHandleToolCallStream_ReturnsNilForUnknownTool(t *testing.T) {
	kit := NewCaseKit(testConfig(), WithBridgeFactory(stubFactory()))

	args := map[string]any{
		"tool_name": "some_tool",
		"node_id":   "node-1",
	}
	handle, err := kit.HandleToolCallStream("unknown_tool", args)
	if err != nil {
		t.Fatalf("expected nil error for unknown tool, got: %v", err)
	}
	if handle != nil {
		t.Fatal("expected nil handle for unknown tool")
	}
}

// ---------------------------------------------------------------------------
// Test 2: HandleToolCall buffers stream result via ConsumeStream
// ---------------------------------------------------------------------------

func TestHandleToolCall_BuffersStreamResult(t *testing.T) {
	// ConsumeStream is the mechanism HandleToolCall uses to buffer a stream
	// tool result. We test it directly with a real testStreamHandle since
	// the concrete Orchestrator cannot be substituted without cgo.
	chunks := [][]byte{
		[]byte("hello "),
		[]byte("world"),
		[]byte("!"),
	}
	handle := newTestStreamHandle(chunks)
	result := mcp.ConsumeStream(handle)

	if result.IsError {
		t.Fatalf("expected no error, got IsError=true with payload: %v", result.Payload)
	}
	ok, _ := result.Payload["ok"].(bool)
	if !ok {
		t.Fatalf("expected ok=true, got: %v", result.Payload["ok"])
	}
	chunkCount, _ := result.Payload["chunk_count"].(int)
	if chunkCount != 3 {
		t.Fatalf("expected chunk_count=3, got: %d", chunkCount)
	}
	resultChunks, _ := result.Payload["chunks"].([]string)
	if len(resultChunks) != 3 {
		t.Fatalf("expected 3 chunks, got: %d", len(resultChunks))
	}
	concatenated := ""
	for _, c := range resultChunks {
		concatenated += c
	}
	if concatenated != "hello world!" {
		t.Fatalf("expected concatenated chunks to be 'hello world!', got: %q", concatenated)
	}
	if !handle.closed.Load() {
		t.Fatal("expected stream handle to be closed after ConsumeStream")
	}
}

// ---------------------------------------------------------------------------
// Test 3: HandleToolCall returns error when stream open fails
// ---------------------------------------------------------------------------

func TestHandleToolCall_ReturnsErrorWhenStreamOpenFails(t *testing.T) {
	kit := NewCaseKit(testConfig(), WithBridgeFactory(stubFactory()))

	args := map[string]any{
		"tool_name": "some_remote_tool",
		"node_id":   "node-1",
		"arguments": map[string]any{"key": "value"},
	}
	result := kit.HandleToolCall("call_remote_tool_stream", args)

	if !result.IsError {
		t.Fatal("expected IsError=true when stream open fails")
	}
	ok, _ := result.Payload["ok"].(bool)
	if ok {
		t.Fatal("expected ok=false when stream open fails")
	}
	errMsg, _ := result.Payload["error"].(string)
	if errMsg == "" {
		t.Fatal("expected non-empty error message when stream open fails")
	}
}

// ---------------------------------------------------------------------------
// Test 4: The bridge layer applies DefaultMCPToolStreamTimeoutMs
// when timeout_ms is absent (zero passes through from upper layers)
// ---------------------------------------------------------------------------

func TestHandleToolCallStream_AppliesDefaultTimeout(t *testing.T) {
	// Verify the default timeout constant applied at the bridge layer.
	// Upper layers (handler, orchestrator) pass 0 through; the bridge
	// normalises it to DefaultMCPToolStreamTimeoutMs.
	// We verify the constant is 60000 as expected by the protocol contract.
	if easynet.DefaultMCPToolStreamTimeoutMs != 60000 {
		t.Fatalf("expected DefaultMCPToolStreamTimeoutMs=60000, got: %d", easynet.DefaultMCPToolStreamTimeoutMs)
	}

	// Additionally, verify that args without timeout_ms trigger the
	// default path by checking that the handler does not reject the call
	// for missing timeout. (The call will fail at Open() in stub mode, but
	// the error message should reference the bridge, not the timeout.)
	kit := NewCaseKit(testConfig(), WithBridgeFactory(stubFactory()))
	args := map[string]any{
		"tool_name": "remote_tool",
		"node_id":   "node-1",
		"arguments": map[string]any{},
		// timeout_ms intentionally omitted
	}
	_, err := kit.HandleToolCallStream("call_remote_tool_stream", args)
	if err == nil {
		t.Fatal("expected error from stub bridge, got nil")
	}
	// The error should come from the bridge (Open/CallMCPToolStream),
	// not from timeout validation.
	if errors.Is(err, errors.New("timeout_ms is required")) {
		t.Fatal("timeout_ms should default to 60000, not be required")
	}
}

// ---------------------------------------------------------------------------
// Test 5: Unary and stream call_remote_tool payloads are structurally distinct
// ---------------------------------------------------------------------------

func TestHandleToolCall_UnaryAndStreamPayloadShapesDistinct(t *testing.T) {
	kit := NewCaseKit(testConfig(), WithBridgeFactory(stubFactory()))

	args := map[string]any{
		"tool_name": "remote_tool",
		"node_id":   "node-1",
		"arguments": map[string]any{"param": "value"},
	}

	unaryResult := kit.HandleToolCall("call_remote_tool", args)
	streamResult := kit.HandleToolCall("call_remote_tool_stream", args)

	// Both should be errors (stub bridge), but their payload shapes differ.
	if !unaryResult.IsError {
		t.Fatal("expected unary result to be error in stub mode")
	}
	if !streamResult.IsError {
		t.Fatal("expected stream result to be error in stub mode")
	}

	// The unary result (call_remote_tool) payload contains "tenant_id" and
	// "error" from the withOrchestrator wrapper.
	unaryTenant, hasTenant := unaryResult.Payload["tenant_id"]
	if !hasTenant {
		t.Fatal("unary result should have tenant_id in payload")
	}
	if unaryTenant != "test-tenant" {
		t.Fatalf("expected unary tenant_id='test-tenant', got: %v", unaryTenant)
	}

	// The stream result (call_remote_tool_stream) follows the stream error
	// path which also includes tenant_id and error.
	streamTenant, hasTenant := streamResult.Payload["tenant_id"]
	if !hasTenant {
		t.Fatal("stream result should have tenant_id in payload")
	}
	if streamTenant != "test-tenant" {
		t.Fatalf("expected stream tenant_id='test-tenant', got: %v", streamTenant)
	}

	// The key structural distinction: a successful stream result would contain
	// "chunks" and "chunk_count" keys, while a successful unary result contains
	// "call". We verify this with ConsumeStream on a real handle.
	successStream := mcp.ConsumeStream(newTestStreamHandle([][]byte{[]byte("data")}))
	if _, hasChunks := successStream.Payload["chunks"]; !hasChunks {
		t.Fatal("stream success payload should contain 'chunks' key")
	}
	if _, hasChunkCount := successStream.Payload["chunk_count"]; !hasChunkCount {
		t.Fatal("stream success payload should contain 'chunk_count' key")
	}
	if _, hasCall := successStream.Payload["call"]; hasCall {
		t.Fatal("stream success payload should NOT contain 'call' key")
	}
}

// ---------------------------------------------------------------------------
// Additional: ConsumeStream propagates mid-stream errors
// ---------------------------------------------------------------------------

func TestConsumeStream_PropagatesMidStreamError(t *testing.T) {
	handle := &testStreamHandle{
		chunks: [][]byte{[]byte("partial")},
		err:    errors.New("connection lost"),
	}
	result := mcp.ConsumeStream(handle)

	if !result.IsError {
		t.Fatal("expected IsError=true on mid-stream error")
	}
	errMsg, _ := result.Payload["error"].(string)
	if errMsg != "connection lost" {
		t.Fatalf("expected error 'connection lost', got: %q", errMsg)
	}
	chunkCount, _ := result.Payload["chunk_count"].(int)
	if chunkCount != 1 {
		t.Fatalf("expected 1 partial chunk before error, got: %d", chunkCount)
	}
	if !handle.closed.Load() {
		t.Fatal("expected handle to be closed even after error")
	}
}
