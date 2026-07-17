package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	v1, err := runContract(t, []string{"fleet", "snapshot", "--output", "json"})
	if err != nil {
		t.Fatalf("fleet snapshot v1: %v", err)
	}

	var v1Resp struct {
		SchemaVersion string `json:"schema_version"`
		Kind          string `json:"kind"`
		Data          struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(v1), &v1Resp); err != nil {
		t.Fatalf("v1 response is not valid JSON: %v", err)
	}
	if !strings.Contains(v1Resp.Data.Message, `"schema": "munsu-fleet-snapshot.v1"`) {
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
	if err != nil {
		WriteContractError(buffer, err, args)
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

func TestWriteContractErrorWrapsNonContractErrors(t *testing.T) {
	var buf bytes.Buffer
	err := fmt.Errorf("something went wrong")
	exitCode := WriteContractError(&buf, err, []string{})
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	output := buf.String()
	if !strings.Contains(output, "error_code: error") {
		t.Errorf("output missing error_code: error\n%s", output)
	}
	if !strings.Contains(output, "something went wrong") {
		t.Errorf("output missing original message\n%s", output)
	}
}

func TestWriteContractErrorRespectsOutputFlag(t *testing.T) {
	var bufJSON bytes.Buffer
	err := fmt.Errorf("json test error")
	exitCode := WriteContractError(&bufJSON, err, []string{"--output", "json"})
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(bufJSON.String(), `"error_code": "error"`) {
		t.Errorf("JSON output missing error_code\n%s", bufJSON.String())
	}
}

func TestBackendCapabilitiesHasHelpHint(t *testing.T) {
	output, err := runContract(t, []string{"backend", "capabilities", "--backend", "tmux"})
	if err != nil {
		t.Fatalf("backend capabilities: %v", err)
	}
	if !strings.Contains(output, "help[1]:") {
		t.Errorf("backend capabilities TOON missing help hint\n%s", output)
	}
	if !strings.Contains(output, "task observe") {
		t.Errorf("backend capabilities help hint should mention task observe\n%s", output)
	}
}

func TestGuardHasContextualHelpHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	output, err := runContract(t, []string{"guard"})
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	if !strings.Contains(output, "help[2]:") && !strings.Contains(output, "help[1]:") {
		t.Errorf("guard TOON should have contextual help\n%s", output)
	}
	if !strings.Contains(output, "fleet snapshot") {
		t.Errorf("guard help hint should mention fleet snapshot\n%s", output)
	}
}

func TestFleetSnapshotV2HasHelpAndAggregates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)

	// Empty snapshot: count:0, total:0, no crewmates, should still have help
	output, err := runContract(t, []string{"fleet", "snapshot", "--version", "2"})
	if err != nil {
		t.Fatalf("fleet snapshot v2: %v", err)
	}
	if !strings.Contains(output, "count: 0") || !strings.Contains(output, "total: 0") {
		t.Errorf("empty snapshot should have count:0 and total:0\n%s", output)
	}
	if !strings.Contains(output, "help[1]:") {
		t.Errorf("empty snapshot missing help hints\n%s", output)
	}

	// Non-empty snapshot: add a task
	if err := task.WriteMeta(home, "alpha", map[string]string{"description": "inspect", "worktree": home}); err != nil {
		t.Fatal(err)
	}
	out2, err := runContract(t, []string{"fleet", "snapshot", "--version", "2"})
	if err != nil {
		t.Fatalf("fleet snapshot v2 non-empty: %v", err)
	}
	if !strings.Contains(out2, "count: 1") || !strings.Contains(out2, "total: 1") {
		t.Errorf("non-empty snapshot should have count:1 and total:1\n%s", out2)
	}
	if !strings.Contains(out2, "crewmates[1]") {
		t.Errorf("non-empty snapshot should have 1 crewmate\n%s", out2)
	}
	if !strings.Contains(out2, "help[1]:") {
		t.Errorf("non-empty snapshot missing help hints\n%s", out2)
	}
}

func TestTaskListShowsAggregateCount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)

	// Empty state: should show "no tasks found"
	output1 := captureTaskList(t)
	if !strings.Contains(output1, "no tasks found") {
		t.Errorf("empty task list should say 'no tasks found', got: %s", output1)
	}

	// Add one task
	if err := task.WriteMeta(home, "beta", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	// Non-empty: should show header and total count
	output2 := captureTaskList(t)
	if !strings.Contains(output2, "Total:") {
		t.Errorf("task list should show total count, got: %s", output2)
	}
	if !strings.Contains(output2, "beta") {
		t.Errorf("task list should list beta, got: %s", output2)
	}
}

func captureTaskList(t *testing.T) string {
	t.Helper()
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"task", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("task list: %v", err)
	}
	return buf.String()
}

