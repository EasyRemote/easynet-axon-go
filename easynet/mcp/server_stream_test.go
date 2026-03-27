package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"easynet.run/axon/sdk/go/easynet"
)

// ---------------------------------------------------------------------------
// Test implementations of McpToolStreamHandle for providing controlled input
// ---------------------------------------------------------------------------

// inMemoryStreamHandle replays a fixed sequence of chunks and then signals done.
type inMemoryStreamHandle struct {
	chunks  [][]byte
	index   int
	closed  atomic.Bool
	closeErr error
}

func (m *inMemoryStreamHandle) Recv() ([]byte, bool, error) {
	if m.index >= len(m.chunks) {
		return nil, true, nil
	}
	chunk := m.chunks[m.index]
	m.index++
	return chunk, false, nil
}

func (m *inMemoryStreamHandle) Close() error {
	m.closed.Store(true)
	return m.closeErr
}

// errorStreamHandle returns an error after delivering a configurable number of
// chunks.
type errorStreamHandle struct {
	chunks       [][]byte
	index        int
	closed       atomic.Bool
	errOnRecv    error
	errAfterN    int // deliver this many chunks before returning the error
}

func (e *errorStreamHandle) Recv() ([]byte, bool, error) {
	if e.index >= e.errAfterN {
		return nil, false, e.errOnRecv
	}
	chunk := e.chunks[e.index]
	e.index++
	return chunk, false, nil
}

func (e *errorStreamHandle) Close() error {
	e.closed.Store(true)
	return nil
}

// largeChunkStreamHandle produces chunks of a given size, up to a limit.
type largeChunkStreamHandle struct {
	chunkSize  int
	remaining  int
	closed     atomic.Bool
}

func (l *largeChunkStreamHandle) Recv() ([]byte, bool, error) {
	if l.remaining <= 0 {
		return nil, true, nil
	}
	size := l.chunkSize
	if size > l.remaining {
		size = l.remaining
	}
	l.remaining -= size
	return bytes.Repeat([]byte("x"), size), false, nil
}

func (l *largeChunkStreamHandle) Close() error {
	l.closed.Store(true)
	return nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestConsumeStream_CollectsChunks(t *testing.T) {
	handle := &inMemoryStreamHandle{
		chunks: [][]byte{
			[]byte("hello"),
			[]byte(" "),
			[]byte("world"),
		},
	}

	result := ConsumeStream(handle)

	if result.IsError {
		t.Fatalf("expected IsError=false, got true; payload=%v", result.Payload)
	}
	if ok, _ := result.Payload["ok"].(bool); !ok {
		t.Fatal("expected ok=true")
	}

	chunkCount, _ := result.Payload["chunk_count"].(int)
	if chunkCount != 3 {
		t.Fatalf("expected chunk_count=3, got %d", chunkCount)
	}

	chunks, _ := result.Payload["chunks"].([]string)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	joined := strings.Join(chunks, "")
	if joined != "hello world" {
		t.Fatalf("expected concatenated chunks to be 'hello world', got %q", joined)
	}

	if !handle.closed.Load() {
		t.Fatal("expected handle to be closed")
	}
}

func TestConsumeStream_EmptyStream(t *testing.T) {
	handle := &inMemoryStreamHandle{chunks: nil}

	result := ConsumeStream(handle)

	if result.IsError {
		t.Fatalf("expected IsError=false, got true; payload=%v", result.Payload)
	}
	if ok, _ := result.Payload["ok"].(bool); !ok {
		t.Fatal("expected ok=true")
	}

	chunkCount, _ := result.Payload["chunk_count"].(int)
	if chunkCount != 0 {
		t.Fatalf("expected chunk_count=0, got %d", chunkCount)
	}

	chunks, _ := result.Payload["chunks"].([]string)
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}

	if !handle.closed.Load() {
		t.Fatal("expected handle to be closed")
	}
}

func TestConsumeStream_HandlesError(t *testing.T) {
	recvErr := errors.New("network failure")
	handle := &errorStreamHandle{
		chunks:    [][]byte{[]byte("partial")},
		errOnRecv: recvErr,
		errAfterN: 1, // deliver one chunk, then fail
	}

	result := ConsumeStream(handle)

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if ok, _ := result.Payload["ok"].(bool); ok {
		t.Fatal("expected ok=false")
	}

	errMsg, _ := result.Payload["error"].(string)
	if errMsg != "network failure" {
		t.Fatalf("expected error='network failure', got %q", errMsg)
	}

	chunkCount, _ := result.Payload["chunk_count"].(int)
	if chunkCount != 1 {
		t.Fatalf("expected chunk_count=1 (partial delivery), got %d", chunkCount)
	}

	chunks, _ := result.Payload["chunks"].([]string)
	if len(chunks) != 1 || chunks[0] != "partial" {
		t.Fatalf("expected chunks=['partial'], got %v", chunks)
	}

	if !handle.closed.Load() {
		t.Fatal("expected handle to be closed after error")
	}
}

func TestConsumeStream_EnforcesBufferLimit(t *testing.T) {
	// Produce just over 64 MiB to trigger the limit.
	totalBytes := DefaultMaxStreamBytes + 1
	chunkSize := 1024 * 1024 // 1 MiB per chunk

	handle := &largeChunkStreamHandle{
		chunkSize: chunkSize,
		remaining: totalBytes,
	}

	result := ConsumeStream(handle)

	if !result.IsError {
		t.Fatal("expected IsError=true when buffer limit exceeded")
	}
	if ok, _ := result.Payload["ok"].(bool); ok {
		t.Fatal("expected ok=false")
	}

	errMsg, _ := result.Payload["error"].(string)
	if !strings.Contains(errMsg, "64 MiB") {
		t.Fatalf("expected error to mention '64 MiB', got %q", errMsg)
	}

	if !handle.closed.Load() {
		t.Fatal("expected handle to be closed after buffer limit exceeded")
	}
}

func TestConsumeStream_AlwaysCloses(t *testing.T) {
	t.Run("closed on success", func(t *testing.T) {
		h := &inMemoryStreamHandle{chunks: [][]byte{[]byte("ok")}}
		ConsumeStream(h)
		if !h.closed.Load() {
			t.Fatal("expected handle to be closed on success path")
		}
	})

	t.Run("closed on recv error", func(t *testing.T) {
		h := &errorStreamHandle{
			chunks:    nil,
			errOnRecv: errors.New("boom"),
			errAfterN: 0,
		}
		ConsumeStream(h)
		if !h.closed.Load() {
			t.Fatal("expected handle to be closed on error path")
		}
	})

	t.Run("closed on buffer overflow", func(t *testing.T) {
		h := &largeChunkStreamHandle{
			chunkSize: DefaultMaxStreamBytes + 1,
			remaining: DefaultMaxStreamBytes + 1,
		}
		ConsumeStream(h)
		if !h.closed.Load() {
			t.Fatal("expected handle to be closed on buffer overflow path")
		}
	})

	t.Run("closed even when Close returns error", func(t *testing.T) {
		h := &inMemoryStreamHandle{
			chunks:   [][]byte{[]byte("data")},
			closeErr: errors.New("close failed"),
		}
		result := ConsumeStream(h)
		if !h.closed.Load() {
			t.Fatal("expected handle to be closed despite Close error")
		}
		// The result should still be successful because the stream itself completed fine.
		if result.IsError {
			t.Fatal("expected IsError=false; Close error should be suppressed")
		}
	})
}

// ---------------------------------------------------------------------------
// McpToolStreamProvider fallback test
// ---------------------------------------------------------------------------

// stubMcpToolProvider implements McpToolProvider and optionally McpToolStreamProvider.
type stubMcpToolProvider struct {
	specs       []map[string]any
	unaryResult McpToolResult
	// When streamHandler is non-nil, the provider also implements McpToolStreamProvider.
	streamHandler func(name string, args map[string]any) (McpToolStreamHandle, error)
}

func (s *stubMcpToolProvider) ToolSpecs() []map[string]any { return s.specs }

func (s *stubMcpToolProvider) HandleToolCall(_ string, _ map[string]any) McpToolResult {
	return s.unaryResult
}

// streamableStubProvider embeds stubMcpToolProvider and adds McpToolStreamProvider.
type streamableStubProvider struct {
	stubMcpToolProvider
}

func (s *streamableStubProvider) HandleToolCallStream(name string, args map[string]any) (McpToolStreamHandle, error) {
	return s.streamHandler(name, args)
}

func TestMcpToolStreamProvider_FallbackToUnary(t *testing.T) {
	unaryPayload := map[string]any{
		"ok":     true,
		"source": "unary",
	}

	provider := &streamableStubProvider{
		stubMcpToolProvider: stubMcpToolProvider{
			specs: []map[string]any{
				{"name": "my_tool", "description": "test tool", "inputSchema": map[string]any{"type": "object"}},
			},
			unaryResult: McpToolResult{Payload: unaryPayload, IsError: false},
		},
	}
	// Stream handler returns nil handle to signal "no stream available, fall back".
	provider.streamHandler = func(_ string, _ map[string]any) (McpToolStreamHandle, error) {
		return nil, nil
	}

	server := NewStdioMcpServer(provider)

	// Build a tools/call JSON-RPC request.
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "my_tool",
			"arguments": map[string]any{},
		},
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	var out bytes.Buffer
	if err := server.Run(bytes.NewReader(reqBytes), &out); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse the JSON-RPC response.
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v\nraw: %s", err, out.String())
	}

	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("expected result in response, got: %v", resp)
	}

	// The content should contain the unary payload, confirming fallback happened.
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected non-empty content array")
	}
	firstItem, _ := content[0].(map[string]any)
	text, _ := firstItem["text"].(string)

	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("failed to decode content text: %v", err)
	}

	if source, _ := decoded["source"].(string); source != "unary" {
		t.Fatalf("expected source='unary' (fallback path), got %q", source)
	}
	if _, hasError := result["isError"]; hasError {
		t.Fatal("expected no isError key on successful unary fallback")
	}
}

// ---------------------------------------------------------------------------
// Additional edge-case tests
// ---------------------------------------------------------------------------

func TestConsumeStream_IdempotentClose(t *testing.T) {
	handle := &inMemoryStreamHandle{
		chunks: [][]byte{[]byte("data")},
	}

	// ConsumeStream calls Close internally.
	result := ConsumeStream(handle)
	if result.IsError {
		t.Fatalf("expected IsError=false, got true; payload=%v", result.Payload)
	}
	if !handle.closed.Load() {
		t.Fatal("expected handle to be closed after ConsumeStream")
	}

	// Call Close a second time — must not panic.
	err := handle.Close()
	if err != nil {
		t.Fatalf("expected no error on second Close, got %v", err)
	}
	if !handle.closed.Load() {
		t.Fatal("expected closed to remain true after second Close")
	}
}

func TestConsumeStream_LossyUTF8(t *testing.T) {
	// Go's string([]byte{0xff, 0xfe, 0x41}) produces a string containing
	// invalid UTF-8 bytes. ConsumeStream does string(chunk) without validation,
	// so the result should still succeed with the raw bytes preserved.
	invalidUTF8 := []byte{0xff, 0xfe, 0x41}
	handle := &inMemoryStreamHandle{
		chunks: [][]byte{invalidUTF8},
	}

	result := ConsumeStream(handle)

	if result.IsError {
		t.Fatalf("expected IsError=false, got true; payload=%v", result.Payload)
	}
	if ok, _ := result.Payload["ok"].(bool); !ok {
		t.Fatal("expected ok=true")
	}

	chunks, _ := result.Payload["chunks"].([]string)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	// Verify the raw bytes are preserved in the Go string, even if invalid UTF-8.
	got := []byte(chunks[0])
	if len(got) != len(invalidUTF8) {
		t.Fatalf("expected chunk length %d, got %d", len(invalidUTF8), len(got))
	}
	for i, b := range invalidUTF8 {
		if got[i] != b {
			t.Fatalf("byte %d: expected 0x%02x, got 0x%02x", i, b, got[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Stream handler priority and cleanup tests
// ---------------------------------------------------------------------------

func TestMcpToolStreamProvider_StreamHandlerCalledFirst(t *testing.T) {
	var unaryCalled atomic.Bool

	provider := &streamableStubProvider{
		stubMcpToolProvider: stubMcpToolProvider{
			specs: []map[string]any{
				{"name": "stream_tool", "description": "a streaming tool", "inputSchema": map[string]any{"type": "object"}},
			},
			unaryResult: McpToolResult{
				Payload: map[string]any{"ok": true, "source": "unary"},
				IsError: false,
			},
		},
	}
	// Override HandleToolCall to track whether it was called.
	provider.unaryResult = McpToolResult{
		Payload: map[string]any{"ok": true, "source": "unary"},
		IsError: false,
	}
	// Wrap HandleToolCall to detect invocation via a tracking provider.
	trackingProvider := &trackingStreamableProvider{
		inner:       provider,
		unaryCalled: &unaryCalled,
	}

	server := NewStdioMcpServer(trackingProvider)

	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "stream_tool",
			"arguments": map[string]any{},
		},
	}
	reqBytes, _ := json.Marshal(request)
	reqBytes = append(reqBytes, '\n')

	var out bytes.Buffer
	if err := server.Run(bytes.NewReader(reqBytes), &out); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Parse multi-line output: notifications + final response.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (2 notifications + 1 response), got %d:\n%s", len(lines), out.String())
	}

	// Verify chunk notifications.
	expectedChunks := []string{"stream-part-1", "stream-part-2"}
	for i, expected := range expectedChunks {
		var notif map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &notif); err != nil {
			t.Fatalf("failed to parse notification %d: %v", i, err)
		}
		if notif["method"] != "axon/streamChunk" {
			t.Fatalf("expected method axon/streamChunk, got %v", notif["method"])
		}
		params, _ := notif["params"].(map[string]any)
		if params["chunk"] != expected {
			t.Fatalf("notification %d: expected chunk %q, got %q", i, expected, params["chunk"])
		}
		seq, _ := params["seq"].(float64)
		if int(seq) != i {
			t.Fatalf("notification %d: expected seq %d, got %v", i, i, params["seq"])
		}
	}

	// Parse final response (last line).
	var resp map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &resp); err != nil {
		t.Fatalf("failed to parse final response: %v\nraw: %s", err, lines[len(lines)-1])
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("expected result in response, got: %v", resp)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected non-empty content array")
	}
	firstItem, _ := content[0].(map[string]any)
	text, _ := firstItem["text"].(string)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("failed to decode content text: %v", err)
	}
	if decoded["streamed"] != true {
		t.Fatalf("expected streamed: true in summary, got: %v", decoded)
	}
	if int(decoded["chunk_count"].(float64)) != 2 {
		t.Fatalf("expected chunk_count 2, got %v", decoded["chunk_count"])
	}

	// Verify unary handler was NOT called.
	if unaryCalled.Load() {
		t.Fatal("expected unary HandleToolCall NOT to be called when stream handler returns a handle")
	}
}

// trackingStreamableProvider wraps a streamableStubProvider and records whether
// the unary HandleToolCall path is invoked.
type trackingStreamableProvider struct {
	inner       *streamableStubProvider
	unaryCalled *atomic.Bool
}

func (t *trackingStreamableProvider) ToolSpecs() []map[string]any {
	return t.inner.ToolSpecs()
}

func (t *trackingStreamableProvider) HandleToolCall(name string, args map[string]any) McpToolResult {
	t.unaryCalled.Store(true)
	return t.inner.HandleToolCall(name, args)
}

func (t *trackingStreamableProvider) HandleToolCallStream(name string, args map[string]any) (McpToolStreamHandle, error) {
	// Return a real handle with two chunks.
	return &inMemoryStreamHandle{
		chunks: [][]byte{
			[]byte("stream-part-1"),
			[]byte("stream-part-2"),
		},
	}, nil
}

func TestConsumeStream_CleanupCallback(t *testing.T) {
	var cleanupRan atomic.Bool

	handle := &callbackStreamHandle{
		inner: &inMemoryStreamHandle{
			chunks: [][]byte{[]byte("data")},
		},
		onClose: func() {
			cleanupRan.Store(true)
		},
	}

	result := ConsumeStream(handle)

	if result.IsError {
		t.Fatalf("expected IsError=false, got true; payload=%v", result.Payload)
	}

	if !cleanupRan.Load() {
		t.Fatal("expected cleanup callback to run after ConsumeStream")
	}
}

// callbackStreamHandle delegates to an inner handle and runs a callback on Close.
type callbackStreamHandle struct {
	inner   McpToolStreamHandle
	onClose func()
}

func (c *callbackStreamHandle) Recv() ([]byte, bool, error) {
	return c.inner.Recv()
}

func (c *callbackStreamHandle) Close() error {
	err := c.inner.Close()
	if c.onClose != nil {
		c.onClose()
	}
	return err
}

func TestDefaultMCPToolStreamTimeoutValue(t *testing.T) {
	if easynet.DefaultMCPToolStreamTimeoutMs != 60000 {
		t.Fatalf("expected DefaultMCPToolStreamTimeoutMs=60000, got %d", easynet.DefaultMCPToolStreamTimeoutMs)
	}
}
