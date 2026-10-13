package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type staleGapProbe bool

func (a staleGapProbe) Probe(string, map[string]string) (bool, error) { return bool(a), nil }

type runStepPort string

func (s runStepPort) ReadTaskState(string, string) (*ObservedTaskState, error) {
	return &ObservedTaskState{NoMistakesRunStep: string(s)}, nil
}

func writeAgedStatus(t *testing.T, home, id, line string, age time.Duration) {
	t.Helper()
	dir := filepath.Join(home, "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".status")
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// A dead pane with a leftover working line is stale unless its no-mistakes
// run is active, which absorbs it.
func TestShouldAbsorbStaleActiveNoMistakesRun(t *testing.T) {
	home := t.TempDir()
	writeAgedStatus(t, home, "t1", "working: leftover", 0)
	if shouldAbsorbStale(home, "t1", false, runStepPort(""), testSeatLiveness) {
		t.Fatal("a dead pane with no active run absorbed")
	}
	if !shouldAbsorbStale(home, "t1", false, runStepPort("ci"), testSeatLiveness) {
		t.Fatal("a dead pane whose no-mistakes run is in ci did not absorb")
	}
}

// An alive pane is stale only when its status is older than SilentAfter, and
// an old working line is absorbed rather than surfaced.
func TestScanTaskIdleThreshold(t *testing.T) {
	meta := map[string]string{"window": "w1"}
	for _, tc := range []struct {
		name  string
		line  string
		age   time.Duration
		stale bool
	}{
		{"fresh status", "note: thinking", time.Second, false},
		{"old status", "note: thinking", testSeatLiveness.SilentAfter + time.Minute, true},
		{"old working line absorbs", "working: implementing", testSeatLiveness.SilentAfter + time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeAgedStatus(t, home, "t1", tc.line, tc.age)
			got := scanTaskWithProbe(home, "t1", meta, staleGapProbe(true), runStepPort(""), testSeatLiveness)
			if (got != nil) != tc.stale {
				t.Fatalf("scanTaskWithProbe = %+v, want stale=%v", got, tc.stale)
			}
		})
	}
}
