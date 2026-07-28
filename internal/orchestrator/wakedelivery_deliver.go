// Package wakedelivery consolidates the report/wake/relay lifecycle behind
// a single deep module with a small public API.
//
// Two operations:
//   - DeliverWake:  soldier-side orchestration (status → receipt → event → wake)
//   - ReconcilePending: captain-side relay (pending receipt → general status → ack → obligation close)
//
// Storage primitives (turnend, lifecycle, event) are imported, not absorbed.
// Injection logic is moved here from report_cmd.go.
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/lifecycle"
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

// wakeMaterialStates is the set of states that warrant waking a parent supervisor.
var wakeMaterialStates = map[string]bool{
	"done":           true,
	"failed":         true,
	"needs-decision": true,
	"blocked":        true,
}

// isMaterial returns true for states that warrant wake/receipt.
func isMaterial(state string) bool {
	return wakeMaterialStates[state]
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
//  6. Complete best-effort wake bookkeeping
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
	syntheticID := SyntheticEventID()
	receipt.EventID = syntheticID
	if err := AppendWithID(req.HomeDir, syntheticID, "task.status", req.TaskID, req.Key, statusLine); err != nil {
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

// --- Activation on receipt (captain agent pane nudge) ---

// ActivationDiagnostic captures a structured record of one activation
// attempt (resolved target, capture, safety verdict, submit result).
// It is written to stderr with a standard prefix for log inspection.
type ActivationDiagnostic struct {
	TargetHandle   string
	ReceiptsFound  int
	ReceiptSkipped string // reason: "already-seen", "capture-error", "unsafe", "submit-failed", ""
	CaptureContent string // normalized capture of composer row (ANSI-stripped)
	SafetyVerdict  string // "empty", "pending", "busy", "unknown"
	SafetyError    string
	SubmitStatus   string // "" if not attempted; "submitted", "queued-while-busy", etc.
	SubmitDetail   string
	SubmitError    string
	ActivationSeen bool
}

// diagLog writes an activation diagnostic to stderr with a structured prefix.
func diagLog(d ActivationDiagnostic) {
	prefix := "[activation-diag]"
	fmt.Fprintf(os.Stderr, "%s target=%q receipts=%d", prefix, d.TargetHandle, d.ReceiptsFound)
	if d.ReceiptSkipped != "" {
		fmt.Fprintf(os.Stderr, " skip=%s", d.ReceiptSkipped)
	}
	if d.CaptureContent != "" {
		maxCap := 200
		if len(d.CaptureContent) > maxCap {
			fmt.Fprintf(os.Stderr, " capture=%q...", d.CaptureContent[:maxCap])
		} else {
			fmt.Fprintf(os.Stderr, " capture=%q", d.CaptureContent)
		}
	}
	fmt.Fprintf(os.Stderr, " verdict=%s", d.SafetyVerdict)
	if d.SafetyError != "" {
		fmt.Fprintf(os.Stderr, " safety_err=%q", d.SafetyError)
	}
	if d.SubmitStatus != "" {
		fmt.Fprintf(os.Stderr, " submit=%s", d.SubmitStatus)
	}
	if d.SubmitDetail != "" {
		fmt.Fprintf(os.Stderr, " submit_detail=%q", d.SubmitDetail)
	}
	if d.SubmitError != "" {
		fmt.Fprintf(os.Stderr, " submit_err=%q", d.SubmitError)
	}
	fmt.Fprintf(os.Stderr, " activation-seen=%v\n", d.ActivationSeen)
}

const activationSeenSuffix = ".activation-seen"

// ActivationSeenPath returns the path for an activation-seen marker.
func ActivationSeenPath(captainHome, taskID, termKey string) string {
	return filepath.Join(turnend.ReceiptDir(captainHome), taskID+"."+termKey+activationSeenSuffix)
}

// IsActivationSeen checks whether a receipt has already triggered an
// activation nudge (idempotency guard).
func IsActivationSeen(captainHome, taskID, termKey string) bool {
	_, err := os.Stat(ActivationSeenPath(captainHome, taskID, termKey))
	return err == nil
}

// MarkActivationSeen writes a durable marker that a receipt has triggered
// an activation nudge, preventing duplicate nudges on subsequent cycles.
func MarkActivationSeen(captainHome, taskID, termKey string) error {
	p := ActivationSeenPath(captainHome, taskID, termKey)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("creating activation-seen dir: %w", err)
	}
	content := fmt.Sprintf("task_id=%s\nkey=%s\nactivated_at=%d\n",
		taskID, termKey, time.Now().UnixNano())
	return os.WriteFile(p, []byte(content), 0644)
}

// resolveCaptainActivationTarget resolves the authoritative captain agent pane
// from captain task meta stored in the parent General home. This is the
// authoritative pane source — NOT the inherited HERDR_PANE_ID from the
// watcher/launcher environment.
//
// Returns the target result with the handle in "session:pane_id" format, or
// an error if the meta cannot be read or has no herdr_pane_id.
func resolveCaptainActivationTarget(captainHome, parentHome string) (afk.TargetResult, error) {
	if parentHome == "" {
		return afk.TargetResult{}, fmt.Errorf("no parent home for captain meta lookup")
	}

	captainID, err := readCaptainID(captainHome)
	if err != nil {
		return afk.TargetResult{}, fmt.Errorf("reading captain id: %w", err)
	}

	taskID := "captain:" + captainID
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		return afk.TargetResult{}, fmt.Errorf("reading captain meta %s: %w", taskID, err)
	}

	paneID := strings.TrimSpace(meta["herdr_pane_id"])
	if paneID == "" {
		return afk.TargetResult{}, fmt.Errorf("captain meta has no herdr_pane_id")
	}

	session := strings.TrimSpace(meta["herdr_session"])
	if session == "" {
		session = "default"
	}

	handle := session + ":" + paneID

	return afk.TargetResult{
		Source:       afk.RuntimeSource,
		Handle:       handle,
		Session:      session,
		SourceDetail: "captain task meta: " + taskID,
	}, nil
}

// listAllReceipts scans the receipts directory and returns ALL receipt files
// (regardless of ack status). This is used by ActivateOnReceipt to find
// receipts that may already be acked (relayed) but not yet activation-seen.
func listAllReceipts(homeDir string) ([]turnend.PendingReceipt, error) {
	dir := turnend.ReceiptDir(homeDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading receipts dir: %w", err)
	}

	var results []turnend.PendingReceipt
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
		key := taskID + "/" + termKey
		if seen[key] {
			continue
		}
		seen[key] = true

		// Read the receipt content to extract state.
		state := ""
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok && k == "state" {
					state = strings.TrimSpace(v)
				}
			}
		}

		results = append(results, turnend.PendingReceipt{TaskID: taskID, TermKey: termKey, State: state})
	}
	return results, nil
}

type ActivationAttempt struct {
	Acknowledged                               bool
	SafetyVerdict, SafetyError, CaptureContent string
	SubmitStatus, SubmitDetail, SubmitError    string
}

type ActivationTransport interface {
	Attempt(homeDir string, target afk.TargetResult, payload string) ActivationAttempt
}

// ActivateOnReceipt scans captain-owned receipt files and for each one
// not yet activation-seen, attempts a safe, idempotent activation nudge to
// the captain agent pane.
//
// The activation target is resolved from captain task meta in the parent
// General home (authoritative herdr_pane_id/herdr_session), NOT from the
// inherited HERDR_PANE_ID environment variable which may reflect the
// watcher/launcher pane instead of the captain agent pane.
//
// Unlike ReconcilePending (which only processes un-acked receipts), this
// function processes ALL receipts because the activation nudge is independent
// of the General relay lifecycle — a receipt may be relayed (acked) but still
// need the captain agent pane nudged.
//
// Safe inject guards are honored: if the captain agent pane is busy or the
// inject target is unsafe, the nudge is skipped and retried on the next cycle.
// Activation-seen is written ONLY after successful acknowledged submission.
//
// Returns the number of activation nudges that were successfully injected.
// Idempotent: safe to call repeatedly. Already activation-seen receipts are
// skipped without examining the pane target again.
func ActivateOnReceiptWithTransport(captainHome, parentHome string, transport ActivationTransport) int {
	allReceipts, err := listAllReceipts(captainHome)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: listing receipts for activation: %v\n", err)
		return 0
	}

	// Resolve the authoritative captain agent pane from captain task meta.
	// This uses the pane record from metadata (written at launch), never the
	// inherited HERDR_PANE_ID which may reflect the watcher/launcher pane.
	target, err := resolveCaptainActivationTarget(captainHome, parentHome)
	if err != nil || target.Handle == "" {
		// No authoritative target available. Do NOT mark activation-seen
		// so retries remain possible if meta becomes available later.
		diagLog(ActivationDiagnostic{
			TargetHandle:   target.Handle,
			ReceiptsFound:  len(allReceipts),
			ReceiptSkipped: "no-target",
			SafetyVerdict:  "unknown",
			SafetyError:    fmt.Sprintf("resolve target: %v", err),
		})
		return 0
	}

	if transport == nil {
		diagLog(ActivationDiagnostic{TargetHandle: target.Handle, ReceiptsFound: len(allReceipts), ReceiptSkipped: "no-transport"})
		return 0
	}

	count := 0
	for _, pr := range allReceipts {
		// Skip if already activation-seen (idempotency guard).
		if IsActivationSeen(captainHome, pr.TaskID, pr.TermKey) {
			continue
		}

		// Build the activation nudge message.
		nudge := fmt.Sprintf("[receipt] %s: soldier %s [key=%s]", pr.State, pr.TaskID, pr.TermKey)
		markedMsg := afk.Mark(nudge)

		attempt := transport.Attempt(captainHome, target, markedMsg)
		diag := ActivationDiagnostic{TargetHandle: target.Handle, ReceiptsFound: len(allReceipts), SafetyVerdict: attempt.SafetyVerdict, SafetyError: attempt.SafetyError, CaptureContent: attempt.CaptureContent, SubmitStatus: attempt.SubmitStatus, SubmitDetail: attempt.SubmitDetail, SubmitError: attempt.SubmitError}
		if !attempt.Acknowledged {
			diag.ReceiptSkipped = "submit-failed"
			if attempt.SubmitStatus == "" {
				diag.ReceiptSkipped = "unsafe"
			}
			diagLog(diag)
			// Submit failed (backend failure, endpoint dead, etc.).
			// Do NOT mark activation-seen — preserve retry.
			continue
		}

		// Only mark activation-seen after successful acknowledged submission.
		if err := MarkActivationSeen(captainHome, pr.TaskID, pr.TermKey); err != nil {
			fmt.Fprintf(os.Stderr, "warning: marking activation-seen for %s/%s: %v\n",
				pr.TaskID, pr.TermKey, err)
		}
		diag.ActivationSeen = true
		diagLog(diag)
		count++
	}
	return count
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
