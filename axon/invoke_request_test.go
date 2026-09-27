package axon

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func invokeStructFieldNames(t *testing.T, sample any) []string {
	t.Helper()
	rt := reflect.TypeOf(sample)
	if rt.Kind() != reflect.Struct {
		t.Fatalf("not a struct: %T", sample)
	}
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		out = append(out, rt.Field(i).Name)
	}
	sort.Strings(out)
	return out
}

func assertInvokeFieldSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	wantCopy := append([]string(nil), want...)
	sort.Strings(wantCopy)
	if !reflect.DeepEqual(got, wantCopy) {
		t.Fatalf("%s field set mismatch\n  got:  %v\n  want: %v", label, got, wantCopy)
	}
}

func TestSDKEnvelopeFieldSetMatchesAXIOMTuple(t *testing.T) {
	assertInvokeFieldSet(t, "Envelope", invokeStructFieldNames(t, Envelope{}), []string{
		"Caller",
		"Callee",
		"Subject",
		"Nonce",
		"CausalContext",
		"RequestID",
		"PresignedCallerSignature",
	})
}

func TestSDKInvokeRequestFieldSetMatchesAXIOMSplit(t *testing.T) {
	assertInvokeFieldSet(t, "InvokeRequest", invokeStructFieldNames(t, InvokeRequest{}), []string{
		"Envelope",
		"Ability",
		"AbilityURA",
		"ArgumentsJSON",
		"ContentEnvelope",
		"Delegation",
		"SessionAuthority",
		"TimeoutMS",
		"IdempotencyKey",
	})
}

func TestSDKCausalVectorRefsCopiesSlice(t *testing.T) {
	a := CausalReceiptRef{HashHex: strings.Repeat("aa", 32), URA: "easynet:///r/acme/receipt/01R-A"}
	src := []CausalReceiptRef{a}
	got := CausalVectorRefs(src)
	src[0].HashHex = strings.Repeat("bb", 32)
	if got.Vector[0] != a {
		t.Fatalf("CausalVectorRefs must defensively copy input: %+v", got.Vector)
	}
}

func TestSDKInvokeErrorNilSafe(t *testing.T) {
	var err *InvokeError
	if got := err.Error(); !strings.Contains(got, "nil") {
		t.Fatalf("nil InvokeError string = %q", got)
	}
}
