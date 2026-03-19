// EasyNet Axon for AgentNet
// =========================
//
// File: sdk/go/easynet/orchestrator_bundle.go
// Description: Bundle reading, digest computation, and MANIFEST.json version extraction
//              for orchestrator-driven capability workflows.
//
// Author: Silan.Hu
// Email: silan.hu@u.nus.edu
// Copyright (c) 2026-2027 easynet. All rights reserved.

package easynet

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BundleRef represents a pre-read bundle with derived attributes.
type BundleRef struct {
	Bytes   []byte
	Base64  string
	Digest  string // sha256:<hex>
	Version string // from MANIFEST.json
}

// ReadBundleFromPath reads a .tar.gz bundle and derives digest/version.
func (o *Orchestrator) ReadBundleFromPath(path string) (BundleRef, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return BundleRef{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return BundleRef{}, fmt.Errorf("bundle not found: %s", abs)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return BundleRef{}, err
	}
	if len(raw) == 0 {
		return BundleRef{}, fmt.Errorf("bundle is empty: %s", abs)
	}
	ver, err := readBundleVersion(raw)
	if err != nil {
		return BundleRef{}, err
	}
	return BundleRef{
		Bytes:   raw,
		Base64:  base64.StdEncoding.EncodeToString(raw),
		Digest:  sha256Prefixed(raw),
		Version: ver,
	}, nil
}

func sha256Prefixed(payload []byte) string {
	d := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(d[:])
}

func readBundleVersion(bundleBytes []byte) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(bundleBytes))
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
		name := strings.TrimSpace(hdr.Name)
		if name == "MANIFEST.json" || strings.HasSuffix(name, "/MANIFEST.json") {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return "", err
			}
			var manifest map[string]any
			if err := json.Unmarshal(raw, &manifest); err != nil {
				return "", err
			}
			version := strings.TrimSpace(fmt.Sprint(manifest["version"]))
			if version == "" {
				return "", errors.New("bundle MANIFEST.json missing non-empty version")
			}
			return version, nil
		}
	}
	return "", errors.New("bundle MANIFEST.json not found")
}
