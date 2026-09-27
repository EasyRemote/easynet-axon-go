package axon

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// AbilityDescriptorRef is the single request-surface identity for an ability
// descriptor. It is always encoded as
// ability_ura@version#descriptor_hash!action.
type AbilityDescriptorRef struct {
	Raw            string
	AbilityURA     string
	Version        string
	DescriptorHash string
	Action         string
}

func ParseAbilityDescriptorRef(raw string) (AbilityDescriptorRef, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_empty")
	}
	if strings.Count(normalized, "@") != 1 {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_malformed")
	}
	parts := strings.SplitN(normalized, "@", 2)
	abilityURA := strings.TrimSpace(parts[0])
	versionAndDigest := strings.TrimSpace(parts[1])
	if abilityURA == "" {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_missing_ability")
	}
	ura, err := ParseURAParts(abilityURA)
	if err != nil || ura.Kind != URAKindAbility {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_not_ability_ura")
	}
	if strings.Count(versionAndDigest, "#") != 1 {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_digest_missing_or_malformed")
	}
	versionParts := strings.SplitN(versionAndDigest, "#", 2)
	version := strings.TrimSpace(versionParts[0])
	digestAndAction := strings.TrimSpace(versionParts[1])
	if version == "" {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_missing_version")
	}
	if strings.Count(digestAndAction, "!") != 1 {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_action_missing_or_malformed")
	}
	digestParts := strings.SplitN(digestAndAction, "!", 2)
	digestHex := strings.TrimSpace(digestParts[0])
	action := strings.TrimSpace(digestParts[1])
	if len(digestHex) != 64 || !isASCIIHex(digestHex) {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_digest_invalid")
	}
	decoded, err := hex.DecodeString(digestHex)
	if err != nil || len(decoded) != 32 {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_digest_invalid")
	}
	if !isAdmissionAction(action) {
		return AbilityDescriptorRef{}, invalidAbilityDescriptorRef("ability_descriptor_ref_action_invalid")
	}
	canonicalDigest := strings.ToLower(digestHex)
	return AbilityDescriptorRef{
		Raw:            abilityURA + "@" + version + "#" + canonicalDigest + "!" + action,
		AbilityURA:     abilityURA,
		Version:        version,
		DescriptorHash: canonicalDigest,
		Action:         action,
	}, nil
}

func invalidAbilityDescriptorRef(reason string) error {
	return fmt.Errorf("invalid ability descriptor ref: %s", reason)
}

func isASCIIHex(raw string) bool {
	for _, b := range []byte(raw) {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return false
		}
	}
	return true
}

func isAdmissionAction(action string) bool {
	switch action {
	case "invoke", "read", "manage", "grant", "stream":
		return true
	default:
		return false
	}
}

func requiredAbilityRef(raw string) (string, error) {
	ref, err := ParseAbilityDescriptorRef(raw)
	if err != nil {
		return "", fmt.Errorf("ability must be an AbilityDescriptorRef: %w", err)
	}
	return ref.Raw, nil
}

func abilityURAFromDescriptorRef(raw string) (string, error) {
	ref, err := ParseAbilityDescriptorRef(raw)
	if err != nil {
		return "", err
	}
	return ref.AbilityURA, nil
}
