package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"easynet.run/axon/sdk/go/easynet"
)

const (
	defaultServerName      = "easynet-axon-remote-go"
	defaultServerVersion   = "0.2.0"
	maxLineLength          = 4 * 1024 * 1024 // 4 MiB per JSON-RPC message
)

// ToolResult is the common execution contract for MCP tool calls.
type ToolResult struct {
	Payload map[string]any
	IsError bool
}

// ToolProvider is the business-layer interface expected by the MCP server.
type ToolProvider interface {
	ToolSpecs() []map[string]any
	HandleToolCall(name string, args map[string]any) ToolResult
}

// StdioServer is a minimal stdio MCP v2 JSON-RPC dispatcher.
type StdioServer struct {
	provider        ToolProvider
	protocolVersion string
	serverName      string
	serverVersion   string
	errorOutput     io.Writer
}

// StdioServerOption customizes server metadata.
type StdioServerOption func(*StdioServer)

func WithProtocolVersion(version string) StdioServerOption {
	return func(server *StdioServer) {
		if strings.TrimSpace(version) != "" {
			server.protocolVersion = version
		}
	}
}

func WithServerName(name string) StdioServerOption {
	return func(server *StdioServer) {
		if strings.TrimSpace(name) != "" {
			server.serverName = name
		}
	}
}

func WithServerVersion(version string) StdioServerOption {
	return func(server *StdioServer) {
		if strings.TrimSpace(version) != "" {
			server.serverVersion = version
		}
	}
}

// NewStdioServer creates a reusable stdio MCP server.
func NewStdioServer(provider ToolProvider, options ...StdioServerOption) *StdioServer {
	server := &StdioServer{
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

// Run executes line-delimited MCP JSON-RPC over stdio streams.
func (server *StdioServer) Run(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	writer := bufio.NewWriter(output)
	defer writer.Flush()

	for {
		raw, readErr := reader.ReadString('\n')
		if readErr != nil && len(raw) == 0 {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}

		if len(raw) > maxLineLength {
			errResp := jsonRPCError(nil, -32000, fmt.Sprintf("input line exceeds maximum length (%d bytes)", maxLineLength))
			if err := writeJSON(writer, errResp); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
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

		response := server.handleRawLine(raw)
		if response != nil {
			if err := writeJSON(writer, response); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
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
func (server *StdioServer) RunWithExitWriter(
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

func (server *StdioServer) handleRawLine(raw string) map[string]any {
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
	return server.handleRequest(message)
}

func (server *StdioServer) handleRequest(message map[string]any) map[string]any {
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

func toolResponsePayload(result ToolResult) map[string]any {
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
