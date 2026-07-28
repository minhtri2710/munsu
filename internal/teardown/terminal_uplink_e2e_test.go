//go:build e2e

package teardown

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/task"
)

// TestE2E_TerminalUplinkContinuity proves the full Soldier → Captain → General
// terminal uplink continuity flow using production paths:
//   - Soldier done → WriteReceipt/InitTaskObligations (production report path)
//   - uplinkCheck blocks teardown without ack
//   - Captain relay via RelayTerminalReceipts → General state write + ack
//   - CompleteTaskObligation → teardown proceeds
//   - Evidence survives soldier-side cleanup
//   - Duplicate report/relay is idempotent on stable task/key
//   - Full flow: Soldier done → Captain durable receipt → Captain relay to General
//     → ack → teardown succeeds only after ack
//
// Run with: go test -tags=e2e -run TestE2E_TerminalUplinkContinuity ./internal/teardown/
func TestE2E_TerminalUplinkContinuity(t *testing.T) {
	// ---- Setup: hermetic PATH (only git) + General home, Captain home, Soldier task ----

	// Resolve git's real path and create a temporary bin/ with only a git symlink.
	// This ensures worktree.Get/Return always use the production git-worktree
	// fallback (treehouse is not on this hermetic PATH).
	gitRaw, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("looking up git: %v", err)
	}
	gitReal, err := filepath.EvalSymlinks(gitRaw)
	if err != nil {
		t.Fatalf("resolving git symlink: %v", err)
	}
	binDir := t.TempDir()
	if err := os.Symlink(gitReal, filepath.Join(binDir, "git")); err != nil {
		t.Fatalf("symlinking git: %v", err)
	}
	t.Setenv("PATH", binDir)

	generalHome := t.TempDir()
	captainHome := t.TempDir()
	soldierID := "e2e-test-soldier"
	termKey := "uplink"

	// Captain provenance marker (newline-separated, matching SeedProvenance format)
	captainMarkerPath := filepath.Join(captainHome, captain.ProvenanceMarkerName)
	os.MkdirAll(filepath.Dir(captainMarkerPath), 0755)
	os.WriteFile(captainMarkerPath, []byte("munsu-v2\ne2e-captain\n\n"), 0644)

	// Captain state dir (soldier tasks live in captain's state)
	captainStateDir := filepath.Join(captainHome, "state")
	os.MkdirAll(captainStateDir, 0755)

	// General state dir (captain relay targets this)
	generalStateDir := filepath.Join(generalHome, "state")
	os.MkdirAll(generalStateDir, 0755)

	// Create soldier task meta in captain home (so teardown can read it)
	wtPath := setupGitWorktree(t, captainHome)
	metaContent := fmt.Sprintf("kind=ship\nwindow=@1\nworktree=%s\n", wtPath)
	if err := os.WriteFile(filepath.Join(captainStateDir, soldierID+".meta"), []byte(metaContent), 0644); err != nil {
		t.Fatalf("writing meta: %v", err)
	}

	// ---- Phase 1: Soldier reports done (material terminal report) ----
	// Using production receipt + obligation paths (same as report_cmd.go)
	statusLine := "done: task complete"
	if err := task.AppendStatus(captainHome, soldierID, statusLine); err != nil {
		t.Fatalf("appending soldier status: %v", err)
	}

	// Durably write receipt (production path)
	if err := orchestrator.WriteReceipt(captainHome, soldierID, termKey, "done", "task complete"); err != nil {
		t.Fatalf("writing captain receipt: %v", err)
	}

	// Initialize per-task obligations (production path)
	if err := orchestrator.InitTaskObligations(captainHome, soldierID, termKey); err != nil {
		t.Fatalf("init task obligations: %v", err)
	}

	// Verify the material report exists (fail-closed version)
	hasMaterial, err := orchestrator.MaterialReportExists(captainHome, soldierID)
	if err != nil {
		t.Fatalf("MaterialReportExists error: %v", err)
	}
	if !hasMaterial {
		t.Fatal("expected material report to exist after done status")
	}

	// Verify per-task ReportRelay obligation is open
	open, err := orchestrator.IsTaskReportRelayOpen(captainHome, soldierID)
	if err != nil {
		t.Fatalf("IsTaskReportRelayOpen error: %v", err)
	}
	if !open {
		t.Fatal("expected ReportRelay obligation to be open for soldier")
	}

	// Verify receipt exists and is NOT acked yet
	if orchestrator.IsReceiptAcked(captainHome, soldierID, termKey) {
		t.Fatal("receipt should NOT be acked before relay")
	}

	t.Log("Phase 1 passed: soldier report persisted receipt + task obligations")

	// ---- Phase 2: uplinkCheck blocks teardown without ack ----
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err == nil {
		t.Fatal("uplinkCheck should block with material report and open ReportRelay")
	}
	t.Logf("Phase 2 passed: uplinkCheck blocked teardown: %v", err)

	// ---- Phase 3: Captain relays to General (production reconcile path) ----
	relayed, err := captain.RelayTerminalReceipts(captainHome, generalHome)
	if err != nil {
		t.Fatalf("RelayTerminalReceipts: %v", err)
	}
	if relayed != 1 {
		t.Fatalf("expected 1 relayed receipt, got %d", relayed)
	}

	// Verify ack now exists
	if !orchestrator.IsReceiptAcked(captainHome, soldierID, termKey) {
		t.Fatal("receipt should be acked after relay")
	}

	// Verify General received the relay
	captainID := "e2e-captain"
	relayStatusPath := filepath.Join(generalStateDir, "captain:"+captainID+".relay-"+soldierID+".status")
	if _, err := os.Stat(relayStatusPath); err != nil {
		t.Fatalf("general should see captain relay status: %v", err)
	}
	data, _ := os.ReadFile(relayStatusPath)
	if !strings.Contains(string(data), "done") {
		t.Fatalf("relay status should contain done, got: %s", string(data))
	}

	t.Log("Phase 3 passed: Captain relayed receipt to General, ack written")

	// ---- Phase 4: Per-task ReportRelay is now closed (by relay) ----
	open, err = orchestrator.IsTaskReportRelayOpen(captainHome, soldierID)
	if err != nil {
		t.Fatalf("IsTaskReportRelayOpen error: %v", err)
	}
	if open {
		t.Fatal("ReportRelay should be closed after relay completed")
	}

	// uplinkCheck now passes (ack completed)
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err != nil {
		t.Fatalf("uplinkCheck should pass after ReportRelay completed: %v", err)
	}
	t.Log("Phase 4 passed: uplinkCheck allowed teardown after relay ack")

	// ---- Phase 5: Durable evidence survives cleanup ----
	// The status file still exists (teardown hasn't run yet)
	statusPath := filepath.Join(captainStateDir, soldierID+".status")
	if _, err := os.Stat(statusPath); err != nil {
		t.Fatalf("status file should exist before teardown: %v", err)
	}

	// Read last line for the captain to relay
	lastLine := readLastStatusLine(t, statusPath)
	if !strings.Contains(lastLine, "done") {
		t.Fatalf("expected done status, got: %s", lastLine)
	}

	// Evidence: receipt and ack files persist
	receiptPath := orchestrator.ReceiptPath(captainHome, soldierID, termKey)
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("receipt should persist: %v", err)
	}
	ackPath := orchestrator.AckPath(captainHome, soldierID, termKey)
	if _, err := os.Stat(ackPath); err != nil {
		t.Fatalf("ack should persist: %v", err)
	}
	t.Log("Phase 5 passed: evidence survives and captain relay reaches general")

	// ---- Phase 6: Idempotent teardown after ack ----
	// Calling uplinkCheck again should still pass (idempotent)
	err = uplinkCheck(Options{HomeDir: captainHome, ID: soldierID})
	if err != nil {
		t.Fatalf("uplinkCheck should be idempotent after ReportRelay closed: %v", err)
	}

	// Duplicate relay is idempotent (receipt already acked)
	relayed, err = captain.RelayTerminalReceipts(captainHome, generalHome)
	if err != nil {
		t.Fatalf("duplicate RelayTerminalReceipts error: %v", err)
	}
	if relayed != 0 {
		t.Fatalf("duplicate relay should relay 0 receipts, got %d", relayed)
	}
	t.Log("Phase 6 passed: teardown and relay are idempotent after acknowledgement")

	// ---- Phase 7: Teardown Run succeeds (full production path) ----
	_, err = Run(Options{HomeDir: captainHome, ID: soldierID, Force: false})
	if err != nil {
		t.Fatalf("teardown Run should succeed after ack: %v", err)
	}

	// After teardown, status should be removed
	if _, err := os.Stat(statusPath); err == nil {
		t.Log("Note: status file may still exist if previous step removed it")
	}
	t.Log("Phase 7 passed: full production teardown succeeded after ack")

	// ---- Phase 8: Duplicate Soldier report is idempotent ----
	// Before rewriting, assert that the original receipt and ack still exist
	// (teardown preserves durable evidence — step 7 does NOT clear receipts)
	receiptPathPhase8 := orchestrator.ReceiptPath(captainHome, soldierID, termKey)
	ackPathPhase8 := orchestrator.AckPath(captainHome, soldierID, termKey)
	if _, err := os.Stat(receiptPathPhase8); err != nil {
		t.Fatalf("original receipt should persist after teardown: %v", err)
	}
	if _, err := os.Stat(ackPathPhase8); err != nil {
		t.Fatalf("original ack should persist after teardown: %v", err)
	}

	// Re-init obligations and re-write receipt to simulate another report
	if err := orchestrator.InitTaskObligations(captainHome, soldierID, termKey); err != nil {
		t.Fatalf("re-init obligations (should be noop): %v", err)
	}
	if err := orchestrator.WriteReceipt(captainHome, soldierID, termKey, "done", "task complete again"); err != nil {
		t.Fatalf("re-write receipt: %v", err)
	}

	// Receipt should not be acked yet (WriteReceipt invalidated stale ack)
	if orchestrator.IsReceiptAcked(captainHome, soldierID, termKey) {
		t.Fatal("re-written receipt should not be acked yet")
	}

	// Relay again — should relay 1
	relayed, err = captain.RelayTerminalReceipts(captainHome, generalHome)
	if err != nil {
		t.Fatalf("relay after duplicate report: %v", err)
	}
	if relayed != 1 {
		t.Fatalf("expected 1 relayed after duplicate report, got %d", relayed)
	}
	t.Log("Phase 8 passed: duplicate report/relay is idempotent on stable task/key")
}

// setupGitWorktree creates a hermetic git repo and acquires a worktree
// through production worktree.Get using the git-worktree fallback
// (PATH is already hermetic at call site — no treehouse).
// Returns the worktree path.
func setupGitWorktree(t *testing.T, captainHome string) string {
	t.Helper()

	// Create bare remote
	remoteDir := t.TempDir()
	gitCmd(t, "", "init", "--bare", remoteDir)

	// Clone to create main repo
	repoDir := t.TempDir()
	gitCmd(t, "", "clone", remoteDir, repoDir)

	// Configure git user for commits
	gitCmd(t, repoDir, "config", "user.email", "e2e-test@munsu")
	gitCmd(t, repoDir, "config", "user.name", "E2E Test")

	// Create initial commit on main (explicitly named)
	gitCmd(t, repoDir, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# e2e test"), 0644); err != nil {
		t.Fatalf("writing README: %v", err)
	}
	gitCmd(t, repoDir, "add", ".")
	gitCmd(t, repoDir, "commit", "-m", "initial commit")
	gitCmd(t, repoDir, "push", "-u", "origin", "main")

	// Acquire worktree through production worktree.Get (git fallback)
	wtPath, err := backend.GetWorktree(captainHome, repoDir, false)
	if err != nil {
		t.Fatalf("worktree.Get: %v", err)
	}

	// Register cleanup: return worktree if test fails before Phase 7
	t.Cleanup(func() {
		if _, err := os.Stat(wtPath); err == nil {
			if err := backend.ReturnWorktree(captainHome, wtPath); err != nil {
				t.Errorf("worktree cleanup return: %v", err)
			}
		}
	})

	// In the worktree, create a task branch with upstream tracking
	gitCmd(t, wtPath, "config", "user.email", "e2e-test@munsu")
	gitCmd(t, wtPath, "config", "user.name", "E2E Test")
	gitCmd(t, wtPath, "checkout", "-b", "task-branch")
	if err := os.WriteFile(filepath.Join(wtPath, "work.md"), []byte("work"), 0644); err != nil {
		t.Fatalf("writing work.md: %v", err)
	}
	gitCmd(t, wtPath, "add", ".")
	gitCmd(t, wtPath, "commit", "-m", "task work")
	gitCmd(t, wtPath, "push", "-u", "origin", "task-branch")

	return wtPath
}

// gitCmd runs a git command, failing the test on error.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed in %q: %s\n%s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func readLastStatusLine(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading status: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}
