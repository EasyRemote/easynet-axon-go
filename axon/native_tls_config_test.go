//go:build cgo

package axon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func nativeOpenCapture(t *testing.T) (string, func() []map[string]any) {
	t.Helper()
	dir := t.TempDir()
	library := filepath.Join(dir, "capture.so")
	args := []string{"-shared", "-fPIC", "testdata/native_open_capture.c", "-o", library}
	if runtime.GOOS == "darwin" {
		args[0] = "-dynamiclib"
	}
	if output, err := exec.Command("cc", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile FFI capture: %v: %s", err, output)
	}
	capture := filepath.Join(dir, "opens.jsonl")
	t.Setenv("AXON_TEST_OPEN_CAPTURE", capture)
	return library, func() []map[string]any {
		t.Helper()
		data, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatal(err)
			}
			out = append(out, entry)
		}
		return out
	}
}

func TestNativeTLSOptionsCrossCGOBoundary(t *testing.T) {
	library, captured := nativeOpenCapture(t)
	bridge, err := OpenDendriteBridge(library)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.CloseLibrary()
	empty, ca := "", "-----BEGIN CERTIFICATE-----\npublic-test-ca\n-----END CERTIFICATE-----\n"
	for _, pem := range []*string{nil, &empty, &ca} {
		handle, err := bridge.OpenClientWithOptions("https://localhost:50051", DendriteClientOptions{TLSCAPEM: pem})
		if err != nil {
			t.Fatal(err)
		}
		if err := bridge.CloseClient(handle); err != nil {
			t.Fatal(err)
		}
	}
	entries := captured()
	if len(entries) != 3 {
		t.Fatalf("opens: %v", entries)
	}
	if _, exists := entries[0]["tls_ca_pem"]; exists {
		t.Fatal("nil CA must be omitted")
	}
	if entries[1]["tls_ca_pem"] != "" || entries[2]["tls_ca_pem"] != ca {
		t.Fatal("explicit CA changed")
	}
	for _, entry := range entries {
		if entry["endpoint"] != "https://localhost:50051" || entry["connect_timeout_ms"] != float64(DefaultConnectTimeoutMs) {
			t.Fatalf("connection changed: %v", entry)
		}
	}
}

func TestSidecarTLSReconnectKeepsTrust(t *testing.T) {
	library, captured := nativeOpenCapture(t)
	ca := "public-test-ca"
	s := &SidecarTransport{Endpoint: "https://localhost:50051", LibraryPath: library, TLSCAPEM: &ca}
	defer s.Close()
	if err := s.ensureClient(); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual reconnect path synchronously without starting a timer worker.
	s.reconnect = &reconnectState{options: ReconnectOptions{MaxAttempts: 1}}
	s.reconnect.disconnected.Store(true)
	s.driveReconnect(context.Background())
	if s.LastReconnectError() != nil || s.ReconnectGeneration() != 1 {
		t.Fatalf("reconnect: %v", s.LastReconnectError())
	}
	entries := captured()
	if len(entries) != 2 || !reflect.DeepEqual(entries[0], entries[1]) || entries[1]["tls_ca_pem"] != ca {
		t.Fatalf("trust changed: %v", entries)
	}
}

func TestSidecarTLSRejectionDoesNotDowngrade(t *testing.T) {
	library, captured := nativeOpenCapture(t)
	t.Setenv("AXON_TEST_OPEN_REJECT", "1")
	ca := "public-test-ca"
	s := &SidecarTransport{Endpoint: "https://localhost:50051", LibraryPath: library, TLSCAPEM: &ca}
	defer s.Close()
	if err := s.ensureClient(); err == nil || !strings.Contains(err.Error(), "TLS trust rejected") {
		t.Fatalf("native error: %v", err)
	}
	if s.bridge != nil || s.handle != 0 {
		t.Fatal("failed connection retained")
	}
	entries := captured()
	if len(entries) != 1 || entries[0]["tls_ca_pem"] != ca || entries[0]["endpoint"] != s.Endpoint {
		t.Fatalf("unexpected retry or trust change: %v", entries)
	}
}

func TestSignedClientTLSRejectionPreservesRequest(t *testing.T) {
	library, captured := nativeOpenCapture(t)
	t.Setenv("AXON_TEST_OPEN_REJECT", "1")
	bridge, err := OpenDendriteBridge(library)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.CloseLibrary()
	// Public test-only seed; this only checks a rejected request, not native signing support.
	signing, err := NewSigningConfig(make([]byte, 32), "easynet:///r/test/agent/caller")
	if err != nil {
		t.Fatal(err)
	}
	ca := "public-test-ca"
	_, err = bridge.OpenSignedClientWithOptions("https://localhost:50051", signing, DendriteClientOptions{TLSCAPEM: &ca})
	if err == nil || !strings.Contains(err.Error(), "TLS trust rejected") {
		t.Fatalf("native rejection: %v", err)
	}
	entries := captured()
	if len(entries) != 1 || entries[0]["tls_ca_pem"] != ca || entries[0]["signing"] == nil {
		t.Fatal("request lost trust or signing configuration")
	}
}
