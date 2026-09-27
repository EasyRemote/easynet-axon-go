package axon

import (
	"fmt"
	"strings"
)

const URAScheme = "easynet:///r/"

type URAKind string

const (
	URAKindUnknown   URAKind = "unknown"
	URAKindUser      URAKind = "user"
	URAKindDevice    URAKind = "device"
	URAKindService   URAKind = "service"
	URAKindAgent     URAKind = "agent"
	URAKindAbility   URAKind = "ability"
	URAKindAuthority URAKind = "authority"
	URAKindResource  URAKind = "resource"
)

// Ura is the Go SDK value object for canonical URAs.
//
// Axon-owned code should construct URAs through this object or the
// package-level façade functions below. Application layers should treat
// returned values as opaque protocol addresses and avoid rebuilding them
// from path fragments.
type Ura struct {
	raw string
}

type ParsedURA struct {
	Raw       string
	Realm     string
	Kind      URAKind
	UserID    string
	DeviceID  string
	AgentID   string
	ServiceID string
	AbilityID string
	// AbilityOwner is populated only for Kind == URAKindAbility. It is the
	// owner-token-first RFC-005 parse result. UserID/AgentID/DeviceID and
	// AbilityID remain compatibility projections for older callers; new code
	// should use AbilityOwner plus AbilityNamespace/AbilityLocalName when it
	// needs ownership semantics.
	AbilityOwner     AbilityOwner
	AbilityNamespace string
	AbilityLocalName string
	OwnerID          string
	Path             string
}

func ParseURA(raw string) (Ura, error) {
	if _, err := ParseURAParts(raw); err != nil {
		return Ura{}, err
	}
	return Ura{raw: raw}, nil
}

func (u Ura) String() string { return u.raw }

func (u Ura) Parts() ParsedURA {
	parts, err := ParseURAParts(u.raw)
	if err != nil {
		panic("Ura stores only validated canonical addresses")
	}
	return parts
}

func (u Ura) Kind() URAKind { return u.Parts().Kind }

func (u Ura) AbilityName() string {
	parts := u.Parts()
	if parts.Kind != URAKindAbility {
		return ""
	}
	switch parts.UserID {
	case "authority":
		return parts.AbilityID
	case "device":
		return parts.AbilityID
	default:
		return parts.AbilityID
	}
}

func (u Ura) PublicAbilityName(registeredName string) string {
	registeredName = strings.TrimSpace(registeredName)
	if registeredName == "" {
		return ""
	}
	return registeredName
}

func (u Ura) PublicAbilityNameForOwner(ownerURA string) string {
	name, ok := PublicAbilityNameFromAbilityURA(ownerURA, u.raw)
	if !ok {
		return ""
	}
	return name
}

func AbilityNameFromURA(raw string) string {
	u, err := ParseURA(raw)
	if err != nil {
		return ""
	}
	return u.AbilityName()
}

func PublicAbilityNameForOwner(ownerURA, registeredName string) string {
	owner, err := ParseURA(ownerURA)
	if err != nil {
		return ""
	}
	switch owner.Kind() {
	case URAKindAgent, URAKindService, URAKindAuthority:
	default:
		return ""
	}
	return Ura{}.PublicAbilityName(registeredName)
}

// OwnerAbilityURA returns the canonical `/ability/` URA an identity owner
// publishes a named ability under.
//
// This is the Go SDK counterpart of the Rust `owner_ability_ura` helper:
// Agent owners publish `<user>.<agent>.<ability>`, device-sponsored Agents
// publish `system-agent.<device>.<agent>.<ability>`, and Authority owners
// publish `authority.<namespace>.<verb>`. Direct Device ability syntax remains
// parseable only for migration; ordinary public abilities use a SystemAgent.
func OwnerAbilityURA(ownerURA, abilityName string) string {
	ownerURA = strings.TrimSpace(ownerURA)
	abilityName = strings.TrimSpace(abilityName)
	if ownerURA == "" || abilityName == "" {
		return ""
	}
	owner, err := ParseURAParts(ownerURA)
	if err != nil {
		return ""
	}
	switch owner.Kind {
	case URAKindAgent:
		if owner.DeviceID != "" {
			return SystemAgentAbilityURA(
				owner.Realm,
				owner.DeviceID,
				owner.AgentID,
				abilityName,
			)
		}
		if owner.UserID == "" || owner.AgentID == "" {
			return ""
		}
		return AbilityURA(owner.Realm, owner.UserID, owner.AgentID, abilityName)
	case URAKindService:
		if owner.UserID == "" || owner.ServiceID == "" {
			return ""
		}
		return ServiceAbilityURA(owner.Realm, owner.UserID, owner.ServiceID, abilityName)
	case URAKindAuthority:
		if !strings.Contains(abilityName, ".") {
			return ""
		}
		if strings.HasPrefix(abilityName, "01AUTHORITY.") {
			return ""
		}
		return fmt.Sprintf("%s%s/ability/authority.%s", URAScheme, owner.Realm, abilityName)
	default:
		return ""
	}
}

func PublicAbilityNameFromAbilityURA(ownerURA, abilityURA string) (string, bool) {
	owner, ownerErr := ParseURA(ownerURA)
	ability, abilityErr := ParseURA(abilityURA)
	if ownerErr != nil || abilityErr != nil {
		return "", false
	}
	ownerParts := owner.Parts()
	abilityParts := ability.Parts()
	if abilityParts.Kind != URAKindAbility {
		return "", false
	}
	switch ownerParts.Kind {
	case URAKindAgent:
		if ownerParts.DeviceID != "" &&
			ownerParts.Realm == abilityParts.Realm &&
			abilityParts.AbilityOwner.Kind == AbilityOwnerSystemAgent &&
			ownerParts.DeviceID == abilityParts.AbilityOwner.DeviceID &&
			ownerParts.AgentID == abilityParts.AbilityOwner.AgentID {
			return abilityParts.AbilityID, true
		}
		if ownerParts.Realm == abilityParts.Realm &&
			abilityParts.AbilityOwner.Kind == AbilityOwnerAgent &&
			ownerParts.UserID == abilityParts.AbilityOwner.UserID &&
			ownerParts.AgentID == abilityParts.AbilityOwner.AgentID {
			return abilityParts.AbilityID, true
		}
	case URAKindAuthority:
		if ownerParts.Realm == abilityParts.Realm &&
			abilityParts.AbilityOwner.Kind == AbilityOwnerAuthority {
			return ability.AbilityName(), true
		}
	case URAKindService:
		if ownerParts.Realm == abilityParts.Realm &&
			abilityParts.AbilityOwner.Kind == AbilityOwnerService &&
			ownerParts.UserID == abilityParts.AbilityOwner.UserID &&
			ownerParts.ServiceID == abilityParts.AbilityOwner.ServiceID {
			return abilityParts.AbilityID, true
		}
	}
	return "", false
}

func UserURA(realm, userID string) string {
	return Ura{raw: fmt.Sprintf("%s%s/user/%s", URAScheme, realm, userID)}.String()
}

func DeviceURA(realm, deviceID string) string {
	return Ura{raw: fmt.Sprintf("%s%s/device/%s", URAScheme, realm, deviceID)}.String()
}

func AgentURA(realm, userID, agentID string) string {
	return Ura{raw: fmt.Sprintf("%s%s/agent/%s.%s", URAScheme, realm, userID, agentID)}.String()
}

func ServiceURA(realm, principalID, serviceID string) string {
	return Ura{raw: fmt.Sprintf("%s%s/service/%s.%s", URAScheme, realm, principalID, serviceID)}.String()
}

// DeviceAgentURA builds the canonical Device-sponsored SystemAgent identity. The
// explicit `device.` discriminant is part of Axon's grammar; callers must not
// infer device ownership from an arbitrary first segment.
func DeviceAgentURA(realm, deviceID, agentID string) string {
	return Ura{raw: fmt.Sprintf("%s%s/agent/device.%s.%s", URAScheme, realm, deviceID, agentID)}.String()
}

func AbilityURA(realm, userID, agentID, abilityID string) string {
	return Ura{raw: fmt.Sprintf("%s%s/ability/%s.%s.%s", URAScheme, realm, userID, agentID, abilityID)}.String()
}

func ServiceAbilityURA(realm, principalID, serviceID, abilityID string) string {
	return Ura{raw: fmt.Sprintf("%s%s/ability/service.%s.%s.%s", URAScheme, realm, principalID, serviceID, abilityID)}.String()
}

func AuthorityURA(realm string) string {
	return Ura{raw: fmt.Sprintf("%s%s/authority", URAScheme, realm)}.String()
}

func AuthorityAbilityURA(realm, abilityName string) string {
	if realm == "" || abilityName == "" {
		return ""
	}
	tail := abilityName
	if strings.HasPrefix(tail, "01AUTHORITY.") {
		return ""
	}
	switch {
	case strings.HasPrefix(tail, "authority."):
	default:
		if !strings.Contains(tail, ".") {
			return ""
		}
		tail = "authority." + tail
	}
	raw := fmt.Sprintf("%s%s/ability/%s", URAScheme, realm, tail)
	if _, err := ParseURA(raw); err != nil {
		return ""
	}
	return raw
}

func ResourceDotURA(realm, ownerID, path string) string {
	clean := strings.TrimPrefix(path, "/")
	if clean == "" {
		return fmt.Sprintf("%s%s/resource/%s", URAScheme, realm, ownerID)
	}
	return fmt.Sprintf("%s%s/resource/%s/%s", URAScheme, realm, ownerID, clean)
}

func RealmUserPrefix(realm string) string   { return fmt.Sprintf("%s%s/user/", URAScheme, realm) }
func RealmDevicePrefix(realm string) string { return fmt.Sprintf("%s%s/device/", URAScheme, realm) }
func RealmAgentPrefix(realm string) string  { return fmt.Sprintf("%s%s/agent/", URAScheme, realm) }
func UserAgentPrefix(realm, userID string) string {
	return fmt.Sprintf("%s%s/agent/%s.", URAScheme, realm, userID)
}
func RealmAbilityPrefix(realm string) string  { return fmt.Sprintf("%s%s/ability/", URAScheme, realm) }
func RealmResourcePrefix(realm string) string { return fmt.Sprintf("%s%s/resource/", URAScheme, realm) }

func DisplayID(raw string) string {
	parts, err := ParseURAParts(raw)
	if err != nil {
		return raw
	}
	switch parts.Kind {
	case URAKindDevice:
		return parts.DeviceID
	case URAKindUser:
		return parts.UserID
	case URAKindService:
		return parts.UserID + "." + parts.ServiceID
	case URAKindAgent:
		if parts.DeviceID != "" {
			return "device." + parts.DeviceID + "." + parts.AgentID
		}
		return parts.UserID + "." + parts.AgentID
	case URAKindAbility:
		switch parts.AbilityOwner.Kind {
		case AbilityOwnerAuthority:
			return "authority." + parts.AbilityID
		case AbilityOwnerDevice:
			return "device." + parts.AbilityOwner.DeviceID + "." + parts.AbilityID
		case AbilityOwnerSystemAgent:
			return "system-agent." + parts.AbilityOwner.DeviceID + "." + parts.AbilityOwner.AgentID + "." + parts.AbilityID
		case AbilityOwnerService:
			return "service." + parts.AbilityOwner.UserID + "." + parts.AbilityOwner.ServiceID + "." + parts.AbilityID
		case AbilityOwnerAgent:
			return parts.AbilityOwner.UserID + "." + parts.AbilityOwner.AgentID + "." + parts.AbilityID
		default:
			return parts.UserID + "." + parts.AgentID + "." + parts.AbilityID
		}
	case URAKindAuthority:
		return "authority"
	case URAKindResource:
		if parts.Path == "" {
			return parts.OwnerID
		}
		return parts.OwnerID + "/" + parts.Path
	default:
		return raw
	}
}

// AbilityOwnerKind discriminates the structural owners of an ability
// URA tail under the RFC-005 §3.1 owner-token-first model (A1).
type AbilityOwnerKind string

const (
	AbilityOwnerAuthority   AbilityOwnerKind = "authority"
	AbilityOwnerAgent       AbilityOwnerKind = "agent"
	AbilityOwnerService     AbilityOwnerKind = "service"
	AbilityOwnerSystemAgent AbilityOwnerKind = "system-agent"
	AbilityOwnerDevice      AbilityOwnerKind = "device"
)

// AbilityOwner is the typed owner of an ability URA. Only the fields valid
// for Kind are populated: Authority carries no id; Agent carries UserID+AgentID;
// SystemAgent carries DeviceID+AgentID; Device carries DeviceID and is
// migration-only runtime syntax.
type AbilityOwner struct {
	Kind      AbilityOwnerKind
	UserID    string
	AgentID   string
	DeviceID  string
	ServiceID string
}

// ParsedAbility is the typed owner-token-first parse of an ability URA tail
// (RFC-005 D35/D61 §3.1). LocalName MAY contain dots; Namespace is the single
// segment left of the first dot in the post-owner remainder (empty when the
// remainder has no dot).
type ParsedAbility struct {
	Owner     AbilityOwner
	Namespace string
	LocalName string
}

// ParseAbilityTail parses the ability tail (the segment after
// `.../ability/`) using the owner-token-first algorithm shared by all SDKs:
//
//   - `authority.<rest>`                        -> Authority owner
//   - `service.<principal>.<service>.<rest>`    -> Service owner
//   - `system-agent.<device>.<agent>.<rest>`    -> SystemAgent owner
//   - `device.<id>.<rest>`                      -> migration Device owner
//   - `<user>.<agent>.<rest>`                   -> Agent owner
//
// then the remainder is split once into namespace.local_name. The exact
// first segments `authority`/`device` are reserved owner-token discriminants, so an
// agent can never express user_id == authority/device through this grammar
// ("authorityx"/"deviceops" are non-equal and remain valid agents).
func ParseAbilityTail(tail string) (ParsedAbility, error) {
	if tail == "" || strings.Contains(tail, "/") {
		return ParsedAbility{}, fmt.Errorf("ability tail must be a single non-empty path segment")
	}
	var owner AbilityOwner
	var rest string
	switch {
	case strings.HasPrefix(tail, "authority."):
		owner = AbilityOwner{Kind: AbilityOwnerAuthority}
		rest = strings.TrimPrefix(tail, "authority.")
	case strings.HasPrefix(tail, "service."):
		after := strings.TrimPrefix(tail, "service.")
		principalID, afterPrincipal, ok := strings.Cut(after, ".")
		if !ok {
			return ParsedAbility{}, fmt.Errorf("service owner requires <principal-id>.<service-id>.<rest>")
		}
		serviceID, r, ok := strings.Cut(afterPrincipal, ".")
		if !ok || principalID == "" || serviceID == "" {
			return ParsedAbility{}, fmt.Errorf("service owner requires <principal-id>.<service-id>.<rest>")
		}
		owner = AbilityOwner{Kind: AbilityOwnerService, UserID: principalID, ServiceID: serviceID}
		rest = r
	case strings.HasPrefix(tail, "system-agent."):
		after := strings.TrimPrefix(tail, "system-agent.")
		deviceID, afterDevice, ok := strings.Cut(after, ".")
		if !ok {
			return ParsedAbility{}, fmt.Errorf("system-agent owner requires <device-id>.<agent-id>.<rest>")
		}
		agentID, r, ok := strings.Cut(afterDevice, ".")
		if !ok || deviceID == "" || agentID == "" {
			return ParsedAbility{}, fmt.Errorf("system-agent owner requires <device-id>.<agent-id>.<rest>")
		}
		owner = AbilityOwner{
			Kind: AbilityOwnerSystemAgent, DeviceID: deviceID, AgentID: agentID,
		}
		rest = r
	case strings.HasPrefix(tail, "device."):
		after := strings.TrimPrefix(tail, "device.")
		deviceID, r, ok := strings.Cut(after, ".")
		if !ok {
			deviceID, r = after, ""
		}
		if deviceID == "" {
			return ParsedAbility{}, fmt.Errorf("device owner requires a <device-id> segment")
		}
		owner = AbilityOwner{Kind: AbilityOwnerDevice, DeviceID: deviceID}
		rest = r
	default:
		userID, afterUser, ok := strings.Cut(tail, ".")
		if !ok {
			return ParsedAbility{}, fmt.Errorf("agent ability tail must be <user-id>.<agent-id>.<rest>")
		}
		agentID, r, ok := strings.Cut(afterUser, ".")
		if !ok {
			agentID, r = afterUser, ""
		}
		if userID == "" || agentID == "" {
			return ParsedAbility{}, fmt.Errorf("agent ability tail must be <user-id>.<agent-id>.<rest>")
		}
		// Defensive reservation guard: authority./device. prefixes are already
		// routed above, so reaching here with an exact authority/device first
		// segment means the tail was malformed.
		if userID == "authority" || userID == "device" || userID == "system-agent" || userID == "service" {
			return ParsedAbility{}, fmt.Errorf("agent owner token %q is reserved", userID)
		}
		owner = AbilityOwner{Kind: AbilityOwnerAgent, UserID: userID, AgentID: agentID}
		rest = r
	}
	namespace, localName, ok := strings.Cut(rest, ".")
	if !ok {
		namespace, localName = "", rest
	}
	if localName == "" {
		return ParsedAbility{}, fmt.Errorf("ability tail missing local name")
	}
	return ParsedAbility{Owner: owner, Namespace: namespace, LocalName: localName}, nil
}

// DeviceAbilityURA builds migration-only direct Device ability syntax.
func DeviceAbilityURA(realm, deviceID, namespace, localName string) string {
	tail := "device." + deviceID
	if namespace != "" {
		tail += "." + namespace
	}
	tail += "." + localName
	return fmt.Sprintf("%s%s/ability/%s", URAScheme, realm, tail)
}

// SystemAgentAbilityURA builds a canonical device-sponsored SystemAgent
// ability URA.
func SystemAgentAbilityURA(realm, deviceID, agentID, abilityName string) string {
	return fmt.Sprintf(
		"%s%s/ability/system-agent.%s.%s.%s",
		URAScheme,
		realm,
		deviceID,
		agentID,
		abilityName,
	)
}

func ParseURAParts(raw string) (ParsedURA, error) {
	rest, ok := strings.CutPrefix(raw, URAScheme)
	if !ok {
		return ParsedURA{}, fmt.Errorf("URA must start with %s", URAScheme)
	}
	realm, afterRealm, ok := strings.Cut(rest, "/")
	if !ok || realm == "" {
		return ParsedURA{}, fmt.Errorf("URA missing realm segment")
	}
	role, tail, ok := strings.Cut(afterRealm, "/")
	if !ok {
		role, tail = afterRealm, ""
	}
	out := ParsedURA{Raw: raw, Realm: realm, Kind: URAKind(role)}
	switch role {
	case "user":
		if tail == "" || strings.Contains(tail, "/") || strings.Contains(tail, ".") {
			return ParsedURA{}, fmt.Errorf("user URA requires one user-id segment")
		}
		out.UserID = tail
	case "device":
		if tail == "" || strings.Contains(tail, "/") || strings.Contains(tail, ".") {
			return ParsedURA{}, fmt.Errorf("device URA requires one device-id segment")
		}
		out.DeviceID = tail
	case "service":
		if tail == "" || strings.Contains(tail, "/") {
			return ParsedURA{}, fmt.Errorf("service URA tail must be <principal-id>.<service-id>")
		}
		principalID, serviceID, ok := strings.Cut(tail, ".")
		if !ok || principalID == "" || serviceID == "" || strings.Contains(serviceID, ".") {
			return ParsedURA{}, fmt.Errorf("service URA tail must be <principal-id>.<service-id>")
		}
		out.UserID, out.ServiceID = principalID, serviceID
	case "agent":
		if strings.Contains(tail, "/") {
			return ParsedURA{}, fmt.Errorf("agent URA tail must be <user-id>.<agent-id> or device.<device-id>.<agent-id>")
		}
		if afterDevice, isDeviceOwned := strings.CutPrefix(tail, "device."); isDeviceOwned {
			deviceID, agentID, ok := strings.Cut(afterDevice, ".")
			if !ok || deviceID == "" || agentID == "" || strings.Contains(agentID, ".") {
				return ParsedURA{}, fmt.Errorf("device-owned agent URA tail must be device.<device-id>.<agent-id>")
			}
			out.DeviceID, out.AgentID = deviceID, agentID
			break
		}
		userID, agentID, ok := strings.Cut(tail, ".")
		if !ok || userID == "" || agentID == "" || strings.Contains(agentID, ".") {
			return ParsedURA{}, fmt.Errorf("agent URA tail must be <user-id>.<agent-id> or device.<device-id>.<agent-id>")
		}
		if userID == "authority" || userID == "device" || userID == "system-agent" {
			return ParsedURA{}, fmt.Errorf("agent owner token %q is reserved", userID)
		}
		out.UserID, out.AgentID = userID, agentID
	case "ability":
		ability, err := ParseAbilityTail(tail)
		if err != nil {
			return ParsedURA{}, fmt.Errorf("ability URA tail invalid: %w", err)
		}
		out.AbilityOwner = ability.Owner
		out.AbilityNamespace = ability.Namespace
		out.AbilityLocalName = ability.LocalName
		abilityID := ability.LocalName
		if ability.Namespace != "" {
			abilityID = ability.Namespace + "." + ability.LocalName
		}
		out.AbilityID = abilityID
		switch ability.Owner.Kind {
		case AbilityOwnerAuthority:
			out.UserID = "authority"
			out.AgentID = ability.Namespace
		case AbilityOwnerDevice:
			out.UserID = "device"
			out.DeviceID = ability.Owner.DeviceID
			out.AgentID = ability.Owner.DeviceID
		case AbilityOwnerSystemAgent:
			out.UserID = "device"
			out.DeviceID = ability.Owner.DeviceID
			out.AgentID = ability.Owner.AgentID
		case AbilityOwnerService:
			out.UserID = ability.Owner.UserID
			out.ServiceID = ability.Owner.ServiceID
		case AbilityOwnerAgent:
			out.UserID = ability.Owner.UserID
			out.AgentID = ability.Owner.AgentID
		default:
			return ParsedURA{}, fmt.Errorf("ability URA owner kind %q is unknown", ability.Owner.Kind)
		}
	case "authority":
		if tail != "" {
			return ParsedURA{}, fmt.Errorf("authority URA must use /authority without a tail")
		}
		out.Kind = URAKindAuthority
	case "resource":
		owner, path, _ := strings.Cut(tail, "/")
		if owner == "" {
			return ParsedURA{}, fmt.Errorf("resource URA requires owner segment")
		}
		out.OwnerID, out.UserID, out.Path = owner, owner, path
	default:
		return ParsedURA{}, fmt.Errorf("unknown URA role %q", role)
	}
	return out, nil
}
