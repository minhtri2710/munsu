package session

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs f and returns everything written to stdout.
func captureStdout(f func()) string {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	os.Stdout = w

	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		outC <- buf.String()
	}()

	f()
	_ = w.Close()
	os.Stdout = old
	return <-outC
}

// TestPrintDataFile tests the printDataFile helper.
func TestPrintDataFile_ShowsContent(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "captain.md"), []byte("captain: jdoe\nfocus: refactor\n"), 0644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printDataFile(tmpDir, "captain.md")
	})

	if !strings.Contains(output, "=== data/captain.md ===") {
		t.Errorf("expected header, got: %s", output)
	}
	if !strings.Contains(output, "captain: jdoe") {
		t.Errorf("expected file content, got: %s", output)
	}
}

func TestPrintDataFile_ShowsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printDataFile(tmpDir, "captain.md")
	})

	if !strings.Contains(output, "ABSENT") || !strings.Contains(output, "captain.md") {
		t.Errorf("expected ABSENT marker, got: %s", output)
	}
}

func TestPrintDataFile_TruncatesLongFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}

	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(filepath.Join(dataDir, "long.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printDataFile(tmpDir, "long.md")
	})

	if !strings.Contains(output, "...(truncated)") {
		t.Errorf("expected truncation marker, got: %s", output)
	}
	// Should print 20 lines plus header line
	gotLines := strings.Split(strings.TrimSpace(output), "\n")
	if len(gotLines) < 21 || len(gotLines) > 23 {
		t.Errorf("expected ~21-22 lines (20 content + header + optional truncation), got %d", len(gotLines))
	}
}

// TestPrintFleetState tests the printFleetState helper.
func TestPrintFleetState_NoTasks(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printFleetState(tmpDir)
	})

	if !strings.Contains(output, "(no in-flight tasks)") {
		t.Errorf("expected no-tasks message, got: %s", output)
	}
}

func TestPrintFleetState_NoStateDir(t *testing.T) {
	tmpDir := t.TempDir()

	output := captureStdout(func() {
		printFleetState(tmpDir)
	})

	if !strings.Contains(output, "(no in-flight tasks)") {
		t.Errorf("expected no-tasks message, got: %s", output)
	}
}

func TestPrintFleetState_ShowsTasks(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a task meta file
	taskID := "task-abc-123"
	metaContent := "window=@42\nkind=deploy\n"
	if err := os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a task status file
	statusContent := "working\nprocessed event\n"
	if err := os.WriteFile(filepath.Join(stateDir, taskID+".status"), []byte(statusContent), 0644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printFleetState(tmpDir)
	})

	// Should show the task with status
	if !strings.Contains(output, taskID) {
		t.Errorf("expected task ID in output, got: %s", output)
	}
	// Status line shows last status entry
	if !strings.Contains(output, "processed event") {
		t.Errorf("expected last status line, got: %s", output)
	}
}

func TestPrintFleetState_IgnoresNonMetaFiles(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a non-meta file that should be ignored
	if err := os.WriteFile(filepath.Join(stateDir, ".lock"), []byte("some data\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a status file without a corresponding meta (should be ignored by the loop)
	if err := os.WriteFile(filepath.Join(stateDir, "orphan.status"), []byte("working\n"), 0644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printFleetState(tmpDir)
	})

	if !strings.Contains(output, "(no in-flight tasks)") {
		t.Errorf("expected no-tasks message when only non-meta files exist, got: %s", output)
	}
}

// TestPrintFleetState_TaskNoStatus tests that printFleetState shows a task with empty status.
func TestPrintFleetState_TaskNoStatus(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	taskID := "task-no-status"
	metaContent := "window=@42\nkind=deploy\n"
	if err := os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printFleetState(tmpDir)
	})

	if !strings.Contains(output, taskID) {
		t.Errorf("expected task ID in output, got: %s", output)
	}
	if !strings.Contains(output, "no status") {
		t.Errorf("expected 'no status' fallback, got: %s", output)
	}
	if !strings.Contains(output, "alive") {
		t.Errorf("expected 'alive' for window=@42 (Snapshot heuristic sets PaneAlive), got: %s", output)
	}
}
