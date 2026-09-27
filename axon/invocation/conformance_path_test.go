package axon

import (
	"os"
	"path/filepath"
	"testing"
)

// conformancePath reads canonical vectors packaged with the Go module.
func conformancePath(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"testdata", "conformance"}, parts...)...)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("packaged conformance data missing: %s: %v", path, err)
	}
	return path
}
