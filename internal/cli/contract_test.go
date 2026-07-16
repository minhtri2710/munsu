package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
)

func TestCapabilitiesContractOutputsTOONAndJSON(t *testing.T) {
	toon, err := runContract(t, []string{"capabilities"})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if !strings.Contains(toon, "kind: capabilities") || !strings.Contains(toon, "output_formats[2]: toon,json") {
		t.Errorf("unexpected TOON output: %s", toon)
	}

	jsonOutput, err := runContract(t, []string{"capabilities", "--output", "json"})
	if err != nil {
		t.Fatalf("capabilities json: %v", err)
	}
	var response struct {
		SchemaVersion string `json:"schema_version"`
		Kind          string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != "munsu.orchestration/v2" || response.Kind != "capabilities" {
		t.Errorf("response = %+v", response)
	}
}

func TestTaskObserveContractDefaultAndExpandedFields(t *testing.T) {
	home := t.TempDir()
	if err := task.WriteMeta(home, "observe-me", map[string]string{"description": "inspect state", "worktree": filepath.Join(home, "branch-name")}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", home)

	output, err := runContract(t, []string{"task", "observe", "observe-me"})
	if err != nil {
		t.Fatalf("task observe: %v", err)
	}
	if !strings.Contains(output, "task_id: observe-me") || strings.Contains(output, "description:") {
		t.Errorf("default task observe must be minimal: %s", output)
	}

	expanded, err := runContract(t, []string{"task", "observe", "observe-me", "--fields", "description,branch"})
	if err != nil {
		t.Fatalf("expanded task observe: %v", err)
	}
	if !strings.Contains(expanded, "description: no window in meta") || !strings.Contains(expanded, "branch: branch-name") {
		t.Errorf("expanded task observe = %s", expanded)
	}
}

func TestContractRejectsInvalidInputBeforeStateLookup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	output, err := runContract(t, []string{"task", "observe", "missing", "--fields", "nope"})
	if err == nil {
		t.Fatal("task observe with unsupported field succeeded")
	}
	if !strings.Contains(output, "error_code: unsupported_input") || !strings.Contains(output, "Unsupported field") {
		t.Errorf("unexpected structured error: %s", output)
	}

	jsonOutput, err := runContract(t, []string{"task", "observe", "missing", "--output", "json"})
	if err == nil || !strings.Contains(jsonOutput, `"error_code": "not_found"`) {
		t.Errorf("JSON not-found error = %q, err = %v", jsonOutput, err)
	}
}

func TestFleetSnapshotV2DefinitiveEmptyAndCompatibilityV1(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)

	v2, err := runContract(t, []string{"fleet", "snapshot", "--version", "2"})
	if err != nil {
		t.Fatalf("fleet snapshot v2: %v", err)
	}
	if !strings.Contains(v2, "kind: fleet.snapshot") || !strings.Contains(v2, "count: 0") || !strings.Contains(v2, "crewmates: []") {
		t.Errorf("fleet snapshot v2 empty output = %s", v2)
	}

	invalidVersion, err := runContract(t, []string{"fleet", "snapshot", "--version", "9"})
	if err == nil || !strings.Contains(invalidVersion, "error_code: unsupported_input") {
		t.Errorf("fleet snapshot invalid version = %q, err = %v", invalidVersion, err)
	}

	v1, err := runContract(t, []string{"fleet", "snapshot"})
	if err != nil {
		t.Fatalf("fleet snapshot v1: %v", err)
	}
	if !strings.Contains(v1, `"schema": "munsu-fleet-snapshot.v1"`) {
		t.Errorf("fleet snapshot v1 changed: %s", v1)
	}
}

func TestBackendCapabilitiesAndGuardContract(t *testing.T) {
	backend, err := runContract(t, []string{"backend", "capabilities", "--backend", "tmux"})
	if err != nil {
		t.Fatalf("backend capabilities: %v", err)
	}
	if !strings.Contains(backend, "kind: backend.capabilities") || !strings.Contains(backend, "features[3]: create_session,send_input,pane_liveness") {
		t.Errorf("backend output = %s", backend)
	}

	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	guard, err := runContract(t, []string{"guard"})
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	if !strings.Contains(guard, "kind: guard") || !strings.Contains(guard, "state: unhealthy") {
		t.Errorf("guard output = %s", guard)
	}
}

func runContract(t *testing.T, args []string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	buffer := new(bytes.Buffer)
	root.SetOut(buffer)
	root.SetErr(buffer)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil && WriteContractError(buffer, err, args) != 0 {
		return strings.TrimSpace(buffer.String()), err
	}
	return strings.TrimSpace(buffer.String()), err
}

func TestContractCLIReadsOnlyFreshTempHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	before, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatal("temp home was not clean")
	}
	if _, err := runContract(t, []string{"fleet", "snapshot", "--version", "2"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Errorf("read-only fleet snapshot changed clean home: %v", after)
	}
}
