package axon

import (
	"errors"
	"fmt"
	"testing"
)

func TestNativeFailureEvidencePreservesNumbersAndErrorComparison(t *testing.T) {
	const evidence = `{"state":6,"timestamp":9007199254740993,"terminal_receipt":{"index":"18446744073709551615"}}`
	err := decodeDendriteFailure([]byte(`{"ok":false,"error":{"code":"BRIDGE_TRANSPORT","message":"business refused","source":"callee","invocation_response":` + evidence + `}}`))
	if err.InvocationResponseJSON != evidence || err.Error() != "business refused" || err.Source != "callee" {
		t.Fatalf("error carrier changed: %+v", err)
	}
	// This compile/runtime check catches accidentally adding a map/slice field.
	values := map[DendriteError]bool{err: true}
	if !values[err] || err != err {
		t.Fatal("DendriteError must remain comparable")
	}
	if !errors.Is(fmt.Errorf("wrapped: %w", err), DendriteError{Code: "BRIDGE_TRANSPORT"}) {
		t.Fatal("code-based sentinel matching changed")
	}
}

func TestNativeFailureEvidenceLegacyAndMalformed(t *testing.T) {
	for _, suffix := range []string{"", `,"invocation_response":null`} {
		err := decodeDendriteFailure([]byte(`{"ok":false,"error":{"code":"BRIDGE","message":"legacy","source":"callee"` + suffix + `}}`))
		if err != (DendriteError{Code: "BRIDGE", Message: "legacy", Source: "callee"}) {
			t.Fatalf("legacy error changed: %+v", err)
		}
	}
	for _, value := range []string{`[]`, `false`, `1`, `"receipt"`} {
		err := decodeDendriteFailure([]byte(`{"error":{"code":"BRIDGE","message":"failure","source":"callee","invocation_response":` + value + `}}`))
		if err.Message != "bridge invocation_response must be an object" || err.InvocationResponseJSON != "" {
			t.Fatalf("malformed evidence accepted: %+v", err)
		}
	}
	for _, document := range []string{`{}`, `{"error":null}`, `{"error":[]}`, `{"error":{"code":1}}`} {
		if err := decodeDendriteFailure([]byte(document)); err.Code != ErrCodeBridge || err.InvocationResponseJSON != "" {
			t.Fatalf("malformed error accepted: %+v", err)
		}
	}
}
