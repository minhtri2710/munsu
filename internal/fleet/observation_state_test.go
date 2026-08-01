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

// TestReadWithProbe_WorkingAliveIsCoherent: a live pane with a working
// aggregate and superseded status log reports working with pane_alive=true.
func TestReadWithProbe_WorkingAliveIsCoherent(t *testing.T) {
	homeDir := t.TempDir()
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
	if err := home.BindTaskEndpoint(homeDir, "task", "1", home.TaskEndpointBinding{
		TaskGeneration: "1", Backend: "tmux", Handle: "w1:p1", LeaseID: "l1", FenceToken: "f1", BoundAtUnix: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "backlog.md"), []byte("# Backlog\n\n- [-] task: in flight\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "working", "spawned"); err != nil {
		t.Fatal(err)
	}

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
	if err := home.BindTaskEndpoint(homeDir, "task", "1", home.TaskEndpointBinding{
		TaskGeneration: "1", Backend: "tmux", Handle: "w1:p1", LeaseID: "l1", FenceToken: "f1", BoundAtUnix: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "backlog.md"), []byte("# Backlog\n\n- [-] task: in flight\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "working", "spawned"); err != nil {
		t.Fatal(err)
	}

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
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "done", "merged"); err != nil {
		t.Fatal(err)
	}

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
	if err := home.BindTaskEndpoint(homeDir, "task", "1", home.TaskEndpointBinding{
		TaskGeneration: "1", Backend: "tmux", Handle: "w1:p1", LeaseID: "l1", FenceToken: "f1", BoundAtUnix: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "data", "backlog.md"), []byte("# Backlog\n\n- [-] task: in flight\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := home.UpdateCurrentTaskAggregateState(homeDir, "task", "working", "spawned"); err != nil {
		t.Fatal(err)
	}

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
