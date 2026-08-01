package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

// runRoot executes a munsu root command against a fresh command tree,
// mirroring runContract but available without the integration build tag.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return strings.TrimSpace(buf.String()), err
}

func enqueueN(t *testing.T, homeDir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := home.EnqueueWake(homeDir, "signal", "task", fmt.Sprintf("payload-%d", i)); err != nil {
			t.Fatalf("enqueue wake %d: %v", i, err)
		}
	}
}

func wakeDrainData(t *testing.T, args ...string) (map[string]any, string) {
	t.Helper()
	full := append([]string{"wake", "drain"}, args...)
	out, err := runRoot(t, full...)
	if err != nil {
		t.Fatalf("runRoot(%v): %v\n%s", full, err, out)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("wake drain output is not JSON: %v\n%s", err, out)
	}
	data, _ := envelope["data"].(map[string]any)
	return data, out
}

func leaseEntries(t *testing.T, homeDir string) int {
	t.Helper()
	entries, err := readLeaseDir(homeDir)
	if err != nil {
		t.Fatalf("reading lease dir: %v", err)
	}
	return len(entries)
}

func readLeaseDir(homeDir string) ([]string, error) {
	entries, err := os.ReadDir(home.LeaseDir(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func TestWakeDrainEmptyQueueContract(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)

	data, out := wakeDrainData(t, "--output", "json")
	if data["state"] != "empty" {
		t.Errorf("state = %v, want empty\n%s", data["state"], out)
	}
	if data["drained"] != float64(0) {
		t.Errorf("drained = %v, want 0\n%s", data["drained"], out)
	}
	if n := leaseEntries(t, homeDir); n != 0 {
		t.Errorf("empty drain created %d lease files, want 0", n)
	}
}

func TestWakeDrainDrainsAllAndSurfacesEvidence(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	enqueueN(t, homeDir, 2)
	if err := home.EnqueueWake(homeDir, "signal", "task-a", "done: shipped"); err != nil {
		t.Fatal(err)
	}

	data, out := wakeDrainData(t, "--output", "json")
	if data["state"] != "drained" {
		t.Errorf("state = %v, want drained\n%s", data["state"], out)
	}
	if data["drained"] != float64(3) {
		t.Errorf("drained = %v, want 3\n%s", data["drained"], out)
	}
	records, _ := data["records"].([]any)
	if len(records) != 3 {
		t.Fatalf("records len = %d, want 3\n%s", len(records), out)
	}
	// Claimed material evidence must be surfaced, never swallowed.
	evidenceFound := false
	for _, r := range records {
		row, _ := r.(map[string]any)
		if row["payload"] == "done: shipped" {
			evidenceFound = true
		}
	}
	if !evidenceFound {
		t.Errorf("drained records must surface the material payload\n%s", out)
	}
	if home.HasQueuedWakes(homeDir) {
		t.Error("wake queue still has wakes after full drain")
	}
	if n := leaseEntries(t, homeDir); n != 0 {
		t.Errorf("%d lease files remain after full drain, want 0", n)
	}
}

func TestWakeDrainLimitBounded(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	enqueueN(t, homeDir, 5)

	data, _ := wakeDrainData(t, "--limit", "2", "--output", "json")
	if data["drained"] != float64(2) {
		t.Errorf("drained = %v, want 2", data["drained"])
	}
	if data["remaining"] != float64(3) {
		t.Errorf("remaining = %v, want 3", data["remaining"])
	}

	data, _ = wakeDrainData(t, "--output", "json")
	if data["drained"] != float64(3) {
		t.Errorf("second drain drained = %v, want 3", data["drained"])
	}
	if data["remaining"] != float64(0) {
		t.Errorf("remaining after second drain = %v, want 0", data["remaining"])
	}
}

func TestWakeDrainDefaultConsumer(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	enqueueN(t, homeDir, 1)

	data, out := wakeDrainData(t, "--output", "json")
	if data["consumer"] != "drain" {
		t.Errorf("consumer = %v, want drain\n%s", data["consumer"], out)
	}
}

func TestWakeDrainReclaimsExpiredLeases(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	enqueueN(t, homeDir, 2)
	// An immediately-expired lease holds one of the wakes; drain must
	// reclaim it before claiming, so nothing is lost.
	claim, err := home.ClaimWakes(homeDir, "stale", 0, 1)
	if err != nil || len(claim.Wakes) != 1 {
		t.Fatalf("seeding expired lease: claim=%+v err=%v", claim, err)
	}

	data, out := wakeDrainData(t, "--output", "json")
	if data["drained"] != float64(2) {
		t.Errorf("drained = %v, want 2 (reclaimed + queued)\n%s", data["drained"], out)
	}
	if data["reclaimed"] != float64(1) {
		t.Errorf("reclaimed = %v, want 1\n%s", data["reclaimed"], out)
	}
	if n := leaseEntries(t, homeDir); n != 0 {
		t.Errorf("%d lease files remain after drain, want 0", n)
	}
}

func TestWakeDrainHiddenAliasRegisteredAndExecutes(t *testing.T) {
	root := NewRootCommand()
	var alias *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "wake-drain" {
			alias = c
		}
	}
	if alias == nil {
		t.Fatal("hidden wake-drain alias not registered on root")
	}
	if !alias.Hidden {
		t.Error("wake-drain alias must be hidden from help")
	}

	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	enqueueN(t, homeDir, 2)

	out, err := runRoot(t, "wake-drain", "--output", "json")
	if err != nil {
		t.Fatalf("wake-drain: %v\n%s", err, out)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("wake-drain output is not JSON: %v\n%s", err, out)
	}
	if envelope["kind"] != "wake.drain" {
		t.Errorf("kind = %v, want wake.drain\n%s", envelope["kind"], out)
	}
	data, _ := envelope["data"].(map[string]any)
	if data["drained"] != float64(2) {
		t.Errorf("drained = %v, want 2\n%s", data["drained"], out)
	}
}

func TestWakeDrainTOONOutput(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	enqueueN(t, homeDir, 1)

	out, err := runRoot(t, "wake", "drain")
	if err != nil {
		t.Fatalf("wake drain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "kind: wake.drain") {
		t.Errorf("TOON output missing kind: wake.drain\n%s", out)
	}
	if !strings.Contains(out, "records[1]{wake_id,kind,key,payload}") {
		t.Errorf("TOON output missing records table\n%s", out)
	}
}

// TestGuardRemediationCopyPasteExecutable proves the guard-emitted
// remediation (`drain with munsu wake-drain`) is executable end-to-end:
// the exact command named by the guard drains the queued wakes.
func TestGuardRemediationCopyPasteExecutable(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	enqueueN(t, homeDir, 2)

	out, err := runRoot(t, "guard", "--output", "json")
	if err != nil {
		t.Fatalf("guard: %v\n%s", err, out)
	}
	if !strings.Contains(out, "drain with munsu wake-drain") {
		t.Fatalf("guard must emit the wake-drain remediation\n%s", out)
	}

	drainOut, err := runRoot(t, "wake-drain", "--output", "json")
	if err != nil {
		t.Fatalf("guard remediation `munsu wake-drain` must run: %v\n%s", err, drainOut)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(drainOut), &envelope); err != nil {
		t.Fatalf("wake-drain output is not JSON: %v\n%s", err, drainOut)
	}
	data, _ := envelope["data"].(map[string]any)
	if data["drained"] != float64(2) {
		t.Errorf("drained = %v, want 2\n%s", data["drained"], drainOut)
	}
	if home.HasQueuedWakes(homeDir) {
		t.Error("wake queue still has wakes after running the guard remediation")
	}
}

// TestGuardAgedRemediationCopyPasteExecutable covers the aged-material
// path: the guard must still emit an executable drain remediation.
func TestGuardAgedRemediationCopyPasteExecutable(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	// A material wake with an old epoch makes the guard aged/unhealthy.
	oldEpoch := time.Now().Add(-10 * time.Minute).Unix()
	queuePath := home.WakeQueuePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(queuePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(queuePath, []byte(fmt.Sprintf("%d\t1-1\tsignal\ttask-a\tdone: shipped old evidence\n", oldEpoch)), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "guard", "--output", "json")
	if err != nil {
		t.Fatalf("guard: %v\n%s", err, out)
	}
	if !strings.Contains(out, "aged") || !strings.Contains(out, "drain with munsu wake-drain") {
		t.Fatalf("aged guard must emit an executable drain remediation\n%s", out)
	}

	drainOut, err := runRoot(t, "wake-drain", "--output", "json")
	if err != nil {
		t.Fatalf("aged remediation `munsu wake-drain` must run: %v\n%s", err, drainOut)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(drainOut), &envelope); err != nil {
		t.Fatalf("wake-drain output is not JSON: %v\n%s", err, drainOut)
	}
	data, _ := envelope["data"].(map[string]any)
	if data["drained"] != float64(1) || data["state"] != "drained" {
		t.Errorf("aged drain = %v, want drained 1\n%s", data, drainOut)
	}
}
