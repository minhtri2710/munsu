// Package turnend implements durable role-aware turn-end obligations.
//
// Turn-end obligations are actions that must be performed at the end of a turn
// and are persisted under the munsu home so they survive process restarts.
// Obligations differ by role: general, captain, and soldier each have a
// distinct set of obligations.
//
// Task+terminal-key binding:
//   - Per-task obligation files replace per-role global files for soldier
//     ReportRelay, scoping the relay obligation to the exact task+key.
//   - Durable relay receipts in captain-owned state ensure Soldier->Captain
//     ack survives restart.
//   - Captain reconcile/turn-end relays to General and acks the exact task/key.
package orchestrator

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// Role identifies which role the obligation set targets.
type Role string

const (
	RoleGeneral Role = "general"
	RoleCaptain Role = "captain"
	RoleSoldier Role = "soldier"
)

// ObligationKind describes one kind of turn-end obligation.
type ObligationKind string

const (
	// ReportRelay: material status change must be relayed to the parent channel.
	ReportRelay ObligationKind = "report-relay"

	// Cleanup: temp artifacts, stale turnend markers, closed-key hygiene.
	Cleanup ObligationKind = "cleanup"
)

// State represents the lifecycle state of an obligation.
type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
)

// Obligation is a single turn-end obligation.
type Obligation struct {
	Kind      ObligationKind `json:"kind"`
	State     State          `json:"state"`
	Key       string         `json:"key,omitempty"`     // terminal key bound to this obligation
	TaskID    string         `json:"task_id,omitempty"` // task ID for per-task obligations
	Detail    string         `json:"detail,omitempty"`
	CreatedAt int64          `json:"created_at"` // unix nanos
	ClosedAt  int64          `json:"closed_at,omitempty"`
}

// Obligations returns the set of obligations for the given role.
func Obligations(role Role) []Obligation {
	now := time.Now().UnixNano()
	switch role {
	case RoleGeneral:
		return []Obligation{
			{
				Kind:      Cleanup,
				State:     StateOpen,
				Detail:    "clean temp artifacts, stale turnend markers, closed-key hygiene",
				CreatedAt: now,
			},
		}
	case RoleCaptain:
		return []Obligation{
			{
				Kind:      ReportRelay,
				State:     StateOpen,
				Detail:    "relay material status changes to General parent channel",
				CreatedAt: now,
			},
			{
				Kind:      Cleanup,
				State:     StateOpen,
				Detail:    "clean up soldier artifacts, stale turnend markers, closed-key hygiene",
				CreatedAt: now,
			},
		}
	case RoleSoldier:
		return []Obligation{
			{
				Kind:      ReportRelay,
				State:     StateOpen,
				Detail:    "relay material status changes to supervisor parent channel",
				CreatedAt: now,
			},
			{
				Kind:      Cleanup,
				State:     StateOpen,
				Detail:    "clean temp artifacts, stale turnend markers, closed-key hygiene",
				CreatedAt: now,
			},
		}
	default:
		return nil
	}
}

// --- Per-task obligation persistence ---
// Per-task obligations bind ReportRelay to the exact taskID + terminalKey.
// File: state/.obligations/<taskID>.obligations

const obligationsDir = "state/.obligations"

// taskObligationsPath returns the per-task obligations file path.
func taskObligationsPath(homeDir, taskID string) string {
	return filepath.Join(homeDir, obligationsDir, taskID+".obligations")
}

// InitTaskObligations creates per-task soldier obligations for a given taskID.
// Idempotent: no-op if per-task file already exists.
func InitTaskObligations(homeDir, taskID, termKey string) error {
	p := taskObligationsPath(homeDir, taskID)
	if _, err := os.Stat(p); err == nil {
		return nil // already exists — idempotent
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating obligations dir: %w", err)
	}
	now := time.Now().UnixNano()
	obligations := []Obligation{
		{
			Kind:      ReportRelay,
			State:     StateOpen,
			Key:       termKey,
			TaskID:    taskID,
			Detail:    "relay material terminal report for task/key",
			CreatedAt: now,
		},
		{
			Kind:      Cleanup,
			State:     StateOpen,
			Key:       "",
			TaskID:    taskID,
			Detail:    "clean temp artifacts, stale turnend markers, closed-key hygiene",
			CreatedAt: now,
		},
	}
	return SaveTaskObligations(homeDir, taskID, obligations)
}

// LoadTaskObligations reads the per-task obligation state.
// Returns nil, nil if no per-task file exists.
func LoadTaskObligations(homeDir, taskID string) ([]Obligation, error) {
	p := taskObligationsPath(homeDir, taskID)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading task obligations %s: %w", taskID, err)
	}
	defer f.Close()

	return parseObligations(f)
}

// SaveTaskObligations persists the per-task obligation state.
func SaveTaskObligations(homeDir, taskID string, obligations []Obligation) error {
	p := taskObligationsPath(homeDir, taskID)
	return writeObligationsFile(p, obligations)
}

// CompleteTaskObligation marks an obligation of the given kind as closed
// for the specific task. Returns true if a matching open obligation was found.
func CompleteTaskObligation(homeDir, taskID string, kind ObligationKind) (bool, error) {
	obligations, err := LoadTaskObligations(homeDir, taskID)
	if err != nil {
		return false, err
	}
	now := time.Now().UnixNano()
	found := false
	for i, o := range obligations {
		if o.Kind == kind && o.State == StateOpen {
			obligations[i].State = StateClosed
			obligations[i].ClosedAt = now
			found = true
		}
	}
	if !found {
		return false, nil
	}
	return true, SaveTaskObligations(homeDir, taskID, obligations)
}

// IsTaskReportRelayOpen returns true if the task has an open ReportRelay obligation.
func IsTaskReportRelayOpen(homeDir, taskID string) (bool, error) {
	obligations, err := LoadTaskObligations(homeDir, taskID)
	if err != nil {
		return false, err
	}
	for _, o := range obligations {
		if o.Kind == ReportRelay && o.State == StateOpen {
			return true, nil
		}
	}
	return false, nil
}

// ClearTaskCompleted removes all closed obligations for a specific task.
func ClearTaskCompleted(homeDir, taskID string) error {
	obligations, err := LoadTaskObligations(homeDir, taskID)
	if err != nil {
		return err
	}
	var open []Obligation
	for _, o := range obligations {
		if o.State == StateOpen {
			open = append(open, o)
		}
	}
	return SaveTaskObligations(homeDir, taskID, open)
}

// --- Durable relay receipts ---
// Receipt: Soldier->Captain durable proof that a material terminal report
// was sent. Stored in captain-owned state/.terminal-receipts/<taskID>.<key>.receipt
// Ack: Captain marks relay to General by writing <taskID>.<key>.ack

const receiptsDir = "state/.terminal-receipts"

// ReceiptDir returns the receipts directory under the given home.
func ReceiptDir(homeDir string) string {
	return filepath.Join(homeDir, receiptsDir)
}

// ReceiptPath returns the path for a terminal receipt file.
func ReceiptPath(homeDir, taskID, termKey string) string {
	return filepath.Join(homeDir, receiptsDir, taskID+"."+termKey+".receipt")
}

// AckPath returns the path for an ack file marking a receipt as relayed.
func AckPath(homeDir, taskID, termKey string) string {
	return filepath.Join(homeDir, receiptsDir, taskID+"."+termKey+".ack")
}

// WriteReceipt writes a durable relay receipt for a terminal report.
// The receipt is identified by taskID + termKey (terminal key).
// If a receipt already exists for this taskID+termKey, it is overwritten
// atomically: writes to a temp file, removes any stale ack, then renames
// the temp file over the existing receipt. On pre-rename failure the temp
// file is cleaned up; on post-rename failure the old receipt (now pending)
// is acceptable fail-closed behavior.
func WriteReceipt(homeDir, taskID, termKey, state, msg string) error {
	p := ReceiptPath(homeDir, taskID, termKey)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating receipts dir: %w", err)
	}

	// Write to a temp file first for atomic replacement
	tmpPath := p + ".tmp"
	content := fmt.Sprintf("task_id=%s\nkey=%s\nstate=%s\nmsg=%s\ntimestamp=%d\n",
		taskID, termKey, state, msg, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp receipt: %w", err)
	}

	// Remove any prior ack so the new receipt starts pending
	if err := os.Remove(AckPath(homeDir, taskID, termKey)); err != nil && !os.IsNotExist(err) {
		os.Remove(tmpPath)
		return fmt.Errorf("removing stale ack: %w", err)
	}

	// Atomic rename: temp file over receipt
	if err := os.Rename(tmpPath, p); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming receipt: %w", err)
	}

	return nil
}

// IsReceiptAcked checks whether a terminal receipt has been acknowledged.
func IsReceiptAcked(homeDir, taskID, termKey string) bool {
	_, err := os.Stat(AckPath(homeDir, taskID, termKey))
	return err == nil
}

// WriteAck marks a terminal receipt as acknowledged by the Captain.
func WriteAck(homeDir, taskID, termKey string) error {
	p := AckPath(homeDir, taskID, termKey)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating receipts dir: %w", err)
	}
	content := fmt.Sprintf("task_id=%s\nkey=%s\nacked_at=%d\n",
		taskID, termKey, time.Now().UnixNano())
	return os.WriteFile(p, []byte(content), 0644)
}

// PendingReceipt represents one un-acked terminal receipt.
type PendingReceipt struct {
	TaskID  string
	TermKey string
	State   string
}

// ListPendingReceipts returns all receipt files without corresponding ack.
func ListPendingReceipts(homeDir string) ([]PendingReceipt, error) {
	dir := ReceiptDir(homeDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading receipts dir: %w", err)
	}

	var pending []PendingReceipt
	seen := make(map[string]bool)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".receipt") || e.IsDir() {
			continue
		}
		core := strings.TrimSuffix(name, ".receipt")
		taskID, termKey, ok := strings.Cut(core, ".")
		if !ok || taskID == "" || termKey == "" {
			continue
		}
		if IsReceiptAcked(homeDir, taskID, termKey) {
			continue
		}
		key := taskID + "." + termKey
		if seen[key] {
			continue
		}
		seen[key] = true

		state := ""
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok && k == "state" {
					state = strings.TrimSpace(v)
				}
			}
		}

		pending = append(pending, PendingReceipt{
			TaskID:  taskID,
			TermKey: termKey,
			State:   state,
		})
	}
	return pending, nil
}

// --- Per-role obligation persistence (backward-compatible) ---

// obligationsPath returns the per-role durable obligations file path.
func obligationsPath(homeDir string, role Role) string {
	return filepath.Join(homeDir, obligationsDir, string(role)+".obligations")
}

// LoadObligations reads the persisted obligation state for the given role.
// Returns the persisted obligations, or the default set if no persisted state
// exists. An empty/zero-length file returns nil.
func LoadObligations(homeDir string, role Role) ([]Obligation, error) {
	p := obligationsPath(homeDir, role)
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Obligations(role), nil
		}
		return nil, fmt.Errorf("reading obligations for %s: %w", role, err)
	}
	defer f.Close()
	obligations, err := parseObligations(f)
	if err != nil {
		return nil, err
	}
	if len(obligations) == 0 {
		return nil, nil
	}
	return obligations, nil
}

// SaveObligations persists the obligation state for the given role.
func SaveObligations(homeDir string, role Role, obligations []Obligation) error {
	return writeObligationsFile(obligationsPath(homeDir, role), obligations)
}

// CompleteObligation marks an obligation of the given kind as closed for a role.
func CompleteObligation(homeDir string, role Role, kind ObligationKind) (bool, error) {
	obligations, err := LoadObligations(homeDir, role)
	if err != nil {
		return false, err
	}
	now := time.Now().UnixNano()
	found := false
	for i, o := range obligations {
		if o.Kind == kind && o.State == StateOpen {
			obligations[i].State = StateClosed
			obligations[i].ClosedAt = now
			found = true
		}
	}
	if !found {
		return false, nil
	}
	return true, SaveObligations(homeDir, role, obligations)
}

// ClearCompleted removes all closed obligations from persisted state.
func ClearCompleted(homeDir string, role Role) error {
	obligations, err := LoadObligations(homeDir, role)
	if err != nil {
		return err
	}
	var open []Obligation
	for _, o := range obligations {
		if o.State == StateOpen {
			open = append(open, o)
		}
	}
	return SaveObligations(homeDir, role, open)
}

// MaterialStates returns true if the given state is a material state that
// warrants report-relay.
func MaterialStates(state string) bool {
	switch state {
	case "done", "failed", "needs-decision", "blocked":
		return true
	default:
		return false
	}
}

// MaterialReportExists returns true if the task has a material status line
// (done, failed, needs-decision, blocked) in its status file.
// FAILS CLOSED: returns error on unreadable status.
func MaterialReportExists(homeDir, taskID string) (bool, error) {
	statusPath, err := home.StatusFilePath(homeDir, taskID)
	if err != nil {
		return false, err
	}
	f, err := os.Open(statusPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading status file %s: %w", statusPath, err)
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lastLine = line
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("scanning status file %s: %w", statusPath, err)
	}
	if lastLine == "" {
		return false, nil
	}
	return MaterialStates(lineVerb(lastLine)), nil
}

// lineVerb extracts the leading verb from a status line.
func lineVerb(line string) string {
	before, _, _ := strings.Cut(line, ":")
	if idx := strings.Index(before, "[key="); idx >= 0 {
		before = strings.TrimSpace(before[:idx])
	}
	return strings.TrimSpace(before)
}

// parseObligations reads obligation entries from a file.
// Format: kind\tstate\tkey\tdetail\tcreated_at\tclosed_at (new)
//
//	or: kind\tstate\tdetail\tcreated_at\tclosed_at (old backward-compat)
func parseObligations(f *os.File) ([]Obligation, error) {
	var obligations []Obligation
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) < 4 {
			continue
		}
		o := Obligation{
			Kind:  ObligationKind(parts[0]),
			State: State(parts[1]),
		}
		if !strings.Contains(parts[2], " ") && len(parts) >= 6 {
			// New 6-field format: kind\tstate\tkey\tdetail\tcreated_at\tclosed_at
			o.Key = parts[2]
			o.Detail = parts[3]
			o.CreatedAt = parseInt(parts[4])
			o.ClosedAt = parseInt(parts[5])
		} else {
			// Old 4/5-field format: kind\tstate\tdetail\tcreated_at\tclosed_at
			o.Detail = parts[2]
			o.CreatedAt = parseInt(parts[3])
			if len(parts) >= 5 {
				o.ClosedAt = parseInt(parts[4])
			}
		}
		obligations = append(obligations, o)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return obligations, nil
}

// writeObligationsFile serializes obligations to a file.
func writeObligationsFile(p string, obligations []Obligation) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating obligations dir: %w", err)
	}
	var b strings.Builder
	for _, o := range obligations {
		if o.Key != "" {
			b.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%d\n",
				o.Kind, o.State, o.Key, o.Detail, o.CreatedAt, o.ClosedAt))
		} else {
			b.WriteString(fmt.Sprintf("%s\t%s\t%s\t%d\t%d\n",
				o.Kind, o.State, o.Detail, o.CreatedAt, o.ClosedAt))
		}
	}
	return os.WriteFile(p, []byte(b.String()), 0644)
}

// ProvenanceMarkerName is the marker file written to a seeded captain home root.
// Duplicated from captain package to avoid import cycle.
const ProvenanceMarkerName = ".munsu-captain-home"

// parseInt parses an int64 from a string; returns 0 on failure.
func parseInt(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}
