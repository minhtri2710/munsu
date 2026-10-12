package fleet

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
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
