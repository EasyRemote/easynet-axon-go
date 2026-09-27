package axon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultLogDir resolves the persistent log root, honouring
// AXON_INVOCATION_LOG_DIR. Creates the directory if missing.
func DefaultLogDir() string {
	if p := os.Getenv("AXON_INVOCATION_LOG_DIR"); p != "" {
		_ = os.MkdirAll(p, 0o700)
		return p
	}
	p := filepath.Join(os.TempDir(), "axon-invocations")
	_ = os.MkdirAll(p, 0o700)
	return p
}

// InvocationIndex — the on-disk summary for a single Invocation.
type InvocationIndex struct {
	InvocationID    string  `json:"invocation_id"`
	LastSequence    int64   `json:"last_sequence"`
	TerminalState   *string `json:"terminal_state"`
	Evicted         bool    `json:"evicted"`
	CreatedAtUnixMs int64   `json:"created_at_unix_ms"`
}

// PersistedLifecycleEvent is the durable representation of one committed
// canonical lifecycle transition.
type PersistedLifecycleEvent struct {
	Sequence           uint64          `json:"sequence"`
	EventType          string          `json:"event_type"`
	State              InvocationState `json:"state"`
	TimestampUnixMs    int64           `json:"timestamp_unix_ms"`
	Payload            []byte          `json:"payload,omitempty"`
	PayloadContentType string          `json:"payload_content_type,omitempty"`
	Reason             string          `json:"reason,omitempty"`
	CleanupComplete    bool            `json:"cleanup_complete"`
	ChildInvocationID  string          `json:"child_invocation_id,omitempty"`
	ReceiptHash        [32]byte        `json:"receipt_hash"`
}

// RecoverySnapshot is the durable launch, checkpoint, and event evidence
// needed to resume one provider-owned invocation.
type RecoverySnapshot struct {
	InvocationID            string                    `json:"invocation_id"`
	Envelope                InvocationEnvelope        `json:"envelope"`
	Signature               CallerSignature           `json:"signature"`
	Payload                 []byte                    `json:"payload,omitempty"`
	ParentInvocationID      string                    `json:"parent_invocation_id,omitempty"`
	SupervisorSpec          SupervisorSpec            `json:"supervisor_spec"`
	AbilityURA              string                    `json:"ability_ura"`
	EffectiveDeadlineUnixMs int64                     `json:"effective_deadline_unix_ms,omitempty"`
	Checkpoint              []byte                    `json:"checkpoint,omitempty"`
	Events                  []PersistedLifecycleEvent `json:"events"`
	Phase                   string                    `json:"phase"`
	LeaseOwner              string                    `json:"lease_owner,omitempty"`
	LeaseUntilUnixMs        int64                     `json:"lease_until_unix_ms,omitempty"`
}

// PersistentLog — append-only JSONL per invocation + index file.
type PersistentLog struct {
	dir   string
	mu    sync.Mutex
	perID map[string]*sync.Mutex
}

// NewPersistentLog constructs a log rooted at dir (default if empty).
func NewPersistentLog(dir string) *PersistentLog {
	if dir == "" {
		dir = DefaultLogDir()
	}
	_ = os.MkdirAll(dir, 0o700)
	return &PersistentLog{dir: dir, perID: make(map[string]*sync.Mutex)}
}

func (p *PersistentLog) Dir() string { return p.dir }

func (p *PersistentLog) EventsPath(id string) string {
	return filepath.Join(p.dir, id+".jsonl")
}
func (p *PersistentLog) IndexPath(id string) string {
	return filepath.Join(p.dir, id+".index.json")
}

// RecoveryPath returns the durable recovery record path.
func (p *PersistentLog) RecoveryPath(id string) string {
	return filepath.Join(p.dir, id+".recovery.json")
}

func (p *PersistentLog) lockFor(id string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	lk, ok := p.perID[id]
	if !ok {
		lk = &sync.Mutex{}
		p.perID[id] = lk
	}
	return lk
}

// AppendEvent writes one event as a JSONL line, then updates the index.
// `terminalState` is non-empty when this event drives a terminal transition.
// `fsync=true` enforces invariant P2.
func (p *PersistentLog) AppendEvent(id string, event map[string]any, terminalState string, fsync bool) error {
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	return p.appendEventUnlocked(id, event, terminalState, fsync)
}

func (p *PersistentLog) appendEventUnlocked(
	id string,
	event map[string]any,
	terminalState string,
	fsync bool,
) error {
	f, err := os.OpenFile(p.EventsPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(event)
	if err != nil {
		_ = f.Close()
		return err
	}
	buf = append(buf, '\n')
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return err
	}
	if fsync {
		_ = f.Sync()
	}
	_ = f.Close()

	idx := p.readIndexUnlocked(id)
	if idx == nil {
		idx = &InvocationIndex{InvocationID: id, LastSequence: -1}
	}
	if seq, ok := integerValue(event["sequence"]); ok && seq > idx.LastSequence {
		idx.LastSequence = seq
	}
	if terminalState != "" {
		idx.TerminalState = &terminalState
	}
	if idx.CreatedAtUnixMs == 0 {
		if ts, ok := integerValue(event["timestamp_unix_ms"]); ok {
			idx.CreatedAtUnixMs = ts
		}
	}
	return p.writeIndexUnlocked(idx, fsync)
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(typed), true
	case float64:
		return int64(typed), true
	default:
		return 0, false
	}
}

// ReadEvents returns every persisted event with sequence >= fromOffset.
// Partial trailing lines (invariant P6) are discarded.
func (p *PersistentLog) ReadEvents(id string, fromOffset int64) ([]map[string]any, error) {
	path := p.EventsPath(id)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	out := []map[string]any{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20) // up to 16 MiB lines
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		if seq, ok := v["sequence"].(float64); ok && int64(seq) >= fromOffset {
			out = append(out, v)
		}
	}
	return out, nil
}

func (p *PersistentLog) ReadIndex(id string) *InvocationIndex {
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	return p.readIndexUnlocked(id)
}

func (p *PersistentLog) readIndexUnlocked(id string) *InvocationIndex {
	text, err := os.ReadFile(p.IndexPath(id))
	if err != nil {
		return nil
	}
	var idx InvocationIndex
	if err := json.Unmarshal(text, &idx); err != nil {
		return nil
	}
	return &idx
}

func (p *PersistentLog) writeIndexUnlocked(idx *InvocationIndex, fsync bool) error {
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tmp := p.IndexPath(idx.InvocationID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if fsync {
		if f, err := os.Open(tmp); err == nil {
			_ = f.Sync()
			_ = f.Close()
		}
	}
	return os.Rename(tmp, p.IndexPath(idx.InvocationID))
}

// ListInvocations returns every invocation id with a log on disk.
func (p *PersistentLog) ListInvocations() []string {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".jsonl") {
			out = append(out, strings.TrimSuffix(name, ".jsonl"))
		}
	}
	sort.Strings(out)
	return out
}

// EvictTerminal removes a terminal invocation's logs. Returns false if
// the invocation is non-terminal or missing.
func (p *PersistentLog) EvictTerminal(id string) bool {
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	idx := p.readIndexUnlocked(id)
	if idx == nil || idx.TerminalState == nil {
		return false
	}
	idx.Evicted = true
	_ = p.writeIndexUnlocked(idx, true)
	_ = os.Remove(p.EventsPath(id))
	return true
}

// Stats returns a TaskList-friendly summary for a single invocation.
func (p *PersistentLog) Stats(id string) map[string]any {
	idx := p.ReadIndex(id)
	var size int64
	if info, err := os.Stat(p.EventsPath(id)); err == nil {
		size = info.Size()
	}
	state := "RUNNING"
	if idx != nil && idx.TerminalState != nil {
		state = *idx.TerminalState
	}
	var lastSeq int64 = -1
	var evicted bool
	var created int64
	if idx != nil {
		lastSeq = idx.LastSequence
		evicted = idx.Evicted
		created = idx.CreatedAtUnixMs
	}
	return map[string]any{
		"invocation_id":      id,
		"state":              state,
		"last_sequence":      lastSeq,
		"bytes_emitted":      size,
		"evicted":            evicted,
		"created_at_unix_ms": created,
	}
}

// InitializeRecovery atomically creates one durable continuation record.
func (p *PersistentLog) InitializeRecovery(snapshot RecoverySnapshot) error {
	if strings.TrimSpace(snapshot.InvocationID) == "" {
		return fmt.Errorf("invocation_id_required")
	}
	lk := p.lockFor(snapshot.InvocationID)
	lk.Lock()
	defer lk.Unlock()
	if _, err := os.Stat(p.RecoveryPath(snapshot.InvocationID)); err == nil {
		return fmt.Errorf("recovery_snapshot_already_exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return p.writeRecoveryUnlocked(cloneRecoverySnapshot(snapshot), true)
}

// AppendLifecycleEvent durably appends event evidence and advances recovery.
func (p *PersistentLog) AppendLifecycleEvent(
	id string,
	event PersistedLifecycleEvent,
) error {
	return p.appendLifecycleEvent(id, "", event)
}

// AppendRecoveredLifecycleEvent appends only while the exact recovery lease
// remains owned by the caller.
func (p *PersistentLog) AppendRecoveredLifecycleEvent(
	id string,
	leaseOwner string,
	event PersistedLifecycleEvent,
) error {
	if strings.TrimSpace(leaseOwner) == "" {
		return fmt.Errorf("recovery_lease_owner_required")
	}
	return p.appendLifecycleEvent(id, leaseOwner, event)
}

func (p *PersistentLog) appendLifecycleEvent(
	id string,
	leaseOwner string,
	event PersistedLifecycleEvent,
) error {
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	snapshot, err := p.readRecoveryUnlocked(id)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return fmt.Errorf("recovery_snapshot_missing")
	}
	if leaseOwner == "" {
		if (snapshot.Phase != "running" && snapshot.Phase != "finalizing") || snapshot.LeaseOwner != "" {
			return fmt.Errorf("recovery_runtime_ownership_lost")
		}
	} else if (snapshot.Phase != "recovering" && snapshot.Phase != "finalizing") ||
		snapshot.LeaseOwner != leaseOwner ||
		snapshot.LeaseUntilUnixMs < time.Now().UnixMilli() {
		return fmt.Errorf("recovery_lease_lost")
	}
	if uint64(len(snapshot.Events)) != event.Sequence {
		return fmt.Errorf(
			"recovery_event_sequence_mismatch:got=%d,want=%d",
			event.Sequence,
			len(snapshot.Events),
		)
	}

	eventMap := map[string]any{
		"sequence":             event.Sequence,
		"event_type":           event.EventType,
		"state":                event.State,
		"timestamp_unix_ms":    event.TimestampUnixMs,
		"payload":              event.Payload,
		"payload_content_type": event.PayloadContentType,
		"reason":               event.Reason,
		"cleanup_complete":     event.CleanupComplete,
		"child_invocation_id":  event.ChildInvocationID,
		"receipt_hash":         event.ReceiptHash,
	}
	terminalState := ""
	if event.State.IsTerminal() {
		terminalState = string(event.State)
	}
	if err := p.appendEventUnlocked(id, eventMap, terminalState, true); err != nil {
		return err
	}
	event.Payload = append([]byte(nil), event.Payload...)
	snapshot.Events = append(snapshot.Events, event)
	if event.State.IsTerminal() {
		snapshot.Phase = "closed"
		snapshot.LeaseOwner = ""
		snapshot.LeaseUntilUnixMs = 0
	}
	return p.writeRecoveryUnlocked(*snapshot, true)
}

// CheckpointRecovery replaces the durable continuation checkpoint.
func (p *PersistentLog) CheckpointRecovery(id string, checkpoint []byte) error {
	return p.checkpointRecovery(id, "", checkpoint)
}

// CheckpointRecoveredInvocation checkpoints only under an exact recovery
// lease.
func (p *PersistentLog) CheckpointRecoveredInvocation(
	id string,
	leaseOwner string,
	checkpoint []byte,
) error {
	if strings.TrimSpace(leaseOwner) == "" {
		return fmt.Errorf("recovery_lease_owner_required")
	}
	return p.checkpointRecovery(id, leaseOwner, checkpoint)
}

func (p *PersistentLog) checkpointRecovery(
	id string,
	leaseOwner string,
	checkpoint []byte,
) error {
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	snapshot, err := p.readRecoveryUnlocked(id)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return fmt.Errorf("recovery_snapshot_missing")
	}
	if snapshot.Phase == "closed" {
		return fmt.Errorf("recovery_snapshot_closed")
	}
	if leaseOwner == "" {
		if snapshot.Phase != "running" || snapshot.LeaseOwner != "" {
			return fmt.Errorf("recovery_runtime_ownership_lost")
		}
	} else if snapshot.Phase != "recovering" ||
		snapshot.LeaseOwner != leaseOwner ||
		snapshot.LeaseUntilUnixMs < time.Now().UnixMilli() {
		return fmt.Errorf("recovery_lease_lost")
	}
	snapshot.Checkpoint = append([]byte(nil), checkpoint...)
	return p.writeRecoveryUnlocked(*snapshot, true)
}

// BeginFinalization seals business recovery while preserving the terminal owner's lease.
func (p *PersistentLog) BeginFinalization(id, owner string) error {
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	snapshot, err := p.readRecoveryUnlocked(id)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return fmt.Errorf("recovery_snapshot_missing")
	}
	switch snapshot.Phase {
	case "running":
		if snapshot.LeaseOwner != "" {
			return fmt.Errorf("recovery_runtime_ownership_lost")
		}
	case "recovering", "finalizing":
		if snapshot.LeaseOwner != "" && (snapshot.LeaseOwner != owner || snapshot.LeaseUntilUnixMs < time.Now().UnixMilli()) {
			return fmt.Errorf("recovery_lease_lost")
		}
	default:
		return fmt.Errorf("recovery_finalization_invalid_phase:%s", snapshot.Phase)
	}
	snapshot.Phase = "finalizing"
	return p.writeRecoveryUnlocked(*snapshot, true)
}

// MarkRecoveryPhase durably changes the runtime ownership phase.
func (p *PersistentLog) MarkRecoveryPhase(id string, phase string) error {
	switch phase {
	case "running", "recovering", "suspended", "closed":
	default:
		return fmt.Errorf("invalid_recovery_phase:%s", phase)
	}
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	snapshot, err := p.readRecoveryUnlocked(id)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return fmt.Errorf("recovery_snapshot_missing")
	}
	if snapshot.Phase == "finalizing" && phase != "closed" {
		return fmt.Errorf("recovery_finalization_required")
	}
	snapshot.Phase = phase
	if phase != "recovering" {
		snapshot.LeaseOwner = ""
		snapshot.LeaseUntilUnixMs = 0
	}
	return p.writeRecoveryUnlocked(*snapshot, true)
}

// ReadRecovery returns a defensive durable recovery snapshot.
func (p *PersistentLog) ReadRecovery(id string) (*RecoverySnapshot, error) {
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	snapshot, err := p.readRecoveryUnlocked(id)
	if snapshot == nil || err != nil {
		return snapshot, err
	}
	copy := cloneRecoverySnapshot(*snapshot)
	return &copy, nil
}

// ClaimRecovery leases suspended records in deterministic invocation order.
func (p *PersistentLog) ClaimRecovery(
	owner string,
	lease time.Duration,
	limit int,
) ([]RecoverySnapshot, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, fmt.Errorf("recovery_lease_owner_required")
	}
	if lease <= 0 {
		return nil, fmt.Errorf("recovery_lease_duration_required")
	}
	ids, err := p.listRecoveryInvocations()
	if err != nil {
		return nil, err
	}
	// Validate every observed record before applying the claim limit or changing leases.
	for _, id := range ids {
		snapshot, err := p.ReadRecovery(id)
		if err != nil {
			return nil, err
		}
		if err := validateRecoveryCandidate(id, snapshot); err != nil {
			return nil, err
		}
	}
	if limit <= 0 || limit > len(ids) {
		limit = len(ids)
	}
	now := time.Now().UnixMilli()
	until := now + lease.Milliseconds()
	claimed := make([]RecoverySnapshot, 0, limit)
	for _, id := range ids {
		if len(claimed) >= limit {
			break
		}
		lk := p.lockFor(id)
		lk.Lock()
		snapshot, err := p.readRecoveryUnlocked(id)
		if err != nil {
			lk.Unlock()
			return nil, err
		}
		if err := validateRecoveryCandidate(id, snapshot); err != nil {
			lk.Unlock()
			return nil, err
		}
		claimable := snapshot.Phase == "suspended" ||
			(snapshot.Phase == "recovering" && snapshot.LeaseUntilUnixMs <= now)
		if !claimable {
			lk.Unlock()
			continue
		}
		snapshot.Phase = "recovering"
		snapshot.LeaseOwner = owner
		snapshot.LeaseUntilUnixMs = until
		if err := p.writeRecoveryUnlocked(*snapshot, true); err != nil {
			lk.Unlock()
			return nil, err
		}
		claimed = append(claimed, cloneRecoverySnapshot(*snapshot))
		lk.Unlock()
	}
	return claimed, nil
}

// validateRecoveryCandidate is shared by batch preflight and the locked claim recheck.
func validateRecoveryCandidate(id string, snapshot *RecoverySnapshot) error {
	if snapshot == nil {
		return ErrUnavailable("recovery_snapshot_missing").WithInvocationID(id)
	}
	if snapshot.Phase == "finalizing" {
		return ErrUnavailable("recovery_finalization_required").WithInvocationID(id)
	}
	return nil
}

// RenewRecoveryLease extends an exact live recovery lease.
func (p *PersistentLog) RenewRecoveryLease(
	id string,
	owner string,
	lease time.Duration,
) error {
	if strings.TrimSpace(owner) == "" {
		return fmt.Errorf("recovery_lease_owner_required")
	}
	if lease <= 0 {
		return fmt.Errorf("recovery_lease_duration_required")
	}
	lk := p.lockFor(id)
	lk.Lock()
	defer lk.Unlock()
	snapshot, err := p.readRecoveryUnlocked(id)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return fmt.Errorf("recovery_snapshot_missing")
	}
	if (snapshot.Phase != "recovering" && snapshot.Phase != "finalizing") || snapshot.LeaseOwner != owner {
		return fmt.Errorf("recovery_lease_lost")
	}
	snapshot.LeaseUntilUnixMs = time.Now().Add(lease).UnixMilli()
	return p.writeRecoveryUnlocked(*snapshot, true)
}

func (p *PersistentLog) listRecoveryInvocations() ([]string, error) {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return nil, ErrUnavailable("recovery_directory_unavailable")
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".recovery.json") {
			ids = append(ids, strings.TrimSuffix(entry.Name(), ".recovery.json"))
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (p *PersistentLog) readRecoveryUnlocked(id string) (*RecoverySnapshot, error) {
	data, err := os.ReadFile(p.RecoveryPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var snapshot RecoverySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (p *PersistentLog) writeRecoveryUnlocked(
	snapshot RecoverySnapshot,
	fsync bool,
) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	path := p.RecoveryPath(snapshot.InvocationID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if fsync {
		if file, err := os.Open(tmp); err == nil {
			_ = file.Sync()
			_ = file.Close()
		}
	}
	return os.Rename(tmp, path)
}

func cloneRecoverySnapshot(snapshot RecoverySnapshot) RecoverySnapshot {
	copy := snapshot
	copy.Envelope.CausalContext = cloneCausalContext(snapshot.Envelope.CausalContext)
	copy.Signature = cloneCallerSignature(snapshot.Signature)
	copy.Payload = append([]byte(nil), snapshot.Payload...)
	copy.SupervisorSpec = *cloneSupervisorSpec(&snapshot.SupervisorSpec)
	copy.Checkpoint = append([]byte(nil), snapshot.Checkpoint...)
	copy.Events = append([]PersistedLifecycleEvent(nil), snapshot.Events...)
	for index := range copy.Events {
		copy.Events[index].Payload = append([]byte(nil), snapshot.Events[index].Payload...)
	}
	return copy
}
