//go:build integration

package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file retains the read-only helper tests (ancestry verification,
// amendment history serialization, revision increment). The amendment
// mutation lifecycle was removed with the legacy delivery path (#414 B);
// delivery execution now runs exclusively through the journaled Deliver
// operation.

// buildAmendGitRepo creates a git repo with two commits: old (ancestor) and new (descendant).
// Returns (repoPath, oldSHA, newSHA).
func buildAmendGitRepo(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()

	gitEnv := gitEnvForDir(repo)

	// Init
	cmd := exec.Command("git", "init")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	for _, cfg := range []string{"user.email test@test.com", "user.name Test"} {
		parts := strings.Split(cfg, " ")
		c := exec.Command("git", append([]string{"config"}, parts...)...)
		c.Dir = repo
		c.Env = gitEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %s", cfg, out)
		}
	}

	// First commit (old head)
	oldFile := filepath.Join(repo, "old.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add old: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "old")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit old: %s", out)
	}

	oldSHAOut, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse old: %v", err)
	}
	oldSHA := strings.TrimSpace(string(oldSHAOut))

	// Second commit (new head, descendant of old)
	newFile := filepath.Join(repo, "new.txt")
	if err := os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatalf("write new: %v", err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add new: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "new")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit new: %s", out)
	}

	newSHAOut, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse new: %v", err)
	}
	newSHA := strings.TrimSpace(string(newSHAOut))

	// Verify ancestry
	ancCmd := exec.Command("git", "merge-base", "--is-ancestor", oldSHA, newSHA)
	ancCmd.Dir = repo
	ancCmd.Env = gitEnv
	if err := ancCmd.Run(); err != nil {
		t.Fatalf("old is not ancestor of new: %v", err)
	}

	return repo, oldSHA, newSHA
}

func TestVerifyAncestry_NonExistentRepo(t *testing.T) {
	err := verifyAncestry("/nonexistent/path", "abc", "def")
	if err == nil {
		t.Fatal("expected error for nonexistent repo")
	}
}

func TestVerifyAncestry_EmptySHA(t *testing.T) {
	repo, _, _ := buildAmendGitRepo(t)
	if err := verifyAncestry(repo, "", "abc"); err == nil {
		t.Fatal("expected error for empty SHA")
	}
	if err := verifyAncestry(repo, "abc", ""); err == nil {
		t.Fatal("expected error for empty SHA")
	}
}

func TestVerifyAncestry_IdenticalSHA(t *testing.T) {
	repo, oldSHA, _ := buildAmendGitRepo(t)
	if err := verifyAncestry(repo, oldSHA, oldSHA); err == nil {
		t.Fatal("expected error for identical SHAs")
	}
}

func TestAppendAmendHistory_Empty(t *testing.T) {
	record := &AmendRecord{
		OldHeadSHA: "aaa", NewHeadSHA: "bbb",
		PRIdentity: "github/owner/repo#1",
		Timestamp:  "2026-07-19T00:00:00Z", Reason: "amendment",
	}
	result := appendAmendHistory("", record)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "aaa") {
		t.Errorf("expected history to contain old head, got: %s", result)
	}
}

func TestAppendAmendHistory_Append(t *testing.T) {
	record1 := &AmendRecord{
		OldHeadSHA: "aaa", NewHeadSHA: "bbb",
		PRIdentity: "github/owner/repo#1",
		Timestamp:  "2026-07-19T00:00:00Z", Reason: "amendment",
	}
	record2 := &AmendRecord{
		OldHeadSHA: "bbb", NewHeadSHA: "ccc",
		PRIdentity: "github/owner/repo#1",
		Timestamp:  "2026-07-20T00:00:00Z", Reason: "reconciliation",
	}

	result := appendAmendHistory("", record1)
	result = appendAmendHistory(result, record2)

	if !strings.Contains(result, "aaa") || !strings.Contains(result, "ccc") {
		t.Errorf("expected history to contain both records, got: %s", result)
	}
	if !strings.Contains(result, "amendment") || !strings.Contains(result, "reconciliation") {
		t.Errorf("expected both reasons, got: %s", result)
	}
}

// --- IncrementRevision tests ---

func TestIncrementRevision_Empty(t *testing.T) {
	if r := incrementRevision(""); r != "1" {
		t.Errorf("expected '1', got %q", r)
	}
}

func TestIncrementRevision_Zero(t *testing.T) {
	if r := incrementRevision("0"); r != "1" {
		t.Errorf("expected '1', got %q", r)
	}
}

func TestIncrementRevision_One(t *testing.T) {
	if r := incrementRevision("1"); r != "2" {
		t.Errorf("expected '2', got %q", r)
	}
}

func TestIncrementRevision_Five(t *testing.T) {
	if r := incrementRevision("5"); r != "6" {
		t.Errorf("expected '6', got %q", r)
	}
}
