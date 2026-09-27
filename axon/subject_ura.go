package axon

import (
	"errors"
	"fmt"
	"strings"
)

// SubjectURA is the signed invocation subject value object used by SDK facades.
//
// A subject is always a canonical URA. Short principals are SDK sugar
// and are expanded into a realm-scoped resource subject; legacy private
// easynet:prv:* identifiers are rejected instead of being silently forwarded.
type SubjectURA struct {
	Raw string
}

func ParseSubjectURA(raw string) (SubjectURA, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return SubjectURA{}, errors.New("subject_ura must be non-empty for invocation")
	}
	if !strings.HasPrefix(normalized, URAScheme) {
		return SubjectURA{}, fmt.Errorf("subject_ura must be a canonical URA: %s", normalized)
	}
	parts := strings.Split(strings.TrimPrefix(normalized, URAScheme), "/")
	if len(parts) < 3 {
		return SubjectURA{}, fmt.Errorf("subject_ura must include realm, subject kind, and subject id: %s", normalized)
	}
	for _, part := range parts {
		if part == "" {
			return SubjectURA{}, fmt.Errorf("subject_ura must not contain empty path segments: %s", normalized)
		}
	}
	return SubjectURA{Raw: normalized}, nil
}

func SubjectURAForInvocation(principalID, abilityRef, defaultSubjectURA string) (SubjectURA, error) {
	normalized := strings.TrimSpace(principalID)
	if normalized == "" {
		return ParseSubjectURA(defaultSubjectURA)
	}
	if strings.HasPrefix(normalized, URAScheme) {
		return ParseSubjectURA(normalized)
	}
	if strings.HasPrefix(normalized, "easynet:") {
		return SubjectURA{}, fmt.Errorf("principal_id must use URA syntax: %s", normalized)
	}
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' {
			continue
		}
		return SubjectURA{}, fmt.Errorf("principal_id contains invalid characters: %s", normalized)
	}
	ref, err := ParseAbilityDescriptorRef(abilityRef)
	if err != nil {
		return SubjectURA{}, err
	}
	realm, err := realmFromAbilityURA(ref.AbilityURA)
	if err != nil {
		return SubjectURA{}, err
	}
	return SubjectURA{Raw: URAScheme + realm + "/resource/sdk.principal/" + normalized}, nil
}

func realmFromAbilityURA(abilityURA string) (string, error) {
	rest := strings.TrimPrefix(abilityURA, URAScheme)
	if rest == abilityURA {
		return "", errors.New("ability descriptor ref is missing realm")
	}
	realm, _, ok := strings.Cut(rest, "/")
	if !ok || realm == "" {
		return "", errors.New("ability descriptor ref is missing realm")
	}
	return realm, nil
}
