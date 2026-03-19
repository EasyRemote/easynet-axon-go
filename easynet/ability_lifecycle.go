// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/ability_lifecycle.go
// Description: Ability lifecycle API: create, deploy, invoke, and export as Agent Skills.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

// AbilityTarget identifies the target platform for Ability export.
type AbilityTarget string

const (
	AbilityTargetClaude      AbilityTarget = "claude"
	AbilityTargetCodex       AbilityTarget = "codex"
	AbilityTargetOpenClaw    AbilityTarget = "openclaw"
	AbilityTargetAgentSkills AbilityTarget = "agent_skills"
)

// ParseAbilityTarget parses a string to AbilityTarget (case-insensitive).
// Unknown values default to AbilityTargetAgentSkills.
func ParseAbilityTarget(raw string) AbilityTarget {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "claude":
		return AbilityTargetClaude
	case "codex":
		return AbilityTargetCodex
	case "openclaw":
		return AbilityTargetOpenClaw
	default:
		return AbilityTargetAgentSkills
	}
}

// EphemeralSignature is a placeholder signature for temporary skill deployments.
const EphemeralSignature = "__AXON_EPHEMERAL_DO_NOT_USE_IN_PROD__"

// DefaultAxonPort is the default Axon runtime gRPC port.
const DefaultAxonPort = 50051

// AbilityDescriptor is a pure data descriptor for an Axon ability.
type AbilityDescriptor struct {
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	CommandTemplate string         `json:"command_template"`
	InputSchema     map[string]any `json:"input_schema"`
	OutputSchema    map[string]any `json:"output_schema"`
	Version         string         `json:"version"`
	Tags            []string       `json:"tags"`
	ResourceURI     string         `json:"resource_uri"`
}

// AbilityExportResult holds the generated SKILL.md and invoke.sh content.
type AbilityExportResult struct {
	AbilityMd    string `json:"ability_md"`
	InvokeScript string `json:"invoke_script"`
	AbilityName  string `json:"ability_name"`
}

// ForgetAllResult holds the result of a destructive forget_all operation.
type ForgetAllResult struct {
	Removed      []string           `json:"removed"`
	RemovedCount int                `json:"removed_count"`
	Failed       []ForgetAllFailure `json:"failed"`
	FailedCount  int                `json:"failed_count"`
}

// ForgetAllFailure records a single failure within a forget_all operation.
type ForgetAllFailure struct {
	ToolName string `json:"tool_name"`
	Error    string `json:"error"`
}

var nonAlphanumericRe = regexp.MustCompile(`[^a-z0-9_\-]`)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v — cannot generate secure identifiers", err))
	}
	return hex.EncodeToString(b)
}

var consecutiveHyphenRe = regexp.MustCompile(`-{2,}`)

func normalizeAbilityName(raw string) string {
	result := nonAlphanumericRe.ReplaceAllString(strings.ToLower(raw), "-")
	result = consecutiveHyphenRe.ReplaceAllString(result, "-")
	result = strings.Trim(result, "-")
	if result == "" {
		return "ability"
	}
	return result
}

// CreateAbility validates and builds an AbilityDescriptor.
func CreateAbility(
	name, description, commandTemplate string,
	inputSchema, outputSchema map[string]any,
	version string,
	tags []string,
	resourceURI string,
) (*AbilityDescriptor, error) {
	if strings.TrimSpace(name) == "" {
		return nil, DendriteError{Message: "ability name cannot be empty", Code: ErrCodeValidation}
	}
	if strings.TrimSpace(commandTemplate) == "" {
		return nil, DendriteError{Message: "command_template cannot be empty", Code: ErrCodeValidation}
	}
	token := normalizeAbilityName(name)
	if inputSchema == nil {
		inputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if outputSchema == nil {
		outputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if version == "" {
		version = "1.0.0"
	}
	if tags == nil {
		tags = []string{}
	}
	if resourceURI == "" {
		resourceURI = fmt.Sprintf("easynet:///r/org/%s", token)
	}
	return &AbilityDescriptor{
		Name:            name,
		Description:     description,
		CommandTemplate: commandTemplate,
		InputSchema:     inputSchema,
		OutputSchema:    outputSchema,
		Version:         version,
		Tags:            tags,
		ResourceURI:     resourceURI,
	}, nil
}

// ToToolSpec converts the descriptor to a ToolSpec for use with AbilityToolAdapter.
func (d *AbilityDescriptor) ToToolSpec() ToolSpec {
	token := normalizeAbilityName(d.Name)
	return ToolSpec{
		Name:        token,
		Description: d.Description,
		ResourceURI: d.ResourceURI,
		Parameters:  d.InputSchema,
	}
}

// ExportAbility generates SKILL.md and invoke.sh for the given descriptor and target.
func ExportAbility(descriptor *AbilityDescriptor, target AbilityTarget, axonEndpoint string) *AbilityExportResult {
	token := normalizeAbilityName(descriptor.Name)
	if axonEndpoint == "" {
		axonEndpoint = fmt.Sprintf("http://127.0.0.1:%d", DefaultAxonPort)
	}
	invokeScript := generateInvokeScript(descriptor.ResourceURI, axonEndpoint)
	abilityMd := generateAbilityMd(descriptor, target, token)
	return &AbilityExportResult{
		AbilityMd:    abilityMd,
		InvokeScript: invokeScript,
		AbilityName:  token,
	}
}

// ---------------------------------------------------------------------------
// Deploy / List / Invoke / Uninstall — bridge-backed operations
// ---------------------------------------------------------------------------

// DeployResult contains the raw result and a DeployTrace for observability.
type DeployResult struct {
	Result map[string]any `json:"result"`
	Trace  DeployTrace    `json:"trace"`
}

// DeployToNode deploys an AbilityDescriptor to a node via the MCP deploy pipeline.
// handle is the DendriteBridge client handle returned by OpenClient.
//
// Uses DeployMCPListDirWithRequest to pass through the full descriptor metadata
// (version, capability name, tool name, signature, schemas, tags).
//
// Returns a DeployResult containing both the raw result and a DeployTrace
// with per-phase receipts for observability.
// DeployToNode deploys an AbilityDescriptor to a node via the MCP deploy
// pipeline (Publish → Install → Activate).
//
// The returned DeployResult always contains a Trace with per-phase timing,
// even when an error is returned.  This lets callers inspect phase latency
// and error codes on failure:
//
//	dr, err := DeployToNode(bridge, handle, tenant, nodeId, desc, sig)
//	if err != nil {
//	    fmt.Println(dr.Trace)  // still available
//	}
func DeployToNode(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
	descriptor *AbilityDescriptor,
	signature string,
) (*DeployResult, error) {
	token := normalizeAbilityName(descriptor.Name)
	abilityID := token

	pubBuilder := BeginPhase(PhaseDeploy, tenant, nodeId, abilityID)
	result, err := bridge.DeployMCPListDirWithRequest(handle, DeployMCPListDirRequest{
		TenantID:        tenant,
		NodeID:          nodeId,
		TargetPath:      token,
		CommandTemplate: descriptor.CommandTemplate,
		CapabilityName:  descriptor.Name,
		ToolName:        token,
		Version:         descriptor.Version,
		SignatureBase64: signature,
	})
	if err != nil {
		receipt := pubBuilder.FinishErr(err)
		trace := BuildDeployTrace([]PhaseReceipt{receipt})
		return &DeployResult{Result: nil, Trace: trace}, &DeployError{
			Message: fmt.Sprintf("deploy failed: %v", err),
			Trace:   &trace,
		}
	}
	installID, _ := result["install_id"].(string)
	receipt := pubBuilder.FinishOk(installID, nil)
	trace := BuildDeployTrace([]PhaseReceipt{receipt})
	return &DeployResult{Result: result, Trace: trace}, nil
}

// ListAbilities queries deployed abilities (MCP tools) on a remote node.
func ListAbilities(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
) ([]map[string]any, error) {
	return bridge.ListMCPTools(handle, tenant, "", nil, nodeId)
}

// InvokeAbility invokes a deployed ability on a remote node by its tool name.
func InvokeAbility(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
	toolName string,
	args map[string]any,
) (map[string]any, error) {
	if args == nil {
		args = map[string]any{}
	}
	return bridge.CallMCPTool(handle, tenant, toolName, nodeId, args)
}

// UninstallAbility uninstalls a deployed ability from a remote node.
// Requires the installId returned by DeployToNode.
func UninstallAbility(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
	installId string,
	reason string,
) (map[string]any, error) {
	if reason == "" {
		reason = "ability lifecycle: uninstall"
	}
	deactivateFirst := true
	force := false
	return bridge.UninstallCapability(
		handle, tenant, nodeId, installId,
		deactivateFirst, reason, force,
	)
}

// DiscoverNodes discovers online devices registered with the Axon Runtime.
func DiscoverNodes(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
) ([]map[string]any, error) {
	return bridge.ListNodes(handle, tenant, "")
}

func shellSingleQuote(raw string) string {
	return "'" + strings.ReplaceAll(raw, "'", "'\"'\"'") + "'"
}

// buildPythonSubprocessTemplate wraps a shell command in a `python3 -c`
// subprocess template that returns structured JSON output with entries,
// stdout, stderr, and exit_code.
// Canonical implementation: sdk/rust/src/presets/remote_control/utils.rs
func buildPythonSubprocessTemplate(command string) string {
	quoted, _ := json.Marshal(command)
	script := strings.Join([]string{
		"import json,subprocess",
		fmt.Sprintf("cmd = %s", string(quoted)),
		"proc = subprocess.run(['/bin/sh', '-c', cmd], text=True, capture_output=True)",
		"combined = (proc.stdout + proc.stderr).strip()",
		"print(json.dumps({'entries': [combined], 'command': cmd, 'exit_code': proc.returncode, 'stdout': proc.stdout, 'stderr': proc.stderr}))",
	}, "; ")
	return fmt.Sprintf("python3 -c %s", shellSingleQuote(script))
}

// ExecuteCommand executes a one-shot command on a remote device (no learn step).
//
// Deploys a temporary ability with the raw command, calls it, then cleans up.
func ExecuteCommand(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
	command string,
) (map[string]any, error) {
	toolName := fmt.Sprintf("cmd_%d_%s", time.Now().UnixMilli(), randomHex(4))
	wrapped := buildPythonSubprocessTemplate(command)
	deployResult, err := bridge.DeployMCPListDirWithRequest(handle, DeployMCPListDirRequest{
		TenantID:        tenant,
		NodeID:          nodeId,
		TargetPath:      toolName,
		CommandTemplate: wrapped,
		ToolName:        toolName,
		SignatureBase64: EphemeralSignature,
	})
	if err != nil {
		return nil, DendriteError{Message: fmt.Sprintf("deploy temp ability: %v", err), Code: ErrCodeInvocation}
	}
	result, err := bridge.CallMCPTool(handle, tenant, toolName, nodeId, map[string]any{})
	cleanupOk := true
	var cleanupError string
	if installID, ok := deployResult["install_id"].(string); ok && installID != "" {
		if _, uErr := bridge.UninstallCapability(handle, tenant, nodeId, installID, true, "execute_command cleanup", false); uErr != nil {
			cliLog(fmt.Sprintf("execute_command cleanup failed for %s: %v", installID, uErr))
			cleanupOk = false
			cleanupError = uErr.Error()
		}
	}
	if result == nil {
		result = map[string]any{}
	}
	result["cleanup"] = map[string]any{"ok": cleanupOk, "error": cleanupError}
	return result, err
}

// ForgetAllOptions configures the ForgetAll operation.
type ForgetAllOptions struct {
	// DryRun, when true, lists all abilities that would be removed without
	// actually uninstalling them.  The returned ForgetAllResult will have
	// the would-be-removed abilities in the Removed slice and zero Failed
	// entries.  DryRun does not require Confirm since it is non-destructive.
	DryRun bool
}

// ForgetAll removes all deployed abilities from a device.
//
// Requires confirm=true as a safety gate — this operation is destructive
// and cannot be undone.
//
// The opts parameter is variadic so callers may omit it entirely for the
// default non-dry-run behavior.  When opts[0].DryRun is true, the function
// lists all abilities that would be removed without performing any
// uninstalls.  The confirm parameter is ignored for dry-run calls since no
// data is modified.
func ForgetAll(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
	confirm bool,
	opts ...ForgetAllOptions,
) (*ForgetAllResult, error) {
	dryRun := len(opts) > 0 && opts[0].DryRun
	if !confirm && !dryRun {
		return nil, DendriteError{Message: "forget_all requires confirm=true (destructive operation)", Code: ErrCodeValidation}
	}
	tools, err := bridge.ListMCPTools(handle, tenant, "", nil, nodeId)
	if err != nil {
		return nil, err
	}

	// Dry-run mode: collect the list of abilities that would be removed
	// without performing any uninstalls.
	if dryRun {
		wouldRemove := make([]string, 0)
		for _, tool := range tools {
			installID, _ := tool["install_id"].(string)
			toolName, _ := tool["tool_name"].(string)
			if installID == "" {
				continue
			}
			wouldRemove = append(wouldRemove, toolName)
		}
		return &ForgetAllResult{
			Removed:      wouldRemove,
			RemovedCount: len(wouldRemove),
			Failed:       []ForgetAllFailure{},
			FailedCount:  0,
		}, nil
	}

	removed := make([]string, 0)
	failed := make([]ForgetAllFailure, 0)
	for _, tool := range tools {
		installID, _ := tool["install_id"].(string)
		toolName, _ := tool["tool_name"].(string)
		if installID == "" {
			continue
		}
		deactivateFirst := true
		force := false
		_, uErr := bridge.UninstallCapability(handle, tenant, nodeId, installID, deactivateFirst, "forget_all", force)
		if uErr != nil {
			cliLog(fmt.Sprintf("forget_all: failed to uninstall %s: %v", toolName, uErr))
			failed = append(failed, ForgetAllFailure{ToolName: toolName, Error: uErr.Error()})
		} else {
			removed = append(removed, toolName)
		}
	}
	result := &ForgetAllResult{
		Removed:      removed,
		RemovedCount: len(removed),
		Failed:       failed,
		FailedCount:  len(failed),
	}
	if len(failed) > 0 {
		return result, DendriteError{
			Message: fmt.Sprintf("forget_all: %d succeeded, %d failed", len(removed), len(failed)),
			Code:    ErrCodePartialSuccess,
		}
	}
	return result, nil
}

// DisconnectDevice disconnects a device from the Axon Runtime (deregister node).
func DisconnectDevice(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
	reason string,
) (map[string]any, error) {
	if reason == "" {
		reason = "sdk: disconnect_device"
	}
	return bridge.DeregisterNode(handle, tenant, nodeId, reason)
}

// DrainDevice drains a device — stop accepting new invocations while finishing in-flight ones.
func DrainDevice(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
	reason string,
) (map[string]any, error) {
	if reason == "" {
		reason = "sdk: drain_device"
	}
	return bridge.DrainNode(handle, tenant, nodeId, reason)
}

// ListRemoteTools lists all MCP tools visible for a tenant (low-level, includes system tools).
func ListRemoteTools(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	namePattern string,
	nodeId string,
) ([]map[string]any, error) {
	return bridge.ListMCPTools(handle, tenant, namePattern, nil, nodeId)
}

// PackageSkill builds a native skill package descriptor from arguments.
func BuildDeployPackage(args map[string]any, signature string) map[string]any {
	abilityName, _ := args["ability_name"].(string)
	toolName, _ := args["tool_name"].(string)
	if toolName == "" {
		toolName = abilityName
	}
	defaultSchema := map[string]any{"type": "object", "properties": map[string]any{}}
	inputSchema := args["input_schema"]
	if inputSchema == nil {
		inputSchema = defaultSchema
	}
	outputSchema := args["output_schema"]
	if outputSchema == nil {
		outputSchema = defaultSchema
	}
	version, _ := args["version"].(string)
	if version == "" {
		version = "1.0.0"
	}
	return map[string]any{
		"ability_name":     abilityName,
		"tool_name":        toolName,
		"description":      args["description"],
		"command_template": args["command_template"],
		"input_schema":     inputSchema,
		"output_schema":    outputSchema,
		"version":          version,
		"tags":             args["tags"],
		"signature_base64": signature,
	}
}

// DeployPackage deploys a native skill package by publish/install/activate.
func DeployPackage(
	bridge *DendriteBridge,
	handle uint64,
	tenant string,
	nodeId string,
	pkg map[string]any,
) (map[string]any, error) {
	abilityName, _ := pkg["ability_name"].(string)
	toolName, _ := pkg["tool_name"].(string)
	if toolName == "" {
		toolName = abilityName
	}
	cmdTemplate, _ := pkg["command_template"].(string)
	version, _ := pkg["version"].(string)
	sig, _ := pkg["signature_base64"].(string)
	inputSchema, _ := pkg["input_schema"].(map[string]any)
	outputSchema, _ := pkg["output_schema"].(map[string]any)
	var tags []string
	if rawTags, ok := pkg["tags"].([]any); ok {
		for _, t := range rawTags {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}
	return bridge.DeployMCPListDirWithRequest(handle, DeployMCPListDirRequest{
		TenantID:        tenant,
		NodeID:          nodeId,
		TargetPath:      toolName,
		CommandTemplate: cmdTemplate,
		CapabilityName:  abilityName,
		ToolName:        toolName,
		Version:         version,
		SignatureBase64: sig,
		InputSchema:     inputSchema,
		OutputSchema:    outputSchema,
		Tags:            tags,
	})
}

// ---------------------------------------------------------------------------
// Server lifecycle — start / connect / stop
// ---------------------------------------------------------------------------

// ServerHandle holds a reference to an Axon runtime — locally spawned or remote.
type ServerHandle struct {
	Endpoint string      // The EasyNet connection URL.
	Process  *os.Process // Server process (nil if connecting to existing).
	Cmd      *exec.Cmd   // exec.Cmd used to start (nil if connecting to existing).
	LogFile  string      // Path to runtime log file ("" if connecting to existing).
}

// Stop terminates the server (only if this handle spawned it).
// Sends SIGTERM first for graceful shutdown, falls back to SIGKILL after 5s.
func (h *ServerHandle) Stop() {
	if h.Process == nil {
		return
	}
	cliLog(fmt.Sprintf("stopping axon-runtime (pid %d)", h.Process.Pid))
	_ = h.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = h.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = h.Process.Kill()
		<-done
	}
	cliLog("axon-runtime stopped")
	h.Process = nil
}

func cliLog(msg string) {
	now := time.Now().Format("15:04:05")
	fmt.Fprintf(os.Stderr, "[axon %s] %s\n", now, msg)
}

func defaultLogDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".easynet", "logs")
}

func findRuntimeBinary() (string, error) {
	if bin := os.Getenv("AXON_RUNTIME_BIN"); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return bin, nil
		}
	}
	if path, err := exec.LookPath("axon-runtime"); err == nil {
		return path, nil
	}
	return "", DendriteError{Message: "axon-runtime not found. Set AXON_RUNTIME_BIN or add to PATH", Code: ErrCodeBridge}
}

func findFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

func waitForPort(host string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func parseEndpoint(endpoint string) (string, int) {
	raw := strings.TrimSpace(endpoint)
	if strings.HasPrefix(raw, "axon://") {
		authority := strings.SplitN(raw[len("axon://"):], "/", 2)[0]
		if authority == "" || authority == "localhost" {
			return "127.0.0.1", DefaultAxonPort
		}
		if strings.HasPrefix(authority, "localhost:") {
			port := DefaultAxonPort
			fmt.Sscanf(authority[len("localhost:"):], "%d", &port)
			return "127.0.0.1", port
		}
		if idx := strings.LastIndex(authority, ":"); idx > 0 {
			port := DefaultAxonPort
			fmt.Sscanf(authority[idx+1:], "%d", &port)
			return authority[:idx], port
		}
		return authority, DefaultAxonPort
	}
	url := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	url = strings.SplitN(url, "/", 2)[0]
	if idx := strings.LastIndex(url, ":"); idx > 0 {
		port := DefaultAxonPort
		fmt.Sscanf(url[idx+1:], "%d", &port)
		return url[:idx], port
	}
	return url, DefaultAxonPort
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeHubEndpoint(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "axon://") {
		host, port := parseEndpoint(trimmed)
		return fmt.Sprintf("http://%s:%d", host, port)
	}
	return trimmed
}

func hostnameOrDefault() string {
	if host, err := os.Hostname(); err == nil {
		trimmed := strings.TrimSpace(host)
		if trimmed != "" {
			return trimmed
		}
	}
	return "localhost"
}

func federationSpawnEnv(hub, hubTenant, hubLabel, hubJoinToken string) []string {
	hubEndpoint := normalizeHubEndpoint(firstNonEmpty(hub, os.Getenv("AXON_HUB")))
	if hubEndpoint == "" {
		return nil
	}
	tenant := firstNonEmpty(hubTenant, os.Getenv("AXON_FEDERATION_TENANT"), "default")
	label := firstNonEmpty(hubLabel, os.Getenv("AXON_FEDERATION_LABEL"), hostnameOrDefault())
	values := []string{
		"AXON_HUB=" + hubEndpoint,
		"AXON_FEDERATION_TENANT=" + tenant,
		"AXON_FEDERATION_LABEL=" + label,
	}
	if joinToken := firstNonEmpty(hubJoinToken, os.Getenv("AXON_HUB_JOIN_TOKEN"), os.Getenv("AXON_FEDERATION_JOIN_TOKEN")); joinToken != "" {
		values = append(values, "AXON_HUB_JOIN_TOKEN="+joinToken)
	}
	return values
}

// StartServerOptions configures local runtime bootstrap and federation join behavior.
type StartServerOptions struct {
	Endpoint  string
	LogFile   string
	Insecure  *bool
	Timeout   time.Duration
	Hub       string
	HubTenant string
	HubLabel  string
	HubJoinToken string
}

// StartServer starts or connects to an Axon runtime.
//
// Usage:
//
//	// Auto-start (zero config):
//	srv, _ := StartServer("", "", nil)
//
//	// Connect via axon:// transport URI:
//	srv, _ := StartServer("axon://localhost", "", nil)
//
//	// Production: enable mTLS:
//	insecure := false
//	srv, _ := StartServer("", "", &insecure)
//
//	// Federation mode:
//	srv, _ := StartServerWithOptions(StartServerOptions{
//		Hub:       "axon://hub.easynet.run:50084",
//		HubTenant: "tenant-test",
//		HubLabel:  "alice-macbook",
//		HubJoinToken: "shared-fed-secret",
//	})
//
// Parameters:
//   - endpoint: Axon transport URI (axon://host) or HTTP URL. If empty,
//     checks EASYNET_AXON_ENDPOINT env var, then auto-starts locally.
//   - logFile:  path to write runtime logs (default: ~/.easynet/logs/axon-runtime.log).
//   - insecure: if non-nil and true, disables mTLS. Defaults to true for local auto-start.
func StartServer(endpoint string, logFile string, insecure *bool) (*ServerHandle, error) {
	return StartServerWithOptions(StartServerOptions{
		Endpoint: endpoint,
		LogFile:  logFile,
		Insecure: insecure,
	})
}

// StartServerWithOptions starts or connects to an Axon runtime with federation-aware options.
func StartServerWithOptions(options StartServerOptions) (*ServerHandle, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	endpoint := strings.TrimSpace(options.Endpoint)
	// Resolve from env if not provided
	if endpoint == "" {
		endpoint = os.Getenv("EASYNET_AXON_ENDPOINT")
	}

	// --- Connect to existing server ---
	if endpoint != "" {
		cliLog(fmt.Sprintf("connecting to %s", endpoint))
		host, port := parseEndpoint(endpoint)
		if !waitForPort(host, port, timeout) {
			return nil, DendriteError{Message: fmt.Sprintf("cannot reach server at %s", endpoint), Code: ErrCodeBridge}
		}
		cliLog(fmt.Sprintf("connected to %s", endpoint))
		return &ServerHandle{Endpoint: endpoint}, nil
	}

	// --- Spawn local server ---
	port, err := findFreePort()
	if err != nil {
		return nil, err
	}
	host := "127.0.0.1"
	bindAddr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	endpointURL := "http://" + bindAddr
	binary, err := findRuntimeBinary()
	if err != nil {
		return nil, err
	}

	logDir := defaultLogDir()
	logFile := options.LogFile
	if logFile == "" {
		logFile = filepath.Join(logDir, "axon-runtime.log")
	}
	_ = os.MkdirAll(filepath.Dir(logFile), 0o755)
	logFH, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, DendriteError{Message: fmt.Sprintf("open log %s: %v", logFile, err), Code: ErrCodeBridge}
	}

	cliLog(fmt.Sprintf("starting axon-runtime on %s (log: %s)", bindAddr, logFile))

	cmd := exec.Command(binary)
	mtls := "false"
	if options.Insecure != nil && !*options.Insecure {
		mtls = "true"
	}
	cmd.Env = append(os.Environ(), "AXON_BIND="+bindAddr, "AXON_ENFORCE_MTLS="+mtls)
	if fedEnv := federationSpawnEnv(options.Hub, options.HubTenant, options.HubLabel, options.HubJoinToken); len(fedEnv) > 0 {
		cmd.Env = append(cmd.Env, fedEnv...)
		cliLog(fmt.Sprintf("federation: will connect to hub at %s", normalizeHubEndpoint(firstNonEmpty(options.Hub, os.Getenv("AXON_HUB")))))
	}
	cmd.Stdout = logFH
	cmd.Stderr = logFH
	if err := cmd.Start(); err != nil {
		logFH.Close()
		return nil, DendriteError{Message: fmt.Sprintf("start axon-runtime: %v", err), Code: ErrCodeBridge}
	}
	logFH.Close()

	if !waitForPort(host, port, timeout) {
		_ = cmd.Process.Kill()
		return nil, DendriteError{Message: fmt.Sprintf("axon-runtime not ready within 10s on %s (see %s)", bindAddr, logFile), Code: ErrCodeBridge}
	}

	cliLog(fmt.Sprintf("axon-runtime ready → %s", endpointURL))
	return &ServerHandle{
		Endpoint: endpointURL,
		Process:  cmd.Process,
		Cmd:      cmd,
		LogFile:  logFile,
	}, nil
}

func generateInvokeScript(resourceURI, endpoint string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
AXON_ENDPOINT="${AXON_ENDPOINT:-%s}"
TENANT="${AXON_TENANT:-default}"
RESOURCE_URI="%s"
ARGS="${1:-{}}"
curl -sS -X POST "${AXON_ENDPOINT}/v1/invoke" \
  -H "Content-Type: application/json" \
  -d "{\"tenant_id\":\"${TENANT}\",\"resource_uri\":\"${RESOURCE_URI}\",\"payload\":${ARGS}}"
`, endpoint, resourceURI)
}

func pushMetadata(b *strings.Builder, version, resourceURI string) {
	b.WriteString("metadata:\n")
	b.WriteString("  author: easynet-axon\n")
	fmt.Fprintf(b, "  version: \"%s\"\n", version)
	fmt.Fprintf(b, "  axon-resource-uri: \"%s\"\n", resourceURI)
}

func generateAbilityMd(descriptor *AbilityDescriptor, target AbilityTarget, token string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", token)
	fmt.Fprintf(&b, "description: %s\n", descriptor.Description)
	b.WriteString("compatibility: Requires network access to Axon runtime\n")

	pushMetadata(&b, descriptor.Version, descriptor.ResourceURI)
	switch target {
	case AbilityTargetClaude:
		b.WriteString("allowed-tools: Bash(*)\n")
	case AbilityTargetOpenClaw:
		b.WriteString("  openclaw:\n")
		b.WriteString("    emoji: \"⚡\"\n")
		b.WriteString("    requires:\n")
		b.WriteString("      network: true\n")
		b.WriteString("    command-dispatch: tool\n")
	}

	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", descriptor.Name)
	fmt.Fprintf(&b, "%s\n\n", descriptor.Description)
	b.WriteString("## Parameters\n\n")
	b.WriteString("| Name | Type | Required | Description |\n")
	b.WriteString("|------|------|----------|-------------|\n")

	if props, ok := descriptor.InputSchema["properties"].(map[string]any); ok {
		required := []string{}
		if req, ok := descriptor.InputSchema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					required = append(required, s)
				}
			}
		}
		for name, schemaDef := range props {
			schema, _ := schemaDef.(map[string]any)
			propType, _ := schema["type"].(string)
			if propType == "" {
				propType = "string"
			}
			propDesc, _ := schema["description"].(string)
			isRequired := "No"
			if slices.Contains(required, name) {
				isRequired = "Yes"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", name, propType, isRequired, propDesc)
		}
	}

	b.WriteString("\n## Invoke\n\n")
	b.WriteString("Run the bundled script with a JSON argument:\n\n")
	skillDirVar := "SKILL_DIR"
	switch target {
	case AbilityTargetClaude:
		skillDirVar = "CLAUDE_SKILL_DIR"
	case AbilityTargetCodex:
		skillDirVar = "CODEX_SKILL_DIR"
	}
	b.WriteString("```bash\n")
	fmt.Fprintf(&b, "${%s}/scripts/invoke.sh '{\"param\": \"value\"}'\n", skillDirVar)
	b.WriteString("```\n\n")
	b.WriteString("## Axon Resource\n\n")
	fmt.Fprintf(&b, "- **URI**: `%s`\n", descriptor.ResourceURI)
	fmt.Fprintf(&b, "- **Version**: %s\n", descriptor.Version)

	return b.String()
}
