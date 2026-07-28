package captain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

type captainTestProbe struct{}

func (captainTestProbe) Probe(string, map[string]string) (bool, error) { return false, nil }

type captainTestSender struct{}

func (captainTestSender) Alive(string, map[string]string) (bool, error) { return false, nil }
func (captainTestSender) Send(string, map[string]string, string) orchestrator.BoundSendResult {
	return orchestrator.BoundSendResult{}
}

func captainRunCycle(home string) (bool, error) {
	return orchestrator.RunCycleWithProbeAndSender(home, captainTestProbe{}, captainTestSender{}, NewWatcherHooks(&captainNotificationTransport{acknowledged: true}, nil), orchestrator.NoopRetirementPort{}, orchestrator.NoopTaskStatePort{})
}

// --- WatcherStatusSummary tests ---

func TestWatcherStatusSummary_Absent(t *testing.T) {
	tmp := t.TempDir()
	status := WatcherStatusSummary(tmp)
	if status != WatcherAbsent {
		t.Errorf("expected absent, got %s", status)
	}
}

func TestWatcherStatusSummary_StoppedWithIdentity(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Write an identity file without a beat — simulates crash residue.
	id := orchestrator.NewIdentity(tmp)
	orchestrator.WriteIdentity(tmp, id)

	status := WatcherStatusSummary(tmp)
	if status != WatcherStopped {
		t.Errorf("expected stopped (identity without beat), got %s", status)
	}
}

func TestWatcherStatusSummary_StoppedStaleBeat(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Write an old beat beyond the stale threshold.
	old := time.Now().Add(-2 * orchestrator.StaleThreshold())
	beatPath := orchestrator.BeatPath(tmp)
	os.WriteFile(beatPath, []byte(old.Format("060102150405")+" 99999\n"), 0644)

	status := WatcherStatusSummary(tmp)
	if status != WatcherStopped {
		t.Errorf("expected stopped (stale beat), got %s", status)
	}
}

// --- EnsureWatcher tests ---

func TestEnsureWatcher_NoChildWorkAndAbsent(t *testing.T) {
	tmp := t.TempDir()
	// No child work + no watcher = no-op (idempotent).
	if err := EnsureWatcher(tmp, false); err != nil {
		t.Fatalf("EnsureWatcher(false) on absent: %v", err)
	}
	// Verify no watcher was started.
	status := WatcherStatusSummary(tmp)
	if status != WatcherAbsent && status != WatcherStopped {
		t.Errorf("expected absent/stopped, got %s", status)
	}
}

func TestEnsureWatcher_StartsWhenChildWorkInFlight(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Set up valid parent-home config so EnsureWatcher validation passes.
	if err := config.Set(tmp, "parent-home", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	// Simulate child work by creating a soldier meta file.
	soldierMeta := map[string]string{"kind": "ship", "window": "win-1"}
	if err := mhome.WriteMeta(tmp, "soldier-1", soldierMeta); err != nil {
		t.Fatal(err)
	}

	// With child work in flight and no watcher, EnsureWatcher should start one.
	if err := EnsureWatcher(tmp, true); err != nil {
		t.Fatalf("EnsureWatcher(true): %v", err)
	}

	// Starting a watcher subprocess in test environment requires a real munsu binary.
	// We skip the beat validation here; integration tests cover the full path.
}

func TestEnsureWatcher_StopsWhenNoChildWork(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Simulate a watcher identity and beat.
	id := orchestrator.NewIdentity(tmp)
	orchestrator.WriteIdentity(tmp, id)
	orchestrator.WriteBeat(tmp)

	status := WatcherStatusSummary(tmp)
	if status != WatcherStopped {
		t.Skipf("watcher status is %s -- no actual watcher process to validate ownership; skip stop test", status)
	}
	// We can't actually stop a non-running watcher, but EnsureWatcher(false)
	// should be idempotent.
	if err := EnsureWatcher(tmp, false); err != nil {
		t.Fatalf("EnsureWatcher(false) with orphan artifacts: %v", err)
	}
}

// --- ConvergeWatcherStatus tests ---

func TestConvergeWatcherStatus_EmptyRegistry(t *testing.T) {
	results := ConvergeWatcherStatus(nil)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}

	results = ConvergeWatcherStatus([]Info{})
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestConvergeWatcherStatus_SkipsEmptyHome(t *testing.T) {
	results := ConvergeWatcherStatus([]Info{
		{ID: "valid", Home: t.TempDir()},
		{ID: "empty", Home: ""},
	})
	if len(results) != 1 {
		t.Errorf("expected 1 result (skipped empty home), got %d", len(results))
	}
	if results[0].CaptainID != "valid" {
		t.Errorf("captain id = %q, want valid", results[0].CaptainID)
	}
}

func TestConvergeWatcherStatus_AllAbsent(t *testing.T) {
	tmp1 := t.TempDir()
	tmp2 := t.TempDir()

	results := ConvergeWatcherStatus([]Info{
		{ID: "c1", Home: tmp1},
		{ID: "c2", Home: tmp2},
	})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status != WatcherAbsent {
			t.Errorf("captain %s: expected absent, got %s", r.CaptainID, r.Status)
		}
	}
}

// --- inFlightSoldierPath tests ---

func TestInFlightSoldierPath_Empty(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)

	if inFlightSoldierPath(tmp) {
		t.Error("expected false for empty captain home")
	}
}

func TestInFlightSoldierPath_WithSoldier(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	mhome.WriteMeta(tmp, "soldier-1", map[string]string{"kind": "ship", "window": "w1"})

	if !inFlightSoldierPath(tmp) {
		t.Error("expected true when soldier meta exists")
	}
}

func TestInFlightSoldierPath_IgnoresNonShip(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Captain meta should not count as child work.
	mhome.WriteMeta(tmp, "captain:test", map[string]string{"kind": "captain", "window": "w1"})

	if inFlightSoldierPath(tmp) {
		t.Error("expected false for non-ship/scout meta")
	}
}

// --- Real-hook E2E: per-cycle terminal reconciliation via reconcileHook ---
//
// These tests invoke orchestrator.RunCycle against the REAL captain
// reconcileHook (relay.go:31), NOT a test-local orchestrator.RelayPendingReceipts
// substitute. They verify receipt ACK + obligation closure, then assert
// exactly-once wake relay after repeated cycles.

// setupE2E creates a captain home (with state dir and provenance marker),
// a general (parent) home (with state dir), sets MUNSU_PARENT_STATUS, and
// seeds provenance. Returns (captainHome, generalHome).
func setupE2E(t *testing.T) (captainHome, generalHome string) {
	t.Helper()
	cptHome := t.TempDir()
	genHome := t.TempDir()
	os.MkdirAll(filepath.Join(cptHome, "state"), 0755)
	os.MkdirAll(filepath.Join(genHome, "state"), 0755)
	if err := SeedProvenance(cptHome, "test-captain"); err != nil {
		t.Fatalf("SeedProvenance: %v", err)
	}
	return cptHome, genHome
}

// setParentStatus sets MUNSU_PARENT_STATUS to parentHome and returns a
// cleanup function that restores the previous value.
func setParentStatus(parentHome string) func() {
	old := os.Getenv("MUNSU_PARENT_STATUS")
	os.Setenv("MUNSU_PARENT_STATUS", parentHome)
	return func() { os.Setenv("MUNSU_PARENT_STATUS", old) }
}

// writeReceiptAndObligation writes a receipt and initializes obligations.
func writeReceiptAndObligation(t *testing.T, home, taskID, termKey, state, msg string) {
	t.Helper()
	if err := orchestrator.WriteReceipt(home, taskID, termKey, state, msg); err != nil {
		t.Fatalf("WriteReceipt(%s): %v", taskID, err)
	}
	if err := orchestrator.InitTaskObligations(home, taskID, termKey); err != nil {
		t.Fatalf("InitTaskObligations(%s): %v", taskID, err)
	}
}

// TestCaptainPerCycle_RealHook_RelaysPostStartupReceipt verifies that the
// real reconcileHook relays a terminal receipt that arrives AFTER the startup
// recovery cycle. This is the core E2E: post-startup receipt → per-cycle
// reconcile → ACK → General wake — without manual converge/restart.
func TestCaptainPerCycle_RealHook_RelaysPostStartupReceipt(t *testing.T) {
	cptHome, genHome := setupE2E(t)
	taskID := "e2e-post-startup"
	termKey := "e2e-key"
	defer setParentStatus(genHome)()

	// First cycle — startup recovery (no receipts pending).
	// The real reconcileHook is passed explicitly to RunCycleWithProbeAndSender.
	emitted, err := captainRunCycle(cptHome)
	if err != nil {
		t.Fatalf("first RunCycle: %v", err)
	}
	_ = emitted

	// Soldier completes AFTER startup: write receipt now.
	writeReceiptAndObligation(t, cptHome, taskID, termKey, "done", "post-startup complete")

	// Second cycle — per-cycle reconcile via real hook should relay the receipt.
	emitted2, err := captainRunCycle(cptHome)
	if err != nil {
		t.Fatalf("second RunCycle: %v", err)
	}
	_ = emitted2

	// Verify receipt was acked.
	if !orchestrator.IsReceiptAcked(cptHome, taskID, termKey) {
		t.Error("receipt should be acked after per-cycle reconcile")
	}

	// Verify General received the relay status.
	relayPath := filepath.Join(genHome, "state", "captain:test-captain.relay-"+taskID+".status")
	data, err := os.ReadFile(relayPath)
	if err != nil {
		t.Fatalf("General relay status should exist: %v", err)
	}
	if !strings.Contains(string(data), "done") {
		t.Errorf("relay status should contain 'done', got: %s", string(data))
	}

	// Verify obligation is closed.
	open, err := orchestrator.IsTaskReportRelayOpen(cptHome, taskID)
	if err != nil {
		t.Fatalf("IsTaskReportRelayOpen: %v", err)
	}
	if open {
		t.Error("ReportRelay should be closed after per-cycle reconcile")
	}
}

// TestCaptainPerCycle_RealHook_ExactlyOneWake verifies that multiple cycles
// after a single receipt produce exactly one relay and no duplicate General
// wake. This proves idempotency of the real reconcileHook.
func TestCaptainPerCycle_RealHook_ExactlyOneWake(t *testing.T) {
	cptHome, genHome := setupE2E(t)
	taskID := "e2e-exactly-one"
	termKey := "e2e-one-key"

	defer setParentStatus(genHome)()

	// Write receipt BEFORE first cycle (startup recovery will relay it).
	writeReceiptAndObligation(t, cptHome, taskID, termKey, "done", "startup pending")

	// First cycle — startup recovery relays via real hook.
	emitted, err := captainRunCycle(cptHome)
	if err != nil {
		t.Fatalf("first RunCycle: %v", err)
	}
	_ = emitted

	// Verify receipt acked.
	if !orchestrator.IsReceiptAcked(cptHome, taskID, termKey) {
		t.Fatal("receipt should be acked after startup recovery")
	}

	// Read initial relay status line count.
	relayPath := filepath.Join(genHome, "state", "captain:test-captain.relay-"+taskID+".status")
	data1, err := os.ReadFile(relayPath)
	if err != nil {
		t.Fatalf("relay status should exist: %v", err)
	}
	lines1 := len(strings.Split(strings.TrimSpace(string(data1)), "\n"))

	// Run 3 more cycles — no duplicate relay should occur.
	for i := 0; i < 3; i++ {
		emittedN, err := captainRunCycle(cptHome)
		if err != nil {
			t.Fatalf("cycle %d: %v", i+2, err)
		}
		_ = emittedN
	}

	// Verify line count did not grow.
	data2, err := os.ReadFile(relayPath)
	if err != nil {
		t.Fatalf("relay status should still exist: %v", err)
	}
	lines2 := len(strings.Split(strings.TrimSpace(string(data2)), "\n"))
	if lines2 != lines1 {
		t.Errorf("relay status grew from %d to %d lines (duplicate relay after %d idle cycles)", lines1, lines2, 3)
	}

	// Receipt remains acked.
	if !orchestrator.IsReceiptAcked(cptHome, taskID, termKey) {
		t.Error("receipt should remain acked after multiple cycles")
	}
}

// TestCaptainPerCycle_RealHook_MultipleReceipts verifies that multiple
// receipts written in sequence across cycles are each relayed by the real hook.
func TestCaptainPerCycle_RealHook_MultipleReceipts(t *testing.T) {
	cptHome, genHome := setupE2E(t)
	defer setParentStatus(genHome)()

	task1 := "e2e-multi-1"
	key1 := "mk-1"
	task2 := "e2e-multi-2"
	key2 := "mk-2"

	// First cycle — no receipts (startup recovery is no-op).
	emitted, err := captainRunCycle(cptHome)
	if err != nil {
		t.Fatalf("first RunCycle: %v", err)
	}
	_ = emitted

	// Write first receipt.
	writeReceiptAndObligation(t, cptHome, task1, key1, "done", "first")

	// Second cycle — should relay first receipt via real hook.
	emitted2, err := captainRunCycle(cptHome)
	if err != nil {
		t.Fatalf("second RunCycle: %v", err)
	}
	_ = emitted2

	if !orchestrator.IsReceiptAcked(cptHome, task1, key1) {
		t.Fatal("first receipt should be acked after second cycle")
	}

	// Write second receipt (simulating another soldier finishing later).
	writeReceiptAndObligation(t, cptHome, task2, key2, "done", "second")

	// Third cycle — should relay second receipt; first is already acked.
	emitted3, err := captainRunCycle(cptHome)
	if err != nil {
		t.Fatalf("third RunCycle: %v", err)
	}
	_ = emitted3

	if !orchestrator.IsReceiptAcked(cptHome, task2, key2) {
		t.Fatal("second receipt should be acked after third cycle")
	}

	// Both relay statuses exist in General.
	for _, pair := range [][2]string{{task1, key1}, {task2, key2}} {
		tID := pair[0]
		relayPath := filepath.Join(genHome, "state", "captain:test-captain.relay-"+tID+".status")
		if _, err := os.Stat(relayPath); os.IsNotExist(err) {
			t.Errorf("relay status for %s should exist", tID)
		}
	}
}

// TestCaptainPerCycle_RealHook_TwoWatchersIsolated verifies that two
// independent watchers (separate homes, separate generals) each process
// their own terminal receipts using the real reconcileHook, without
// interfering with each other. This tests the same-binary two-watcher
// isolation contract: both watchers run from the same binary/process
// but operate on independent file-system homes.
func TestCaptainPerCycle_RealHook_TwoWatchersIsolated(t *testing.T) {
	// Create two independent captain+general pairs.
	cpt1, gen1 := setupE2E(t)
	task1 := "two-watcher-1"
	cpt2, gen2 := setupE2E(t)
	task2 := "two-watcher-2"

	// Run initial cycle on both watchers (startup recovery — no receipts).
	// Each watcher runs with its own MUNSU_PARENT_STATUS set.
	func() {
		defer setParentStatus(gen1)()
		if _, err := captainRunCycle(cpt1); err != nil {
			t.Errorf("watcher 1 first cycle: %v", err)
		}
	}()
	func() {
		defer setParentStatus(gen2)()
		if _, err := captainRunCycle(cpt2); err != nil {
			t.Errorf("watcher 2 first cycle: %v", err)
		}
	}()

	// Write receipts for both watchers (post-startup).
	writeReceiptAndObligation(t, cpt1, task1, "key-1", "done", "watcher 1 complete")
	writeReceiptAndObligation(t, cpt2, task2, "key-2", "done", "watcher 2 complete")

	// Run per-cycle reconcile on both watchers (sequentially, each with own parent).
	func() {
		defer setParentStatus(gen1)()
		if _, err := captainRunCycle(cpt1); err != nil {
			t.Errorf("watcher 1 second cycle: %v", err)
		}
	}()
	func() {
		defer setParentStatus(gen2)()
		if _, err := captainRunCycle(cpt2); err != nil {
			t.Errorf("watcher 2 second cycle: %v", err)
		}
	}()

	// Verify watcher 1's receipt was acked (isolated from watcher 2).
	if !orchestrator.IsReceiptAcked(cpt1, task1, "key-1") {
		t.Error("watcher 1 receipt should be acked (isolation from watcher 2)")
	}
	if !orchestrator.IsReceiptAcked(cpt2, task2, "key-2") {
		t.Error("watcher 2 receipt should be acked (isolation from watcher 1)")
	}

	// Verify watcher 1's obligation is closed in its own general, watcher 2 in its own.
	for _, pair := range [][3]string{
		{cpt1, gen1, task1},
		{cpt2, gen2, task2},
	} {
		cpt, gen, tid := pair[0], pair[1], pair[2]
		relayPath := filepath.Join(gen, "state", "captain:test-captain.relay-"+tid+".status")
		if _, err := os.Stat(relayPath); os.IsNotExist(err) {
			t.Errorf("relay status for %s should exist in its own general", tid)
		}
		open, err := orchestrator.IsTaskReportRelayOpen(cpt, tid)
		if err != nil {
			t.Fatalf("IsTaskReportRelayOpen(%s): %v", tid, err)
		}
		if open {
			t.Errorf("ReportRelay for %s should be closed", tid)
		}
	}

	// Additional isolation check: watcher 1's receipt must NOT appear in watcher 2's space.
	cross1 := filepath.Join(gen2, "state", "captain:test-captain.relay-"+task1+".status")
	if _, err := os.Stat(cross1); err == nil {
		t.Error("watcher 1 receipt must NOT appear in watcher 2's general")
	}
	cross2 := filepath.Join(gen1, "state", "captain:test-captain.relay-"+task2+".status")
	if _, err := os.Stat(cross2); err == nil {
		t.Error("watcher 2 receipt must NOT appear in watcher 1's general")
	}
}

// --- Captain activation-on-receipt integration tests ---

// TestCaptainActivationOnReceipt_HookWired verifies that the captain init()
// correctly wires CaptainActivationHook and that calling RunCycle with a
// captain context (MUNSU_PARENT_STATUS set) triggers ActivateOnReceipt.
// Without captain meta (no parent meta with herdr_pane_id), activation-seen
// markers are NOT written — retries remain possible.
func TestCaptainActivationOnReceipt_HookWired(t *testing.T) {
	cptHome, genHome := setupE2E(t)
	defer setParentStatus(genHome)()
	taskID, termKey := "activation-e2e", "e2e-key"

	// Write a pending receipt (captain has received soldier report).
	writeReceiptAndObligation(t, cptHome, taskID, termKey, "done", "activation test")

	// Run cycles to get past startup recovery and trigger per-cycle hooks.
	captainRunCycle(cptHome)
	captainRunCycle(cptHome)

	// Without captain meta (genHome has no captain:<id>.meta with herdr_pane_id),
	// activation-seen must NOT be written — retries must remain possible.
	if orchestrator.IsActivationSeen(cptHome, taskID, termKey) {
		t.Error("activation-seen should NOT be written without captain meta")
	}

	// Another cycle — idempotency: the hook must not panic or error.
	captainRunCycle(cptHome)
}

// TestCaptainActivationOnReceipt_IdempotentSegregation verifies that
// activation-seen markings are independent of ack/relay markers. Receipts
// already marked activation-seen are not re-processed, while new receipts
// receive an activation attempt only when meta provides a target.
func TestCaptainActivationOnReceipt_IdempotentSegregation(t *testing.T) {
	cptHome, genHome := setupE2E(t)
	defer setParentStatus(genHome)()

	taskSeen := "seen-task"
	keySeen := "seen-key"
	taskNotSeen := "not-seen-task"
	keyNotSeen := "not-seen-key"

	// First receipt: write + mark activation-seen.
	writeReceiptAndObligation(t, cptHome, taskSeen, keySeen, "done", "seen")
	if err := orchestrator.MarkActivationSeen(cptHome, taskSeen, keySeen); err != nil {
		t.Fatalf("MarkActivationSeen: %v", err)
	}

	// Second receipt: write but do NOT mark activation-seen.
	writeReceiptAndObligation(t, cptHome, taskNotSeen, keyNotSeen, "failed", "not seen")

	// Run cycles until the per-cycle hook fires (at least 2 cycles to
	// get past recovery guard).
	captainRunCycle(cptHome)
	captainRunCycle(cptHome)

	// First receipt should still be activation-seen (pre-written marker).
	if !orchestrator.IsActivationSeen(cptHome, taskSeen, keySeen) {
		t.Error("first receipt should remain activation-seen")
	}

	// Without captain meta (no herdr_pane_id in genHome meta),
	// second receipt must NOT be activation-seen.
	if orchestrator.IsActivationSeen(cptHome, taskNotSeen, keyNotSeen) {
		t.Error("second receipt should NOT be activation-seen without captain meta")
	}

	// Both should become acked by the terminal reconcile hook.
	if !orchestrator.IsReceiptAcked(cptHome, taskSeen, keySeen) {
		t.Error("first receipt should be acked")
	}
	if !orchestrator.IsReceiptAcked(cptHome, taskNotSeen, keyNotSeen) {
		t.Error("second receipt should be acked")
	}
}

// TestCaptainActivationOnReceipt_NoParentContext verifies that when
// MUNSU_PARENT_STATUS equals the home dir (not a real captain context),
// the captainActivationHook is a no-op and does not attempt activation.
func TestCaptainActivationOnReceipt_NoParentContext(t *testing.T) {
	captainHome := t.TempDir()
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)
	if err := SeedProvenance(captainHome, "test-captain"); err != nil {
		t.Fatalf("SeedProvenance: %v", err)
	}
	taskID, termKey := "no-parent", "nop-key"

	writeReceiptAndObligation(t, captainHome, taskID, termKey, "done", "no parent context")

	// Set MUNSU_PARENT_STATUS to captainHome itself (not a real parent).
	defer setParentStatus(captainHome)()

	// Cycle 1 (recovery).
	captainRunCycle(captainHome)

	// Cycle 2 (per-cycle) — hook should see parent == home and skip.
	captainRunCycle(captainHome)

	// Activation-seen should NOT be written because the hook short-circuits
	// when parentHome == homeDir.
	if orchestrator.IsActivationSeen(captainHome, taskID, termKey) {
		t.Error("activation-seen should NOT be written when parentHome == homeDir")
	}
}

// TestCaptainActivationOnReceipt_NoParentEnv verifies that when
// MUNSU_PARENT_STATUS is not set, the captainActivationHook is a no-op.
func TestCaptainActivationOnReceipt_NoParentEnv(t *testing.T) {
	captainHome := t.TempDir()
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)
	if err := SeedProvenance(captainHome, "test-captain"); err != nil {
		t.Fatalf("SeedProvenance: %v", err)
	}
	taskID, termKey := "no-env", "noenv-key"

	writeReceiptAndObligation(t, captainHome, taskID, termKey, "done", "no parent env")

	// Ensure MUNSU_PARENT_STATUS is not set (clean up any prior value).
	oldParent := os.Getenv("MUNSU_PARENT_STATUS")
	os.Unsetenv("MUNSU_PARENT_STATUS")
	defer os.Setenv("MUNSU_PARENT_STATUS", oldParent)

	// Cycle 1 (recovery).
	captainRunCycle(captainHome)

	// Cycle 2 (per-cycle) — hook should see empty parent and skip.
	captainRunCycle(captainHome)

	// Activation-seen should NOT be written.
	if orchestrator.IsActivationSeen(captainHome, taskID, termKey) {
		t.Error("activation-seen should NOT be written when MUNSU_PARENT_STATUS is unset")
	}
}
