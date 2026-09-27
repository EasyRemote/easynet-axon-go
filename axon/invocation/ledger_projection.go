package axon

// Axon-owned portable JSON projection for invocation history and trace graphs.
//
// This layer deliberately models only protocol/runtime concepts. Product
// transports may wrap these records, but must not re-declare their receipt,
// causal, usage, or trace semantics.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	axonsdk "axon.run/sdk/go/axon"
)

// InvocationReceiptAnchor identifies one receipt in an invocation lifecycle
// chain without duplicating the receipt's signed body.
type InvocationReceiptAnchor struct {
	ReceiptURA      string `json:"receipt_ura"`
	ReceiptHash     string `json:"receipt_hash"`
	ReceiptType     string `json:"receipt_type"`
	State           string `json:"state"`
	TimestampUnixMs int64  `json:"timestamp_unix_ms"`
}

// InvocationReceiptChainSummary is the query projection of a verified receipt
// chain. Canonical receipt bytes and signature verification remain owned by
// InvocationReceipt.
type InvocationReceiptChainSummary struct {
	Anchors            []InvocationReceiptAnchor `json:"anchors"`
	Verified           bool                      `json:"verified"`
	HeadReceiptHash    *string                   `json:"head_receipt_hash"`
	VerificationDetail string                    `json:"verification_detail"`
}

// InvocationCausalLink records a direct predecessor relation for one ledger
// record. It is a projection of canonical receipt causality, not a second
// causal model.
type InvocationCausalLink struct {
	SourceInvocationURA *string `json:"source_invocation_ura"`
	SourceReceiptURA    string  `json:"source_receipt_ura"`
	SourceReceiptHash   string  `json:"source_receipt_hash"`
	Relation            string  `json:"relation"`
}

// InvocationTraceEdge is the graph form of InvocationCausalLink.
type InvocationTraceEdge struct {
	FromInvocationURA *string `json:"from_invocation_ura"`
	FromReceiptURA    string  `json:"from_receipt_ura"`
	FromReceiptHash   string  `json:"from_receipt_hash"`
	ToInvocationURA   string  `json:"to_invocation_ura"`
	Relation          string  `json:"relation"`
}

// InvocationLedgerRecord is the product-neutral, queryable projection of one
// invocation. Payload-bearing objects stay as RawMessage so providers can add
// fields without an SDK consumer silently dropping them.
type InvocationLedgerRecord struct {
	InvocationURA   string                        `json:"invocation_ura"`
	RequestID       string                        `json:"request_id"`
	TraceID         string                        `json:"trace_id"`
	SpanID          string                        `json:"span_id"`
	CallerURA       string                        `json:"caller_ura"`
	CalleeURA       string                        `json:"callee_ura"`
	SubjectURA      string                        `json:"subject_ura"`
	AbilityURA      string                        `json:"ability_ura"`
	AbilityName     string                        `json:"ability_name"`
	State           string                        `json:"state"`
	StartedUnixMs   int64                         `json:"started_unix_ms"`
	CompletedUnixMs *int64                        `json:"completed_unix_ms"`
	ElapsedMs       *uint64                       `json:"elapsed_ms"`
	Args            json.RawMessage               `json:"args"`
	Result          json.RawMessage               `json:"result"`
	Error           json.RawMessage               `json:"error"`
	Diagnostics     []json.RawMessage             `json:"diagnostics"`
	CausalLinks     []InvocationCausalLink        `json:"causal_links"`
	ReceiptChain    InvocationReceiptChainSummary `json:"receipt_chain"`
	Visibility      json.RawMessage               `json:"visibility"`
	AuthorityForm   string                        `json:"authority_form"`
	Usage           InvocationUsage               `json:"usage"`
}

type invocationLedgerUsageJSON struct {
	TokensIn      uint64 `json:"tokens_in"`
	TokensOut     uint64 `json:"tokens_out"`
	DurationMs    uint64 `json:"duration_ms"`
	ExternalCalls uint32 `json:"external_calls"`
}

// UnmarshalJSON keeps InvocationUsage as the single canonical Go usage model
// while projecting its language-idiomatic field names onto the shared
// snake_case ledger JSON contract.
func (r *InvocationLedgerRecord) UnmarshalJSON(data []byte) error {
	type plain InvocationLedgerRecord
	decoded := struct {
		*plain
		Usage invocationLedgerUsageJSON `json:"usage"`
	}{plain: (*plain)(r)}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	r.Usage = InvocationUsage{
		TokensIn:      decoded.Usage.TokensIn,
		TokensOut:     decoded.Usage.TokensOut,
		DurationMs:    decoded.Usage.DurationMs,
		ExternalCalls: decoded.Usage.ExternalCalls,
	}
	return nil
}

// MarshalJSON is the inverse shared-JSON projection of UnmarshalJSON.
func (r InvocationLedgerRecord) MarshalJSON() ([]byte, error) {
	type plain InvocationLedgerRecord
	return json.Marshal(struct {
		plain
		Usage invocationLedgerUsageJSON `json:"usage"`
	}{
		plain: plain(r),
		Usage: invocationLedgerUsageJSON{
			TokensIn:      r.Usage.TokensIn,
			TokensOut:     r.Usage.TokensOut,
			DurationMs:    r.Usage.DurationMs,
			ExternalCalls: r.Usage.ExternalCalls,
		},
	})
}

// InvocationTraceGraph is a causal graph over ledger records sharing a trace.
type InvocationTraceGraph struct {
	TraceID string                   `json:"trace_id"`
	Records []InvocationLedgerRecord `json:"records"`
	Edges   []InvocationTraceEdge    `json:"edges"`
}

// ParseInvocationLedgerRecordJSON parses and validates the Axon ledger JSON
// projection while preserving provider-owned payload objects verbatim.
func ParseInvocationLedgerRecordJSON(data []byte) (InvocationLedgerRecord, error) {
	var record InvocationLedgerRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return InvocationLedgerRecord{}, ErrInvalidArgument(fmt.Sprintf("bad_invocation_ledger_json:%s", err))
	}
	if err := record.Validate(); err != nil {
		return InvocationLedgerRecord{}, err
	}
	return record, nil
}

// ParseInvocationTraceGraphJSON parses and validates an Axon trace graph.
func ParseInvocationTraceGraphJSON(data []byte) (InvocationTraceGraph, error) {
	var graph InvocationTraceGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		return InvocationTraceGraph{}, ErrInvalidArgument(fmt.Sprintf("bad_invocation_trace_graph_json:%s", err))
	}
	if err := graph.Validate(); err != nil {
		return InvocationTraceGraph{}, err
	}
	return graph, nil
}

// Validate checks the structural invariants of a ledger record. Cryptographic
// receipt verification is intentionally not repeated here.
func (r InvocationLedgerRecord) Validate() error {
	if err := validateLedgerURA("invocation_ura", r.InvocationURA); err != nil {
		return err
	}
	if err := requiredLedgerText("request_id", r.RequestID); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "caller_ura", value: r.CallerURA},
		{name: "callee_ura", value: r.CalleeURA},
		{name: "subject_ura", value: r.SubjectURA},
		{name: "ability_ura", value: r.AbilityURA},
	} {
		if err := validateLedgerURA(field.name, field.value); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "ability_name", value: r.AbilityName},
		{name: "state", value: r.State},
	} {
		if err := requiredLedgerText(field.name, field.value); err != nil {
			return err
		}
	}
	if err := validateJSONObject("args", r.Args, true); err != nil {
		return err
	}
	if err := validateJSONObject("result", r.Result, false); err != nil {
		return err
	}
	if err := validateJSONObject("error", r.Error, false); err != nil {
		return err
	}
	for index, diagnostic := range r.Diagnostics {
		if err := validateJSONObject(fmt.Sprintf("diagnostics[%d]", index), diagnostic, true); err != nil {
			return err
		}
	}
	if err := validateJSONObject("visibility", r.Visibility, false); err != nil {
		return err
	}
	for index, link := range r.CausalLinks {
		if err := link.validate(index); err != nil {
			return err
		}
	}
	return r.ReceiptChain.validate()
}

// Validate checks graph identity and every contained record/edge.
func (g InvocationTraceGraph) Validate() error {
	if err := requiredLedgerText("trace_id", g.TraceID); err != nil {
		return err
	}
	if g.Records == nil {
		return ErrInvalidArgument("records_must_be_array")
	}
	if g.Edges == nil {
		return ErrInvalidArgument("edges_must_be_array")
	}
	for index, record := range g.Records {
		if err := record.Validate(); err != nil {
			return ErrInvalidArgument(fmt.Sprintf("records[%d]:%s", index, err))
		}
		if record.TraceID != "" && record.TraceID != g.TraceID {
			return ErrInvalidArgument(fmt.Sprintf("records[%d].trace_id_mismatch", index))
		}
	}
	for index, edge := range g.Edges {
		if err := edge.validate(index); err != nil {
			return err
		}
	}
	return nil
}

func (s InvocationReceiptChainSummary) validate() error {
	for index, anchor := range s.Anchors {
		if err := anchor.validate(index); err != nil {
			return err
		}
	}
	if s.HeadReceiptHash != nil {
		if err := validateLedgerHash("head_receipt_hash", *s.HeadReceiptHash); err != nil {
			return err
		}
	}
	return nil
}

func (a InvocationReceiptAnchor) validate(index int) error {
	prefix := fmt.Sprintf("receipt_chain.anchors[%d]", index)
	if err := validateLedgerURA(prefix+".receipt_ura", a.ReceiptURA); err != nil {
		return err
	}
	if err := validateLedgerHash(prefix+".receipt_hash", a.ReceiptHash); err != nil {
		return err
	}
	if err := requiredLedgerText(prefix+".receipt_type", a.ReceiptType); err != nil {
		return err
	}
	return requiredLedgerText(prefix+".state", a.State)
}

func (l InvocationCausalLink) validate(index int) error {
	prefix := fmt.Sprintf("causal_links[%d]", index)
	if l.SourceInvocationURA != nil {
		if err := validateLedgerURA(prefix+".source_invocation_ura", *l.SourceInvocationURA); err != nil {
			return err
		}
	}
	if err := validateLedgerURA(prefix+".source_receipt_ura", l.SourceReceiptURA); err != nil {
		return err
	}
	if err := validateLedgerHash(prefix+".source_receipt_hash", l.SourceReceiptHash); err != nil {
		return err
	}
	return requiredLedgerText(prefix+".relation", l.Relation)
}

func (e InvocationTraceEdge) validate(index int) error {
	prefix := fmt.Sprintf("edges[%d]", index)
	if e.FromInvocationURA != nil {
		if err := validateLedgerURA(prefix+".from_invocation_ura", *e.FromInvocationURA); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: prefix + ".from_receipt_ura", value: e.FromReceiptURA},
		{name: prefix + ".to_invocation_ura", value: e.ToInvocationURA},
	} {
		if err := validateLedgerURA(field.name, field.value); err != nil {
			return err
		}
	}
	if err := validateLedgerHash(prefix+".from_receipt_hash", e.FromReceiptHash); err != nil {
		return err
	}
	return requiredLedgerText(prefix+".relation", e.Relation)
}

func validateLedgerURA(field, value string) error {
	if err := requiredLedgerText(field, value); err != nil {
		return err
	}
	if _, err := axonsdk.ParseURA(value); err != nil {
		return ErrInvalidArgument(fmt.Sprintf("%s_invalid:%s", field, err))
	}
	return nil
}

func validateLedgerHash(field, value string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return ErrInvalidArgument(fmt.Sprintf("%s_must_be_32_byte_hex", field))
	}
	return nil
}

func requiredLedgerText(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return ErrInvalidArgument(field + "_required")
	}
	return nil
}

func validateJSONObject(field string, raw json.RawMessage, required bool) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		if required {
			return ErrInvalidArgument(field + "_required")
		}
		return nil
	}
	if trimmed[0] != '{' {
		return ErrInvalidArgument(field + "_must_be_object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return ErrInvalidArgument(fmt.Sprintf("%s_invalid:%s", field, err))
	}
	return nil
}
