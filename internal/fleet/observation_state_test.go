package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// Observation must expose one coherent authoritative state and never report
// working with pane_alive=false plus status_log_superseded=true. These tests
// are the RED contract for the unified soldier observation state.

type fixedProbe struct{ alive bool }

func (p fixedProbe) Probe(string, map[string]string) (bool, error) { return p.alive, nil }

// setupObservationHome seeds a home with a v1 aggregate, meta, and (for
// working states) an endpoint binding and in-flight backlog line. The generic
// home state setter and endpoint binder were deleted with the spawn cutover
// (Task 4.2); fixtures read-mutate-write through home.WriteTaskAggregate.
func setupObservationHome(t *testing.T, homeDir, state string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, "task", map[string]string{"window": "w1:p1", "backend": "tmux"}); err != nil {
		t.Fatal(err)
	}
	if _, err := home.CreateTaskAggregate(homeDir, "task", "owner", "work", "ship", ""); err != nil {
		t.Fatal(err)
	}
	agg, ok, err := home.ReadCurrentTaskAggregate(homeDir, "task")
	if err != nil || !ok {
		t.Fatalf("ReadCurrentTaskAggregate ok=%v err=%v", ok, err)
	}
	if state == "working" {
		agg.Endpoint = &home.TaskEndpointBinding{
			TaskGeneration: agg.Generation, Backend: "tmux", Handle: "w1:p1",
			LeaseID: "l1", FenceToken: "f1", BoundAtUnix: 1000,
		}
		if err := os.WriteFile(filepath.Join(homeDir, "data", "backlog.md"), []byte("# Backlog\n\n- [-] task: in flight\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	agg.State = state
	agg.StateDetail = "spawned"
	if err := home.WriteTaskAggregate(homeDir, *agg); err != nil {
		t.Fatal(err)
	}
}

// TestReadWithProbe_WorkingAliveIsCoherent: a live pane with a working
// aggregate and superseded status log reports working with pane_alive=true.
func TestReadWithProbe_WorkingAliveIsCoherent(t *testing.T) {
	homeDir := t.TempDir()
	setupObservationHome(t, homeDir, "working")

	state, err := ReadWithProbe(homeDir, "task", fixedProbe{alive: true})
	if err != nil {
		t.Fatal(err)
	}
	if !state.PaneAlive {
		t.Fatal("pane_alive must be true when the probe reports the pane alive")
	}
	if state.Status != "working" {
		t.Fatalf("status = %q, want working for a live pane with working aggregate", state.Status)
	}
}

// TestReadWithProbe_WorkingDeadPaneSupersededIsCoherent: a dead pane with a
// working aggregate and superseded status log must NOT report working. A dead
// pane cannot be working; observation must downgrade to a coherent state.
func TestReadWithProbe_WorkingDeadPaneSupersededIsCoherent(t *testing.T) {
	homeDir := t.TempDir()
	setupObservationHome(t, homeDir, "working")

	state, err := ReadWithProbe(homeDir, "task", fixedProbe{alive: false})
	if err != nil {
		t.Fatal(err)
	}
	if state.PaneAlive {
		t.Fatal("pane_alive must be false when the probe reports the pane dead")
	}
	if !state.StatusLogSuperseded {
		t.Fatal("status_log_superseded must be true (aggregate/backlog supersede the log)")
	}
	if state.Status == "working" {
		t.Fatalf("must never report working with pane_alive=false plus status_log_superseded=true; got status %q description %q", state.Status, state.Description)
	}
}

// TestReadWithProbe_TerminalDeadPaneKeepsTerminalState: a dead pane must not
// corrupt a terminal state (done) — coherence only downgrades live claims.
func TestReadWithProbe_TerminalDeadPaneKeepsTerminalState(t *testing.T) {
	homeDir := t.TempDir()
	setupObservationHome(t, homeDir, "done")

	state, err := ReadWithProbe(homeDir, "task", fixedProbe{alive: false})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "done" {
		t.Fatalf("status = %q, want done (terminal state preserved despite dead pane)", state.Status)
	}
}

// TestReadWithProbe_NoProbeUnknownPaneDoesNotFabricate: without a probe the
// pane liveness is unknown, and a superseded working aggregate must not be
// reported as a coherent "working" (the current nil-probe path).
func TestReadWithProbe_NoProbeSupersededWorkingIsCoherent(t *testing.T) {
	homeDir := t.TempDir()
	setupObservationHome(t, homeDir, "working")

	// ReadSoldierState is the nil-probe path used by task observe / soldier-state.
	state, err := ReadSoldierState(homeDir, "task")
	if err != nil {
		t.Fatal(err)
	}
	if !state.StatusLogSuperseded {
		t.Fatal("status_log_superseded must be true")
	}
	if state.Status == "working" {
		t.Fatalf("nil-probe observation must not report working when pane liveness is unknown and the status log is superseded; got %q", state.Status)
	}
}
