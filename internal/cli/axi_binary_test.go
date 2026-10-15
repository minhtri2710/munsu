//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

	binPath := filepath.Join(axiBuildDir, "munsu")
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

	if _, err := orchestrator.StopWatcher(home); err != nil {
		t.Errorf("stop test watcher: %v", err)
	}
	if pid <= 0 {
		return
	}

	dead := func() bool {
		if !testutil.IsProcessAlive(pid) {
			return true
		}
		out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
		return err != nil || strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
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
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	cleanupTestWatcher(t, home, cmd.Process.Pid)
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected terminated helper process")
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
