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

// TestSupervisionBlockHeader checks the header line for every harness.
func TestSupervisionBlockHeader(t *testing.T) {
	harnesses := []string{"claude", "codex", "grok", "pi", "opencode", "unknown"}
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			output := captureStdout(func() {
				printSupervisionBlock(h, true)
			})
			if !strings.Contains(output, "primary harness: "+h) {
				t.Errorf("expected harness name %q in header, got: %s", h, output)
			}
			if !strings.Contains(output, "Drain:   munsu wake-drain") {
				t.Errorf("expected Drain line, got: %s", output)
			}
			if !strings.Contains(output, "Guard:   munsu guard") {
				t.Errorf("expected Guard line, got: %s", output)
			}
		})
	}
}

func TestSupervisionBlock_LockReadOnly(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock("claude", false)
	})
	if !strings.Contains(output, "read-only") {
		t.Errorf("expected read-only lock warning, got: %s", output)
	}
	if !strings.Contains(output, "do not drain, arm, or repair") {
		t.Errorf("expected drain/arm/repair warning, got: %s", output)
	}
}

func TestSupervisionBlock_LockAcquired(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock("codex", true)
	})
	if !strings.Contains(output, "owns normal supervision") {
		t.Errorf("expected 'owns normal supervision', got: %s", output)
	}
}

func TestSupervisionBlock_ClaudeKeywords(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock("claude", true)
	})
	if !strings.Contains(output, "background-notify") {
		t.Errorf("expected background-notify mode, got: %s", output)
	}
	if !strings.Contains(output, "munsu watch-arm") {
		t.Errorf("expected watch-arm reference, got: %s", output)
	}
	if !strings.Contains(output, "Never use shell") {
		t.Errorf("expected no-shell-& warning, got: %s", output)
	}
	if !strings.Contains(output, "--restart") {
		t.Errorf("expected --restart flag, got: %s", output)
	}
}

func TestSupervisionBlock_CodexKeywords(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock("codex", true)
	})
	if !strings.Contains(output, "foreground checkpoint") {
		t.Errorf("expected foreground checkpoint mode, got: %s", output)
	}
	if !strings.Contains(output, "munsu watch run") {
		t.Errorf("expected watch run reference, got: %s", output)
	}
	if !strings.Contains(output, "next checkpoint") {
		t.Errorf("expected next-checkpoint reference, got: %s", output)
	}
}

func TestSupervisionBlock_GrokKeywords(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock("grok", true)
	})
	if !strings.Contains(output, "background-notify") {
		t.Errorf("expected background-notify mode, got: %s", output)
	}
	if !strings.Contains(output, "run_terminal_command") {
		t.Errorf("expected run_terminal_command ref, got: %s", output)
	}
	if !strings.Contains(output, "background: true") {
		t.Errorf("expected background:true ref, got: %s", output)
	}
	if !strings.Contains(output, "Never use shell") {
		t.Errorf("expected no-shell-& warning, got: %s", output)
	}
}

func TestSupervisionBlock_PiKeywords(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock("pi", true)
	})
	if !strings.Contains(output, "extension background wake") {
		t.Errorf("expected extension background wake mode, got: %s", output)
	}
	if !strings.Contains(output, "fm_watch_arm_pi") {
		t.Errorf("expected fm_watch_arm_pi tool ref, got: %s", output)
	}
	if !strings.Contains(output, "Do NOT run") {
		t.Errorf("expected Do NOT run warning, got: %s", output)
	}
	if !strings.Contains(output, "Pi extension re-arms") {
		t.Errorf("expected Pi extension re-arm ref, got: %s", output)
	}
}

func TestSupervisionBlock_OpencodeKeywords(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock("opencode", true)
	})
	if !strings.Contains(output, "TUI plugin background wake") {
		t.Errorf("expected TUI plugin background wake mode, got: %s", output)
	}
	if !strings.Contains(output, "fm-primary-watch-arm.js") {
		t.Errorf("expected plugin file ref, got: %s", output)
	}
	if !strings.Contains(output, "arms after session goes idle") {
		t.Errorf("expected idle-arm ref, got: %s", output)
	}
	if !strings.Contains(output, "recovery probe") {
		t.Errorf("expected recovery probe ref, got: %s", output)
	}
}

func TestSupervisionBlock_DefaultKeywords(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock("unknown-harness", true)
	})
	if !strings.Contains(output, "generic fallback") {
		t.Errorf("expected generic fallback mode, got: %s", output)
	}
	if !strings.Contains(output, "munsu watch ensure") {
		t.Errorf("expected watch ensure ref, got: %s", output)
	}
	if !strings.Contains(output, "Never use shell") {
		t.Errorf("expected no-shell-& warning, got: %s", output)
	}
}

func TestSupervisionMode_AllKnown(t *testing.T) {
	harnesses := []string{"claude", "codex", "grok", "pi", "opencode"}
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			m := supervisionMode(h)
			if m == "generic fallback" {
				t.Errorf("supervisionMode(%q) returned generic fallback, expected a real mode", h)
			}
			if m == "" {
				t.Error("supervisionMode returned empty string")
			}
		})
	}
}

func TestSupervisionMode_Unknown(t *testing.T) {
	m := supervisionMode("nonexistent")
	if m != "generic fallback" {
		t.Errorf("supervisionMode('nonexistent') = %q, want 'generic fallback'", m)
	}
}
