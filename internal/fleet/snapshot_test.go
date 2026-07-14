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
}
