package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
)

func TestSummarizeCaptainHome_ActiveChild(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	os.WriteFile(filepath.Join(home, "data", "backlog.md"), []byte("# Backlog\n\n## 2026-01-01\n- [-] t1: work\n- [ ] t2: queued\n"), 0644)
	if err := task.WriteMeta(home, "t1", map[string]string{"kind": "ship", "window": "w1"}); err != nil {
		t.Fatal(err)
	}
	if err := task.AppendStatus(home, "t1", "working: implementing"); err != nil {
		t.Fatal(err)
	}
	sum := SummarizeCaptainHome(home)
	if sum.State != "active_child_work" {
		t.Fatalf("state=%q want active_child_work", sum.State)
	}
	if sum.Counts.ActiveChildren != 1 || sum.Counts.InFlight != 1 || sum.Counts.Queued != 1 {
		t.Fatalf("counts=%+v", sum.Counts)
	}
	if len(sum.ActiveChildren) != 1 || sum.ActiveChildren[0].ID != "t1" {
		t.Fatalf("active=%+v", sum.ActiveChildren)
	}
}

func TestLastParentStatus(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	if err := task.AppendStatus(parent, "captain:api", "done [key=x]: PR https://example/1"); err != nil {
		t.Fatal(err)
	}
	got := LastParentStatus(parent, "api")
	if !strings.Contains(got, "done") {
		t.Fatalf("got %q", got)
	}
}
