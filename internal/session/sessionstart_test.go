package session

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckSessionScope_RefusesAmbientGateWithoutProjects(t *testing.T) {
	t.Setenv("NO_MISTAKES_GATE", "")
	if err := checkSessionScope(t.TempDir()); err == nil {
		t.Fatal("expected ambient gate refusal")
	}
}

func TestCheckSessionScope_UsesRegisteredProjectPath(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "demo")
	commonDir := filepath.Join(t.TempDir(), ".no-mistakes", "repos", "gate.git")
	if err := os.MkdirAll(filepath.Dir(commonDir), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "--bare", commonDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".git"), []byte("gitdir: "+commonDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	registry := "- demo - https://github.com/example/demo.git (added 2026-07-18)\n"
	if err := os.WriteFile(filepath.Join(home, "data", "projects.md"), []byte(registry), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NM_HOME", filepath.Dir(filepath.Dir(commonDir)))
	if err := checkSessionScope(home); err == nil {
		t.Fatal("expected registered gate checkout refusal")
	}
}

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
		printDataFile(os.Stdout, tmpDir, "captain.md")
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
		printDataFile(os.Stdout, tmpDir, "captain.md")
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
		printDataFile(os.Stdout, tmpDir, "long.md")
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
		printFleetState(os.Stdout, tmpDir)
	})

	if !strings.Contains(output, "(no in-flight tasks)") {
		t.Errorf("expected no-tasks message, got: %s", output)
	}
}

func TestPrintFleetState_NoStateDir(t *testing.T) {
	tmpDir := t.TempDir()

	output := captureStdout(func() {
		printFleetState(os.Stdout, tmpDir)
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
		printFleetState(os.Stdout, tmpDir)
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
		printFleetState(os.Stdout, tmpDir)
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
		printFleetState(os.Stdout, tmpDir)
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
				printSupervisionBlock(os.Stdout, h, true)
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
		printSupervisionBlock(os.Stdout, "claude", false)
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
		printSupervisionBlock(os.Stdout, "codex", true)
	})
	if !strings.Contains(output, "owns normal supervision") {
		t.Errorf("expected 'owns normal supervision', got: %s", output)
	}
}

func TestSupervisionBlock_UsesPersistentWatcherGuidance(t *testing.T) {
	for _, h := range []string{"claude", "codex", "grok", "pi", "opencode", "unknown"} {
		t.Run(h, func(t *testing.T) {
			output := captureStdout(func() {
				printSupervisionBlock(os.Stdout, h, true)
			})
			if !strings.Contains(output, "munsu watch ensure") {
				t.Errorf("expected watch ensure guidance, got: %s", output)
			}
			if !strings.Contains(output, "munsu watch run") {
				t.Errorf("expected one-cycle watch run guidance, got: %s", output)
			}
			for _, legacy := range []string{"fm_watch_arm_pi", "watch-arm", "re-arms automatically", "extension background wake"} {
				if strings.Contains(output, legacy) {
					t.Errorf("output contains legacy watcher guidance %q: %s", legacy, output)
				}
			}
		})
	}
}

func TestSupervisionMode_AllKnown(t *testing.T) {
	harnesses := []string{"claude", "codex", "grok", "pi", "opencode"}
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			if got := supervisionMode(h); got != "persistent daemon" {
				t.Errorf("supervisionMode(%q) = %q, want persistent daemon", h, got)
			}
		})
	}
}

func TestSupervisionMode_Unknown(t *testing.T) {
	if got := supervisionMode("nonexistent"); got != "persistent daemon" {
		t.Errorf("supervisionMode('nonexistent') = %q, want persistent daemon", got)
	}
}

func TestEnsureWatcherForSession_StartsOnlyForOwnedInFlightFleet(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.meta"), []byte("kind=ship\n"), 0644)

	calls := 0
	result := ensureWatcherForSession(tmp, true, func(home string) WatchEnsureResult {
		calls++
		return WatchEnsureResult{State: "started"}
	})
	if calls != 1 || result.State != "started" {
		t.Fatalf("calls=%d result=%+v, want one started ensure", calls, result)
	}

	calls = 0
	result = ensureWatcherForSession(tmp, false, func(home string) WatchEnsureResult {
		calls++
		return WatchEnsureResult{State: "started"}
	})
	if calls != 0 || result.State != "read-only" {
		t.Fatalf("read-only calls=%d result=%+v", calls, result)
	}
}

func TestEnsureWatcherForSession_IdleFleetDoesNotStart(t *testing.T) {
	tmp := t.TempDir()
	calls := 0
	result := ensureWatcherForSession(tmp, true, func(home string) WatchEnsureResult {
		calls++
		return WatchEnsureResult{State: "started"}
	})
	if calls != 0 || result.State != "idle" {
		t.Fatalf("calls=%d result=%+v, want idle no-op", calls, result)
	}
}

func TestEnsureWatcherForSession_HealthyWatcherIsReported(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.meta"), []byte("kind=scout\n"), 0644)

	result := ensureWatcherForSession(tmp, true, func(home string) WatchEnsureResult {
		return WatchEnsureResult{State: "healthy"}
	})
	if result.State != "healthy" {
		t.Fatalf("result=%+v, want healthy", result)
	}
}
