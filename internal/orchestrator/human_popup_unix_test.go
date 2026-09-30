//go:build !windows

package orchestrator

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// fakeHerdrForPopups installs a herdr on PATH that appends its arguments, one
// call per line, to the returned log, and fails with a noisy message while the
// returned marker file exists.
func fakeHerdrForPopups(t *testing.T) (logPath, failMarker string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	failMarker = filepath.Join(dir, "fail")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> '" + logPath + "'\n" +
		"if [ -e '" + failMarker + "' ]; then\n" +
		"  echo 'boom   went\twrong' >&2\n" +
		"  exit 1\n" +
		"fi\n"
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath, failMarker
}

func popupCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	sort.Strings(lines)
	return lines
}

func popupEvents(t *testing.T, homeDir string) []Record {
	t.Helper()
	var out []Record
	for _, r := range readEventLog(t, homeDir) {
		if strings.HasPrefix(r.Type, "popup.") {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Producer < out[j].Producer })
	return out
}

func popupTask(t *testing.T, homeDir, taskID string, meta map[string]string) {
	t.Helper()
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatal(err)
	}
}

func TestPopupHumanNeededNamesVerbAndTaskOnTheTaskBackendAndAuditsIt(t *testing.T) {
	logPath, _ := fakeHerdrForPopups(t)
	homeDir := t.TempDir()
	popupTask(t, homeDir, "task-a", map[string]string{"backend": "herdr", "herdr_session": "s1"})
	popupTask(t, homeDir, "task-b", map[string]string{"backend": "herdr", "herdr_session": "s2"})

	popupHumanNeeded(homeDir, []humanNeededEvent{
		{TaskID: "task-a", Line: "needs-decision [key=x]: pick a branch"},
		{TaskID: "task-b", Line: "failed: CI red"},
	})

	wantCalls := []string{
		"--session s1 notification show munsu: Human needed --body needs-decision on task-a",
		"--session s2 notification show munsu: Human needed --body failed on task-b",
	}
	if got := popupCalls(t, logPath); strings.Join(got, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("herdr calls = %q, want %q", got, wantCalls)
	}
	events := popupEvents(t, homeDir)
	if len(events) != 2 ||
		events[0].Type != "popup.sent" || events[0].Producer != "task-a" || events[0].Key != "needs-decision" || events[0].Payload != "needs-decision" ||
		events[1].Type != "popup.sent" || events[1].Producer != "task-b" || events[1].Key != "failed" || events[1].Payload != "failed" {
		t.Fatalf("popup events = %+v, want popup.sent for each task naming its verb", events)
	}
}

func TestPopupHumanNeededAuditsAFailedPopupAndNeverRetriesIt(t *testing.T) {
	logPath, failMarker := fakeHerdrForPopups(t)
	if err := os.WriteFile(failMarker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	homeDir := t.TempDir()
	popupTask(t, homeDir, "task-a", map[string]string{"backend": "herdr", "herdr_session": "s1"})

	out := captureStderr(t, func() {
		popupHumanNeeded(homeDir, []humanNeededEvent{{TaskID: "task-a", Line: "blocked: needs credentials"}})
	})
	if !strings.Contains(out, "warning: popup for task-a:") {
		t.Fatalf("stderr = %q, want the logged popup failure", out)
	}
	if calls := popupCalls(t, logPath); len(calls) != 1 {
		t.Fatalf("herdr calls = %q, want exactly one attempt", calls)
	}
	events := popupEvents(t, homeDir)
	if len(events) != 1 || events[0].Type != "popup.failed" || events[0].Producer != "task-a" || events[0].Key != "blocked" ||
		!strings.HasPrefix(events[0].Payload, "blocked: ") || !strings.Contains(events[0].Payload, "boom went wrong") || strings.ContainsAny(events[0].Payload, "\t\n") {
		t.Fatalf("popup events = %+v, want one popup.failed carrying the verb and the one-line error", events)
	}
}

func TestPopupHumanNeededSkipsWhatHasNoNotifier(t *testing.T) {
	logPath, _ := fakeHerdrForPopups(t)
	homeDir := t.TempDir()
	popupTask(t, homeDir, "no-backend", map[string]string{"kind": "ship"})
	popupTask(t, homeDir, "other-backend", map[string]string{"backend": "tmux"})

	out := captureStderr(t, func() {
		popupHumanNeeded(homeDir, []humanNeededEvent{
			{TaskID: "no-meta", Line: "blocked: x"},
			{TaskID: "no-backend", Line: "blocked: x"},
			{TaskID: "other-backend", Line: "blocked: x"},
		})
	})
	if calls := popupCalls(t, logPath); len(calls) != 0 {
		t.Fatalf("herdr calls = %q, want none", calls)
	}
	if events := popupEvents(t, homeDir); len(events) != 0 {
		t.Fatalf("popup events = %+v, want none for a skipped task", events)
	}
	if strings.Contains(out, "warning") {
		t.Fatalf("stderr = %q, want a skip to be silent", out)
	}
}

// The watcher pops up exactly the Human-needed signal wakes it enqueues, once
// per fingerprint: stale wakes pop up nothing, completion lines wake nothing, and an unchanged line is
// suppressed before the popup.
func TestRunCyclePopsUpOnlyFreshHumanNeededSignals(t *testing.T) {
	logPath, _ := fakeHerdrForPopups(t)
	homeDir := t.TempDir()
	popupTask(t, homeDir, "needs", map[string]string{"kind": "ship", "backend": "herdr", "herdr_session": "s1", "window": "s1:p1"})
	popupTask(t, homeDir, "finished", map[string]string{"kind": "ship", "backend": "herdr", "herdr_session": "s1", "window": "s1:p2"})
	popupTask(t, homeDir, "idle", map[string]string{"kind": "ship", "backend": "herdr", "herdr_session": "s1", "window": "s1:p3"})
	if err := home.AppendStatus(homeDir, "idle", "working: still going"); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "needs", "blocked: waiting on a key"); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "finished", "done: shipped"); err != nil {
		t.Fatal(err)
	}

	if _, err := testRunCycle(homeDir); err != nil {
		t.Fatal(err)
	}
	var stale bool
	for _, w := range mustReadWakeQueue(t, homeDir) {
		stale = stale || (w.Kind == "stale" && w.Key == "idle")
	}
	if !stale {
		t.Fatalf("wake queue = %+v, want a stale wake for the dead idle task (control: a non-signal wake exists)", mustReadWakeQueue(t, homeDir))
	}
	want := "--session s1 notification show munsu: Human needed --body blocked on needs"
	if calls := popupCalls(t, logPath); len(calls) != 1 || calls[0] != want {
		t.Fatalf("herdr calls = %q, want only %q", calls, want)
	}

	if _, err := testRunCycle(homeDir); err != nil {
		t.Fatal(err)
	}
	if calls := popupCalls(t, logPath); len(calls) != 1 {
		t.Fatalf("herdr calls after an unchanged cycle = %q, want no second popup", calls)
	}
}

func TestRunCycleClearsTheWakeMarkerOnceASignalResolves(t *testing.T) {
	homeDir := t.TempDir()
	popupTask(t, homeDir, "flip", map[string]string{"kind": "ship"})
	wakesFor := func() int {
		n := 0
		for _, w := range mustReadWakeQueue(t, homeDir) {
			if w.Key == "flip" {
				n++
			}
		}
		return n
	}
	cycle := func(status string, want int) {
		t.Helper()
		if err := home.AppendStatus(homeDir, "flip", status); err != nil {
			t.Fatal(err)
		}
		if _, err := testRunCycle(homeDir); err != nil {
			t.Fatal(err)
		}
		if got := wakesFor(); got != want {
			t.Fatalf("after %q: wakes for flip = %d, want %d", status, got, want)
		}
	}
	cycle("blocked: waiting on a key", 1)
	cycle("working: resumed", 1)
	cycle("blocked: waiting on a key", 2)
}
