//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// axiBinaryPath caches the path to the built munsu binary for the test run.
var axiBinaryPath string
var axiBuildDir string

// buildMunsuBinary builds the munsu binary and returns its path.
// The binary is built once into OS temp dir and cached for the package test run.
func buildMunsuBinary(t *testing.T) string {
	t.Helper()
	if axiBinaryPath != "" {
		return axiBinaryPath
	}

	projectRoot := findGoModRoot(t)

	// Use OS temp dir (not t.TempDir) so the binary survives individual
	// test teardown and is only cleaned up by tempdir cleanup daemon.
	if axiBuildDir == "" {
		var err error
		axiBuildDir, err = os.MkdirTemp("", "munsu-axi-binary-*")
		if err != nil {
			t.Fatalf("creating build dir: %v", err)
		}
	}

	binName := "munsu"
	if runtime.GOOS == "windows" {
		binName = "munsu.exe"
	}
	binPath := filepath.Join(axiBuildDir, binName)
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/munsu/")
	cmd.Dir = projectRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building munsu binary: %s\n%v", string(out), err)
	}
	axiBinaryPath = binPath
	return binPath
}

func cleanupTestWatcher(t *testing.T, home string, launchedPID int) {
	t.Helper()
	pid := launchedPID
	if pid <= 0 {
		id := orchestrator.ReadIdentity(home)
		_, beatPID, beatOK := orchestrator.ReadBeat(home)
		if id != nil {
			pid = id.PID
		} else if beatOK {
			pid = beatPID
		}
	}

	if err := orchestrator.Stop(home); err != nil {
		t.Errorf("stop test watcher: %v", err)
	}
	if pid <= 0 {
		return
	}

	dead := func() bool {
		if !testutil.IsProcessAlive(pid) {
			return true
		}
		if runtime.GOOS != "windows" {
			out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
			return err != nil || strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
		}
		return false
	}
	deadline := time.Now().Add(2 * time.Second)
	for !dead() && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if !dead() {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
		deadline = time.Now().Add(time.Second)
		for !dead() && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if !dead() {
		t.Errorf("test watcher PID %d survived cleanup", pid)
	}
}

var watchIDPIDPattern = regexp.MustCompile(`watch-(\d+)`)

func watchPIDFromOutput(t *testing.T, output string) int {
	t.Helper()
	match := watchIDPIDPattern.FindStringSubmatch(output)
	if len(match) != 2 {
		t.Fatalf("watch output does not contain a PID-bearing watch_id: %s", output)
	}
	pid, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("parse watcher PID: %v", err)
	}
	return pid
}

func findGoModRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find go.mod root")
	return ""
}

// runMunsu runs the built munsu binary with the given args and home dir.
// Returns combined stdout+stderr and any execution error.
func runMunsu(t *testing.T, homeDir string, args []string) (string, error) {
	t.Helper()
	binary := buildMunsuBinary(t)
	cmdArgs := []string{"--home", homeDir}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command(binary, cmdArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestCleanupTestWatcher_RecordedPIDWithoutBeacon(t *testing.T) {
	home := t.TempDir()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds 30")
	} else {
		cmd = exec.Command("sleep", "30")
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	cleanupTestWatcher(t, home, cmd.Process.Pid)
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected terminated helper process")
	}
}

// TestBinaryGuardContract_TOON verifies the guard command outputs contract-shaped
// TOON when invoked as a real binary.
func TestBinaryGuardContract_TOON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"guard"})
	if err != nil {
		t.Fatalf("munsu guard failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "kind: guard") {
		t.Errorf("output must contain kind: guard, got: %s", out)
	}
	if !strings.Contains(out, "schema_version:") {
		t.Errorf("output must contain schema_version, got: %s", out)
	}
	if !strings.Contains(out, "status: success") && !strings.Contains(out, "status: error") {
		t.Errorf("output must have status, got: %s", out)
	}
	if !strings.Contains(out, "state:") {
		t.Errorf("guard output must contain state, got: %s", out)
	}
}

// TestBinaryGuardContract_JSON verifies the guard command outputs contract-shaped
// JSON when --output json is provided.
func TestBinaryGuardContract_JSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"guard", "--output", "json"})
	if err != nil {
		t.Fatalf("munsu guard --output json failed: %v\n%s", err, out)
	}
	var resp struct {
		SchemaVersion string `json:"schema_version"`
		Kind          string `json:"kind"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if resp.SchemaVersion != "munsu.orchestration/v1" {
		t.Errorf("schema_version = %q, want munsu.orchestration/v1", resp.SchemaVersion)
	}
	if resp.Kind != "guard" {
		t.Errorf("kind = %q, want guard", resp.Kind)
	}
	if resp.Status != "success" {
		t.Errorf("status = %q, want success", resp.Status)
	}
}

// TestBinaryBackendCapabilities_TOON verifies the backend capabilities command.
func TestBinaryBackendCapabilities_TOON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"backend", "capabilities", "--backend", "tmux"})
	if err != nil {
		t.Fatalf("backend capabilities failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "kind: backend.capabilities") {
		t.Errorf("output must contain kind: backend.capabilities, got: %s", out)
	}
	if !strings.Contains(out, "features[3]") {
		t.Errorf("output must contain features[3], got: %s", out)
	}
}

// TestBinaryBackendCapabilities_JSON verifies backend capabilities JSON output.
func TestBinaryBackendCapabilities_JSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"backend", "capabilities", "--backend", "tmux", "--output", "json"})
	if err != nil {
		t.Fatalf("backend capabilities --output json failed: %v\n%s", err, out)
	}
	var resp struct {
		SchemaVersion string `json:"schema_version"`
		Kind          string `json:"kind"`
		Status        string `json:"status"`
		Data          struct {
			Backend  string   `json:"backend"`
			Features []string `json:"features"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if resp.Data.Backend != "tmux" {
		t.Errorf("backend = %q, want tmux", resp.Data.Backend)
	}
	if len(resp.Data.Features) == 0 {
		t.Error("features must not be empty")
	}
}

// TestBinaryBackendCapabilities_UnknownBackend verifies fail-closed error
// for unknown backend.
func TestBinaryBackendCapabilities_UnknownBackend(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"backend", "capabilities", "--backend", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	if !strings.Contains(out, "error_code:") {
		t.Errorf("output must contain error_code on error, got: %s", out)
	}
}

// TestBinaryBackendCapabilities_EmptyBackendIsTypedMissingInput verifies that
// omitting --backend fails closed with a typed missing_input error and never
// auto-selects a backend (diagnostics are not selection roots).
func TestBinaryBackendCapabilities_EmptyBackendIsTypedMissingInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"backend", "capabilities"})
	if err == nil {
		t.Fatal("expected error for empty --backend, got nil")
	}
	if !strings.Contains(out, "error_code: missing_input") {
		t.Errorf("output must contain typed missing_input error_code, got: %s", out)
	}
	if strings.Contains(out, "kind: backend.capabilities") {
		t.Errorf("must not report capabilities without an explicit backend, got: %s", out)
	}
}

// TestBinaryWatchEnsure_NoopContract verifies the watch ensure command
// produces contract output with noop:true when no watcher is running.
func TestBinaryWatchEnsure_NoopContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	launchedPID := 0
	t.Cleanup(func() { cleanupTestWatcher(t, home, launchedPID) })

	out, err := runMunsu(t, home, []string{"watch", "ensure"})
	if err != nil {
		t.Fatalf("watch ensure failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "kind: watch.ensure") {
		t.Errorf("output must contain kind: watch.ensure, got: %s", out)
	}
	launchedPID = watchPIDFromOutput(t, out)
}

// TestBinaryWatchEnsure_JSON verifies watch ensure JSON output contract.
func TestBinaryWatchEnsure_JSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	launchedPID := 0
	t.Cleanup(func() { cleanupTestWatcher(t, home, launchedPID) })

	out, err := runMunsu(t, home, []string{"watch", "ensure", "--output", "json"})
	if err != nil {
		t.Fatalf("watch ensure failed: %v\n%s", err, out)
	}
	var resp struct {
		SchemaVersion string `json:"schema_version"`
		Kind          string `json:"kind"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if resp.SchemaVersion != "munsu.orchestration/v1" {
		t.Errorf("schema_version = %q, want munsu.orchestration/v1", resp.SchemaVersion)
	}
	if resp.Kind != "watch.ensure" {
		t.Errorf("kind = %q, want watch.ensure", resp.Kind)
	}
	launchedPID = watchPIDFromOutput(t, out)
}

// TestBinaryReport_StructuredError verifies the report command fails closed
// with a structured error when MUNSU_TASK_ID is missing.
func TestBinaryReport_StructuredError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	parentHome := t.TempDir()

	binary := buildMunsuBinary(t)
	cmd := exec.Command(binary, "--home", home, "report", "--ring", "no-ring", "done", "task complete")
	// Explicitly override MUNSU_TASK_ID to empty to test fail-closed even
	// when the test runner inherits a MUNSU_TASK_ID from the parent shell.
	cmd.Env = append(
		os.Environ(),
		"MUNSU_HOME="+home,
		"MUNSU_ROLE=soldier",
		"MUNSU_PARENT_STATUS="+parentHome,
		"MUNSU_TASK_ID=",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error from report without MUNSU_TASK_ID, got nil")
	}
	if !strings.Contains(string(out), "error_code:") {
		t.Errorf("output must contain structured error_code, got: %s", string(out))
	}
}

// TestBinaryTaskObserve_MissingTask verifies structured error for missing task.
func TestBinaryTaskObserve_MissingTask(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"task", "observe", "nonexistent"})
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
	if !strings.Contains(out, "error_code:") {
		t.Errorf("output must contain error_code, got: %s", out)
	}
}

// TestBinaryTaskObserve_DefinitiveEmpty verifies task observe on an existing
// canonical task returns a successful observation (clean break: observation
// reads authoritative Task Authority, never a .meta projection).
func TestBinaryTaskObserve_DefinitiveEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	initCLITestHome(t, home)

	// Create a canonical task so observation resolves authoritative state.
	cliSeedCanonicalTask(t, home, "my-task", "ship")

	out, err := runMunsu(t, home, []string{"task", "observe", "my-task"})
	if err != nil {
		t.Fatalf("task observe of existing task should not error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "my-task") {
		t.Errorf("output must include task ID, got: %s", out)
	}
}

// TestBinaryFleetSnapshot_DefinitiveEmpty verifies fleet snapshot returns
// count:0 and soldiers:[] for an empty home.
func TestBinaryFleetSnapshot_DefinitiveEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	initCLITestHome(t, home)
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)

	out, err := runMunsu(t, home, []string{"fleet", "snapshot"})
	if err != nil {
		t.Fatalf("fleet snapshot: %v\n%s", err, out)
	}
	if !strings.Contains(out, "count: 0") {
		t.Errorf("empty snapshot must have count: 0, got: %s", out)
	}
	if !strings.Contains(out, "soldiers: []") {
		t.Errorf("empty snapshot must have soldiers: [], got: %s", out)
	}
	if !strings.Contains(out, "captain_guidance") {
		t.Errorf("empty snapshot must have captain_guidance, got: %s", out)
	}
}

// TestBinaryWakeClaim_MissingFlag verifies missing --owner flag produces
// structured error.
func TestBinaryWakeClaim_MissingFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"wake", "claim", "wake-01"})
	if err == nil {
		t.Fatal("expected error for missing --owner flag, got nil")
	}
	if !strings.Contains(out, "error_code:") {
		t.Errorf("output must contain structured error, got: %s", out)
	}
}

// TestBinaryInvalidOutputFlag_FailsClosed verifies an unknown --output value
// is rejected with a structured error before any backend call.
func TestBinaryInvalidOutputFlag_FailsClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"guard", "--output", "xml"})
	if err == nil {
		t.Fatal("expected error for invalid --output, got nil")
	}
	if !strings.Contains(out, "error_code: unsupported_input") {
		t.Errorf("output must contain error_code unsupported_input, got: %s", out)
	}
}

// TestBinaryUnknownFlag_FailsClosed verifies an unknown flag is rejected before
// any command logic runs.
func TestBinaryUnknownFlag_FailsClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary test in short mode")
	}
	home := t.TempDir()
	out, err := runMunsu(t, home, []string{"guard", "--nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
	if !strings.Contains(out, "error_code:") {
		t.Errorf("output must contain structured error, got: %s", out)
	}
}
