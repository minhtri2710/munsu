package fleet

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
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

	if !strings.Contains(out, "No in-flight tasks. Fleet is idle.") {
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
	if err := task.WriteMeta(tmpDir, "task-ship", shipMeta); err != nil {
		t.Fatalf("failed to write ship meta: %v", err)
	}
	if err := task.AppendStatus(tmpDir, "task-ship", "running builds"); err != nil {
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
	if err := task.WriteMeta(tmpDir, "task-scout", scoutMeta); err != nil {
		t.Fatalf("failed to write scout meta: %v", err)
	}
	if err := task.AppendStatus(tmpDir, "task-scout", "scouting around"); err != nil {
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
	if err := task.WriteMeta(tmpDir, "task-other", otherMeta); err != nil {
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
	if err := task.WriteMeta(tmp, "pre-spawn-task", preSpawnMeta); err != nil {
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

func TestCaptainStatus_Seeded(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(smHome, 0755)

	status := CaptainStatus(smHome)
	if status != "seeded" {
		t.Errorf("CaptainStatus = %q, want %q", status, "seeded")
	}
}

func TestCaptainStatus_Alive(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.WriteFile(filepath.Join(smHome, "state", ".lock"), []byte("999999\n"), 0644)

	status := CaptainStatus(smHome)
	if status != "alive" {
		t.Errorf("CaptainStatus = %q, want %q", status, "alive")
	}
}

func TestCaptainStatus_Dead(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "captains", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.WriteFile(filepath.Join(smHome, "state", ".lock"), []byte("invalid\n"), 0644)

	status := CaptainStatus(smHome)
	if status != "dead" {
		t.Errorf("CaptainStatus = %q, want %q", status, "dead")
	}
}

func TestCaptainStatus_Unknown(t *testing.T) {
	// Non-existent home should return unknown
	status := CaptainStatus("/nonexistent/sm")
	if status != "unknown" {
		t.Errorf("CaptainStatus = %q, want %q", status, "unknown")
	}
}
