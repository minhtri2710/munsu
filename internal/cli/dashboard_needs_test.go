package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// attentionHome is a real home whose snapshot holds one row for each kind of
// item that waits on the operator, rows that do not wait (hotel, the working
// ship delta reviews, among them), an unreadable captain home and a queued
// wake. alpha's decision hold stays open under a later status line.
func attentionHome(t *testing.T) string {
	t.Helper()
	t.Setenv("MUNSU_GUARD_SKIP", "1")
	h := deliveryGuardShip(t, "hotel")
	for _, id := range []string{"alpha", "bravo", "charlie", "echo", "foxtrot", "golf", "india", "juliet"} {
		cliSeedCanonicalTask(t, h, id, "ship")
	}
	auth := cliCanonicalForHome(t, h)
	review := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskIDFor(t, "delta"), Owner: "owner", Description: "review",
		Kind: taskauthority.KindReview, ReviewTaskID: "hotel", ReviewHead: deliveryGuardHead, Reason: "test",
	}
	if _, err := auth.Create(mustCanonicalOp(t, "op-create-delta", review), review); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "task", "done", "golf", "--home", h); err != nil {
		t.Fatalf("task done: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "decision-hold", "hold", "approach", "--reason", "Pick the UI framework", "--from", "alpha", "--home", h); err != nil {
		t.Fatalf("hold: %v\n%s", err, out)
	}
	for id, line := range map[string]string{
		"alpha":   "working: on the parts the decision does not touch",
		"bravo":   "failed: CI red",
		"charlie": "blocked: waiting on creds",
		"delta":   domain.ReportStatusLine("done", "PASS at abc123", "review-verdict:x"),
		"echo":    "working: fine",
		"foxtrot": domain.ReportStatusLine("needs-decision", "which db?", "db"),
		"golf":    "failed: an old failure",
		"india":   "done: tests failed: flaky",
		"juliet":  "Needs-decision: pick",
	} {
		if err := home.AppendStatus(h, id, line); err != nil {
			t.Fatal(err)
		}
	}
	if err := home.WriteMeta(h, "echo", map[string]string{"id": "echo", "kind": "ship", "harness": "claude", "model": "opus", "mode": "direct-PR", "window": "w-echo", "worktree": "/wt/echo"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h, "captains", "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := home.EnqueueWake(h, "report", "bravo", "x"); err != nil {
		t.Fatal(err)
	}
	return h
}

func attentionModel(t *testing.T, h string, w, height int) dashboardModel {
	t.Helper()
	m := newDashboardModel(h, "/bin/munsu")
	m.now = func() time.Time { return dashNow }
	m = resized(m, w, height)
	return send(m, m.read())
}

// needsLines are the NEEDS YOU title and items of a frame, spaces collapsed.
func needsLines(frame string) []string {
	var got []string
	in := false
	for _, l := range strings.Split(ansi.Strip(frame), "\n") {
		l = strings.TrimRight(l, " ")
		switch {
		case strings.HasPrefix(l, "NEEDS YOU"):
			in = true
			got = append(got, l)
		case in && strings.HasPrefix(l, "  "):
			got = append(got, strings.Join(strings.Fields(l), " "))
		default:
			in = false
		}
	}
	return got
}

// NEEDS YOU lists, from the real snapshot and holds, the open decisions, the
// unresolved wakes, the failed sources and seats, the stuck seats and the
// reported verdicts, in that order. It lists by status exactly the rows the
// header counts as Human-needed, an open hold whatever the row's last line
// says, and neither a working row nor a finished one.
func TestDashboardNeedsYou(t *testing.T) {
	h := attentionHome(t)
	m := attentionModel(t, h, 120, 40)
	f := m.View().Content
	if !strings.Contains(ansi.Strip(f), "Human-needed 5") {
		t.Fatalf("header: want Human-needed 5:\n%s", f)
	}
	first := needsLines(f)
	want := []string{
		"NEEDS YOU 9 - showing 5",
		"decision foxtrot needs-decision: which db? [key=db]",
		"decision juliet Needs-decision: pick",
		"decision alpha approach: Pick the UI framework",
		"wakes 1 unresolved",
		"failed captain:broken reading canonical task authority state for",
	}
	if len(first) == len(want) && strings.HasPrefix(first[5], want[5]) {
		first[5] = want[5]
	}
	if !slices.Equal(first, want) {
		t.Fatalf("NEEDS YOU =\n%s\nwant\n%s", strings.Join(first, "\n"), strings.Join(want, "\n"))
	}

	// The rest, with the five above out of the read.
	rest := attentionModel(t, h, 120, 40)
	rest.wakes = func(time.Time) (int, error) { return 0, nil }
	read := rest.readFleet
	rest.readFleet = func() (*fleet.DisplaySnapshot, error) {
		snap, err := read()
		if err != nil {
			return nil, err
		}
		snap.Tasks = slices.DeleteFunc(snap.Tasks, func(ts fleet.TaskSnapshot) bool {
			return ts.ID == "foxtrot" || ts.ID == "juliet" || ts.ID == "alpha"
		})
		snap.Failures = nil
		return snap, nil
	}
	rest = send(rest, rest.read())
	want = []string{
		"NEEDS YOU 4",
		"failed bravo failed: CI red",
		"failed india done: tests failed: flaky",
		"stuck charlie blocked: waiting on creds",
		"verdict delta done: PASS at abc123 [key=review-verdict:x]",
	}
	if got := needsLines(rest.View().Content); !slices.Equal(got, want) {
		t.Fatalf("NEEDS YOU =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	t.Run("a hold is listed once beside its own needs-decision line", func(t *testing.T) {
		h := t.TempDir()
		writeTaskMeta(t, h, "kilo", "ship")
		if out, err := runRoot(t, "decision-hold", "hold", "db", "--reason", "Pick a db", "--from", "kilo", "--home", h); err != nil {
			t.Fatalf("hold: %v\n%s", err, out)
		}
		want := []string{"NEEDS YOU 1", "decision kilo db: Pick a db"}
		if got := needsLines(attentionModel(t, h, 120, 40).View().Content); !slices.Equal(got, want) {
			t.Fatalf("NEEDS YOU =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})

	t.Run("unreadable holds are listed as failed", func(t *testing.T) {
		m := attentionModel(t, h, 120, 40)
		m.readHolds = func(string, *fleet.DisplaySnapshot) (map[dashHoldKey][]dashHold, error) {
			return nil, errors.New("holds unreadable")
		}
		m = send(m, m.read())
		got := needsLines(m.View().Content)
		if !slices.Contains(got, "failed decision holds holds unreadable") || slices.Contains(got, "decision alpha approach: Pick the UI framework") {
			t.Fatalf("NEEDS YOU =\n%s", strings.Join(got, "\n"))
		}
	})

	t.Run("a small terminal shrinks NEEDS YOU before the list and the footer", func(t *testing.T) {
		m := attentionModel(t, h, 80, 9)
		lines := strings.Split(strings.TrimRight(ansi.Strip(m.View().Content), "\n"), "\n")
		f := strings.Join(lines, "\n")
		if len(lines) != 9 || !strings.HasSuffix(strings.TrimRight(lines[len(lines)-1], " "), "q quit") {
			t.Fatalf("the footer is cut:\n%s", f)
		}
		if !strings.Contains(f, "NEEDS YOU 9 - showing 0") || !strings.Contains(f, "Failed sources and tasks - Showing 1 of 11") || !strings.Contains(f, "> x captain:broken") {
			t.Fatalf("want the NEEDS YOU count line, the list title and its selected row:\n%s", f)
		}
	})
}
