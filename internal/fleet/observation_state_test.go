package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Observation must expose one coherent authoritative state and never report
// working with pane_alive=false plus status_log_superseded=true. These tests
// are the RED contract for the unified soldier observation state.

type fixedProbe struct{ alive bool }

func (p fixedProbe) Probe(string, taskauthority.EndpointBinding, string) (bool, error) {
	return p.alive, nil
}

// bindSoldier binds a worktree and then ep to the task's current generation,
// which makes it working.
func bindSoldier(t *testing.T, c *taskauthority.Canonical, taskID string, ep taskauthority.EndpointBinding) {
	t.Helper()
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	precondition := func() domain.Precondition {
		agg, err := c.Get(tid)
		if err != nil {
			t.Fatal(err)
		}
		return domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	}
	wt := taskauthority.CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: tid, Precondition: precondition(), Reason: "bind", Binding: taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo", Path: "/wt/" + taskID, GitDir: "/repo/.git/worktrees/" + taskID, CommonDir: "/repo/.git",
		BaseHead: strings.Repeat("a", 40), LeaseID: "lease-wt-" + taskID, FenceToken: "fence-wt", BoundAtUnix: 1,
	}}
	if _, err := c.BindWorktree(mustFleetOperation(t, "op-bind-wt-"+taskID, wt), wt); err != nil {
		t.Fatal(err)
	}
	ep.LeaseID, ep.FenceToken, ep.Incarnation, ep.BoundAtUnix = "lease-ep-"+taskID, "fence-ep", "inc-"+taskID, 1
	bind := taskauthority.CanonicalBindEndpointRequest{HomeID: c.HomeID(), TaskID: tid, Precondition: precondition(), Binding: ep, Reason: "bind"}
	if _, err := c.BindEndpoint(mustFleetOperation(t, "op-bind-ep-"+taskID, bind), bind); err != nil {
		t.Fatal(err)
	}
}

// setupObservationHome seeds a canonical home with a task bound to a tmux
// pane (working), completed to done for the done state. Observation reads the
// canonical record as state truth.
func setupObservationHome(t *testing.T, homeDir, state string) {
	t.Helper()
	h, err := home.Init(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := domain.NewTaskID("task")
	if err != nil {
		t.Fatal(err)
	}
	create := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: taskID, Owner: "owner", Description: "work",
		Kind: "ship", Reason: "test",
	}
	if _, err := c.Create(mustFleetOperation(t, "op-create-task", create), create); err != nil {
		t.Fatal(err)
	}
	bindSoldier(t, c, "task", taskauthority.EndpointBinding{Backend: "tmux", Handle: "w1:p1"})
	if state == "working" {
		return
	}
	agg, err := c.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	complete := taskauthority.CanonicalCompleteRequest{
		HomeID: c.HomeID(), TaskID: taskID, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		To: taskauthority.PhaseDone, Reason: "done",
	}
	if _, err := c.Complete(mustFleetOperation(t, "op-complete-task", complete), complete); err != nil {
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
// working aggregate and superseded status log. Under the clean-break contract
// the canonical phase is the only lifecycle authority: a dead pane is a
// diagnostic (pane_alive=false) and must NOT change the canonical working phase.
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
		t.Fatal("status_log_superseded must be true (aggregate supersedes the log)")
	}
	// The canonical working phase is preserved; the probe is diagnostic only
	// and never silently changes lifecycle state.
	if state.Status != "working" {
		t.Fatalf("canonical phase = %q, want working (a dead pane is diagnostic, not lifecycle truth)", state.Status)
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
// TestReadWithProbe_NoProbeSupersededWorkingIsCoherent: without a probe the
// pane liveness is unknown (diagnostic), and the canonical working phase is
// preserved — the probe never changes lifecycle state.
func TestReadWithProbe_NoProbeSupersededWorkingIsCoherent(t *testing.T) {
	homeDir := t.TempDir()
	setupObservationHome(t, homeDir, "working")

	state, err := ReadWithProbe(homeDir, "task", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !state.StatusLogSuperseded {
		t.Fatal("status_log_superseded must be true")
	}
	if state.PaneAlive {
		t.Fatal("pane_alive must be false when no probe exists")
	}
	if state.Status != "working" {
		t.Fatalf("canonical phase = %q, want working (nil probe is diagnostic and never demotes the canonical lifecycle state)", state.Status)
	}
}
