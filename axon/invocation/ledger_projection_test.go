package axon

import (
	"encoding/json"
	"os"
	"testing"
)

type invocationLedgerVector struct {
	Record     json.RawMessage `json:"record"`
	TraceGraph json.RawMessage `json:"trace_graph"`
}

func loadInvocationLedgerVector(t *testing.T) invocationLedgerVector {
	t.Helper()
	path := conformancePath(t, "invocation-ledger-json-v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared ledger vector: %v", err)
	}
	var vector invocationLedgerVector
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatalf("decode shared ledger vector: %v", err)
	}
	return vector
}

func TestInvocationLedgerProjectionConsumesSharedVector(t *testing.T) {
	vector := loadInvocationLedgerVector(t)
	record, err := ParseInvocationLedgerRecordJSON(vector.Record)
	if err != nil {
		t.Fatalf("parse record: %v", err)
	}
	if record.Usage.TokensIn != 120 || record.Usage.ExternalCalls != 2 {
		t.Fatalf("usage was not preserved: %+v", record.Usage)
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(record.Args, &args); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	var extension map[string]any
	if err := json.Unmarshal(args["provider_extension"], &extension); err != nil {
		t.Fatalf("decode provider extension: %v", err)
	}
	if _, ok := extension["opaque"]; !ok {
		t.Fatal("unknown provider payload extension was dropped")
	}

	graph, err := ParseInvocationTraceGraphJSON(vector.TraceGraph)
	if err != nil {
		t.Fatalf("parse trace graph: %v", err)
	}
	if len(graph.Records) != 1 || len(graph.Edges) != 1 {
		t.Fatalf("unexpected graph shape: records=%d edges=%d", len(graph.Records), len(graph.Edges))
	}
	if graph.Edges[0].Relation != "child_spawned" {
		t.Fatalf("edge relation was not preserved: %q", graph.Edges[0].Relation)
	}
}

func TestInvocationLedgerProjectionRejectsInvalidHashAndNegativeUsage(t *testing.T) {
	vector := loadInvocationLedgerVector(t)
	var raw map[string]any
	if err := json.Unmarshal(vector.Record, &raw); err != nil {
		t.Fatal(err)
	}
	receiptChain := raw["receipt_chain"].(map[string]any)
	anchors := receiptChain["anchors"].([]any)
	anchors[0].(map[string]any)["receipt_hash"] = "abcd"
	invalidHash, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseInvocationLedgerRecordJSON(invalidHash); err == nil {
		t.Fatal("expected short receipt hash rejection")
	}

	if err := json.Unmarshal(vector.Record, &raw); err != nil {
		t.Fatal(err)
	}
	raw["usage"].(map[string]any)["tokens_in"] = -1
	negativeUsage, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseInvocationLedgerRecordJSON(negativeUsage); err == nil {
		t.Fatal("expected negative usage rejection")
	}
}

func TestInvocationTraceProjectionRequiresExplicitArrays(t *testing.T) {
	for _, raw := range []string{
		`{"trace_id":"trace-1","edges":[]}`,
		`{"trace_id":"trace-1","records":[]}`,
		`{"trace_id":"trace-1","records":null,"edges":[]}`,
		`{"trace_id":"trace-1","records":[],"edges":null}`,
	} {
		if _, err := ParseInvocationTraceGraphJSON([]byte(raw)); err == nil {
			t.Fatalf("expected explicit array rejection for %s", raw)
		}
	}
}
