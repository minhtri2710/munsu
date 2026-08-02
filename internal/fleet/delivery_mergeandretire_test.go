//go:build integration

package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestMergeAndRetire_AlreadyMergedSkipsMerge(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-already-merged"
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Write meta with delivery_state=merged (scout kind, no worktree so
	// retirement can proceed without a real backend).
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\ndelivery_state=merged\n"
	os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte(metaContent), 0644)

	// Create report.md so scout safety check passes when Force=false.
	reportDir := filepath.Join(homeDir, "data", taskID)
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, "report.md"), []byte("findings"), 0644)

	// Call MergeAndRetire with already-merged state.
	result := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Merge should be AlreadyMerged (skipped).
	if result.MergeOutcome != MergeOutcomeAlreadyMerged {
		t.Errorf("expected merge outcome %q, got %q", MergeOutcomeAlreadyMerged, result.MergeOutcome)
	}

	// Teardown should have run.
	if result.TeardownResult == nil {
		t.Fatal("expected teardown result")
	}
	if result.TeardownError != nil {
		t.Fatalf("unexpected teardown error: %v", result.TeardownError)
	}

	// Verify teardown steps include retirement actions.
	if len(result.TeardownResult.Steps) == 0 {
		t.Error("expected teardown steps")
	}

	// Meta should be removed after successful retirement.
	metaPath := filepath.Join(stateDir, taskID+".meta")
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Error("meta should have been removed after successful retirement")
	}

	// IsError should be false (both merge and teardown succeeded).
	if result.IsError() {
		t.Error("expected IsError=false for fully successful operation")
	}
}

func TestMergeAndRetire_AlreadyMergedPartialCleanupRetry(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-partial-retry"
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Phase 1: Setup meta with delivery_state=merged and a worktree path.
	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)

	metaContent := "kind=ship\nbackend=tmux\nwindow=@1\ndelivery_state=merged\nworktree=" + wtDir + "\n"
	os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte(metaContent), 0644)

	// Create residual artifacts.
	residuals := []string{
		taskID + ".status",
		taskID + ".check",
		taskID + ".turnend",
	}
	for _, name := range residuals {
		os.WriteFile(filepath.Join(stateDir, name), []byte("stale"), 0644)
	}

	// Phase 2: First retirement attempt — return worktree successfully but
	// simulate a failure that prevents cleanup (e.g., dispose error stops
	// the process before meta removal).
	// We use a fakeTeardown that returns an error on Dispose, which causes
	// RetireTask to fail before removing meta.
	firstResult := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true, disposeErr: os.ErrPermission}, fakeRetirementJournals{}, nil)
	if firstResult == nil {
		t.Fatal("expected non-nil first result")
	}

	// Merge should be AlreadyMerged (skipped).
	if firstResult.MergeOutcome != MergeOutcomeAlreadyMerged {
		t.Errorf("expected merge outcome %q, got %q", MergeOutcomeAlreadyMerged, firstResult.MergeOutcome)
	}

	// Teardown should have failed (dispose error).
	if firstResult.TeardownError == nil {
		t.Fatal("expected teardown error on first attempt")
	}
	if !strings.Contains(firstResult.TeardownError.Error(), "permission") {
		t.Errorf("expected permission error, got: %v", firstResult.TeardownError)
	}

	// Meta should still exist (preserved for retry).
	metaPath := filepath.Join(stateDir, taskID+".meta")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta should be preserved for retry, but got: %v", err)
	}

	// Verify delivery_state is still merged.
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Errorf("expected delivery_state to remain %q, got %q", DeliveryStateMerged, meta[MetaDeliveryState])
	}

	// IsError should be true (teardown failed).
	if !firstResult.IsError() {
		t.Error("expected IsError=true for partial failure")
	}

	// Phase 3: Retry — merge should be skipped again (already merged),
	// and retirement should resume from the beginning.
	secondResult := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, nil)
	if secondResult == nil {
		t.Fatal("expected non-nil second result")
	}

	// Merge should be AlreadyMerged again (skipped).
	if secondResult.MergeOutcome != MergeOutcomeAlreadyMerged {
		t.Errorf("expected merge outcome %q on retry, got %q", MergeOutcomeAlreadyMerged, secondResult.MergeOutcome)
	}

	// Teardown should succeed on retry.
	if secondResult.TeardownError != nil {
		t.Fatalf("unexpected teardown error on retry: %v", secondResult.TeardownError)
	}

	// Meta should be removed after successful retirement.
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Error("meta should have been removed after successful retirement on retry")
	}

	// IsError should be false.
	if secondResult.IsError() {
		t.Error("expected IsError=false after successful retry")
	}
}

func TestMergeAndRetire_AlreadyMergedWorktreeGone(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-worktree-gone"
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Setup meta with delivery_state=merged and a worktree path that no longer exists.
	metaContent := "kind=ship\nbackend=tmux\nwindow=@1\ndelivery_state=merged\nworktree=/nonexistent/worktree\n"
	os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte(metaContent), 0644)

	// Call MergeAndRetire with already-merged state.
	// The worktree path doesn't exist, but Force=true (due to alreadyMerged)
	// skips the worktree-based safety checks. RetireTask handles the missing
	// worktree path gracefully ("worktree path no longer exists").
	result := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.MergeOutcome != MergeOutcomeAlreadyMerged {
		t.Errorf("expected merge outcome %q, got %q", MergeOutcomeAlreadyMerged, result.MergeOutcome)
	}
	if result.TeardownError != nil {
		t.Fatalf("unexpected teardown error: %v", result.TeardownError)
	}
	if result.TeardownResult == nil {
		t.Fatal("expected teardown result")
	}

	// Verify the teardown handled the missing worktree gracefully.
	foundWorktreeGone := false
	for _, step := range result.TeardownResult.Steps {
		if strings.Contains(step, "worktree path no longer exists") {
			foundWorktreeGone = true
			break
		}
	}
	if !foundWorktreeGone {
		t.Errorf("expected teardown to handle missing worktree gracefully, steps: %v", result.TeardownResult.Steps)
	}

	if result.IsError() {
		t.Error("expected IsError=false for successful operation")
	}
}

func TestMergeAndRetire_MergeFailed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-merge-failed"
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Write meta without delivery_state=merged, so PRMerge would be called.
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\n"
	os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte(metaContent), 0644)

	// Call MergeAndRetire with a malformed PR URL so PRMerge fails.
	result := MergeAndRetire(homeDir, taskID, "not-a-valid-url", nil, fakeTeardown{}, fakeRetirementJournals{}, nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.MergeOutcome != MergeOutcomeFailed {
		t.Errorf("expected merge outcome %q, got %q", MergeOutcomeFailed, result.MergeOutcome)
	}
	if result.MergeDetail == "" {
		t.Error("expected non-empty merge detail")
	}

	// Teardown should not have been called.
	if result.TeardownResult != nil {
		t.Error("expected nil teardown result when merge failed")
	}
	if result.TeardownError != nil {
		t.Errorf("expected nil teardown error when merge failed, got: %v", result.TeardownError)
	}

	// IsError should be true.
	if !result.IsError() {
		t.Error("expected IsError=true for merge failure")
	}
}

func TestMergeAndRetire_NilResultIsError(t *testing.T) {
	var nilResult *MergeAndRetireResult
	if !nilResult.IsError() {
		t.Error("expected IsError=true for nil result")
	}
}

func TestMergeAndRetire_NilMeta(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-nonexistent"

	// No meta file exists — PRMerge won't be called because ReadMeta fails first.
	result := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{}, fakeRetirementJournals{}, nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MergeOutcome != MergeOutcomeFailed {
		t.Errorf("expected merge outcome %q, got %q", MergeOutcomeFailed, result.MergeOutcome)
	}
	if !result.IsError() {
		t.Error("expected IsError=true for nonexistent task")
	}
}

func TestMergeAndRetire_AlreadyMergedResidualArtifacts(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-residual-retry"
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Setup meta with delivery_state=merged and a scout kind (no worktree).
	metaContent := "kind=scout\nbackend=tmux\nwindow=@1\ndelivery_state=merged\n"
	os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte(metaContent), 0644)

	// Create report.md for scout.
	reportDir := filepath.Join(homeDir, "data", taskID)
	os.MkdirAll(reportDir, 0755)
	os.WriteFile(filepath.Join(reportDir, "report.md"), []byte("findings"), 0644)

	// Create residual artifacts.
	residuals := []string{
		taskID + ".status",
		taskID + ".check",
		taskID + ".turnend",
	}
	for _, name := range residuals {
		os.WriteFile(filepath.Join(stateDir, name), []byte("stale"), 0644)
	}

	// First attempt: success (all artifacts cleaned).
	result := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.TeardownError != nil {
		t.Fatalf("unexpected teardown error: %v", result.TeardownError)
	}

	// Verify residual artifacts were removed.
	for _, name := range residuals {
		path := filepath.Join(stateDir, name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("residual %s should have been removed, but still exists", name)
		}
	}

	// Verify teardown steps mention residual removal.
	foundResidual := false
	for _, step := range result.TeardownResult.Steps {
		if strings.Contains(step, "residual") {
			foundResidual = true
			break
		}
	}
	if !foundResidual {
		t.Errorf("expected teardown steps to mention residual removal, got: %v", result.TeardownResult.Steps)
	}
}
