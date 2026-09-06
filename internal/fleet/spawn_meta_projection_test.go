package fleet

import (
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

	err := projectAttestationEvidence(homeDir, taskID, taskauthority.Generation(1))

	raw, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatalf("reading meta back: %v", readErr)
	}
	if !strings.Contains(string(raw), "project=existing-project") {
		t.Error("pre-existing project key was erased by the projection")
	}
	if !strings.Contains(string(raw), "worktree=/tmp/wt") {
		t.Error("pre-existing worktree key was erased by the projection")
	}
	if strings.Contains(string(raw), "attestation_generation") {
		t.Error("projection wrote its own key over an unreadable meta")
	}

	if err == nil {
		t.Fatal("projectAttestationEvidence returned nil for an unreadable .meta")
	}
	var typed *AttestationProjectionError
	if !errors.As(err, &typed) {
		t.Fatalf("projection error = %v, want *AttestationProjectionError", err)
	}
}
