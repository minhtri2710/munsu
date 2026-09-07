package fleet

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// writeUnreadableMeta writes a .meta file whose first lines are valid keys and
// whose last line exceeds bufio.Scanner's token limit. os.Open succeeds and the
// valid lines parse, so the read fails for a reason that is not absence, while
// the file stays a readable regular file an atomic rename can replace.
func writeUnreadableMeta(t *testing.T, homeDir, taskID string) string {
	t.Helper()
	p, err := home.MetaFilePath(homeDir, taskID)
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatalf("creating state directory: %v", err)
	}
	body := "project=existing-project\nworktree=/tmp/wt\n" + strings.Repeat("x", 128*1024) + "\n"
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatalf("writing unreadable meta: %v", err)
	}
	return p
}

// TestProjectAttestationEvidence_UnreadableMetaIsNotErased proves the projection
// refuses to write when the existing .meta cannot be read for a reason other than
// absence, instead of replacing the file with its own key alone.
func TestProjectAttestationEvidence_UnreadableMetaIsNotErased(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-unreadable-meta"
	p := writeUnreadableMeta(t, homeDir, taskID)

	before, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatalf("reading meta before projection: %v", readErr)
	}
	beforeHash := sha256.Sum256(before)
	err := projectAttestationEvidence(homeDir, taskID, taskauthority.Generation(1))

	raw, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatalf("reading meta back: %v", readErr)
	}
	afterHash := sha256.Sum256(raw)
	t.Logf("attestation projection error: %v; meta SHA-256 before=%x after=%x", err, beforeHash, afterHash)
	if !strings.Contains(string(raw), "project=existing-project") {
		t.Error("pre-existing project key was erased by the projection")
	}
	if !strings.Contains(string(raw), "worktree=/tmp/wt") {
		t.Error("pre-existing worktree key was erased by the projection")
	}
	if strings.Contains(string(raw), "attestation_generation") {
		t.Error("projection wrote its own key over an unreadable meta")
	}
	if string(before) != string(raw) || beforeHash != afterHash {
		t.Fatalf("refused projection changed meta: before=%x after=%x", beforeHash, afterHash)
	}
	t.Logf("attempted projection key absent: attestation_generation")

	if err == nil {
		t.Fatal("projectAttestationEvidence returned nil for an unreadable .meta")
	}
	var typed *AttestationProjectionError
	if !errors.As(err, &typed) {
		t.Fatalf("projection error = %v, want *AttestationProjectionError", err)
	}
}

func TestWriteTaskMetaUnreadableMetaIsNotErased(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-write-unreadable-meta"
	p := writeUnreadableMeta(t, homeDir, taskID)
	r := &Runner{homeDir: homeDir, args: Args{ID: taskID}, windowID: "pane", wtPath: "/tmp/wt"}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading meta before projection: %v", err)
	}
	beforeHash := sha256.Sum256(before)
	err = r.writeTaskMeta()
	after, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatalf("reading meta after projection: %v", readErr)
	}
	afterHash := sha256.Sum256(after)
	t.Logf("writeTaskMeta error: %v; meta SHA-256 before=%x after=%x", err, beforeHash, afterHash)
	if err == nil {
		t.Fatal("writeTaskMeta returned nil for an unreadable meta")
	}
	if string(before) != string(after) {
		t.Fatal("writeTaskMeta changed unreadable meta")
	}
	t.Logf("attempted runtime projection key absent: window")
	if strings.Contains(string(after), "window=") {
		t.Fatal("writeTaskMeta wrote its own key over unreadable meta")
	}
}
