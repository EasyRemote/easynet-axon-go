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

func TestRemoteControlToolSpecsExposeMetadataParity(t *testing.T) {
	specs := remoteControlToolSpecs()
	for _, toolName := range []string{"deploy_ability", "package_ability", "deploy_ability_package"} {
		var found map[string]any
		for _, spec := range specs {
			if spec["name"] == toolName {
				found = spec
				break
			}
		}
		if found == nil {
			t.Fatalf("expected tool spec %q to be present", toolName)
		}
		inputSchema, _ := found["inputSchema"].(map[string]any)
		properties, _ := inputSchema["properties"].(map[string]any)
		metadata, ok := properties["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("expected %q to expose metadata property", toolName)
		}
		if metadata["type"] != "object" {
			t.Fatalf("expected %q metadata property type to be object, got %v", toolName, metadata["type"])
		}
	}
}
