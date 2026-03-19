package abilitydispatch

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	easynet "easynet.run/axon/sdk/go/easynet"
)

// AbilityDispatchCase owns a configurable media-capability dispatch flow.
type AbilityDispatchCase struct {
	config             Config
	orchestratorBuild  func(cfg Config) (*easynet.Orchestrator, error)
	now                func() time.Time
	metadataBuilder    func(mode string, skillID string, ts int64) map[string]string
	taskPayloadBuilder func(mode string, cfg Config, ts int64) map[string]any
}

// AbilityDispatchCaseOption customizes runtime behaviour.
type AbilityDispatchCaseOption func(*AbilityDispatchCase)

// MediaCaptureMetadataBuilder is the default metadata builder for media-capture dispatch.
// It produces the A2A skill metadata expected by the standard media-capture capability package.
func MediaCaptureMetadataBuilder(mode string, skillID string, ts int64) map[string]string {
	abilityDisplayName := "Record Video"
	timeout := "180000"
	if mode == "photo" {
		abilityDisplayName = "Take Photo"
		timeout = "120000"
	}
	return map[string]string{
		"a2a.skill_id":                              skillID,
		"a2a.ability_name":                            abilityDisplayName,
		"axon.exec.command":                         "sh {ability_package_root}/scripts/run.sh " + mode + " {node_id}",
		"axon.exec.timeout_ms":                      timeout,
		"axon.package.install_bootstrap_command":    "bash {ability_package_root}/scripts/bootstrap.sh",
		"axon.package.install_bootstrap_timeout_ms": "300000",
		"ability_dispatch.timestamp_unix_ms":        fmt.Sprintf("%d", ts),
	}
}

// MediaCaptureTaskPayloadBuilder is the default task-payload builder for media-capture dispatch.
// It produces the A2A task input expected by the standard media-capture capability package.
func MediaCaptureTaskPayloadBuilder(mode string, cfg Config, _ int64) map[string]any {
	payload := map[string]any{
		"camera_id":               cfg.CameraID,
		"prefer_real_camera":      true,
		"require_real":            cfg.RequireRealCapture(),
		"auto_request_permission": cfg.AutoRequestPermission,
	}
	if mode == "photo" {
		payload["resolution"] = cfg.PhotoResolution
		return payload
	}
	payload["resolution"] = cfg.VideoResolution
	payload["duration_seconds"] = cfg.DurationSeconds
	return payload
}

// NewAbilityDispatchCase creates a dispatch case instance.
// Neither metadataBuilder nor taskPayloadBuilder is wired by default; callers
// should provide them explicitly via options or use RunFromArgs which wires the
// media-capture presets.
func NewAbilityDispatchCase(cfg Config, opts ...AbilityDispatchCaseOption) *AbilityDispatchCase {
	instance := &AbilityDispatchCase{
		config: cfg,
		orchestratorBuild: func(next Config) (*easynet.Orchestrator, error) {
			orch := easynet.NewOrchestrator(
				easynet.WithEndpoint(next.Endpoint),
				easynet.WithTenant(next.Tenant),
				easynet.WithConnectTimeoutMs(next.ConnectTimeout),
			)
			if err := orch.Open(); err != nil {
				return nil, err
			}
			return orch, nil
		},
		now: time.Now,
	}
	for _, opt := range opts {
		opt(instance)
	}
	return instance
}

// NewCase preserves a small compatibility surface for existing call-sites.
func NewCase(cfg Config, opts ...AbilityDispatchCaseOption) *AbilityDispatchCase {
	return NewAbilityDispatchCase(cfg, opts...)
}

// WithOrchestratorFactory replaces how orchestrator is created.
func WithOrchestratorFactory(factory func(Config) (*easynet.Orchestrator, error)) AbilityDispatchCaseOption {
	return func(c *AbilityDispatchCase) {
		c.orchestratorBuild = factory
	}
}

// WithClock overrides timestamps for mode IDs and metadata.
func WithClock(clock func() time.Time) AbilityDispatchCaseOption {
	return func(c *AbilityDispatchCase) {
		c.now = clock
	}
}

// WithMetadataBuilder overrides capability metadata.
func WithMetadataBuilder(
	fn func(mode string, skillID string, ts int64) map[string]string,
) AbilityDispatchCaseOption {
	return func(c *AbilityDispatchCase) {
		c.metadataBuilder = fn
	}
}

// WithTaskPayloadBuilder overrides execution payloads.
func WithTaskPayloadBuilder(
	fn func(mode string, cfg Config, ts int64) map[string]any,
) AbilityDispatchCaseOption {
	return func(c *AbilityDispatchCase) {
		c.taskPayloadBuilder = fn
	}
}

// RunFromArgs is a convenience entrypoint for command args.
// It wires the media-capture presets (MediaCaptureModeSpecs, MediaCaptureMetadataBuilder,
// MediaCaptureTaskPayloadBuilder) by default; additional opts are applied after.
func RunFromArgs(argv []string, opts ...AbilityDispatchCaseOption) (map[string]any, error) {
	cfg, err := ParseConfigFromArgs(argv)
	if err != nil {
		return nil, err
	}
	// Wire media-capture presets explicitly; caller opts can still override.
	presets := []AbilityDispatchCaseOption{
		WithMetadataBuilder(MediaCaptureMetadataBuilder),
		WithTaskPayloadBuilder(MediaCaptureTaskPayloadBuilder),
	}
	allOpts := append(presets, opts...)
	return NewAbilityDispatchCase(cfg, allOpts...).Run()
}

// Run executes the whole case flow and returns JSON-serializable payload.
func (dispatcher *AbilityDispatchCase) Run() (map[string]any, error) {
	cfg := dispatcher.config
	cfg.Mode = cfg.ResolveMode()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.BundlePath == "" {
		bundlePath, err := cfg.ResolveBundlePath()
		if err != nil {
			return nil, err
		}
		cfg.BundlePath = bundlePath
	}

	orchestrator, err := dispatcher.orchestratorBuild(cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = orchestrator.Close()
	}()

	node, err := orchestrator.SelectNode(cfg.NodeID, cfg.OwnerID)
	if err != nil {
		return nil, err
	}
	nodeID := easynet.AsString(node["node_id"])
	if nodeID == "" {
		return nil, errors.New("selected node_id is empty")
	}

	bundle, err := orchestrator.ReadBundleFromPath(cfg.BundlePath)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"ok":               true,
		"endpoint":         cfg.Endpoint,
		"tenant":           cfg.Tenant,
		"selected_node_id": nodeID,
		"selected_node":    node,
		"bundle": map[string]any{
			"version": bundle.Version,
			"digest":  bundle.Digest,
		},
		"bundle_path": cfg.BundlePath,
	}

	var installRefs []easynet.InstallRef

	for _, mode := range []string{"photo", "video"} {
		if mode != cfg.Mode && cfg.Mode != "both" {
			continue
		}
		result, refs, err := dispatcher.runMode(orchestrator, nodeID, bundle, cfg, mode)
		if len(refs) > 0 {
			installRefs = append(installRefs, refs...)
		}
		if err != nil {
			if !cfg.KeepInstalled && len(installRefs) > 0 {
				out["cleanup"] = orchestrator.CleanupInstalls(installRefs)
			}
			return nil, err
		}
		out[mode] = result
	}

	if !cfg.KeepInstalled && len(installRefs) > 0 {
		out["cleanup"] = orchestrator.CleanupInstalls(installRefs)
	}

	return out, nil
}

// modeSpec looks up the ModeSpec for the given mode from the config's ModeSpecs map.
func modeSpec(mode string, cfg Config) (ModeSpec, error) {
	spec, ok := cfg.ModeSpecs[mode]
	if !ok {
		return ModeSpec{}, fmt.Errorf("mode %q not found in mode specs", mode)
	}
	return spec, nil
}

func (dispatcher *AbilityDispatchCase) runMode(
	orchestrator *easynet.Orchestrator,
	nodeID string,
	bundle easynet.BundleRef,
	cfg Config,
	mode string,
) (map[string]any, []easynet.InstallRef, error) {
	timestamp := dispatcher.now().UnixMilli()
	spec, err := modeSpec(mode, cfg)
	if err != nil {
		return nil, nil, err
	}
	capability := spec.CapabilityName
	skillID := fmt.Sprintf("%s_%d", spec.AbilityPrefix, timestamp)
	packageID := fmt.Sprintf("pkg.%s.%d", capability, timestamp)

	var metadata map[string]string
	if dispatcher.metadataBuilder != nil {
		metadata = dispatcher.metadataBuilder(mode, skillID, timestamp)
	}

	lifecycle, err := orchestrator.PublishInstallActivate(
		nodeID,
		packageID,
		capability,
		bundle,
		metadata,
	)
	if err != nil {
		return nil, nil, err
	}

	// Track install for cleanup.
	installID := strings.TrimSpace(fmt.Sprint(lifecycle.Install["install_id"]))
	var refs []easynet.InstallRef
	if installID != "" {
		refs = append(refs, easynet.InstallRef{
			Mode:      mode,
			NodeID:    nodeID,
			InstallID: installID,
		})
	}

	var taskPayload map[string]any
	if dispatcher.taskPayloadBuilder != nil {
		taskPayload = dispatcher.taskPayloadBuilder(mode, cfg, timestamp)
	}

	task, err := orchestrator.SendA2ATask(nodeID, skillID, taskPayload)
	if err != nil {
		return nil, refs, err
	}

	resultJSON, ok := task["result_json"].(map[string]any)
	if !ok {
		return nil, refs, errors.New("task.result_json missing or malformed")
	}

	mediaField := spec.MediaField
	media, ok := resultJSON[mediaField]
	if !ok || easynet.AsString(media) == "" {
		return nil, refs, fmt.Errorf("%s result field %q is empty", mode, mediaField)
	}

	mediaStr := strings.TrimSpace(fmt.Sprintf("%v", media))
	mediaBytes, err := base64.StdEncoding.DecodeString(mediaStr)
	if err != nil {
		return nil, refs, fmt.Errorf("failed to decode %s base64: %w", mediaField, err)
	}

	out := map[string]any{
		"lifecycle":        lifecycle,
		"task_result":      task,
		"result_json":      resultJSON,
		"skill_id":         skillID,
		"media_size_bytes": len(mediaBytes),
	}

	if !cfg.NoWriteMediaFiles {
		outputDir := cfg.OutputDir
		if outputDir == "" {
			outputDir = "output"
		}
		if mkErr := os.MkdirAll(outputDir, 0o755); mkErr != nil {
			return nil, refs, fmt.Errorf("failed to create output dir %s: %w", outputDir, mkErr)
		}
		ext := spec.MediaExt
		fileName := fmt.Sprintf("%s_%d.%s", mode, timestamp, ext)
		filePath := filepath.Join(outputDir, fileName)
		if writeErr := os.WriteFile(filePath, mediaBytes, 0o644); writeErr != nil {
			return nil, refs, fmt.Errorf("failed to write media file %s: %w", filePath, writeErr)
		}
		out["saved_file"] = filePath
	}

	return out, refs, nil
}
