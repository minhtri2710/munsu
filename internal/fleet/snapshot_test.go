//go:build integration

package fleet

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func captureStdout(fn func() error) (string, error) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w

	errChan := make(chan error, 1)
	outChan := make(chan string)

	go func() {
		var buf strings.Builder
		_, err := io.Copy(&buf, r)
		if err != nil {
			errChan <- err
			return
		}
		outChan <- buf.String()
	}()

	fnErr := fn()
	w.Close()
	os.Stdout = old

	select {
	case err := <-errChan:
		return "", err
	case out := <-outChan:
		return out, fnErr
	}
}

// testSnapshotDeps returns the clean-break snapshot dependencies for tests:
// the canonical current-state query is always present; the optional endpoint
// probe (diagnostic only) is attached when provided.
func testSnapshotDeps(t *testing.T, probes ...EndpointProbe) SnapshotDependencies {
	t.Helper()
	deps := SnapshotDependencies{CurrentState: NewCanonicalCurrentState()}
	if len(probes) > 0 {
		deps.Endpoint = probes[0]
	}
	return deps
}

// failingCurrentState fails every read, simulating an unreadable canonical
// Task Authority.
type failingCurrentState struct {
	err error
}

func (f failingCurrentState) Read(homeDir, taskID string) (*CurrentStateInfo, error) {
	return nil, f.err
}

// mustCreateFleetTask seeds a queued canonical task of the given kind into a
// fresh canonical home at homeDir.
func mustCreateFleetTask(t *testing.T, homeDir, taskID, kind string) *taskauthority.Canonical {
	t.Helper()
	auth := mustCanonicalForHome(t, homeDir)
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := domain.NewProjectID("munsu")
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "owner",
		Description: "work", Kind: kind, Project: project, Reason: "test",
	}
	if kind == "scout" {
		req.ScoutScope = "investigate scope"
		req.ScoutRuntimeBudgetSecs = 300
	}
	if _, err := auth.Create(mustFleetOperation(t, "op-create-"+taskID, req), req); err != nil {
		t.Fatalf("Create(%s): %v", taskID, err)
	}
	return auth
}

// startFleetTask drives a queued canonical task into working.
func startFleetTask(t *testing.T, auth *taskauthority.Canonical, taskID string) {
	t.Helper()
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalStartRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(cur.Generation), uint64(cur.Revision)),
		Reason:       "test",
	}
	if _, err := auth.Start(mustFleetOperation(t, "op-start-"+taskID, req), req); err != nil {
		t.Fatalf("Start(%s): %v", taskID, err)
	}
}

// TestPhaseFromMeta verifies all three display-phase transitions.
func TestPhaseFromMeta(t *testing.T) {
	tests := []struct {
		window    string
		paneAlive bool
		want      string
	}{
		{"", false, "registered"}, // pre-spawn: no window
		{"@1", true, "alive"},     // active pane
		{"@1", false, "dead"},     // window set but pane gone
		{"", true, "registered"},  // window empty, paneAlive irrelevant
	}
	for _, tc := range tests {
		got := PhaseFromMeta(tc.window, tc.paneAlive)
		if got != tc.want {
			t.Errorf("PhaseFromMeta(%q, %v) = %q, want %q", tc.window, tc.paneAlive, got, tc.want)
		}
	}
}

func TestBearings_Idle(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(func() error {
		return Bearings(tmpDir, "", testSnapshotDeps(t))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "No in-flight ship/scout tasks. Fleet is idle.") {
		t.Errorf("expected idle message, got: %q", out)
	}
}

func TestBearings_WithTasks(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}

	// Canonical ship/scout tasks drive the bearings resume; a canonical
	// kind=other task is filtered out of the ship/scout report.
	mustCreateFleetTask(t, tmpDir, "task-ship", "ship")
	mustCreateFleetTask(t, tmpDir, "task-scout", "scout")
	mustCreateFleetTask(t, tmpDir, "task-other", "other")

	out, err := captureStdout(func() error {
		return Bearings(tmpDir, "", testSnapshotDeps(t))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "task-ship") {
		t.Errorf("expected output to contain task-ship, got: %q", out)
	}
	if !strings.Contains(out, "task-scout") {
		t.Errorf("expected output to contain task-scout, got: %q", out)
	}
	if strings.Contains(out, "task-other") {
		t.Errorf("expected output not to contain task-other, got: %q", out)
	}
}

func TestView_RegisteredPhase(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}

	// A queued (not-yet-started) canonical task has canonical phase "queued",
	// which must win over any diagnostic projection.
	mustCreateFleetTask(t, tmp, "pre-spawn-task", "ship")

	out, err := captureStdout(func() error {
		return View(tmp, testSnapshotDeps(t))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "queued") {
		t.Errorf("expected View to show canonical 'queued' phase, got: %q", out)
	}
}

func TestCaptainStatus_Seeded(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(parent, "state"), 0755)

	// Home exists, no parent meta → seeded (lock ignored).
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.WriteFile(filepath.Join(smHome, "state", ".lock"), []byte("999999\n"), 0644)

	status := CaptainStatus(parent, "test-sm", smHome, nil)
	if status != "seeded" {
		t.Errorf("CaptainStatus = %q, want %q", status, "seeded")
	}
}

func TestCaptainStatus_Unknown(t *testing.T) {
	// Non-existent home should return unknown
	status := CaptainStatus("/nonexistent/parent", "sm", "/nonexistent/sm", nil)
	if status != "unknown" {
		t.Errorf("CaptainStatus = %q, want %q", status, "unknown")
	}
}

type snapshotProbe struct {
	status EndpointStatus
	err    error
}

func (p snapshotProbe) ProbeEndpoint(EndpointRef) (EndpointStatus, error) { return p.status, p.err }

func TestCaptainStatusTypedObservations(t *testing.T) {
	tests := []struct {
		name  string
		state EndpointObservationState
		err   error
		want  string
	}{
		{name: "alive", state: EndpointAlive, want: "alive"},
		{name: "starting", state: EndpointStarting, want: "starting"},
		{name: "dead", state: EndpointDead, want: "dead"},
		{name: "unresponsive", state: EndpointUnresponsive, want: "unresponsive"},
		{name: "unresolved", state: EndpointUnresolved, want: "unresolved"},
		{name: "stale identity", state: EndpointStaleIdentity, want: "stale-identity"},
		{name: "unknown", state: EndpointUnknown, want: "unknown"},
		{name: "backend error remains unknown", err: fmt.Errorf("backend unavailable"), want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			parent := filepath.Join(tmp, "parent")
			smHome := filepath.Join(tmp, "captains", "test-sm")
			os.MkdirAll(smHome, 0755)
			os.MkdirAll(filepath.Join(parent, "state"), 0755)
			if err := home.WriteMeta(parent, "captain:test-sm", map[string]string{"kind": "captain", "sm_id": "test-sm", "home": smHome, "window": "@cap", "backend": "tmux"}); err != nil {
				t.Fatal(err)
			}
			probe := snapshotProbe{err: tt.err}
			if tt.err == nil {
				probe.status = endpointStatusFromState(tt.state)
			}
			if got := CaptainStatus(parent, "test-sm", smHome, probe); got != tt.want {
				t.Fatalf("CaptainStatus=%q want %q", got, tt.want)
			}
		})
	}
}

func TestCaptainStatusProbeErrorIsUnknownNotDead(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	if err := home.WriteMeta(parent, "captain:test-sm", map[string]string{"kind": "captain", "sm_id": "test-sm", "home": smHome, "window": "@cap", "backend": "tmux"}); err != nil {
		t.Fatal(err)
	}
	if got := CaptainStatus(parent, "test-sm", smHome, snapshotProbe{err: fmt.Errorf("backend unavailable")}); got != "unknown" {
		t.Fatalf("CaptainStatus=%q want %q", got, "unknown")
	}
}

// TestSnapshot_NilCurrentStateFailsClosed proves Snapshot rejects a nil
// current-state dependency instead of silently falling back to a projection.
func TestSnapshot_NilCurrentStateFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	mustCreateFleetTask(t, tmp, "t1", "ship")
	if err := home.AppendStatus(tmp, "t1", "working: stale tail"); err != nil {
		t.Fatal(err)
	}
	_, err := Snapshot(tmp, SnapshotDependencies{})
	if err == nil {
		t.Fatalf("Snapshot(nil CurrentState) = nil error, want fail-closed error")
	}
}

// TestSnapshot_MetaOnlySoldierTaskRejected proves a task-facing .meta without a
// canonical record fails closed (clean break) instead of being projected.
func TestSnapshot_MetaOnlySoldierTaskRejected(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)
	if err := home.WriteMeta(tmp, "legacy-task", map[string]string{"kind": "ship", "project": "munsu"}); err != nil {
		t.Fatal(err)
	}
	_, err := Snapshot(tmp, testSnapshotDeps(t))
	if err == nil {
		t.Fatalf("Snapshot over a meta-only soldier task = nil error, want fail-closed rejection")
	}
}

// TestSnapshot_MetaOnlyCaptainMetadataAllowed proves captain metadata (kind=captain)
// is exempt from the rejection: it is non-task authority metadata by design.
func TestSnapshot_MetaOnlyCaptainMetadataAllowed(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)
	if err := home.WriteMeta(tmp, "captain:test-sm", map[string]string{"kind": "captain", "sm_id": "test-sm"}); err != nil {
		t.Fatal(err)
	}
	snap, err := Snapshot(tmp, testSnapshotDeps(t))
	if err != nil {
		t.Fatalf("Snapshot with captain metadata = %v, want nil", err)
	}
	if len(snap.Tasks) != 1 || snap.Tasks[0].Kind != "captain" {
		t.Fatalf("expected the captain metadata entry, got %+v", snap.Tasks)
	}
}

// TestSnapshot_CanonicalPhaseWinsOverStaleStatus proves the canonical current
// state (working phase) is reported even when a stale .status tail disagrees.
func TestSnapshot_CanonicalPhaseWinsOverStaleStatus(t *testing.T) {
	cases := []struct {
		name        string
		phase       string
		kind        string
		project     string
		staleStatus string
	}{
		{name: "working beats stale done", phase: "working", kind: "ship", project: "munsu", staleStatus: "done: stale build complete"},
		{name: "done beats stale working and tampered metadata", phase: "done", kind: "scout", project: "hacked", staleStatus: "working: still working"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			if tc.phase == "working" {
				if _, err := home.Init(tmp); err != nil {
					t.Fatal(err)
				}
				auth := mustCreateFleetTask(t, tmp, "t1", "ship")
				startFleetTask(t, auth, "t1")
			} else {
				seedCanonicalShipTask(t, tmp, "t1", "done")
			}
			if err := home.WriteMeta(tmp, "t1", map[string]string{"window": "@win", "worktree": "/tmp/wt", "kind": tc.kind, "project": tc.project}); err != nil {
				t.Fatal(err)
			}
			if err := home.AppendStatus(tmp, "t1", tc.staleStatus); err != nil {
				t.Fatal(err)
			}
			snap, err := Snapshot(tmp, testSnapshotDeps(t))
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if len(snap.Tasks) != 1 {
				t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
			}
			ts := snap.Tasks[0]
			if ts.CurrentState != tc.phase {
				t.Errorf("CurrentState = %q, want canonical %q", ts.CurrentState, tc.phase)
			}
			if !ts.StatusLogSuperseded {
				t.Errorf("StatusLogSuperseded = false, want true")
			}
			if tc.phase == "done" && (ts.Kind != "ship" || ts.Project != "proj-x") {
				t.Errorf("kind=%q project=%q, want canonical ship/proj-x", ts.Kind, ts.Project)
			}
		})
	}
}

// TestSnapshot_ResolverErrorFailsClientCurrentStateError proves a failing
// current-state query makes Snapshot fail with task+home context even when a
// valid stale .status tail exists.
func TestSnapshot_ResolverErrorFailsClosedOverStaleStatus(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	mustCreateFleetTask(t, tmp, "t1", "ship")
	// A valid, stale .status tail must NOT be silently projected when the
	// canonical query fails (fail-closed posture).
	if err := home.AppendStatus(tmp, "t1", "working: stale tail claim"); err != nil {
		t.Fatal(err)
	}
	deps := SnapshotDependencies{CurrentState: failingCurrentState{err: errors.New("canonical read failed: recovery required")}}
	_, err := Snapshot(tmp, deps)
	if err == nil {
		t.Fatal("Snapshot = nil error, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "t1") || !testutil.PathInMessage(err.Error(), tmp) {
		t.Errorf("resolver error must carry task and home context, got: %v", err)
	}
	if strings.Contains(err.Error(), strconv.Quote(tmp)) {
		t.Errorf("resolver error pre-quotes home path, got: %v", err)
	}
}

// TestSnapshot_PaneAliveProbeTrue proves a diagnostic endpoint probe reports a
// live pane without changing the canonical phase.
func TestSnapshot_PaneAliveProbeTrue(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	mustCreateFleetTask(t, tmp, "t1", "ship")
	if err := home.WriteMeta(tmp, "t1", map[string]string{"window": "@win", "project": "munsu", "kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	snap, err := Snapshot(tmp, testSnapshotDeps(t, snapshotProbe{status: endpointStatusFromState(EndpointAlive)}))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]
	if !ts.PaneAlive {
		t.Errorf("PaneAlive = false, want true")
	}
	if ts.PaneAliveUnknown {
		t.Errorf("PaneAliveUnknown = true, want false")
	}
	// Pane liveness is diagnostic only: a queued canonical phase stays queued.
	if ts.CurrentState != string(taskauthority.PhaseQueued) {
		t.Errorf("CurrentState = %q, want %q (probe must not change lifecycle state)", ts.CurrentState, taskauthority.PhaseQueued)
	}
}

func TestSnapshot_PaneAliveProbeFalse(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	mustCreateFleetTask(t, tmp, "t1", "ship")
	if err := home.WriteMeta(tmp, "t1", map[string]string{"window": "@win", "project": "munsu", "kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	snap, err := Snapshot(tmp, testSnapshotDeps(t, snapshotProbe{status: endpointStatusFromState(EndpointDead)}))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]
	if ts.PaneAlive {
		t.Errorf("PaneAlive = true, want false")
	}
	if !ts.PaneAliveUnknown {
		t.Errorf("PaneAliveUnknown = false, want true for non-alive typed observation")
	}
	// A dead pane is diagnostic only and must not change the canonical phase.
	if ts.CurrentState != string(taskauthority.PhaseQueued) {
		t.Errorf("CurrentState = %q, want %q (dead probe must not change lifecycle state)", ts.CurrentState, taskauthority.PhaseQueued)
	}
}

// TestSnapshot_PaneAliveUnknownWhenNoProbe proves absence of a diagnostic probe
// yields an unknown pane, not a lifecycle decision.
func TestSnapshot_PaneAliveUnknownWhenNoProbe(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	mustCreateFleetTask(t, tmp, "t1", "ship")
	if err := home.WriteMeta(tmp, "t1", map[string]string{"window": "@win", "project": "munsu", "kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	snap, err := Snapshot(tmp, testSnapshotDeps(t))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]
	if !ts.PaneAliveUnknown {
		t.Errorf("PaneAliveUnknown = false, want true (no probe wired)")
	}
	if ts.PaneAlive {
		t.Errorf("PaneAlive = true, want false (no probe)")
	}
}

// TestSnapshot_IncludesCaptainHomeTasks proves soldiers under a captain home are
// enumerated with the captain source/home labels.
func TestSnapshot_IncludesCaptainHomeTasks(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}

	capHome := filepath.Join(parent, "captains", "munsu")
	if _, err := home.Init(capHome); err != nil {
		t.Fatal(err)
	}
	mustCreateFleetTask(t, capHome, "ship-child", "ship")

	snap, err := Snapshot(parent, testSnapshotDeps(t))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var foundChild bool
	for _, ts := range snap.Tasks {
		if ts.ID == "ship-child" {
			foundChild = true
			if ts.Source != "captain:munsu" {
				t.Fatalf("child source = %q", ts.Source)
			}
			if ts.Home != capHome {
				t.Fatalf("child home = %q", ts.Home)
			}
			if ts.Kind != "ship" {
				t.Fatalf("kind = %q", ts.Kind)
			}
		}
	}
	if !foundChild {
		t.Fatal("expected ship-child from captain home in snapshot")
	}
}

// TestReadWithProbeCanonicalPhaseUnaffectedByProbe proves soldier-state keeps
// the canonical phase even when an endpoint probe is dead/unknown: probe output
// is diagnostic and never changes lifecycle state.
func TestReadWithProbeCanonicalPhaseUnaffectedByProbe(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	auth := mustCreateFleetTask(t, tmp, "t1", "ship")
	startFleetTask(t, auth, "t1")
	if err := home.WriteMeta(tmp, "t1", map[string]string{"window": "@win", "project": "munsu", "kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	st, err := ReadWithProbe(tmp, "t1", &boolProbe{v: false})
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != string(taskauthority.PhaseWorking) {
		t.Errorf("Status = %q, want %q (canonical working must not be demoted by a dead probe)", st.Status, taskauthority.PhaseWorking)
	}
	if st.PaneAlive {
		t.Errorf("PaneAlive = true, want false (diagnostic)")
	}
}

// boolProbe is a StateEndpointProbe returning a fixed alive flag.
type boolProbe struct{ v bool }

func (p *boolProbe) Probe(homeDir string, meta map[string]string) (bool, error) { return p.v, nil }

// TestSnapshotDisplay_FailedCaptainHomeIsNamedFailure proves a captain home
// that cannot be read becomes one named failure with no rows, the healthy
// sources keep their rows, and Snapshot still refuses the same input.
func TestSnapshotDisplay_FailedCaptainHomeIsNamedFailure(t *testing.T) {
	cases := []struct {
		name string
		bad  func(t *testing.T, capHome string)
	}{
		{"not a canonical home", func(t *testing.T, capHome string) {
			if err := os.MkdirAll(capHome, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"task without canonical record", func(t *testing.T, capHome string) {
			if _, err := home.Init(capHome); err != nil {
				t.Fatal(err)
			}
			mustCreateFleetTask(t, capHome, "ok-task", "ship")
			if err := home.WriteMeta(capHome, "legacy-task", map[string]string{"kind": "ship"}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			if _, err := home.Init(parent); err != nil {
				t.Fatal(err)
			}
			mustCreateFleetTask(t, parent, "primary-task", "ship")
			goodHome := filepath.Join(parent, "captains", "good")
			if _, err := home.Init(goodHome); err != nil {
				t.Fatal(err)
			}
			mustCreateFleetTask(t, goodHome, "good-task", "ship")
			badHome := filepath.Join(parent, "captains", "bad")
			tc.bad(t, badHome)

			d, err := SnapshotDisplay(parent, testSnapshotDeps(t))
			if err != nil {
				t.Fatalf("SnapshotDisplay: %v", err)
			}
			if len(d.Failures) != 1 {
				t.Fatalf("Failures = %v, want exactly one", d.Failures)
			}
			f := d.Failures[0]
			if f.Source != "captain:bad" || f.Home != badHome || f.Err == nil {
				t.Fatalf("failure = %+v, want source captain:bad home %s with an error", f, badHome)
			}
			var ids []string
			for _, ts := range d.Tasks {
				ids = append(ids, ts.ID)
				if ts.Source == "captain:bad" {
					t.Fatalf("failed source contributed row %q", ts.ID)
				}
			}
			if strings.Join(ids, ",") != "primary-task,good-task" {
				t.Fatalf("rows = %v, want primary-task,good-task", ids)
			}

			_, err = Snapshot(parent, testSnapshotDeps(t))
			if want := "scanning captain home " + badHome + ": " + f.Err.Error(); err == nil || err.Error() != want {
				t.Fatalf("Snapshot = %v, want %q", err, want)
			}
		})
	}
}

// TestSnapshotDisplay_FailedPrimaryKeepsCaptainRows proves a failing primary
// home is one named failure while captain rows survive, and Snapshot refuses
// with the primary's raw error.
func TestSnapshotDisplay_FailedPrimaryKeepsCaptainRows(t *testing.T) {
	parent := t.TempDir()
	goodHome := filepath.Join(parent, "captains", "good")
	if _, err := home.Init(goodHome); err != nil {
		t.Fatal(err)
	}
	mustCreateFleetTask(t, goodHome, "good-task", "ship")

	d, err := SnapshotDisplay(parent, testSnapshotDeps(t))
	if err != nil {
		t.Fatalf("SnapshotDisplay: %v", err)
	}
	if len(d.Failures) != 1 || d.Failures[0].Source != "primary" || d.Failures[0].Home != parent {
		t.Fatalf("Failures = %v, want one primary failure for %s", d.Failures, parent)
	}
	if len(d.Tasks) != 1 || d.Tasks[0].ID != "good-task" {
		t.Fatalf("rows = %+v, want only good-task", d.Tasks)
	}

	_, err = Snapshot(parent, testSnapshotDeps(t))
	if err == nil || err.Error() != d.Failures[0].Err.Error() {
		t.Fatalf("Snapshot = %v, want the primary read error %v", err, d.Failures[0].Err)
	}
}

// TestSnapshotDisplay_NilCurrentStateRefused proves the display read has no
// per-source answer without a current-state query.
func TestSnapshotDisplay_NilCurrentStateRefused(t *testing.T) {
	if _, err := SnapshotDisplay(t.TempDir(), SnapshotDependencies{}); err == nil {
		t.Fatal("SnapshotDisplay(nil CurrentState) = nil error")
	}
}

// TestHumanNeeded pins the canonical phase over the last status line.
func TestHumanNeeded(t *testing.T) {
	tests := []struct {
		name  string
		state string
		last  string
		want  bool
	}{
		{"blocked without status line", "blocked", "", true},
		{"blocked with working line", "blocked", "working: on it", true},
		{"done with needs-decision line", "done", "needs-decision: which db?", false},
		{"resolved with blocked line", "resolved", "blocked: waiting", false},
		{"retired with failed line", "retired", "failed: build", false},
		{"working with needs-decision line", "working", "needs-decision: which db?", true},
		{"queued with failed line", "queued", "failed: build", true},
		{"working with progress line", "working", "working: blocked: is prose", false},
		{"working with done line", "working", "done: shipped", false},
		{"working with no line", "working", "", false},
		{"captain metadata row", "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HumanNeeded(TaskSnapshot{CurrentState: tc.state, LastStatus: tc.last}); got != tc.want {
				t.Fatalf("HumanNeeded(state=%q, last=%q) = %v, want %v", tc.state, tc.last, got, tc.want)
			}
		})
	}
}

// TestHumanNeeded_CanonicalBlockedRow proves the phase value HumanNeeded
// relies on is what Snapshot reports for a canonical blocked task.
func TestHumanNeeded_CanonicalBlockedRow(t *testing.T) {
	tmp := t.TempDir()
	if _, err := home.Init(tmp); err != nil {
		t.Fatal(err)
	}
	auth := mustCreateFleetTask(t, tmp, "t1", "ship")
	req := taskauthority.CanonicalBlockRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskID(t, "t1"),
		Precondition: taPrecondition(1, 1), Detail: "waiting on Human", Reason: "test",
	}
	if _, err := auth.Block(mustFleetOperation(t, "op-block-t1", req), req); err != nil {
		t.Fatalf("Block: %v", err)
	}
	snap, err := Snapshot(tmp, testSnapshotDeps(t))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 || snap.Tasks[0].LastStatus != "" {
		t.Fatalf("rows = %+v, want one blocked row without a status line", snap.Tasks)
	}
	if !HumanNeeded(snap.Tasks[0]) {
		t.Fatalf("HumanNeeded(%q row) = false, want true", snap.Tasks[0].CurrentState)
	}
}
