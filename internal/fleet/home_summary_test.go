package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// setManualMode forces manual backlog backend for tests that use native backlog.md.
func setManualMode(t *testing.T, homeDir string) {
	t.Helper()
	configDir := filepath.Join(homeDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644); err != nil {
		t.Fatalf("writing backlog-backend config: %v", err)
	}
}

func TestSummarizeCaptainHome_ActiveChild(t *testing.T) {
	home := t.TempDir()
	setManualMode(t, home)
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	os.WriteFile(filepath.Join(home, "data", "backlog.md"), []byte("# Backlog\n\n## 2026-01-01\n- [-] t1: work\n- [ ] t2: queued\n"), 0644)
	if err := mhome.WriteMeta(home, "t1", map[string]string{"kind": "ship", "window": "w1"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(home, "t1", "working: implementing"); err != nil {
		t.Fatal(err)
	}
	sum := SummarizeCaptainHome(home)
	if !sum.Valid {
		t.Fatalf("valid=false reason=%q", sum.Reason)
	}
	if sum.State != "active_child_work" {
		t.Fatalf("state=%q want active_child_work", sum.State)
	}
	if sum.Counts.ActiveChildren != 1 || sum.Counts.InFlight != 1 || sum.Counts.Queued != 1 {
		t.Fatalf("counts=%+v", sum.Counts)
	}
	if len(sum.ActiveChildren) != 1 || sum.ActiveChildren[0].ID != "t1" {
		t.Fatalf("active=%+v", sum.ActiveChildren)
	}
	if sum.ActiveChildren[0].Doing != "implementing" {
		t.Fatalf("doing=%q", sum.ActiveChildren[0].Doing)
	}
	if len(sum.Queued) != 1 || sum.Queued[0].ID != "t2" {
		t.Fatalf("queued=%+v", sum.Queued)
	}
}

func TestSummarizeCaptainHome_DecisionsHoldsLanded(t *testing.T) {
	home := t.TempDir()
	setManualMode(t, home)
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	os.WriteFile(filepath.Join(home, "data", "backlog.md"), []byte(`# Backlog

## day
- [-] t-decision: needs input
- [!] t-blocked: waiting dep
- [x] t-done: shipped feature
- [ ] t-queued: next work
`), 0644)
	if err := mhome.WriteMeta(home, "t-decision", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(home, "t-decision", "needs-decision [key=approach]: pick A or B"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteMeta(home, "t-done", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(home, "t-done", "done: PR https://github.com/example/repo/pull/9"); err != nil {
		t.Fatal(err)
	}

	sum := SummarizeCaptainHome(home)
	if !sum.Valid {
		t.Fatalf("valid=false reason=%q", sum.Reason)
	}
	if sum.State != "captain_decision" {
		t.Fatalf("state=%q want captain_decision", sum.State)
	}
	if sum.Counts.DecisionsOpen < 1 || len(sum.DecisionsOpen) < 1 {
		t.Fatalf("decisions=%+v counts=%+v", sum.DecisionsOpen, sum.Counts)
	}
	if sum.DecisionsOpen[0].Verb != "needs-decision" {
		t.Fatalf("decision verb=%q", sum.DecisionsOpen[0].Verb)
	}
	if sum.Counts.Holds < 1 {
		t.Fatalf("holds count=%d holds=%+v", sum.Counts.Holds, sum.Holds)
	}
	if sum.Counts.Landed != 1 || len(sum.Landed) != 1 {
		t.Fatalf("landed=%+v counts=%+v", sum.Landed, sum.Counts)
	}
	if !strings.Contains(sum.Landed[0].PRURL, "github.com/example/repo/pull/9") {
		t.Fatalf("pr_url=%q", sum.Landed[0].PRURL)
	}
	if sum.Counts.Queued != 1 {
		t.Fatalf("queued count=%d", sum.Counts.Queued)
	}
}

func TestSummarizeCaptainHome_MissingBacklogInvalid(t *testing.T) {
	home := t.TempDir()
	setManualMode(t, home)
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	sum := SummarizeCaptainHome(home)
	// Without any backlog items, the summary is still valid (empty backlog).
	// "Missing structured backlog" only occurs when the backend reports an error.
	if sum.State != "no_active_work" {
		t.Fatalf("state=%q want no_active_work for empty backlog", sum.State)
	}
}

func TestSummarizeCaptainHome_OmittedCaps(t *testing.T) {
	home := t.TempDir()
	setManualMode(t, home)
	os.MkdirAll(filepath.Join(home, "data"), 0755)

	var b strings.Builder
	b.WriteString("# Backlog\n\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "- [ ] q%02d: queued item\n", i)
	}
	os.WriteFile(filepath.Join(home, "data", "backlog.md"), []byte(b.String()), 0644)

	sum := SummarizeCaptainHome(home)
	if sum.Counts.Queued != 25 {
		t.Fatalf("queued count=%d", sum.Counts.Queued)
	}
	if len(sum.Queued) != maxQueued {
		t.Fatalf("queued list len=%d want %d", len(sum.Queued), maxQueued)
	}
	found := false
	for _, o := range sum.Omitted {
		if o.Surface == "queued" && o.Count == 5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("omitted=%+v", sum.Omitted)
	}
}

func TestLastParentStatus(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	if err := mhome.AppendStatus(parent, "captain:api", "done [key=x]: PR https://example/1"); err != nil {
		t.Fatal(err)
	}
	got := LastParentStatus(parent, "api")
	if !strings.Contains(got, "done") {
		t.Fatalf("got %q", got)
	}
}
