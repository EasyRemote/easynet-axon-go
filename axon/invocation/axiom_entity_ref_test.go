package axon

import (
	"strings"
	"testing"
)

func TestEntityRefForSubjectAcceptsServiceAsExecutableEndpoint(t *testing.T) {
	for _, ura := range []string{
		"easynet:///r/localhost/service/2ce7a746-fb6c-45dc-9aff-d494296acf48.pages",
		"easynet:///r/localhost/services/2ce7a746-fb6c-45dc-9aff-d494296acf48.pages",
	} {
		ref, err := EntityRefForSubject(SubjectIdentity{URA: ura, Profile: ProfileStrictV2})
		if err != nil {
			t.Fatalf("service subject %q rejected: %v", ura, err)
		}
		if ref.Kind != EntityRefAgent {
			t.Fatalf("service subject %q mapped to %v, want %v", ura, ref.Kind, EntityRefAgent)
		}
		if ref.URA != ura {
			t.Fatalf("service subject URA changed: got %q want %q", ref.URA, ura)
		}
	}
}

func TestEntityRefForSubjectStillRejectsUserPrincipal(t *testing.T) {
	_, err := EntityRefForSubject(SubjectIdentity{
		URA:     "easynet:///r/localhost/user/2ce7a746-fb6c-45dc-9aff-d494296acf48",
		Profile: ProfileStrictV2,
	})
	if err == nil || !strings.Contains(err.Error(), "subject_ref_kind_unsupported:user") {
		t.Fatalf("user principal subject error = %v, want subject_ref_kind_unsupported:user", err)
	}
}
