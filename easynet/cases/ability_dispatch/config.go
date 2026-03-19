package abilitydispatch

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"easynet.run/axon/sdk/go/easynet"
)

const (
	defaultTenant          = "tenant-test"
	defaultMode            = "both"
	defaultNodeID          = ""
	defaultOwnerID         = ""
	defaultBundlePath      = ""
	defaultCameraID        = "default"
	defaultPhotoRes        = "1280x720"
	defaultVideoRes        = "1280x720"
	defaultDurationSeconds = 3
	defaultConnectTimeout  = 5000
)

var defaultBundleFilePrefixes = []string{"ability-dispatch-media-capability-", "case01-media-capability-"}

const (
	ModePhoto = "photo"
	ModeVideo = "video"
	ModeBoth  = "both"
)

// ModeSpec describes one dispatch mode's capability and media field mapping.
type ModeSpec struct {
	CapabilityName string // e.g. "desktop_camera_photo"
	AbilityPrefix    string // e.g. "take_photo" -> skill_id = "take_photo_{ts}"
	MediaField     string // e.g. "image_base64"
	MediaExt       string // e.g. "jpg"
}

// MediaCaptureModeSpecs returns the default photo/video media-capture mode specs.
func MediaCaptureModeSpecs() map[string]ModeSpec {
	return map[string]ModeSpec{
		"photo": {CapabilityName: "desktop_camera_photo", AbilityPrefix: "take_photo", MediaField: "image_base64", MediaExt: "jpg"},
		"video": {CapabilityName: "desktop_camera_video", AbilityPrefix: "record_video", MediaField: "video_base64", MediaExt: "mp4"},
	}
}

// Config is the runtime input shape for media-capability dispatch.
type Config struct {
	Endpoint              string
	Tenant                string
	NodeID                string
	OwnerID               string
	ConnectTimeout        int
	BundlePath            string
	Mode                  string
	CameraID              string
	PhotoResolution       string
	VideoResolution       string
	DurationSeconds       int
	AutoRequestPermission bool
	OutputDir             string
	NoWriteMediaFiles     bool
	KeepInstalled         bool
	RequireReal           bool
	AllowMockFallback     bool
	ModeSpecs             map[string]ModeSpec
	BundleFilePrefixes    []string
}

// ParseConfigFromArgs parses command line args for media-capability dispatch.
func ParseConfigFromArgs(argv []string) (Config, error) {
	fs := flag.NewFlagSet("ability-dispatch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	cfg := Config{
		Endpoint:           easynet.DefaultEndpoint,
		Tenant:             defaultTenant,
		NodeID:             defaultNodeID,
		OwnerID:            defaultOwnerID,
		ConnectTimeout:     defaultConnectTimeout,
		BundlePath:         defaultBundlePath,
		Mode:               defaultMode,
		CameraID:           defaultCameraID,
		PhotoResolution:    defaultPhotoRes,
		VideoResolution:    defaultVideoRes,
		DurationSeconds:    defaultDurationSeconds,
		ModeSpecs:          MediaCaptureModeSpecs(),
		BundleFilePrefixes: append([]string{}, defaultBundleFilePrefixes...),
	}

	var bundleFilePrefixesRaw string

	fs.StringVar(&cfg.Endpoint, "endpoint", easynet.DefaultEndpoint, "Axon endpoint")
	fs.StringVar(&cfg.Tenant, "tenant", defaultTenant, "tenant id")
	fs.StringVar(&cfg.NodeID, "node-id", defaultNodeID, "target node; empty for first online node")
	fs.StringVar(&cfg.OwnerID, "owner-id", defaultOwnerID, "optional owner filter")
	fs.IntVar(&cfg.ConnectTimeout, "connect-timeout-ms", defaultConnectTimeout, "bridge connect timeout")
	fs.StringVar(&cfg.BundlePath, "bundle-path", defaultBundlePath, "capability package path")
	fs.StringVar(&cfg.Mode, "mode", defaultMode, "photo|video|both")
	fs.StringVar(&cfg.CameraID, "camera-id", defaultCameraID, "camera id")
	fs.StringVar(&cfg.PhotoResolution, "photo-resolution", defaultPhotoRes, "photo resolution")
	fs.StringVar(&cfg.VideoResolution, "video-resolution", defaultVideoRes, "video resolution")
	fs.IntVar(&cfg.DurationSeconds, "duration-seconds", defaultDurationSeconds, "video duration seconds")
	fs.BoolVar(&cfg.AutoRequestPermission, "auto-request-permission", false, "request camera permission on client host")
	fs.StringVar(&cfg.OutputDir, "output-dir", "", "output directory for media files")
	fs.BoolVar(&cfg.NoWriteMediaFiles, "no-write-media-files", false, "disable file writes")
	fs.BoolVar(&cfg.KeepInstalled, "keep-installed", false, "skip cleanup")
	fs.BoolVar(&cfg.RequireReal, "require-real", false, "force real capture")
	fs.BoolVar(&cfg.AllowMockFallback, "allow-mock-fallback", false, "allow mock fallback")
	fs.StringVar(&bundleFilePrefixesRaw, "bundle-file-prefixes", "", "comma-separated bundle filename prefixes")

	if err := fs.Parse(argv); err != nil {
		return cfg, err
	}

	if bundleFilePrefixesRaw != "" {
		parts := strings.Split(bundleFilePrefixesRaw, ",")
		prefixes := make([]string, 0, len(parts))
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				prefixes = append(prefixes, trimmed)
			}
		}
		if len(prefixes) > 0 {
			cfg.BundleFilePrefixes = prefixes
		}
	}

	cfg = cfg.Normalize()
	return cfg, cfg.Validate()
}

// Validate checks runtime args (read-only — use Normalize for defaults).
func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return errors.New("--endpoint is required")
	}
	if strings.TrimSpace(cfg.Tenant) == "" {
		return errors.New("--tenant is required")
	}
	if cfg.ConnectTimeout <= 0 {
		return errors.New("--connect-timeout-ms must be > 0")
	}
	if cfg.DurationSeconds <= 0 {
		return errors.New("--duration-seconds must be > 0")
	}
	if !cfg.ModeValid() {
		return errors.New("--mode must be one of: photo, video, both")
	}
	if len(cfg.ModeSpecs) == 0 {
		return errors.New("ModeSpecs must not be empty")
	}
	return nil
}

// Normalize trims and normalizes shared fields.
func (cfg Config) Normalize() Config {
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.Tenant = strings.TrimSpace(cfg.Tenant)
	cfg.NodeID = strings.TrimSpace(cfg.NodeID)
	cfg.OwnerID = strings.TrimSpace(cfg.OwnerID)
	cfg.BundlePath = strings.TrimSpace(cfg.BundlePath)
	cfg.Mode = cfg.ResolveMode()
	cfg.CameraID = strings.TrimSpace(cfg.CameraID)
	if cfg.CameraID == "" {
		cfg.CameraID = defaultCameraID
	}
	cfg.PhotoResolution = strings.TrimSpace(cfg.PhotoResolution)
	if cfg.PhotoResolution == "" {
		cfg.PhotoResolution = defaultPhotoRes
	}
	cfg.VideoResolution = strings.TrimSpace(cfg.VideoResolution)
	if cfg.VideoResolution == "" {
		cfg.VideoResolution = defaultVideoRes
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.DurationSeconds <= 0 {
		cfg.DurationSeconds = defaultDurationSeconds
	}
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = defaultMode
	}
	return cfg
}

// RequireRealCapture computes whether this run must force real capture.
func (cfg Config) RequireRealCapture() bool {
	return cfg.RequireReal || !cfg.AllowMockFallback
}

// ResolveBundlePath resolves an empty bundle path to the latest discovered package.
func (cfg Config) ResolveBundlePath() (string, error) {
	if strings.TrimSpace(cfg.BundlePath) != "" {
		return cfg.BundlePath, nil
	}
	prefixes := cfg.BundleFilePrefixes
	if len(prefixes) == 0 {
		prefixes = defaultBundleFilePrefixes
	}
	return DefaultBundlePath(prefixes...)
}

// ModeValid reports whether this mode is supported.
func (cfg Config) ModeValid() bool {
	switch strings.ToLower(strings.TrimSpace(cfg.Mode)) {
	case ModePhoto, ModeVideo, ModeBoth:
		return true
	default:
		return false
	}
}

// ResolveMode returns mode with trim/lowercase canonicalization.
func (cfg Config) ResolveMode() string {
	return strings.ToLower(strings.TrimSpace(cfg.Mode))
}

// DefaultBundlePath tries to discover the latest media capability bundle if not explicitly set.
// The optional prefixes parameter overrides the default filename prefix list used to match bundles.
func DefaultBundlePath(prefixes ...string) (string, error) {
	if len(prefixes) == 0 {
		prefixes = defaultBundleFilePrefixes
	}
	paths := candidateSearchRoots()
	for _, dir := range paths {
		bundles, err := findBundleFiles(dir, prefixes)
		if err != nil {
			continue
		}
		if len(bundles) == 0 {
			continue
		}
		return latestBundlePath(bundles)
	}
	return "", os.ErrNotExist
}

func latestBundlePath(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", os.ErrNotExist
	}
	best := paths[0]
	bestMtime := int64(0)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		mt := info.ModTime().UnixNano()
		if mt > bestMtime {
			bestMtime = mt
			best = path
		}
	}
	return best, nil
}

func findBundleFiles(dir string, prefixes []string) ([]string, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		name := item.Name()
		if !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		matched := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out, nil
}

func candidateSearchRoots() []string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cwd = filepath.Clean(cwd)

	roots := make([]string, 0, 6)
	current := cwd
	for {
		roots = append(roots, current)
		if len(roots) >= 6 {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	paths := make([]string, 0, len(roots)*3)
	seen := make(map[string]struct{}, len(roots)*3)
	for _, root := range roots {
		candidates := []string{
			root,
			filepath.Join(root, "capability-package", "dist"),
			filepath.Join(root, "dist"),
		}
		for _, candidate := range candidates {
			candidate = filepath.Clean(candidate)
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			paths = append(paths, candidate)
		}
	}
	return paths
}
