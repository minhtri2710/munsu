package orchestrator

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureStdout runs f and returns everything written to stdout.
func afkCaptureStdout(f func()) string {
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

// --- IsActive tests ---

func TestIsActive_FlagNotSet(t *testing.T) {
	tmp := t.TempDir()
	if IsActive(tmp) {
		t.Error("IsActive() = true for empty home, want false")
	}
}

func TestIsActive_FlagSet(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	if err := os.WriteFile(flagPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsActive(tmp) {
		t.Error("IsActive() = false when flag file exists, want true")
	}
}

func TestIsActive_FlagRemoved(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	if err := os.WriteFile(flagPath, []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsActive(tmp) {
		t.Fatal("IsActive() = false when flag file exists")
	}
	os.Remove(flagPath)
	if IsActive(tmp) {
		t.Error("IsActive() = true after flag removed, want false")
	}
}

// --- scanStatusFiles tests ---

func TestScanStatusFiles_NoStateDir(t *testing.T) {
	tmp := t.TempDir()
	// Should not panic
	scanStatusFiles(tmp)
}

func TestScanStatusFiles_EmptyStateDir(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)
	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if output != "" {
		t.Errorf("expected no output for empty state dir, got %q", output)
	}
}

func TestScanStatusFiles_IgnoresNonStatusFiles(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	// Non-status files should be ignored
	os.WriteFile(filepath.Join(stateDir, ".lock"), []byte("some data\n"), 0644)
	os.WriteFile(filepath.Join(stateDir, ".hidden.status"), []byte("done: hidden\n"), 0644)
	os.WriteFile(filepath.Join(stateDir, "task-1.meta"), []byte("window=@0\n"), 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if output != "" {
		t.Errorf("expected no output for non-status files, got %q", output)
	}
}

func TestScanStatusFiles_DetectsDoneEvent(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: doing stuff\ndone: PR #42 checks green\n"), 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if !strings.Contains(output, "done:") || !strings.Contains(output, "task-1.status") {
		t.Errorf("expected done event in output, got %q", output)
	}
}

func TestScanStatusFiles_DetectsFailedEvent(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: doing stuff\nfailed: build broken\n"), 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if !strings.Contains(output, "failed:") || !strings.Contains(output, "task-1.status") {
		t.Errorf("expected failed event in output, got %q", output)
	}
}

func TestScanStatusFiles_DetectsNeedsDecisionEvent(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: need input\nneeds-decision: which branch to target\n"), 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if !strings.Contains(output, "needs-decision:") || !strings.Contains(output, "task-1.status") {
		t.Errorf("expected needs-decision event in output, got %q", output)
	}
}

func TestScanStatusFiles_IgnoresWorkingStatus(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: in progress\nworking: still working\n"), 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if output != "" {
		t.Errorf("expected no output for working status, got %q", output)
	}
}

func TestScanStatusFiles_MultipleTasks(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: in progress\ndone: completed\n"), 0644)
	os.WriteFile(filepath.Join(stateDir, "task-2.status"), []byte("working: ongoing\n"), 0644)
	os.WriteFile(filepath.Join(stateDir, "task-3.status"), []byte("working: stuck\nneeds-decision: what next\n"), 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if !strings.Contains(output, "done:") {
		t.Errorf("expected done event, got %q", output)
	}
	if !strings.Contains(output, "needs-decision:") {
		t.Errorf("expected needs-decision event, got %q", output)
	}
	if !strings.Contains(output, "task-1.status") {
		t.Errorf("expected task-1 reference, got %q", output)
	}
	if !strings.Contains(output, "task-3.status") {
		t.Errorf("expected task-3 reference, got %q", output)
	}
	// Should NOT contain task-2 (working only)
	if strings.Contains(output, "task-2") {
		t.Errorf("task-2 should not appear in output (no general-relevant events), got %q", output)
	}
}

func TestScanStatusFiles_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte{}, 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if output != "" {
		t.Errorf("expected no output for empty status file, got %q", output)
	}
}

func TestScanStatusFiles_OnlyWhitespace(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("  \n\n  \n"), 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if output != "" {
		t.Errorf("expected no output for whitespace-only file, got %q", output)
	}
}

func TestScanStatusFiles_DoneAtFirstLine(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	// Event on the last line — that's what scanStatusFiles checks
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("done: first action\n"), 0644)

	output := afkCaptureStdout(func() {
		scanStatusFiles(tmp)
	})
	if !strings.Contains(output, "done:") {
		t.Errorf("expected done event on single-line file, got %q", output)
	}
}

// --- Status and Disable tests ---

func TestStatus_NoFlagFile(t *testing.T) {
	tmp := t.TempDir()
	active, startedAt, err := Status(tmp)
	if err != nil {
		t.Fatalf("Status() with no flag: unexpected error: %v", err)
	}
	if active {
		t.Error("Status() with no flag: active=true, want false")
	}
	if startedAt != "" {
		t.Errorf("Status() with no flag: startedAt=%q, want empty", startedAt)
	}
}

func TestStatus_WithFlag(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	ts := "2024-06-01T12:00:00Z"
	if err := os.WriteFile(flagPath, []byte(ts+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	active, startedAt, err := Status(tmp)
	if err != nil {
		t.Fatalf("Status() with flag: unexpected error: %v", err)
	}
	if !active {
		t.Error("Status() with flag: active=false, want true")
	}
	if startedAt != ts {
		t.Errorf("Status() with flag: startedAt=%q, want %q", startedAt, ts)
	}
}

func TestDisable_RemovesFlag(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, afkFlagFile)
	os.MkdirAll(filepath.Dir(flagPath), 0755)
	if err := os.WriteFile(flagPath, []byte("2024-01-01T00:00:00Z\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsActive(tmp) {
		t.Fatal("IsActive() should be true before Disable()")
	}
	if err := Disable(tmp); err != nil {
		t.Fatalf("Disable() error: %v", err)
	}
	if IsActive(tmp) {
		t.Error("IsActive() = true after Disable(), want false")
	}
}

func TestDisable_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	// Disable() on an already-clean home should not error
	if err := Disable(tmp); err != nil {
		t.Fatalf("Disable() on empty home: unexpected error: %v", err)
	}
	// Repeated calls should also succeed
	if err := Disable(tmp); err != nil {
		t.Fatalf("Disable() captain call: unexpected error: %v", err)
	}
}

// --- Dedup + wake-queue tests ---

func TestScanStatusFiles_DedupSkipsRepeat(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Write a done status
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("done: PR merged\n"), 0644)

	// Reset seen set for isolated test
	seenMu.Lock()
	seenLines = make(map[string]string)
	seenMu.Unlock()

	// First call: should escalate
	firstOut := afkCaptureStdout(func() { scanStatusFiles(tmp) })
	if !strings.Contains(firstOut, "done:") {
		t.Fatalf("expected first call to escalate 'done:', got %q", firstOut)
	}

	// Verify wake-queue was written by the first call
	wakePath := filepath.Join(tmp, "state", ".wake-queue")
	data, err := os.ReadFile(wakePath)
	if err != nil {
		t.Fatalf("expected wake-queue file after escalation: %v", err)
	}
	if !strings.Contains(string(data), "task-1") {
		t.Errorf("expected wake-queue to reference task-1, got: %s", string(data))
	}
	if !strings.Contains(string(data), "PR merged") {
		t.Errorf("expected wake-queue to contain 'PR merged', got: %s", string(data))
	}

	// Captain call: same content, should be suppressed
	captainOut := afkCaptureStdout(func() { scanStatusFiles(tmp) })
	if captainOut != "" {
		t.Errorf("expected no output on repeat, got %q", captainOut)
	}
}

func TestScanStatusFiles_EscalatesOnChange(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	// Write initial done status and simulate first poll
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("done: build green\n"), 0644)

	seenMu.Lock()
	seenLines = make(map[string]string)
	seenMu.Unlock()

	// First call: escalate
	firstOut := afkCaptureStdout(func() { scanStatusFiles(tmp) })
	if !strings.Contains(firstOut, "done:") {
		t.Fatalf("expected first call to escalate 'done:', got %q", firstOut)
	}

	// Change the status line to something new
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("done: build green\nfailed: test failure\n"), 0644)

	// Captain call: line changed, should escalate again
	captainOut := afkCaptureStdout(func() { scanStatusFiles(tmp) })
	if !strings.Contains(captainOut, "failed:") {
		t.Errorf("expected escalation on line change, got %q", captainOut)
	}
}

func TestScanStatusFiles_WakeQueueAppendFormat(t *testing.T) {
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)

	os.WriteFile(filepath.Join(stateDir, "task-99.status"), []byte("needs-decision: which target branch\n"), 0644)

	seenMu.Lock()
	seenLines = make(map[string]string)
	seenMu.Unlock()

	scanStatusFiles(tmp)

	wakePath := filepath.Join(tmp, "state", ".wake-queue")
	data, err := os.ReadFile(wakePath)
	if err != nil {
		t.Fatalf("expected wake-queue file: %v", err)
	}

	// Format: epoch\tseq\tkind\tkey\tpayload\n
	line := strings.TrimSpace(string(data))
	parts := strings.SplitN(line, "\t", 5)
	if len(parts) != 5 {
		t.Fatalf("expected 5 tab-separated fields, got %d: %q", len(parts), line)
	}

	if parts[2] != "afk" {
		t.Errorf("expected kind 'afk', got %q", parts[2])
	}
	if parts[3] != "task-99" {
		t.Errorf("expected key 'task-99', got %q", parts[3])
	}
	if parts[4] != "which target branch" {
		t.Errorf("expected payload 'which target branch', got %q", parts[4])
	}
}
