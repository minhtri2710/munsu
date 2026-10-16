package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// reportAs runs `munsu report` with a seat's launch environment.
func reportAs(t *testing.T, role, homeDir, parentHome, taskID, kind string, args ...string) error {
	t.Helper()
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_ROLE", role)
	t.Setenv("MUNSU_TASK_ID", taskID)
	t.Setenv("MUNSU_TASK_KIND", kind)
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)
	_, err := runRoot(t, append([]string{"report"}, args...)...)
	return err
}

// settleReports claims every wake in homeDir and acknowledges it, as the
// parent does, and returns the reports whose settlement could not finish.
func settleReports(t *testing.T, homeDir string) []orchestrator.UnacceptedReport {
	t.Helper()
	claim, err := orchestrator.ClaimWakes(homeDir, "general", 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, w := range claim.Wakes {
		ids = append(ids, w.Epoch+":"+w.Seq)
	}
	unaccepted, err := orchestrator.AckWakes(homeDir, claim.LeaseID, ids)
	if err != nil {
		t.Fatal(err)
	}
	return unaccepted
}

// One report is one status line: marked for a soldier or a captain, in the home
// its parent reads, and plain for the dispatcher's own reports. A report munsu
// refuses writes nothing.
func TestReportWritesOneLine(t *testing.T) {
	const (
		task       = "t-1"
		emptyMsg   = "message is required"
		invalidKey = "invalid terminal key"
		ship       = taskauthority.KindShip
	)
	marked := func(state, msg, key string) string { return domain.ReportStatusLine(state, msg, key) }
	for _, tc := range []struct {
		name    string
		role    string
		kind    string
		args    []string
		want    []string // lines in the status file the parent reads
		wantErr string
	}{
		{"soldier material report", "soldier", ship, []string{"done", "PR landed"}, []string{marked("done", "PR landed", "default")}, ""},
		{"captain material report", "captain", ship, []string{"blocked", "waiting", "--key", "gate"}, []string{marked("blocked", "waiting", "gate")}, ""},
		{"dispatcher report", "general", ship, []string{"done", "merged"}, []string{"done: merged [key=default]"}, ""},
		{"empty message", "soldier", ship, []string{"done", ""}, nil, emptyMsg},
		{"key outside the slug on a progress report", "soldier", ship, []string{"working", "build", "--key", "a.b"}, nil, invalidKey},
		{"slug key on a progress report", "soldier", ship, []string{"working", "build", "--key", "a-b_c"}, []string{marked("working", "build", "a-b_c")}, ""},
		{"key outside the slug on a material report", "soldier", ship, []string{"done", "PR landed", "--key", "a.b"}, []string{marked("done", "PR landed", "a.b")}, ""},
		{"key outside the slug on a scout done report", "soldier", taskauthority.KindScout, []string{"done", "found it", "--key", "a.b"}, nil, invalidKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			own, parent := t.TempDir(), t.TempDir()
			initCLITestHome(t, parent)
			reader := own
			if tc.role == "captain" {
				reader = parent
				if err := home.SeedCaptainProvenance(own, "cap-1"); err != nil {
					t.Fatal(err)
				}
			}
			err := reportAs(t, tc.role, own, parent, task, tc.kind, tc.args...)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("report = %v, want nil", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("report = %v, want %q", err, tc.wantErr)
			}
			lines, err := home.ReadStatus(reader, task)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(lines, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("status lines = %q, want %q", lines, tc.want)
			}
			if tc.role == "captain" {
				if mine, _ := home.ReadStatus(own, task); len(mine) != 0 {
					t.Fatalf("captain's own home holds %q, want no line", mine)
				}
			}
		})
	}
}

// Every soldier report is a report of its own: an identical repeat derives a
// second wake, and each holds teardown until its own wake settles. Only the
// dispatcher's unmarked repeat appends nothing, and says so.
func TestARepeatedReportIsAReportOfItsOwn(t *testing.T) {
	for _, tc := range []struct {
		name        string
		role        string
		settleFirst bool
		wantLines   int
		wantWakes   int
		wantNoop    bool
	}{
		{"soldier repeat after the first was settled", "soldier", true, 2, 1, false},
		{"soldier repeat before the first was settled", "soldier", false, 2, 2, false},
		{"dispatcher repeat", "general", false, 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			if err := reportAs(t, tc.role, homeDir, homeDir, "t-1", taskauthority.KindShip, "failed", "CI red"); err != nil {
				t.Fatal(err)
			}
			if tc.settleFirst {
				settleReports(t, homeDir)
			}
			out, err := runRoot(t, "report", "failed", "CI red", "--output", "json")
			if err != nil {
				t.Fatalf("repeat: %v\n%s", err, out)
			}
			var resp Response[MessageResult]
			if err := json.Unmarshal([]byte(out), &resp); err != nil {
				t.Fatalf("repeat output is not JSON: %v\n%s", err, out)
			}
			if resp.Data.Noop != tc.wantNoop {
				t.Fatalf("repeat output = %s, want noop=%v", out, tc.wantNoop)
			}
			if lines, _ := home.ReadStatus(homeDir, "t-1"); len(lines) != tc.wantLines {
				t.Fatalf("status lines = %q, want %d", lines, tc.wantLines)
			}
			claim, err := orchestrator.ClaimWakes(homeDir, "general", 60, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(claim.Wakes) != tc.wantWakes {
				t.Fatalf("wakes = %+v, want %d", claim.Wakes, tc.wantWakes)
			}
			if len(claim.Wakes) == 2 && claim.Wakes[0].Ref == claim.Wakes[1].Ref {
				t.Fatalf("two identical reports share the ref %q, want one ref each", claim.Wakes[0].Ref)
			}
			for i, w := range claim.Wakes {
				if _, err := orchestrator.AckWakes(homeDir, claim.LeaseID, []string{w.Epoch + ":" + w.Seq}); err != nil {
					t.Fatal(err)
				}
				if left, err := home.UnsettledReports(homeDir, "t-1"); err != nil || len(left) != len(claim.Wakes)-1-i {
					t.Fatalf("after settling %d of %d: unsettled = %q err=%v", i+1, len(claim.Wakes), left, err)
				}
			}
		})
	}
}

// Settling a report completes only a scout's done: the parent's settlement is
// the scout's completion, and nothing else changes a task's phase.
func TestSettlingAReportCompletesOnlyAScoutsDone(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      string
		state     string
		completed bool // the task was completed before its report settled
		wantDone  bool
	}{
		{"scout done", taskauthority.KindScout, "done", false, true},
		{"scout blocked", taskauthority.KindScout, "blocked", false, false},
		{"ship done", taskauthority.KindShip, "done", false, false},
		{"scout already done", taskauthority.KindScout, "done", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			registerCLITestProject(t, homeDir, "munsu")
			addArgs := []string{"task", "add", "t-1", "work", "--kind", tc.kind, "--repo", "munsu", "--home", homeDir}
			if tc.kind == taskauthority.KindScout {
				addArgs = append(addArgs, "--scope", "investigate report")
			}
			if out, err := runTaskCommand(t, addArgs); err != nil {
				t.Fatalf("task add: %v\n%s", err, out)
			}
			auth := testAuthorityFor(t, homeDir)
			tid := mustTaskIDFor(t, "t-1")
			if tc.completed {
				done := taskauthority.CanonicalCompleteRequest{HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), To: taskauthority.PhaseDone, Reason: "test"}
				if _, err := auth.Complete(mustCanonicalOp(t, "op-complete-t-1", done), done); err != nil {
					t.Fatal(err)
				}
			}
			if err := reportAs(t, "soldier", homeDir, homeDir, "t-1", tc.kind, tc.state, "report"); err != nil {
				t.Fatalf("report: %v", err)
			}
			if unaccepted := settleReports(t, homeDir); len(unaccepted) != 0 {
				t.Fatalf("unaccepted = %+v, want none", unaccepted)
			}
			agg, err := auth.Get(tid)
			if err != nil {
				t.Fatal(err)
			}
			if got := agg.Phase == taskauthority.PhaseDone; got != tc.wantDone {
				t.Fatalf("phase = %s, want done=%v", agg.Phase, tc.wantDone)
			}
			if left, err := home.UnsettledReports(homeDir, "t-1"); err != nil || len(left) != 0 {
				t.Fatalf("unsettled = %q err=%v, want none", left, err)
			}
		})
	}
}

// TestReportDoneDoesNotCaptureDeliveryIdentity proves the terminal report
// path never captures or commits delivery identity through a parallel path:
// `report done` with a PR message leaves no pr_* meta projection and no
// delivery state behind. Delivery truth is consumed from canonical
// authorization/outcome records only.
func TestReportDoneDoesNotCaptureDeliveryIdentity(t *testing.T) {
	homeDir := t.TempDir()
	parentHome := t.TempDir()
	initCLITestHome(t, parentHome)

	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-report")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"report", "done", "work landed, PR https://github.com/owner/repo/pull/7"})
	if err := root.Execute(); err != nil {
		t.Fatalf("report done: %v\n%s", err, buf.String())
	}

	// No delivery journal or canonical delivery evidence may exist.
	if _, err := os.Stat(filepath.Join(homeDir, "state", "delivery")); !os.IsNotExist(err) {
		t.Fatalf("report done created delivery state: %v", err)
	}
}

// TestReportResolvedRefusalReadsTheTaskKind pins the `resolved` refusal for a
// ship task. A soldier reads its kind from its launch environment and never
// opens the dispatcher's home; a captain or the dispatcher reads the canonical
// task, and a canonical record that cannot be read refuses.
func TestReportResolvedRefusalReadsTheTaskKind(t *testing.T) {
	const (
		shipRefusal = "cannot use 'resolved' as delivery completion"
		readRefusal = "cannot read task res-1 kind to check 'resolved'"
	)
	for _, tc := range []struct {
		name    string
		role    string
		envKind string
		addArgs []string
		corrupt bool
		want    string
	}{
		{"soldier whose launch names ship is refused", "soldier", "ship", []string{"--kind", "scout", "--scope", "investigate report"}, false, shipRefusal},
		{"soldier whose launch names scout passes over a corrupt record", "soldier", "scout", []string{"--kind", "ship"}, true, ""},
		{"canonical ship is refused", "general", "", []string{"--kind", "ship"}, false, shipRefusal},
		{"unreadable canonical record is refused", "general", "", []string{"--kind", "scout", "--scope", "investigate report"}, true, readRefusal},
		{"canonical scout passes", "general", "", []string{"--kind", "scout", "--scope", "investigate report"}, false, ""},
		{"task with no canonical record is allowed", "general", "", nil, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			if tc.addArgs != nil {
				registerCLITestProject(t, homeDir, "munsu")
				if out, err := runTaskCommand(t, append([]string{"task", "add", "res-1", "resolve", "--repo", "munsu", "--home", homeDir}, tc.addArgs...)); err != nil {
					t.Fatalf("task add: %v\n%s", err, out)
				}
			}
			if tc.corrupt {
				current := filepath.Join(homeDir, "state", "task-authority", "tasks", "res-1", "current.json")
				if err := os.WriteFile(current, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := reportAs(t, tc.role, homeDir, homeDir, "res-1", tc.envKind, "resolved", "done with it", "--key", "res-1")
			for _, refusal := range []string{shipRefusal, readRefusal} {
				got := err != nil && strings.Contains(err.Error(), refusal)
				if got != (refusal == tc.want) {
					t.Fatalf("report resolved err = %v, refusal %q seen=%v, want only %q", err, refusal, got, tc.want)
				}
			}
			if tc.want == "" {
				if err != nil {
					t.Fatalf("report resolved: %v", err)
				}
				lines, statusErr := home.ReadStatus(homeDir, "res-1")
				if statusErr != nil || len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "resolved: done with it") {
					t.Fatalf("status lines = %q (%v), want a resolved line ending the file", lines, statusErr)
				}
			}
		})
	}
}
