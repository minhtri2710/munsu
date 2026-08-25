package orchestrator

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	homepkg "github.com/minhtri2710/munsu/internal/home"
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
	metaPath, err := homepkg.MetaFilePath(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
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

func TestRunCycle_RetirementThenDiscoverySameRefusalReportsOnce(t *testing.T) {
	home := staleCheckHome(t)
	checkPath := filepath.Join(home, "state", "task-1.check")
	metaPath, err := homepkg.MetaFilePath(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	metaFI, err := os.Stat(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	accepted := metaFI.ModTime().Add(time.Hour)
	if err := os.Chtimes(checkPath, accepted, accepted); err != nil {
		t.Fatal(err)
	}

	cause := errors.New("same validation refusal")
	validationCalls := 0
	validation := &testCheckValidationPort{validate: func(string) error {
		validationCalls++
		if validationCalls == 1 {
			return nil
		}
		return cause
	}}
	retirement := &testRetirementPort{
		retireErr: fmt.Errorf("%w: poll validation failed: %w", domain.ErrCheckValidationRefused, cause),
	}

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outputDone := make(chan string, 1)
	go func() {
		output, _ := io.ReadAll(stderrR)
		outputDone <- string(output)
	}()
	original := os.Stderr
	os.Stderr = stderrW
	for cycle := 0; cycle < 2; cycle++ {
		resetRecovery()
		if _, err := RunCycleWithProbeAndSender(home, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, retirement, validation, testTaskStatePort{}); err != nil {
			os.Stderr = original
			_ = stderrW.Close()
			t.Fatalf("cycle %d: %v", cycle+1, err)
		}
	}
	os.Stderr = original
	_ = stderrW.Close()
	output := <-outputDone
	_ = stderrR.Close()

	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "poll check refused") || strings.Contains(line, "poll check invalid") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("refusal lines across retirement and discovery = %d, want 1\n%s", count, output)
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

func TestRunCycle_CollidingLabelsReportSeparately(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.b", "a_b"} {
		checkPath := filepath.Join(stateDir, name+".check")
		if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho ready\n"), 0755); err != nil {
			t.Fatal(err)
		}
		metaPath := filepath.Join(stateDir, name+".meta")
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
	}

	count, output := countRefusalLines(t, home, 5, nil)
	if count != 2 {
		t.Fatalf("refusal lines across 5 cycles = %d, want 2 (one per colliding label)\n%s", count, output)
	}
}

func TestRunCycle_RefusalReportsAgainAfterRecreation(t *testing.T) {
	home := staleCheckHome(t)
	checkPath := filepath.Join(home, "state", "task-1.check")

	if n, out := countRefusalLines(t, home, 2, nil); n != 1 {
		t.Fatalf("initial refusal lines = %d, want 1\n%s", n, out)
	}
	if err := os.Remove(checkPath); err != nil {
		t.Fatal(err)
	}
	if n, out := countRefusalLines(t, home, 1, nil); n != 0 {
		t.Fatalf("absent phase lines = %d, want 0\n%s", n, out)
	}
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho ready\n"), 0755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(home, "state", "task-1.meta")
	metaFI, err := os.Stat(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeMeta := metaFI.ModTime().Add(-time.Hour)
	if err := os.Chtimes(checkPath, beforeMeta, beforeMeta); err != nil {
		t.Fatal(err)
	}
	if n, out := countRefusalLines(t, home, 2, nil); n != 1 {
		t.Fatalf("recreated refusal lines = %d, want 1\n%s", n, out)
	}
}

func TestRunCycle_RefusedReplacementWithSameReasonReportsAgain(t *testing.T) {
	home := staleCheckHome(t)
	checkPath := filepath.Join(home, "state", "task-1.check")
	metaPath, err := homepkg.MetaFilePath(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	validate := func(string) error { return errors.New("same reason") }

	if n, out := countRefusalLines(t, home, 2, validate); n != 1 {
		t.Fatalf("initial refusal lines = %d, want 1\n%s", n, out)
	}
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho replacement with more content\n"), 0755); err != nil {
		t.Fatal(err)
	}
	metaFI, err := os.Stat(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	replacementTime := metaFI.ModTime().Add(-2 * time.Hour)
	if err := os.Chtimes(checkPath, replacementTime, replacementTime); err != nil {
		t.Fatal(err)
	}
	if n, out := countRefusalLines(t, home, 2, validate); n != 1 {
		t.Fatalf("replacement refusal lines = %d, want 1\n%s", n, out)
	}
}

func TestRunCycle_UntouchedRefusalReportsOnce(t *testing.T) {
	home := staleCheckHome(t)
	validate := func(string) error { return errors.New("unchanged reason") }
	if n, out := countRefusalLines(t, home, 5, validate); n != 1 {
		t.Fatalf("untouched refusal lines = %d, want 1\n%s", n, out)
	}
}

func TestRunCycle_ReplacementDuringValidationWithSameReasonReportsAgain(t *testing.T) {
	home := staleCheckHome(t)
	checkPath := filepath.Join(home, "state", "task-1.check")
	replacement := filepath.Join(home, "state", "task-1.check.new")
	reason := errors.New("same validation reason")
	calls := 0
	validate := func(path string) error {
		calls++
		if calls == 1 {
			if err := os.WriteFile(replacement, []byte("#!/bin/sh\necho replaced during validation\n"), 0755); err != nil {
				t.Fatal(err)
			}
			mtime := time.Now().Add(2 * time.Hour)
			if err := os.Chtimes(replacement, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, checkPath); err != nil {
				t.Fatal(err)
			}
		}
		return reason
	}

	if n, out := countRefusalLines(t, home, 2, validate); n != 2 {
		t.Fatalf("replacement during validation refusal lines = %d, want 2\n%s", n, out)
	}
}

func TestClearCheckRefusalMarkerReportsRemovalFailure(t *testing.T) {
	home := t.TempDir()
	artifactPath := filepath.Join(home, "state", "task-1.check")
	marker := checkRefusalMarkerPath(home, artifactPath)
	if err := os.MkdirAll(marker, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marker, "held"), []byte("marker"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := clearCheckRefusalMarker(home, artifactPath); err == nil {
		t.Fatal("clearCheckRefusalMarker returned nil for a non-empty marker directory")
	}
}

func TestReportCheckRefusalDoesNotPrintBeforePersistence(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "state"), []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = stderrW
	reportErr := reportCheckRefusal(home, filepath.Join(home, "task-1.check"), "state", "should not print")
	os.Stderr = original
	_ = stderrW.Close()
	output, readErr := io.ReadAll(stderrR)
	_ = stderrR.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if reportErr == nil {
		t.Fatal("reportCheckRefusal returned nil when marker persistence failed")
	}
	if len(output) != 0 {
		t.Fatalf("stderr = %q, want empty output", output)
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
	metaPath, err := homepkg.MetaFilePath(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}

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
