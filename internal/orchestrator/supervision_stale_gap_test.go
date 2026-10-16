package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type staleGapProbe bool

func (a staleGapProbe) Probe(string, taskauthority.EndpointBinding, string) (bool, error) {
	return bool(a), nil
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

// An alive pane is stale only when its status is older than SilentAfter, and
// an old working line is absorbed rather than surfaced.
func TestScanTaskIdleThreshold(t *testing.T) {
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
			got := scanTaskWithProbe(home, BoundEndpoint{TaskID: "t1", Endpoint: taskauthority.EndpointBinding{Handle: "w1"}}, staleGapProbe(true), testSeatLiveness)
			if (got != nil) != tc.stale {
				t.Fatalf("scanTaskWithProbe = %+v, want stale=%v", got, tc.stale)
			}
		})
	}
}
