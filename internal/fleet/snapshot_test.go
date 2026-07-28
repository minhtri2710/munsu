package fleet

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
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

	out, err := captureStdout(func() error {
		return Bearings(tmpDir, "")
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

	// Let's create state directory
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("failed to create state dir: %v", err)
	}

	// Create a "ship" task
	shipMeta := map[string]string{
		"window":   "@ship-win",
		"worktree": "/tmp/wt-ship",
		"project":  "munsu",
		"harness":  "pi",
		"model":    "claude-sonnet",
		"kind":     "ship",
		"mode":     "no-mistakes",
		"yolo":     "off",
	}
	if err := home.WriteMeta(tmpDir, "task-ship", shipMeta); err != nil {
		t.Fatalf("failed to write ship meta: %v", err)
	}
	if err := home.AppendStatus(tmpDir, "task-ship", "running builds"); err != nil {
		t.Fatalf("failed to append ship status: %v", err)
	}

	// Create a "scout" task
	scoutMeta := map[string]string{
		"window":   "@scout-win",
		"worktree": "/tmp/wt-scout",
		"project":  "munsu",
		"harness":  "pi",
		"model":    "claude-sonnet",
		"kind":     "scout",
		"mode":     "no-mistakes",
		"yolo":     "off",
	}
	if err := home.WriteMeta(tmpDir, "task-scout", scoutMeta); err != nil {
		t.Fatalf("failed to write scout meta: %v", err)
	}
	if err := home.AppendStatus(tmpDir, "task-scout", "scouting around"); err != nil {
		t.Fatalf("failed to append scout status: %v", err)
	}

	// Create an ignored task (kind = "other")
	otherMeta := map[string]string{
		"window":   "@other-win",
		"worktree": "/tmp/wt-other",
		"project":  "munsu",
		"harness":  "pi",
		"model":    "claude-sonnet",
		"kind":     "other",
		"mode":     "no-mistakes",
		"yolo":     "off",
	}
	if err := home.WriteMeta(tmpDir, "task-other", otherMeta); err != nil {
		t.Fatalf("failed to write other meta: %v", err)
	}

	out, err := captureStdout(func() error {
		return Bearings(tmpDir, "")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Output should contain task-ship and task-scout, but not task-other
	if !strings.Contains(out, "task-ship") {
		t.Errorf("expected output to contain task-ship, got: %q", out)
	}
	if !strings.Contains(out, "task-scout") {
		t.Errorf("expected output to contain task-scout, got: %q", out)
	}
	if strings.Contains(out, "task-other") {
		t.Errorf("expected output not to contain task-other, got: %q", out)
	}
	if !strings.Contains(out, "running builds") {
		t.Errorf("expected output to contain status 'running builds', got: %q", out)
	}
	if !strings.Contains(out, "scouting around") {
		t.Errorf("expected output to contain status 'scouting around', got: %q", out)
	}
	if !strings.Contains(out, "scouting around") {
		t.Errorf("expected output to contain status 'scouting around', got: %q", out)
	}
}

func TestView_RegisteredPhase(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create a pre-spawn meta (no window, no worktree)
	preSpawnMeta := map[string]string{
		"project": "munsu",
		"harness": "pi",
		"model":   "claude-sonnet",
		"kind":    "ship",
		"mode":    "no-mistakes",
	}
	if err := home.WriteMeta(tmp, "pre-spawn-task", preSpawnMeta); err != nil {
		t.Fatalf("failed to write meta: %v", err)
	}

	out, err := captureStdout(func() error {
		return View(tmp)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should show "registered" not "dead"
	if !strings.Contains(out, "registered") {
		t.Errorf("expected View to show 'registered' for pre-spawn task, got: %q", out)
	}
}

func withPaneAlive(t *testing.T, fn func(parentHome string, meta map[string]string) (bool, error)) {
	t.Helper()
	old := paneAliveForCaptain
	paneAliveForCaptain = fn
	t.Cleanup(func() { paneAliveForCaptain = old })
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

	status := CaptainStatus(parent, "test-sm", smHome)
	if status != "seeded" {
		t.Errorf("CaptainStatus = %q, want %q", status, "seeded")
	}
}

func TestCaptainStatus_Alive(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(parent, "state"), 0755)

	if err := home.WriteMeta(parent, "captain:test-sm", map[string]string{
		"kind":    "captain",
		"sm_id":   "test-sm",
		"home":    smHome,
		"window":  "@cap",
		"backend": "tmux",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	withPaneAlive(t, func(parentHome string, meta map[string]string) (bool, error) {
		if meta["window"] != "@cap" {
			t.Errorf("window = %q, want @cap", meta["window"])
		}
		return true, nil
	})

	// Stale/missing lock must not matter when pane is alive.
	status := CaptainStatus(parent, "test-sm", smHome)
	if status != "alive" {
		t.Errorf("CaptainStatus = %q, want %q", status, "alive")
	}
}

func TestCaptainStatus_Dead(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.WriteFile(filepath.Join(smHome, "state", ".lock"), []byte("999999\n"), 0644)

	if err := home.WriteMeta(parent, "captain:test-sm", map[string]string{
		"kind":    "captain",
		"sm_id":   "test-sm",
		"home":    smHome,
		"window":  "@cap",
		"backend": "tmux",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	withPaneAlive(t, func(parentHome string, meta map[string]string) (bool, error) {
		return false, nil
	})

	// Live lock must not override dead pane.
	status := CaptainStatus(parent, "test-sm", smHome)
	if status != "dead" {
		t.Errorf("CaptainStatus = %q, want %q", status, "dead")
	}
}

func TestCaptainStatus_Unknown(t *testing.T) {
	// Non-existent home should return unknown
	status := CaptainStatus("/nonexistent/parent", "sm", "/nonexistent/sm")
	if status != "unknown" {
		t.Errorf("CaptainStatus = %q, want %q", status, "unknown")
	}
}

func TestCaptainStatus_BackendErrorIsDead(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(parent, "state"), 0755)

	if err := home.WriteMeta(parent, "captain:test-sm", map[string]string{
		"kind":   "captain",
		"sm_id":  "test-sm",
		"window": "@cap",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	withPaneAlive(t, func(parentHome string, meta map[string]string) (bool, error) {
		return false, fmt.Errorf("backend unavailable")
	})

	status := CaptainStatus(parent, "test-sm", smHome)
	if status != "dead" {
		t.Errorf("CaptainStatus = %q, want %q", status, "dead")
	}
}

// withResolver wraps resolveCurrentState for testing.
func withResolver(t *testing.T, fn func(homeDir, id string) (*CurrentStateInfo, error)) {
	t.Helper()
	old := resolveCurrentState
	resolveCurrentState = fn
	t.Cleanup(func() { resolveCurrentState = old })
}

func TestCurrentState_PaneAliveOverDone(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create meta with window (pane assumed alive)
	if err := home.WriteMeta(tmp, "t1", map[string]string{
		"window":   "@win",
		"worktree": "/tmp/wt",
		"project":  "munsu",
		"kind":     "ship",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Status says 'done' but we wire a resolver that reports pane alive.
	if err := home.AppendStatus(tmp, "t1", "done: build complete"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}

	withResolver(t, func(homeDir, id string) (*CurrentStateInfo, error) {
		return &CurrentStateInfo{
			State:       "alive",
			Description: "pane is alive",
		}, nil
	})

	snap, err := Snapshot(tmp)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]
	if ts.CurrentState != "alive" {
		t.Errorf("CurrentState = %q, want 'alive' (pane alive overrides stale done status)", ts.CurrentState)
	}
	if ts.CurrentDescription != "pane is alive" {
		t.Errorf("CurrentDescription = %q, want 'pane is alive'", ts.CurrentDescription)
	}
}

func TestCurrentState_NoMistakesOverridesBlocked(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create meta with window and worktree
	if err := home.WriteMeta(tmp, "t1", map[string]string{
		"window":   "@win",
		"worktree": "/tmp/wt",
		"project":  "munsu",
		"kind":     "ship",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Status says 'blocked' but no-mistakes run-step is active.
	if err := home.AppendStatus(tmp, "t1", "blocked: waiting for review"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}

	withResolver(t, func(homeDir, id string) (*CurrentStateInfo, error) {
		return &CurrentStateInfo{
			State:               "working",
			Description:         "no-mistakes: fixing",
			NoMistakesRunStep:   "fixing",
			StatusLogSuperseded: true,
		}, nil
	})

	snap, err := Snapshot(tmp)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]
	if ts.CurrentState != "working" {
		t.Errorf("CurrentState = %q, want 'working' (no-mistakes run-step overrides blocked status)", ts.CurrentState)
	}
	if ts.NoMistakesRunStep != "fixing" {
		t.Errorf("NoMistakesRunStep = %q, want 'fixing'", ts.NoMistakesRunStep)
	}
	if !ts.StatusLogSuperseded {
		t.Errorf("StatusLogSuperseded = false, want true")
	}
}

func TestCurrentState_ResolvedNotCurrentStatus(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Create meta with window
	if err := home.WriteMeta(tmp, "t1", map[string]string{
		"window":   "@win",
		"worktree": "/tmp/wt",
		"project":  "munsu",
		"kind":     "ship",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Last line is 'resolved' which must not appear as current state.
	if err := home.AppendStatus(tmp, "t1", "working [key=phase1]: initial work"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	if err := home.AppendStatus(tmp, "t1", "resolved [key=phase1]: initial work complete"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}

	// No resolver wired — uses fallback CurrentState().
	snap, err := Snapshot(tmp)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]

	// 'resolved' is not a recognized current-state verb, so fallback should keep
	// the meta-derived phase (alive) rather than showing 'resolved'.
	if ts.CurrentState == "resolved" {
		t.Errorf("CurrentState = 'resolved', want something else (resolved is not current state)")
	}

	// OpenActivities should have no open activities since resolved closed the key.
	if len(ts.OpenActivities) != 0 {
		t.Errorf("OpenActivities = %d entries, want 0 (resolved closed the key)", len(ts.OpenActivities))
	}
}

func TestSnapshot_IncludesCaptainHomeTasks(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.MkdirAll(filepath.Join(parent, "data"), 0755)

	// primary captain meta only
	if err := home.WriteMeta(parent, "captain:munsu", map[string]string{
		"kind": "captain", "window": "w1",
	}); err != nil {
		t.Fatal(err)
	}

	capHome := filepath.Join(parent, "captains", "munsu")
	os.MkdirAll(filepath.Join(capHome, "state"), 0755)
	if err := home.WriteMeta(capHome, "ship-child", map[string]string{
		"kind": "ship", "project": "munsu", "window": "w-child",
	}); err != nil {
		t.Fatal(err)
	}
	reg := "- munsu - (home: " + capHome + "; scope: ; projects: ; added: 2026-07-20)\n"
	if err := os.WriteFile(filepath.Join(parent, "data", "captains.md"), []byte(reg), 0644); err != nil {
		t.Fatal(err)
	}

	snap, err := Snapshot(parent)
	if err != nil {
		t.Fatal(err)
	}
	var foundChild, foundCaptain bool
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
		if ts.ID == "captain:munsu" {
			foundCaptain = true
			if ts.Source != "primary" {
				t.Fatalf("captain source = %q", ts.Source)
			}
		}
	}
	if !foundChild {
		t.Fatal("expected ship-child from captain home in snapshot")
	}
	if !foundCaptain {
		t.Fatal("expected captain:munsu from primary")
	}
}

func TestSnapshot_PaneAliveProbeTrue(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	if err := home.WriteMeta(tmp, "t1", map[string]string{
		"window":   "@win",
		"worktree": "/tmp/wt",
		"project":  "munsu",
		"kind":     "ship",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	withPaneAlive(t, func(parentHome string, meta map[string]string) (bool, error) {
		return true, nil
	})

	snap, err := Snapshot(tmp)
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
}

func TestSnapshot_PaneAliveProbeFalse(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	if err := home.WriteMeta(tmp, "t1", map[string]string{
		"window":   "@win",
		"worktree": "/tmp/wt",
		"project":  "munsu",
		"kind":     "ship",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	withPaneAlive(t, func(parentHome string, meta map[string]string) (bool, error) {
		return false, nil
	})

	snap, err := Snapshot(tmp)
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
	if ts.PaneAliveUnknown {
		t.Errorf("PaneAliveUnknown = true, want false")
	}
}

func TestSnapshot_PaneAliveUnknownWhenNoProbe(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	if err := home.WriteMeta(tmp, "t1", map[string]string{
		"window":   "@win",
		"worktree": "/tmp/wt",
		"project":  "munsu",
		"kind":     "ship",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// No probe wired — paneAliveForCaptain is nil
	snap, err := Snapshot(tmp)
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
