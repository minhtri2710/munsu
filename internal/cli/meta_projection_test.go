package cli

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
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

func TestProjectTaskMetaUnreadableMetaIsNotErased(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "cli-task-unreadable"
	before, p := writeUnreadableCLIProjectionMeta(t, homeDir, taskID)
	err := projectTaskMeta(homeDir, taskauthority.Aggregate{
		TaskID: taskID, Generation: 1,
		Definition: taskauthority.TaskDefinition{Owner: "owner", Kind: "ship", Project: "project"},
		Phase:      taskauthority.PhaseQueued,
	}, nil)
	assertUnchangedProjectionMeta(t, before, p, err, "owner")
}

func TestProjectDeliveryIdentityUnreadableMetaIsNotErased(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "cli-delivery-unreadable"
	before, p := writeUnreadableCLIProjectionMeta(t, homeDir, taskID)
	err := projectDeliveryIdentity(homeDir, taskID, domain.DeliveryIdentity{Provider: "github", Owner: "owner", Repo: "repo", Number: 1})
	assertUnchangedProjectionMeta(t, before, p, err, "pr_provider")
}
