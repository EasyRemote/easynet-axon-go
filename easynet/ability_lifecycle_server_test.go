package easynet

import (
	"strings"
	"testing"
)

func TestNormalizeHubEndpointConvertsAxonURI(t *testing.T) {
	got := normalizeHubEndpoint("axon://hub.easynet.run:50084")
	want := "http://hub.easynet.run:50084"
	if got != want {
		t.Fatalf("unexpected normalized hub endpoint: got %q want %q", got, want)
	}
}

func TestFederationSpawnEnvUsesProvidedOverrides(t *testing.T) {
	env := federationSpawnEnv("axon://hub.easynet.run:50084", "tenant-test", "runtime-a", "shared-fed-secret")
	if len(env) != 4 {
		t.Fatalf("expected 4 federation env entries, got %d", len(env))
	}
	got := map[string]string{}
	for _, entry := range env {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid env entry %q", entry)
		}
		got[parts[0]] = parts[1]
	}
	if got["AXON_HUB"] != "http://hub.easynet.run:50084" {
		t.Fatalf("unexpected AXON_HUB %q", got["AXON_HUB"])
	}
	if got["AXON_FEDERATION_TENANT"] != "tenant-test" {
		t.Fatalf("unexpected AXON_FEDERATION_TENANT %q", got["AXON_FEDERATION_TENANT"])
	}
	if got["AXON_FEDERATION_LABEL"] != "runtime-a" {
		t.Fatalf("unexpected AXON_FEDERATION_LABEL %q", got["AXON_FEDERATION_LABEL"])
	}
	if got["AXON_HUB_JOIN_TOKEN"] != "shared-fed-secret" {
		t.Fatalf("unexpected AXON_HUB_JOIN_TOKEN %q", got["AXON_HUB_JOIN_TOKEN"])
	}
}
