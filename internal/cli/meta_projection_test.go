package cli

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func writeUnreadableCLIProjectionMeta(t *testing.T, homeDir, taskID string) ([]byte, string) {
	t.Helper()
	p, err := home.MetaFilePath(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	before := []byte("project=existing-project\nworktree=/tmp/wt\n" + strings.Repeat("x", 128*1024) + "\n")
	if err := os.WriteFile(p, before, 0600); err != nil {
		t.Fatal(err)
	}
	return before, p
}

func assertUnchangedProjectionMeta(t *testing.T, before []byte, p string, err error, attemptedKey string) {
	t.Helper()
	after, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatal(readErr)
	}
	beforeHash := sha256.Sum256(before)
	afterHash := sha256.Sum256(after)
	t.Logf("projection error: %v; meta SHA-256 before=%x after=%x", err, beforeHash, afterHash)
	if err == nil {
		t.Fatal("projection returned nil for an unreadable meta")
	}
	if string(before) != string(after) || beforeHash != afterHash {
		t.Fatal("projection changed unreadable meta")
	}
	t.Logf("attempted projection key absent: %s", attemptedKey)
	if strings.Contains(string(after), attemptedKey+"=") {
		t.Fatalf("projection wrote %s over unreadable meta", attemptedKey)
	}
}

func TestTouchTaskMetaUnreadableMetaIsNotErased(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "cli-task-unreadable"
	before, p := writeUnreadableCLIProjectionMeta(t, homeDir, taskID)
	err := touchTaskMeta(homeDir, taskID)
	assertUnchangedProjectionMeta(t, before, p, err, "kind")
}

func TestProjectDeliveryIdentityUnreadableMetaIsNotErased(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "cli-delivery-unreadable"
	before, p := writeUnreadableCLIProjectionMeta(t, homeDir, taskID)
	err := projectDeliveryIdentity(homeDir, taskID, domain.DeliveryIdentity{Provider: "github", Owner: "owner", Repo: "repo", Number: 1})
	assertUnchangedProjectionMeta(t, before, p, err, "pr_provider")
}

// TestMetaMtimeWitnessesTaskStateAdvance pins the constraint beside
// touchTaskMeta: a .check written before task add, promote or the scout-done
// report settlement is refused by AcceptOrRefuseStale afterwards, and one written after
// it is accepted. Deleting the touch from any one writer turns that row red.
func TestMetaMtimeWitnessesTaskStateAdvance(t *testing.T) {
	cases := []struct {
		name string
		// advance moves the task state and returns the id.
		advance func(t *testing.T, homeDir string) string
	}{
		{"task add", func(t *testing.T, homeDir string) string {
			if out, err := runTaskCommand(t, []string{"task", "add", "w-add", "witness", "--home", homeDir}); err != nil {
				t.Fatalf("task add: %v\n%s", err, out)
			}
			return "w-add"
		}},
		{"task promote", func(t *testing.T, homeDir string) string {
			auth := testAuthorityFor(t, homeDir)
			tid := mustTaskIDFor(t, "w-promote")
			req := taskauthority.CanonicalCreateRequest{
				HomeID: auth.HomeID(), TaskID: tid, Owner: "owner", Description: "explore", Kind: "scout",
				ScoutScope: "investigate the requested question", Reason: "test",
			}
			if _, err := auth.Create(mustCanonicalOp(t, "op-create-w-promote", req), req); err != nil {
				t.Fatal(err)
			}
			done := taskauthority.CanonicalCompleteRequest{
				HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), To: taskauthority.PhaseDone, Reason: "explored",
			}
			if _, err := auth.Complete(mustCanonicalOp(t, "op-complete-w-promote", done), done); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(homeDir, "data", "w-promote"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fleet.ReportPath(homeDir, "w-promote", 1), []byte("# findings"), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := runTaskCommand(t, []string{"promote", "w-promote", "--home", homeDir}); err != nil {
				t.Fatalf("promote: %v\n%s", err, out)
			}
			return "w-promote"
		}},
		{"scout-done report settled", func(t *testing.T, homeDir string) string {
			auth := testAuthorityFor(t, homeDir)
			req := taskauthority.CanonicalCreateRequest{
				HomeID: auth.HomeID(), TaskID: mustTaskIDFor(t, "w-scout"), Owner: "owner", Description: "explore", Kind: "scout",
				ScoutScope: "investigate the requested question", Reason: "test",
			}
			if _, err := auth.Create(mustCanonicalOp(t, "op-create-w-scout", req), req); err != nil {
				t.Fatal(err)
			}
			if err := reportAs(t, "soldier", homeDir, homeDir, "w-scout", "scout", "done", "findings"); err != nil {
				t.Fatalf("report: %v", err)
			}
			if unaccepted := settleReports(t, homeDir); len(unaccepted) != 0 {
				t.Fatalf("unaccepted = %+v", unaccepted)
			}
			return "w-scout"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			id := tc.advance(t, homeDir)
			p, err := home.DurableFilePath(home.StateDir(homeDir), id, ".check")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			// A check authored before the advance has an mtime before the
			// meta write; one authored after it has a later mtime.
			older := time.Now().Add(-time.Hour)
			if err := os.Chtimes(p, older, older); err != nil {
				t.Fatal(err)
			}
			if err := orchestrator.AcceptOrRefuseStale(p); err == nil || !strings.Contains(err.Error(), "meta newer") {
				t.Fatalf("check older than %s was not refused as stale: %v", tc.name, err)
			}
			newer := time.Now().Add(time.Minute)
			if err := os.Chtimes(p, newer, newer); err != nil {
				t.Fatal(err)
			}
			if err := orchestrator.AcceptOrRefuseStale(p); err != nil {
				t.Fatalf("check newer than %s was refused: %v", tc.name, err)
			}
		})
	}
}
