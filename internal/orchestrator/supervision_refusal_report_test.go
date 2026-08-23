package orchestrator

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// countRefusalLines runs n real cycles over the given home and returns how many
// check-refusal lines reached stderr across all of them. This is the probe
// shape #601 used to quantify the volume: several cycles over one stale
// artifact, lines counted.
func countRefusalLines(t *testing.T, home string, n int, validate func(string) error) (int, string) {
	t.Helper()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(stderrR)
		done <- string(out)
	}()
	original := os.Stderr
	os.Stderr = stderrW
	for i := 0; i < n; i++ {
		resetRecovery()
		if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, &testRetirementPort{}, &testCheckValidationPort{validate: validate}, testTaskStatePort{}); err != nil {
			os.Stderr = original
			_ = stderrW.Close()
			t.Fatalf("cycle %d: %v", i+1, err)
		}
	}
	os.Stderr = original
	_ = stderrW.Close()
	output := <-done
	_ = stderrR.Close()

	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "poll check refused") || strings.Contains(line, "poll check invalid") {
			count++
		}
	}
	return count, output
}

// staleCheckHome builds a home with one per-task check that is valid but older
// than its .meta — the steady state #601 is about, since nothing deletes it.
func staleCheckHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	checkPath := filepath.Join(home, "state", "task-1.check")
	if err := os.MkdirAll(filepath.Dir(checkPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho ready\n"), 0755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(home, "state", "task-1.meta")
	if err := os.WriteFile(metaPath, []byte("kind=ship\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(checkPath)
	if err != nil {
		t.Fatal(err)
	}
	future := fi.ModTime().Add(time.Hour)
	if err := os.Chtimes(metaPath, future, future); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestRunCycle_StaleCheckReportsItsRefusalOncePerState(t *testing.T) {
	home := staleCheckHome(t)
	count, output := countRefusalLines(t, home, 5, nil)
	if count != 1 {
		t.Fatalf("refusal lines across 5 cycles = %d, want 1\n%s", count, output)
	}
}

// Dedupe is per artifact, not per loop: two refused artifacts each get their
// own line, and a global counter or single marker would collapse them.
func TestRunCycle_EachRefusedArtifactReportsSeparately(t *testing.T) {
	home := staleCheckHome(t)
	second := filepath.Join(home, "state", "checks", "global.check")
	if err := os.MkdirAll(filepath.Dir(second), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("echo hello\n"), 0755); err != nil {
		t.Fatal(err)
	}

	count, output := countRefusalLines(t, home, 5, refuseUnrunnableCheck)
	if count != 2 {
		t.Fatalf("refusal lines across 5 cycles = %d, want 2 (one per artifact)\n%s", count, output)
	}
}

// A refusal that changes its answer is a new state and says so.
func TestRunCycle_ChangedRefusalReasonReportsAgain(t *testing.T) {
	home := staleCheckHome(t)
	reason := "first reason"
	validate := func(string) error { return errors.New(reason) }

	first, firstOut := countRefusalLines(t, home, 3, validate)
	reason = "second reason"
	second, secondOut := countRefusalLines(t, home, 3, validate)
	if first != 1 || second != 1 {
		t.Fatalf("lines = %d then %d, want 1 each\n%s%s", first, second, firstOut, secondOut)
	}
	if !strings.Contains(secondOut, "second reason") {
		t.Fatalf("stderr = %q, want the changed reason reported", secondOut)
	}
}

// Once an artifact is accepted the refusal it was carrying is spent: if it goes
// stale again, that is news even though the wording repeats.
func TestRunCycle_RefusalReportsAgainAfterTheArtifactIsAccepted(t *testing.T) {
	home := staleCheckHome(t)
	checkPath := filepath.Join(home, "state", "task-1.check")
	metaPath := filepath.Join(home, "state", "task-1.meta")

	if n, out := countRefusalLines(t, home, 3, nil); n != 1 {
		t.Fatalf("stale phase lines = %d, want 1\n%s", n, out)
	}

	// Operator regenerates the check: newer than meta, so the loop accepts it.
	metaFI, err := os.Stat(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	after := metaFI.ModTime().Add(time.Hour)
	if err := os.Chtimes(checkPath, after, after); err != nil {
		t.Fatal(err)
	}
	if n, out := countRefusalLines(t, home, 1, nil); n != 0 {
		t.Fatalf("accepted phase lines = %d, want 0\n%s", n, out)
	}

	// Meta advances again, so the same refusal is a fresh state.
	later := after.Add(time.Hour)
	if err := os.Chtimes(metaPath, later, later); err != nil {
		t.Fatal(err)
	}
	if n, out := countRefusalLines(t, home, 3, nil); n != 1 {
		t.Fatalf("re-stale phase lines = %d, want 1\n%s", n, out)
	}
}
