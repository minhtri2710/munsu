// Package wakedelivery consolidates the report/wake/relay lifecycle behind
// a single deep module with a small public API.
//
// Two operations:
//   - DeliverWake:  soldier-side orchestration (status → receipt → event → wake → injection)
//   - ReconcilePending: captain-side relay (pending receipt → general status → ack → obligation close)
//
// Storage primitives (turnend, lifecycle, event) are imported, not absorbed.
// Injection logic is moved here from report_cmd.go.
package wakedelivery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/event"
	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/turnend"
)

// DeliverRequest captures all inputs needed to deliver a terminal report
// through the full wake pipeline.
type DeliverRequest struct {
	HomeDir    string // soldier's home (status, event log, wake queue)
	ParentHome string // captain's home (receipt, obligations); empty if no parent
	TaskID     string
	State      string // one of task.ValidStatusStates
	Message    string
	Key        string // optional correlation/slug; empty defaults to "default"
	Role       string // "soldier", "captain", "general"
}

// WakeReceipt captures the outcome of a single wake delivery.
type WakeReceipt struct {
	EventID         uint64
	EnqueueUnix     int64
	ReceiptWritten  bool
	ObligationsInit bool
}

// ReconcileOutcome describes one receipt's relay outcome.
type ReconcileOutcome struct {
	TaskID  string
	Key     string
	State   string
	Outcome string // "relayed", "already-acked", "relay-failed", "ack-failed", "obligation-close-failed"
	Err     error
}

// ReconcileResult aggregates the reconciliation sweep.
type ReconcileResult struct {
	Outcomes []ReconcileOutcome
}

// Relayed returns the number of receipts successfully relayed.
func (r *ReconcileResult) Relayed() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.Outcome == "relayed" {
			n++
		}
	}
	return n
}

// Failed returns the number of receipts that failed to relay.
func (r *ReconcileResult) Failed() int {
	n := 0
	for _, o := range r.Outcomes {
		switch o.Outcome {
		case "relay-failed", "ack-failed", "obligation-close-failed":
			n++
		}
	}
	return n
}

// materialStates is the set of states that warrant waking a parent supervisor.
var materialStates = map[string]bool{
	"done":           true,
	"failed":         true,
	"needs-decision": true,
	"blocked":        true,
}

// isMaterial returns true for states that warrant wake/receipt.
func isMaterial(state string) bool {
	return materialStates[state]
}

// --- DeliverWake ---

// DeliverWake orchestrates the full wake delivery pipeline for a soldier
// terminal report. Steps:
//
//  1. Validate inputs and state
//  2. Write task status (durable append)
//  3. Write captain receipt + init obligations (if parentHome set and material)
//  4. Append typed event (best-effort)
//  5. Enqueue wake (material states only)
//  6. Inject sentinel to parent pane (best-effort)
//
// Fail-closed: steps 1–3 must succeed; steps 4–6 are best-effort (return
// non-fatal warnings via stderr).
//
// Event append and injection errors never fail the report — they are logged
// to stderr and the receipt is returned without error.
func DeliverWake(req DeliverRequest) (*WakeReceipt, error) {
	// Step 0: Validate inputs
	if req.HomeDir == "" {
		return nil, fmt.Errorf("HomeDir is required")
	}
	if req.TaskID == "" {
		return nil, fmt.Errorf("TaskID is required")
	}
	if req.Message == "" {
		return nil, fmt.Errorf("Message is required")
	}
	if !task.IsValidStatusState(req.State) {
		return nil, fmt.Errorf("invalid status state %q", req.State)
	}
	if req.Key == "" {
		req.Key = "default"
	}

	receipt := &WakeReceipt{}

	// Step 1: Write task status
	statusLine := req.State + ": " + req.Message
	if req.Key != "" {
		statusLine += " [key=" + req.Key + "]"
	}
	if err := task.AppendStatus(req.HomeDir, req.TaskID, statusLine); err != nil {
		return nil, fmt.Errorf("appending status: %w", err)
	}

	// Step 2: For material states with a parent home, write captain receipt
	// and init obligations. Fail-closed: if either fails, no event/wake is produced.
	if isMaterial(req.State) && req.ParentHome != "" && req.Role == "soldier" {
		if err := turnend.WriteReceipt(req.ParentHome, req.TaskID, req.Key, req.State, req.Message); err != nil {
			return nil, fmt.Errorf("writing captain receipt: %w", err)
		}
		receipt.ReceiptWritten = true

		if err := turnend.InitTaskObligations(req.ParentHome, req.TaskID, req.Key); err != nil {
			return nil, fmt.Errorf("init task obligations: %w", err)
		}
		receipt.ObligationsInit = true
	}

	// Step 3: Append to typed event log (best-effort)
	syntheticID := event.SyntheticEventID()
	receipt.EventID = syntheticID
	if err := event.AppendWithID(req.HomeDir, syntheticID, "task.status", req.TaskID, req.Key, statusLine); err != nil {
		fmt.Fprintf(os.Stderr, "warning: event append: %v\n", err)
	}

	// Step 4: For material states, enqueue a wake
	if isMaterial(req.State) {
		wakePayload := fmt.Sprintf("%s: %s [event=%d]", req.TaskID, statusLine, syntheticID)
		receipt.EnqueueUnix = time.Now().Unix()
		lifecycle.EnqueueWake(req.HomeDir, "signal", req.TaskID, wakePayload)
	}

	return receipt, nil
}

// --- Pane injection (moved from report_cmd.go) ---

// injectedEvents tracks synthetic event IDs that have already been injected
// to prevent duplicate injection of the same event.
var injectedEvents sync.Map

// ReportSessionResolver is the seam for resolving a session backend.
// Production code uses session.Resolve; tests can inject a fake.
type ReportSessionResolver func(string, string) (session.Backend, string, error)

// InjectToParentPane resolves the parent pane target and injects a sentinel
// message. Returns the typed InjectResult with outcome diagnostics.
func InjectToParentPane(role, homeDir, parentHome, taskID, msg, state string, syntheticID uint64) afk.InjectResult {
	return InjectToParentPaneWithResolver(role, homeDir, parentHome, taskID, msg, state, syntheticID, session.Resolve)
}

// InjectToParentPaneWithResolver is the testable core of InjectToParentPane.
func InjectToParentPaneWithResolver(role, homeDir, parentHome, taskID, msg, state string, syntheticID uint64, resolveSession ReportSessionResolver) afk.InjectResult {
	// Dedup: skip if this event was already injected.
	eventKey := fmt.Sprintf("%s/%s/%d", taskID, state, syntheticID)
	if _, loaded := injectedEvents.LoadOrStore(eventKey, true); loaded {
		return afk.InjectResult{Outcome: afk.OutcomeInjected, Target: "(deduped)"}
	}

	// Resolve the target home for parent pane resolution.
	targetHome, err := ResolveInjectionTargetHome(role, homeDir, parentHome)
	if err != nil {
		return afk.InjectResult{
			Outcome: afk.OutcomeEndpointDead,
			Error:   fmt.Sprintf("target home resolution: %v", err),
		}
	}

	target, err := afk.ResolveTargetWithSource(targetHome)
	if err != nil || target.Handle == "" || target.Source == afk.Unsupported {
		return afk.InjectResult{
			Outcome: afk.OutcomeEndpointDead,
			Target:  target.Handle,
			Error:   fmt.Sprintf("target resolution from %s: %v no-handle=%v unsupported=%v", targetHome, err, target.Handle == "", target.Source == afk.Unsupported),
		}
	}

	// Obtain a session backend for SendKeys and Capture.
	bk, _, err := resolveSession(homeDir, "")
	if err != nil {
		return afk.InjectResult{
			Outcome: afk.OutcomeEndpointDead,
			Target:  target.Handle,
			Error:   fmt.Sprintf("backend resolve: %v", err),
		}
	}

	var afkCap afk.PaneCapture = bk

	injectMsg := fmt.Sprintf("[report] %s: %s (task=%s)", state, msg, taskID)
	markedMsg := afk.Mark(injectMsg)
	if syntheticID > 0 {
		markedMsg = fmt.Sprintf("%s [event=%d]", markedMsg, syntheticID)
	}

	safe, verdict, err := afk.IsSafeInjectTarget(afkCap, target.Handle)
	if err != nil {
		return afk.InjectResult{Outcome: afk.OutcomeEndpointDead, Verdict: verdict.String(), Target: target.Handle, Error: err.Error()}
	}
	if !safe {
		return afk.InjectResult{Outcome: afk.OutcomeUnsafe, Verdict: verdict.String(), Target: target.Handle}
	}

	result := session.SubmitPrompt(bk, target.Handle, markedMsg)
	if !result.Acknowledged() {
		return afk.InjectResult{Outcome: afk.OutcomeBackendFailed, Verdict: verdict.String(), Target: target.Handle, Error: string(result.Status)}
	}
	return afk.InjectResult{Outcome: afk.OutcomeInjected, Verdict: verdict.String(), Target: target.Handle}
}

// ResolveInjectionTargetHome returns the target home directory for parent pane
// notification target resolution.
func ResolveInjectionTargetHome(role, homeDir, parentHome string) (string, error) {
	switch role {
	case "soldier", "captain":
		if parentHome == "" {
			return "", fmt.Errorf("MUNSU_PARENT_STATUS not set for role %q", role)
		}
		return parentHome, nil
	default:
		return homeDir, nil
	}
}

// --- ReconcilePending ---

// ReconcilePending scans captain-owned pending receipts and relays each one
// to the parent (General) home. The relay chain is:
//
//	pending receipt → General relay status → General .turnend event →
//	captain ack → captain obligation close
//
// Idempotent: safe to call repeatedly. On any partial failure, preserves
// retryable receipt/obligation state and returns per-receipt outcomes rather
// than an aggregate error. Callers should inspect outcomes for diagnostics.
func ReconcilePending(captainHome, parentHome string) (*ReconcileResult, error) {
	pending, err := turnend.ListPendingReceipts(captainHome)
	if err != nil {
		return nil, fmt.Errorf("listing pending receipts: %w", err)
	}
	if len(pending) == 0 {
		return &ReconcileResult{}, nil
	}

	result := &ReconcileResult{}
	for _, pr := range pending {
		outcome := reconcileOne(captainHome, parentHome, pr)
		result.Outcomes = append(result.Outcomes, outcome)
	}
	return result, nil
}

// reconcileOne processes a single pending receipt through the full relay chain.
func reconcileOne(captainHome, parentHome string, pr turnend.PendingReceipt) ReconcileOutcome {
	base := ReconcileOutcome{TaskID: pr.TaskID, Key: pr.TermKey, State: pr.State}

	// Already acked: skip.
	if turnend.IsReceiptAcked(captainHome, pr.TaskID, pr.TermKey) {
		base.Outcome = "already-acked"
		return base
	}

	// Resolve captain ID from provenance marker.
	captainID, err := readCaptainID(captainHome)
	if err != nil {
		base.Outcome = "relay-failed"
		base.Err = fmt.Errorf("reading captain id: %w", err)
		return base
	}

	// Step 1: Relay status to General.
	relayTaskID := fmt.Sprintf("captain:%s.relay-%s", captainID, pr.TaskID)
	relayLine := fmt.Sprintf("%s: soldier %s [key=%s]", pr.State, pr.TaskID, pr.TermKey)
	if err := task.AppendStatus(parentHome, relayTaskID, relayLine); err != nil {
		base.Outcome = "relay-failed"
		base.Err = fmt.Errorf("writing general relay status: %w", err)
		return base
	}

	// Step 2: Write .turnend event to General for permanent durability.
	now := time.Now().UnixNano()
	eventContent := fmt.Sprintf("terminal_uplink_task=%s key=%s captain=%s relayed_at=%d\n",
		pr.TaskID, pr.TermKey, captainID, now)
	eventPath := filepath.Join(parentHome, "state", relayTaskID+".turnend")
	if err := os.MkdirAll(filepath.Dir(eventPath), 0755); err == nil {
		os.WriteFile(eventPath, []byte(eventContent), 0644)
	}

	// Step 3: Write ack in captain home (marks receipt as acknowledged).
	if err := turnend.WriteAck(captainHome, pr.TaskID, pr.TermKey); err != nil {
		base.Outcome = "ack-failed"
		base.Err = fmt.Errorf("writing ack: %w", err)
		return base
	}

	// Step 4: Complete per-task obligation in captain home.
	if _, err := turnend.CompleteTaskObligation(captainHome, pr.TaskID, turnend.ReportRelay); err != nil {
		base.Outcome = "obligation-close-failed"
		base.Err = fmt.Errorf("completing obligation: %w", err)
		return base
	}

	base.Outcome = "relayed"
	return base
}

// readCaptainID reads the captain ID from the provenance marker file.
// Falls back to the directory basename if no marker exists.
func readCaptainID(captainHome string) (string, error) {
	markerPath := filepath.Join(captainHome, turnend.ProvenanceMarkerName)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return filepath.Base(captainHome), nil
		}
		return "", fmt.Errorf("reading provenance marker: %w", err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 3)
	if len(lines) < 2 {
		return filepath.Base(captainHome), nil
	}
	return strings.TrimSpace(lines[1]), nil
}
