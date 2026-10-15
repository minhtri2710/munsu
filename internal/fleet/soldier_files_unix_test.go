package fleet

import (
	"fmt"
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

// A scout writes its status file and report, so it can put a symlink, a FIFO
// or a directory at those names. The fleet's unfenced readers read nothing
// through any of them, without blocking, and read the nearest valid case, a
// regular file. A FIFO row that blocked fails on its timer.
func TestSoldierWritableFilesReadOnlyAsRegularFiles(t *testing.T) {
	readers := []struct {
		name    string
		file    func(homeDir string) string
		run     func(homeDir string) string
		want    string // for the regular file
		refused string // "" when the reader reports an error to match instead
	}{
		{"snapshot last status", func(h string) string { return filepath.Join(h, "state", "t1.status") }, func(h string) string {
			snap, err := Snapshot(h, SnapshotDependencies{CurrentState: NewCanonicalCurrentState()})
			if err != nil || len(snap.Tasks) != 1 {
				return fmt.Sprintf("snapshot: %v %d", err, len(snap.Tasks))
			}
			return snap.Tasks[0].LastStatus
		}, "working: x", ""},
		{"report exists", func(h string) string { return ReportPath(h, "t1", 1) }, func(h string) string {
			return fmt.Sprint(ReportExists(h, "t1", 1))
		}, "true", "false"},
		{"scout safety check", func(h string) string { return ReportPath(h, "t1", 1) }, func(h string) string {
			if err := scoutSafetyCheck(Options{ID: "t1", HomeDir: h}, nil, 1); err != nil {
				return err.Error()
			}
			return "ok"
		}, "ok", "is not a regular file (use --force to override)"},
	}
	plants := []struct {
		name  string
		plant func(path, other string) error
		ok    bool
	}{
		{"a symlink to another state file", func(path, other string) error { return os.Symlink(other, path) }, false},
		{"a FIFO", func(path, _ string) error { return syscall.Mkfifo(path, 0o600) }, false},
		{"a directory", func(path, _ string) error { return os.Mkdir(path, 0o700) }, false},
		{"a regular file", func(path, _ string) error { return os.WriteFile(path, []byte("working: x\n"), 0o600) }, true},
	}
	for _, rd := range readers {
		for _, pl := range plants {
			t.Run(rd.name+" of "+pl.name, func(t *testing.T) {
				homeDir := t.TempDir()
				if _, err := home.Init(homeDir); err != nil {
					t.Fatal(err)
				}
				auth := canonicalAtHome(t, homeDir)
				req := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: mustTaskID(t, "t1"),
					Owner: "general", Description: "d", Kind: taskauthority.KindScout, ScoutScope: "s", Reason: "test"}
				op, err := domain.NewOperation(mustOpID(t, "op-create"), req)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := auth.Create(op, req); err != nil {
					t.Fatal(err)
				}
				other := filepath.Join(homeDir, "state", "other")
				if err := os.WriteFile(other, []byte("working: x\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				path := rd.file(homeDir)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := pl.plant(path, other); err != nil {
					t.Fatal(err)
				}
				done := make(chan string, 1)
				go func() { done <- rd.run(homeDir) }()
				var got string
				select {
				case got = <-done:
				case <-time.After(5 * time.Second):
					t.Fatalf("%s blocked on %s", rd.name, pl.name)
				}
				switch {
				case pl.ok:
					if got != rd.want {
						t.Fatalf("got %q, want %q", got, rd.want)
					}
				case rd.refused == "":
					if got != "" {
						t.Fatalf("got %q, want nothing read", got)
					}
				default:
					if !strings.Contains(got, rd.refused) {
						t.Fatalf("got %q, want %q", got, rd.refused)
					}
				}
			})
		}
	}
}
