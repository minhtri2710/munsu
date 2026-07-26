package domain_test

import (
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestTaskMetaIO(t *testing.T) {
	home := testutil.TempHome(t)
	taskID := "test-task-meta"

	meta := map[string]string{
		"kind":     "ship",
		"project":  "munsu",
		"harness":  "claude",
		"worktree": filepath.Join(home, "projects", "munsu"),
	}

	if err := domain.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta failed: %v", err)
	}

	readMeta, err := domain.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta failed: %v", err)
	}

	if readMeta["kind"] != "ship" || readMeta["project"] != "munsu" {
		t.Errorf("ReadMeta mismatch: got %v", readMeta)
	}
}

func TestAppendStatusWithFlock(t *testing.T) {
	home := testutil.TempHome(t)
	taskID := "test-task-status"

	if err := domain.AppendStatus(home, taskID, "working: starting implementation"); err != nil {
		t.Fatalf("AppendStatus 1 failed: %v", err)
	}
	if err := domain.AppendStatus(home, taskID, "resolved [key=step1]: finished phase 1"); err != nil {
		t.Fatalf("AppendStatus 2 failed: %v", err)
	}

	lines, err := domain.ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("ReadStatus failed: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 status lines, got %d", len(lines))
	}

	msg, key := domain.ParseStatusKey(lines[1])
	if msg != "resolved" || key != "step1" {
		t.Errorf("ParseStatusKey mismatch: msg=%q, key=%q", msg, key)
	}
}

func TestCompareAndSwapMeta(t *testing.T) {
	home := testutil.TempHome(t)
	taskID := "test-cas-meta"

	initial := map[string]string{"state": "pending", "owner": "soldier1"}
	if err := domain.WriteMeta(home, taskID, initial); err != nil {
		t.Fatal(err)
	}

	checks := map[string]string{"state": "pending"}
	updates := map[string]string{"state": "running"}

	updated, err := domain.CompareAndSwapMeta(home, taskID, checks, updates)
	if err != nil {
		t.Fatalf("CAS failed: %v", err)
	}
	if updated["state"] != "running" {
		t.Errorf("expected state=running, got %s", updated["state"])
	}
}

func TestPRCanMerge(t *testing.T) {
	pr := domain.PR{
		Number:     101,
		Title:      "Feature PR",
		Status:     domain.PROpen,
		BaseBranch: "main",
		HeadBranch: "mu/feature-101",
		Checks: []domain.CheckRun{
			{Name: "test", Status: domain.CheckPassed},
			{Name: "lint", Status: domain.CheckPassed},
		},
		Reviews: []domain.Review{
			{State: domain.ReviewApproved, Body: "LGTM"},
		},
	}

	if !pr.CanMerge() {
		t.Error("expected PR.CanMerge() to be true for open PR with green checks and approval")
	}

	// Add failed check
	pr.Checks = append(pr.Checks, domain.CheckRun{Name: "security", Status: domain.CheckFailed})
	if pr.CanMerge() {
		t.Error("expected PR.CanMerge() to be false when a check fails")
	}
}

func TestValidateIdentity(t *testing.T) {
	ident := &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     12,
		URL:        "https://github.com/minhtri2710/munsu/pull/12",
		BaseRef:    "main",
		HeadRef:    "mu/task-12",
		HeadSHA:    "abc1234567890",
		CapturedAt: "2026-07-26T12:00:00Z",
	}

	if err := domain.ValidateIdentity(ident); err != nil {
		t.Errorf("expected valid identity, got: %v", err)
	}

	ident.Number = -1
	if err := domain.ValidateIdentity(ident); err == nil {
		t.Error("expected error for invalid PR number")
	}
}
