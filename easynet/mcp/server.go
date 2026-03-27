package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"easynet.run/axon/sdk/go/easynet"
)

const (
	defaultServerName    = "easynet-axon-remote-go"
	defaultServerVersion = "0.2.0"
	maxLineLength        = 4 * 1024 * 1024 // 4 MiB per JSON-RPC message
)

// McpToolResult is the common execution contract for MCP tool calls.
type McpToolResult struct {
	Payload map[string]any
	IsError bool
}

// McpToolStreamHandle is the optional incremental stream contract exposed by
// stream-capable MCP providers.
type McpToolStreamHandle interface {
	Recv() (chunk []byte, done bool, err error)
	Close() error
}

// McpToolProvider is the business-layer interface expected by the MCP server.
type McpToolProvider interface {
	ToolSpecs() []map[string]any
	HandleToolCall(name string, args map[string]any) McpToolResult
}

// McpToolStreamProvider is an optional extension for providers that can open tool
// streams directly.
type McpToolStreamProvider interface {
	HandleToolCallStream(name string, args map[string]any) (McpToolStreamHandle, error)
}

// StdioMcpServer is a minimal stdio MCP v2 JSON-RPC dispatcher.
type StdioMcpServer struct {
	provider        McpToolProvider
	protocolVersion string
	serverName      string
	serverVersion   string
	errorOutput     io.Writer
}

// StdioMcpServerOption customizes server metadata.
type StdioMcpServerOption func(*StdioMcpServer)

func WithProtocolVersion(version string) StdioMcpServerOption {
	return func(server *StdioMcpServer) {
		if strings.TrimSpace(version) != "" {
			server.protocolVersion = version
		}
	}
}

func WithServerName(name string) StdioMcpServerOption {
	return func(server *StdioMcpServer) {
		if strings.TrimSpace(name) != "" {
			server.serverName = name
		}
	}
}

func WithServerVersion(version string) StdioMcpServerOption {
	return func(server *StdioMcpServer) {
		if strings.TrimSpace(version) != "" {
			server.serverVersion = version
		}
	}
}

// NewStdioMcpServer creates a reusable stdio MCP server.
func NewStdioMcpServer(provider McpToolProvider, options ...StdioMcpServerOption) *StdioMcpServer {
	server := &StdioMcpServer{
		provider:        provider,
		protocolVersion: easynet.McpProtocolVersion,
		serverName:      defaultServerName,
		serverVersion:   defaultServerVersion,
	}
	for _, option := range options {
		option(server)
	}
	return server
}

// WriteFn is a callback for writing a JSON-RPC message to the transport.
type WriteFn func(payload map[string]any) error

// Run executes line-delimited MCP JSON-RPC over stdio streams.
func (server *StdioMcpServer) Run(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	writer := bufio.NewWriter(output)
	defer writer.Flush()

	writeFn := func(payload map[string]any) error {
		if err := writeJSON(writer, payload); err != nil {
			return err
		}
		return writer.Flush()
	}

	for {
		raw, readErr := reader.ReadString('\n')
		if readErr != nil && len(raw) == 0 {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}

		if len(raw) > maxLineLength {
			if err := writeFn(jsonRPCError(nil, -32000, fmt.Sprintf("input line exceeds maximum length (%d bytes)", maxLineLength))); err != nil {
				return err
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				return readErr
			}
			continue
		}

		response := server.handleRawLine(raw, writeFn)
		if response != nil {
			if err := writeFn(response); err != nil {
				return err
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

// RunWithExitWriter runs the server and logs diagnostic errors to errorOutput.
func (server *StdioMcpServer) RunWithExitWriter(
	input io.Reader,
	output io.Writer,
	errorOutput io.Writer,
) error {
	if errorOutput == nil {
		errorOutput = os.Stderr
	}
	server.errorOutput = errorOutput
	return server.Run(input, output)
}

func (server *StdioMcpServer) handleRawLine(raw string, writeFn WriteFn) map[string]any {
	message, parseErr := parseJSONLine(raw)
	if parseErr != nil {
		if server.errorOutput != nil {
			fmt.Fprintf(server.errorOutput, "mcp: json parse error: %v\n", parseErr)
		}
		return jsonrpcParseError()
	}
	if message == nil {
		return nil
	}
	return server.handleRequest(message, writeFn)
}

func (server *StdioMcpServer) handleRequest(message map[string]any, writeFn WriteFn) map[string]any {
	if message == nil {
		return nil
	}

	id, hasID := message["id"]
	method, _ := message["method"].(string)
	params, _ := message["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}

	// JSON-RPC 2.0 requires "jsonrpc": "2.0" on every request.
	if ver, _ := message["jsonrpc"].(string); ver != "2.0" && hasID {
		return jsonRPCError(id, -32600, "invalid request: missing jsonrpc version")
	}

	switch method {
	case "notifications/initialized":
		return nil
	case "initialize":
		if !hasID {
			return nil
		}
		return jsonRPCSuccess(id, map[string]any{
			"protocolVersion": server.protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    server.serverName,
				"version": server.serverVersion,
			},
		})
	case "tools/list":
		if !hasID {
			return nil
		}
		return jsonRPCSuccess(id, map[string]any{
			"tools": server.provider.ToolSpecs(),
		})
	case "tools/call":
		if !hasID {
			return nil
		}
		name, ok := params["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			return jsonRPCError(id, -32602, "tool name is required")
		}

		arguments, _ := params["arguments"].(map[string]any)
		if arguments == nil {
			arguments = map[string]any{}
		}
		if streamingProvider, ok := server.provider.(McpToolStreamProvider); ok {
			handle, err := streamingProvider.HandleToolCallStream(name, arguments)
			if err != nil {
				return jsonRPCSuccess(id, toolResponsePayload(McpToolResult{
					Payload: map[string]any{
						"ok":    false,
						"error": err.Error(),
					},
					IsError: true,
				}))
			}
			if handle != nil {
				maxBytes := resolveMaxBytes(arguments["max_bytes"])
				if writeFn != nil {
					return StreamToClient(handle, id, writeFn, maxBytes)
				}
				return jsonRPCSuccess(id, toolResponsePayload(ConsumeStream(handle, maxBytes)))
			}
		}
		result := server.provider.HandleToolCall(name, arguments)
		return jsonRPCSuccess(id, toolResponsePayload(result))
	case "ping":
		if !hasID {
			return nil
		}
		return jsonRPCSuccess(id, map[string]any{})
	default:
		if !hasID {
			return nil
		}
		return jsonRPCError(id, -32601, fmt.Sprintf("method not found: %s", method))
	}
}

// DefaultMaxStreamBytes is the default maximum total bytes (64 MiB) accepted
// from a single streamed MCP tool call.  Callers may override via max_bytes.
const DefaultMaxStreamBytes = 64 * 1024 * 1024 // 64 MiB

// streamResult holds the outcome of processStream.
type streamResult struct {
	chunkCount     int
	hadError       string
	hadInvalidUtf8 bool
	chunks         []string // only populated when collectChunks is true
}

// processStream is the shared loop that drives chunk consumption from a stream
// handle.  It handles recv, UTF-8 validation, byte counting, maxBytes
// enforcement, defer-close with error logging, and invokes onChunk for each
// decoded chunk.  When collectChunks is true the decoded strings are also
// accumulated in the returned streamResult.
func processStream(
	handle McpToolStreamHandle,
	maxBytes int,
	collectChunks bool,
	onChunk func(decoded string, seq int) error,
) streamResult {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxStreamBytes
	}

	var res streamResult
	if collectChunks {
		res.chunks = make([]string, 0, 8)
	}

	totalBytes := 0

	defer func() {
		if err := handle.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: stream close failed: %v\n", err)
		}
	}()

	for {
		chunk, done, err := handle.Recv()
		if err != nil {
			res.hadError = err.Error()
			return res
		}
		if done {
			return res
		}
		if !res.hadInvalidUtf8 && len(chunk) > 0 && !utf8.Valid(chunk) {
			res.hadInvalidUtf8 = true
		}
		decoded := string(chunk)
		totalBytes += len(chunk)
		if totalBytes > maxBytes {
			res.hadError = fmt.Sprintf("stream output exceeded %s limit", formatBytes(maxBytes))
			return res
		}
		if onChunk != nil {
			if cbErr := onChunk(decoded, res.chunkCount); cbErr != nil {
				res.hadError = cbErr.Error()
				return res
			}
		}
		if collectChunks {
			res.chunks = append(res.chunks, decoded)
		}
		res.chunkCount++
	}
}

// StreamToClient sends each chunk as an axon/streamChunk JSON-RPC notification
// in real time, then returns a summary response.  maxBytes overrides the buffer
// limit; pass 0 to use the default (64 MiB).
func StreamToClient(handle McpToolStreamHandle, requestID any, writeFn WriteFn, maxBytes int) map[string]any {
	res := processStream(handle, maxBytes, false, func(decoded string, seq int) error {
		notification := jsonRPCNotification("axon/streamChunk", map[string]any{
			"requestId": requestID,
			"seq":       seq,
			"chunk":     decoded,
		})
		if writeErr := writeFn(notification); writeErr != nil {
			fmt.Fprintf(os.Stderr, "mcp: failed to write streamChunk notification: %v\n", writeErr)
			return fmt.Errorf("write notification failed: %v", writeErr)
		}
		return nil
	})

	summary := map[string]any{
		"ok":          res.hadError == "",
		"chunk_count": res.chunkCount,
		"streamed":    true,
	}
	if res.hadError != "" {
		summary["error"] = res.hadError
	}
	if res.hadInvalidUtf8 {
		summary["contains_invalid_utf8"] = true
	}
	return jsonRPCSuccess(requestID, toolResponsePayload(McpToolResult{
		Payload: summary,
		IsError: res.hadError != "",
	}))
}

// ConsumeStream buffers all chunks from a stream handle and returns a single
// McpToolResult.  maxBytes overrides the buffer limit; pass 0 to use the
// default (64 MiB).
func ConsumeStream(handle McpToolStreamHandle, maxBytes ...int) McpToolResult {
	limit := DefaultMaxStreamBytes
	if len(maxBytes) > 0 && maxBytes[0] > 0 {
		limit = maxBytes[0]
	}

	res := processStream(handle, limit, true, nil)

	if res.hadError != "" {
		errMsg := res.hadError
		// Preserve the "buffer limit" wording for maxBytes overflow in ConsumeStream.
		if strings.Contains(errMsg, "exceeded") && strings.Contains(errMsg, "limit") {
			errMsg = fmt.Sprintf("stream output exceeded %s buffer limit", formatBytes(limit))
		}
		return McpToolResult{
			Payload: map[string]any{
				"ok":          false,
				"chunk_count": len(res.chunks),
				"chunks":      res.chunks,
				"error":       errMsg,
			},
			IsError: true,
		}
	}

	payload := map[string]any{
		"ok":          true,
		"chunk_count": len(res.chunks),
		"chunks":      res.chunks,
	}
	if res.hadInvalidUtf8 {
		payload["contains_invalid_utf8"] = true
	}
	return McpToolResult{Payload: payload, IsError: false}
}

func resolveMaxBytes(raw any) int {
	switch v := raw.(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	}
	return DefaultMaxStreamBytes
}

func formatBytes(n int) string {
	switch {
	case n >= 1024*1024*1024:
		return fmt.Sprintf("%d GiB", n/(1024*1024*1024))
	case n >= 1024*1024:
		return fmt.Sprintf("%d MiB", n/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%d KiB", n/1024)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

func parseJSONLine(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var message map[string]any
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		return nil, err
	}
	return message, nil
}

func jsonrpcParseError() map[string]any {
	return jsonRPCError(nil, -32700, "parse error")
}

func toolResponsePayload(result McpToolResult) map[string]any {
	serialized, err := json.Marshal(result.Payload)
	text := string(serialized)
	if err != nil {
		text = `{"ok":false,"error":"tool response serialization failed"}`
	}
	payload := map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}
	if result.IsError {
		payload["isError"] = true
	}
	return payload
}

func jsonRPCSuccess(id any, result map[string]any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
}

func jsonRPCNotification(method string, params map[string]any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
}

func jsonRPCError(id any, code int, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
}

func writeJSON(writer *bufio.Writer, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err = writer.Write(data); err != nil {
		return err
	}
	return writer.WriteByte('\n')
}
