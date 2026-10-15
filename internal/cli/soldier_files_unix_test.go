package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// A captain's status file in the General home is written by the captain, so
// it can be a symlink, a FIFO or a directory. readLastLine reads nothing
// through any of them, without blocking, and reads a regular file.
func TestReadLastLineReadsOnlyARegularFile(t *testing.T) {
	for _, tt := range []struct {
		name  string
		plant func(path, other string) error
		want  string
	}{
		{"a symlink to another state file", func(path, other string) error { return os.Symlink(other, path) }, ""},
		{"a FIFO", func(path, _ string) error { return syscall.Mkfifo(path, 0o600) }, ""},
		{"a directory", func(path, _ string) error { return os.Mkdir(path, 0o700) }, ""},
		{"a regular file", func(path, _ string) error { return os.WriteFile(path, []byte("a\nworking: x\n"), 0o600) }, "working: x"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			other := filepath.Join(dir, "other")
			if err := os.WriteFile(other, []byte("working: x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "captain:c1.status")
			if err := tt.plant(path, other); err != nil {
				t.Fatal(err)
			}
			done := make(chan string, 1)
			go func() { done <- readLastLine(path) }()
			select {
			case got := <-done:
				if got != tt.want {
					t.Fatalf("readLastLine = %q, want %q", got, tt.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("readLastLine blocked on %s", tt.name)
			}
		})
	}
}

// A soldier's git guard reads its task's binding as last committed: journal
// recovery writes .journal and .lock, which the soldier's fence refuses (here
// an unwritable .lock stands in for the fence), so an in-flight dispatcher
// commit must not refuse the soldier's commit. The read reaches the task and
// the binding check after it; the nearest refusal, a home that cannot be
// opened, still refuses.
func TestGitGuardReadsTheBindingAsLastCommitted(t *testing.T) {
	for _, tt := range []struct {
		name string
		init bool
		want string
	}{
		{"a dispatcher commit in flight", true, "git mutation requires active worktree binding"},
		{"a home that cannot be opened", false, "git mutation worktree binding unavailable: opening task authority home"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			if tt.init {
				h, err := home.Init(homeDir)
				if err != nil {
					t.Fatal(err)
				}
				auth, err := taskauthority.NewCanonical(h)
				if err != nil {
					t.Fatal(err)
				}
				tid, err := domain.NewTaskID("t1")
				if err != nil {
					t.Fatal(err)
				}
				opID, err := domain.NewOperationID("op-create")
				if err != nil {
					t.Fatal(err)
				}
				req := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: tid, Owner: "general", Description: "d", Kind: taskauthority.KindShip, Reason: "test"}
				op, err := domain.NewOperation(opID, req)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := auth.Create(op, req); err != nil {
					t.Fatal(err)
				}
				record := `{"txn_id":"t1","scope":"task-x","fence_token":1,"expected_revision":0,"new_revision":1,"items":[]}`
				if err := os.WriteFile(filepath.Join(homeDir, ".journal", "task-x.t1.json"), []byte(record), 0o600); err != nil {
					t.Fatal(err)
				}
				lockDir := filepath.Join(homeDir, ".lock")
				if err := os.Chmod(lockDir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(lockDir, 0o700) })
			}
			refused, reason := evaluateParsedGitMutation(homeDir, "t1", gitCommandSafety{isGit: true, mutating: true})
			if !refused || !strings.HasPrefix(reason, tt.want) {
				t.Fatalf("evaluateParsedGitMutation = %v %q, want a refusal starting %q", refused, reason, tt.want)
			}
		})
	}
}
