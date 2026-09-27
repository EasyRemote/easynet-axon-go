package axon

import "testing"

func TestUraValueObjectBuildsAndParsesCanonicalShapes(t *testing.T) {
	ability := AbilityURA("localhost", "dev", "frontend-engineer", "chat")
	if ability != "easynet:///r/localhost/ability/dev.frontend-engineer.chat" {
		t.Fatalf("ability URA = %q", ability)
	}
	parsed, err := ParseURAParts(ability)
	if err != nil {
		t.Fatalf("parse ability URA: %v", err)
	}
	if parsed.Kind != URAKindAbility || parsed.UserID != "dev" || parsed.AgentID != "frontend-engineer" || parsed.AbilityID != "chat" {
		t.Fatalf("parsed ability = %#v", parsed)
	}
	if parsed.AbilityOwner.Kind != AbilityOwnerAgent ||
		parsed.AbilityOwner.UserID != "dev" ||
		parsed.AbilityOwner.AgentID != "frontend-engineer" ||
		parsed.AbilityLocalName != "chat" {
		t.Fatalf("typed parsed ability owner = %#v", parsed)
	}
	uAbility, err := ParseURA(ability)
	if err != nil {
		t.Fatalf("parse ability value object: %v", err)
	}
	if uAbility.AbilityName() != "chat" {
		t.Fatalf("ability name = %q, want chat", uAbility.AbilityName())
	}
	if AbilityNameFromURA(AuthorityAbilityURA("localhost", "runtime.forward")) != "runtime.forward" {
		t.Fatalf("authority ability name projection failed")
	}
	if AuthorityAbilityURA("localhost", "01AUTHORITY.runtime.forward") != "" {
		t.Fatalf("retired 01AUTHORITY authority ability prefix must not be accepted")
	}
	if PublicAbilityNameForOwner(AgentURA("localhost", "dev", "frontend-engineer"), "frontend-engineer.chat") != "frontend-engineer.chat" {
		t.Fatalf("agent owner public ability name projection failed")
	}
	if PublicAbilityNameForOwner(AuthorityURA("localhost"), "runtime.forward") != "runtime.forward" {
		t.Fatalf("authority owner public ability name projection failed")
	}
	if PublicAbilityNameForOwner(AuthorityURA("localhost"), "authority.runtime.forward") != "authority.runtime.forward" {
		t.Fatalf("authority owner public ability name projection must preserve registered name")
	}
	if PublicAbilityNameForOwner(DeviceURA("localhost", "device-a"), "runtime.inspect") != "" {
		t.Fatalf("device owner public ability name projection must fail closed")
	}
	if AuthorityURA("localhost") != "easynet:///r/localhost/authority" {
		t.Fatalf("authority URA = %q", AuthorityURA("localhost"))
	}
	if name, ok := PublicAbilityNameFromAbilityURA(AgentURA("localhost", "dev", "frontend-engineer"), ability); !ok || name != "chat" {
		t.Fatalf("owner + ability URA public name = %q %v, want chat true", name, ok)
	}
	deviceAbility := DeviceAbilityURA("localhost", "device-a", "runtime", "inspect")
	deviceAbilityParts, err := ParseURAParts(deviceAbility)
	if err != nil {
		t.Fatalf("parse device ability URA: %v", err)
	}
	if deviceAbilityParts.Kind != URAKindAbility ||
		deviceAbilityParts.AbilityOwner.Kind != AbilityOwnerDevice ||
		deviceAbilityParts.AbilityOwner.DeviceID != "device-a" ||
		deviceAbilityParts.AbilityNamespace != "runtime" ||
		deviceAbilityParts.AbilityLocalName != "inspect" ||
		deviceAbilityParts.AbilityID != "runtime.inspect" {
		t.Fatalf("parsed device ability = %#v", deviceAbilityParts)
	}
	if name, ok := PublicAbilityNameFromAbilityURA(DeviceURA("localhost", "device-a"), deviceAbility); ok || name != "" {
		t.Fatalf("device owner + ability URA public name = %q %v, want empty false", name, ok)
	}

	u, err := ParseURA(DeviceURA("localhost", "device-a"))
	if err != nil {
		t.Fatalf("parse device URA: %v", err)
	}
	if u.String() != "easynet:///r/localhost/device/device-a" || u.Kind() != URAKindDevice {
		t.Fatalf("device value object mismatch: %q %s", u.String(), u.Kind())
	}
}

func TestUraRejectsTransportEndpoint(t *testing.T) {
	if _, err := ParseURA("axon://localhost:50051"); err == nil {
		t.Fatal("transport endpoints must not parse as URAs")
	}
}

func TestDeviceSponsoredSystemAgentGrammarMatchesRustReference(t *testing.T) {
	raw := DeviceAgentURA("localhost", "dev-1", "palimpzest")
	if raw != "easynet:///r/localhost/agent/device.dev-1.palimpzest" {
		t.Fatalf("device-owned Agent URA = %q", raw)
	}
	parts, err := ParseURAParts(raw)
	if err != nil {
		t.Fatalf("parse device-owned Agent URA: %v", err)
	}
	if parts.Kind != URAKindAgent || parts.DeviceID != "dev-1" || parts.AgentID != "palimpzest" || parts.UserID != "" {
		t.Fatalf("device-owned Agent parts = %#v", parts)
	}
	if DisplayID(raw) != "device.dev-1.palimpzest" {
		t.Fatalf("device-owned Agent display = %q", DisplayID(raw))
	}
	ability := OwnerAbilityURA(raw, "shell.run")
	if ability != "easynet:///r/localhost/ability/system-agent.dev-1.palimpzest.shell.run" {
		t.Fatalf("device-sponsored SystemAgent ability = %q", ability)
	}
	parsedAbility, err := ParseURAParts(ability)
	if err != nil {
		t.Fatalf("parse SystemAgent ability: %v", err)
	}
	if parsedAbility.AbilityOwner.Kind != AbilityOwnerSystemAgent ||
		parsedAbility.AbilityOwner.DeviceID != "dev-1" ||
		parsedAbility.AbilityOwner.AgentID != "palimpzest" {
		t.Fatalf("SystemAgent owner projection = %#v", parsedAbility.AbilityOwner)
	}
	if name, ok := PublicAbilityNameFromAbilityURA(raw, ability); !ok || name != "shell.run" {
		t.Fatalf("device-owned Agent public ability = %q %v", name, ok)
	}

	for _, invalid := range []string{
		"easynet:///r/localhost/agent/device.justone",
		"easynet:///r/localhost/agent/device..terminal",
		"easynet:///r/localhost/agent/device.dev-1.terminal.extra",
		"easynet:///r/localhost/agent/authority.bot",
	} {
		if _, err := ParseURAParts(invalid); err == nil {
			t.Fatalf("invalid Agent URA accepted: %s", invalid)
		}
	}
	if _, err := ParseURAParts("easynet:///r/localhost/agent/authorityx.bot"); err != nil {
		t.Fatalf("reserved-token lookalike must remain valid: %v", err)
	}
}

func TestOwnerAbilityURABuildsCanonicalOwnerTokenShape(t *testing.T) {
	tests := []struct {
		name        string
		ownerURA    string
		abilityName string
		want        string
	}{
		{
			name:        "agent owner",
			ownerURA:    AgentURA("localhost", "dev", "frontend-engineer"),
			abilityName: "chat",
			want:        "easynet:///r/localhost/ability/dev.frontend-engineer.chat",
		},
		{
			name:        "authority owner",
			ownerURA:    AuthorityURA("localhost"),
			abilityName: "runtime.forward",
			want:        "easynet:///r/localhost/ability/authority.runtime.forward",
		},
		{
			name:        "direct Device owner rejected",
			ownerURA:    DeviceURA("localhost", "dev-1"),
			abilityName: "invocation.history.list",
			want:        "",
		},
		{
			name:        "authority owner accepts explicit owner token",
			ownerURA:    AuthorityURA("localhost"),
			abilityName: "authority.runtime.forward",
			want:        "easynet:///r/localhost/ability/authority.authority.runtime.forward",
		},
		{
			name:        "bare authority ability rejected",
			ownerURA:    AuthorityURA("localhost"),
			abilityName: "chat",
			want:        "",
		},
		{
			name:        "user is not publisher",
			ownerURA:    UserURA("localhost", "alice"),
			abilityName: "chat",
			want:        "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OwnerAbilityURA(tt.ownerURA, tt.abilityName); got != tt.want {
				t.Fatalf("OwnerAbilityURA() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResourceDotURAPreservesOpaqueResourcePath(t *testing.T) {
	got := ResourceDotURA("localhost", "alice", "/documents/report.txt")
	want := "easynet:///r/localhost/resource/alice/documents/report.txt"
	if got != want {
		t.Fatalf("ResourceDotURA() = %q, want %q", got, want)
	}

	parts, err := ParseURAParts(got)
	if err != nil {
		t.Fatalf("parse resource URA: %v", err)
	}
	if parts.Kind != URAKindResource ||
		parts.OwnerID != "alice" ||
		parts.Path != "documents/report.txt" {
		t.Fatalf("parsed resource = %#v", parts)
	}
	if _, err := ParseURAParts("easynet:///r/localhost/resource/alice"); err != nil {
		t.Fatalf("owner-only resource URA must remain valid: %v", err)
	}
}

func TestResourceDotURAAllowsStructuredOwnerAndPath(t *testing.T) {
	raw := ResourceDotURA(
		"localhost",
		"agent.alice.codex",
		"provider/catalog/references/schema.md",
	)
	parts, err := ParseURAParts(raw)
	if err != nil {
		t.Fatalf("parse dot-owner resource: %v", err)
	}
	if parts.Kind != URAKindResource ||
		parts.OwnerID != "agent.alice.codex" ||
		parts.Path != "provider/catalog/references/schema.md" {
		t.Fatalf("parsed dot-owner resource = %#v", parts)
	}
}

func TestDisplayIDProjectsCanonicalURA(t *testing.T) {
	if DisplayID(AuthorityAbilityURA("localhost", "runtime.forward")) != "authority.runtime.forward" {
		t.Fatalf("authority ability display projection failed")
	}
	if DisplayID(DeviceAbilityURA("localhost", "device-a", "runtime", "inspect")) != "device.device-a.runtime.inspect" {
		t.Fatalf("device ability display projection failed")
	}
	if DisplayID(ResourceDotURA("localhost", "alice", "documents/notes.md")) != "alice/documents/notes.md" {
		t.Fatalf("resource display projection failed")
	}
	if stale := "easynet:///r/localhost/agent/legacy"; DisplayID(stale) != stale {
		t.Fatalf("invalid URA display should preserve input")
	}
}
