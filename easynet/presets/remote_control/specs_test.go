package remotecontrol

import "testing"

func TestRemoteControlToolSpecsIncludeLifecycleParityTools(t *testing.T) {
	specs := remoteControlToolSpecs()
	names := map[string]bool{}
	for _, spec := range specs {
		name, _ := spec["name"].(string)
		names[name] = true
	}

	for _, required := range []string{"disconnect_device", "uninstall_ability"} {
		if !names[required] {
			t.Fatalf("expected tool spec %q to be present", required)
		}
	}
}

func TestRemoteControlDefaultInstallTimeoutMatchesCanonicalValue(t *testing.T) {
	if defaultInstallTimeoutSeconds != 45 {
		t.Fatalf("expected default install timeout to be 45 seconds, got %d", defaultInstallTimeoutSeconds)
	}
}
