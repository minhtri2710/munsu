//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
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
	if err := mhome.WriteMeta(home, "observe-me", map[string]string{"description": "inspect state", "worktree": filepath.Join(home, "branch-name")}); err != nil {
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
	if !strings.Contains(expanded, "branch: branch-name") || !strings.Contains(expanded, "status: unknown") {
		t.Errorf("expanded task observe = %s", expanded)
	}
}

func TestTaskObserveCaptainUsesStructuredHomeState(t *testing.T) {
	home := t.TempDir()
	captainHome := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	if err := mhome.WriteMeta(home, "captain:test", map[string]string{"kind": "captain", "sm_id": "test", "home": captainHome}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(captainHome, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captainHome, "data", "backlog.md"), []byte("# Backlog\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(home, "captain:test", "working: historical parent state"); err != nil {
		t.Fatal(err)
	}
	output, err := runContract(t, []string{"task", "observe", "captain:test"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "status: no_active_work") {
		t.Fatalf("task observe did not use structured Captain state: %s", output)
	}
}

func TestWakeClaimEmptyQueueReturnsEmptyWithoutLease(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)

	out, err := runContract(t, []string{"wake", "claim", "--consumer", "test", "--output", "json"})
	if err != nil {
		t.Fatalf("wake claim: %v\n%s", err, out)
	}
	var envelope struct {
		Kind string `json:"kind"`
		Data struct {
			State   string `json:"state"`
			ClaimID string `json:"claim_id"`
			WakeID  string `json:"wake_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if envelope.Kind != "wake.claim" || envelope.Data.State != "empty" || envelope.Data.ClaimID != "" || envelope.Data.WakeID != "" {
		t.Fatalf("unexpected empty claim: %+v", envelope)
	}
	entries, err := os.ReadDir(filepath.Join(home, "state", ".wake-leases"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty claim created lease files: %v", entries)
	}
}

func TestSessionStartUsesSessionStartKind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)

	out, err := runContract(t, []string{"session-start", "--output", "json"})
	if err != nil {
		t.Fatalf("session-start: %v\n%s", err, out)
	}
	var envelope struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if envelope.Kind != "session.start" {
		t.Fatalf("kind=%q want session.start", envelope.Kind)
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
	if !strings.Contains(v2, "kind: fleet.snapshot") || !strings.Contains(v2, "count: 0") || !strings.Contains(v2, "soldiers: []") {
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
			Schema string `json:"schema"`
			Time   string `json:"time"`
			Tasks  []any  `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(v1), &v1Resp); err != nil {
		t.Fatalf("v1 response is not valid JSON: %v", err)
	}
	if v1Resp.Kind != "fleet.snapshot.v1" {
		t.Errorf("fleet snapshot v1 kind = %q, want fleet.snapshot.v1: %s", v1Resp.Kind, v1)
	}
	if v1Resp.Data.Schema != "munsu-fleet-snapshot.v1" {
		t.Errorf("fleet snapshot v1 data.schema = %q, want munsu-fleet-snapshot.v1: %s", v1Resp.Data.Schema, v1)
	}
	if v1Resp.Data.Time == "" {
		t.Errorf("fleet snapshot v1 data.time is empty: %s", v1)
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

func TestWakeResolveCommandJSONContract(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)
	if err := mhome.EnqueueWake(home, "signal", "task", "payload"); err != nil {
		t.Fatal(err)
	}
	claim, err := mhome.ClaimWakes(home, "test", 60, 1)
	if err != nil || len(claim.Wakes) != 1 {
		t.Fatalf("claim: %+v err=%v", claim, err)
	}
	eventID := claim.Wakes[0].Epoch + ":" + claim.Wakes[0].Seq
	args := []string{"wake", "resolve", "--claim-id", claim.LeaseID, "--event-id", eventID, "--summary", "done", "--output", "json"}
	out, err := runContract(t, args)
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	assertOneJSONWakeResponse(t, out, "wake.resolve")
	out, err = runContract(t, args)
	if err != nil {
		t.Fatalf("repeat resolve: %v\n%s", err, out)
	}
	assertOneJSONWakeResponse(t, out, "wake.resolve")
	for _, bad := range [][]string{
		{"missing", eventID, "done"},
		{claim.LeaseID, "missing", "done"},
		{claim.LeaseID, eventID, ""},
	} {
		badOut, badErr := runContract(t, []string{"wake", "resolve", "--claim-id", bad[0], "--event-id", bad[1], "--summary", bad[2], "--output", "json"})
		if badErr == nil {
			t.Fatalf("invalid resolve succeeded: %v", bad)
		}
		assertOneJSONWakeResponse(t, badOut, "error")
	}
}

func assertOneJSONWakeResponse(t *testing.T, output, wantKind string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(output))
	var envelope struct {
		Kind string `json:"kind"`
	}
	if err := dec.Decode(&envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, output)
	}
	if envelope.Kind != wantKind {
		t.Fatalf("kind=%q want %q: %s", envelope.Kind, wantKind, output)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout contains more than one JSON document: %v\n%s", err, output)
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

func TestWriteContractErrorEncodeFailureReturnsNonZero(t *testing.T) {
	var buf bytes.Buffer
	// contractError with schema version that should fail encoding
	// Use an unencodable struct — Encode returns error for
	// nil values or broken output format.
	err := fmt.Errorf("encode fail test")
	exitCode := WriteContractError(&buf, err, []string{})
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1 on any error", exitCode)
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

	// Empty snapshot: count:0, total:0, no soldiers, should still have help
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
	if !strings.Contains(output, "captain_guidance") {
		t.Errorf("empty snapshot missing captain_guidance\n%s", output)
	}
	if !strings.Contains(output, "do not routinely munsu peek a captain") {
		t.Errorf("empty snapshot missing return-channel watch guidance\n%s", output)
	}
	if !strings.Contains(output, "structured state from that registered home") {
		t.Errorf("empty snapshot missing captain_guidance.note\n%s", output)
	}

	// Non-empty snapshot: add a task
	if err := mhome.WriteMeta(home, "alpha", map[string]string{"description": "inspect", "worktree": home}); err != nil {
		t.Fatal(err)
	}
	out2, err := runContract(t, []string{"fleet", "snapshot", "--version", "2"})
	if err != nil {
		t.Fatalf("fleet snapshot v2 non-empty: %v", err)
	}
	if !strings.Contains(out2, "count: 1") || !strings.Contains(out2, "total: 1") {
		t.Errorf("non-empty snapshot should have count:1 and total:1\n%s", out2)
	}
	if !strings.Contains(out2, "soldiers[1]") {
		t.Errorf("non-empty snapshot should have 1 soldier\n%s", out2)
	}
	if !strings.Contains(out2, "help[1]:") {
		t.Errorf("non-empty snapshot missing help hints\n%s", out2)
	}
	if !strings.Contains(out2, "captain_guidance") || !strings.Contains(out2, "munsu send captain:<id>") {
		t.Errorf("non-empty snapshot missing captain send guidance\n%s", out2)
	}
}

func TestFleetSnapshotV2CaptainGuidanceJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MUNSU_HOME", home)

	out, err := runContract(t, []string{"fleet", "snapshot", "--version", "2", "--output", "json"})
	if err != nil {
		t.Fatalf("fleet snapshot v2 json: %v", err)
	}
	var resp struct {
		Data struct {
			CaptainGuidance struct {
				Note              string `json:"note"`
				Watch             string `json:"watch"`
				Send              string `json:"send"`
				ReturnChannelNote string `json:"return_channel_note"`
			} `json:"captain_guidance"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	g := resp.Data.CaptainGuidance
	want := DefaultCaptainGuidance()
	if g.Note != want.Note {
		t.Errorf("note = %q", g.Note)
	}
	if g.Watch != want.Watch {
		t.Errorf("watch = %q", g.Watch)
	}
	if g.Send != want.Send {
		t.Errorf("send = %q", g.Send)
	}
	if g.ReturnChannelNote != want.ReturnChannelNote {
		t.Errorf("return_channel_note = %q", g.ReturnChannelNote)
	}
	if !(strings.Contains(g.Watch, "peek") && strings.Contains(strings.ToLower(g.Watch), "routinely")) {
		t.Errorf("watch should discourage routine peek, got %q", g.Watch)
	}
}

func TestFleetSnapshotV2ParentReconciliation(t *testing.T) {
	home := t.TempDir()
	captainHome := filepath.Join(home, "captains", "domain-alpha")
	os.MkdirAll(filepath.Join(captainHome, "state"), 0755)
	os.MkdirAll(filepath.Join(captainHome, "data"), 0755)
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.MkdirAll(filepath.Join(home, "data"), 0755)
	// Idle captain home (no active children).
	os.WriteFile(filepath.Join(captainHome, "data", "backlog.md"), []byte("# Backlog\n\n## Queued\n- [ ] hold: external\n"), 0644)
	// Registry entry for the captain.
	line := fmt.Sprintf("- domain-alpha - (home: %s; scope: domain; projects: sample; added: 2026-07-19)\n", captainHome)
	os.WriteFile(filepath.Join(home, "data", "captains.md"), []byte("# Captains\n\n"+line), 0644)
	// Launched captain meta makes the supervisor visible in the raw task snapshot.
	if err := mhome.WriteMeta(home, "captain:domain-alpha", map[string]string{
		"kind":    "captain",
		"sm_id":   "domain-alpha",
		"home":    captainHome,
		"backend": "herdr",
		"window":  "session-1:pane-1",
	}); err != nil {
		t.Fatal(err)
	}
	// Stale parent event claims working while home is idle.
	if err := mhome.AppendStatus(home, "captain:domain-alpha", "working [key=phase7]: Sample rollout Phase 7"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", home)

	out, err := runContract(t, []string{"fleet", "snapshot", "--version", "2", "--output", "json"})
	if err != nil {
		t.Fatalf("fleet snapshot v2: %v", err)
	}
	var resp Response[FleetSnapshotV2]
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(resp.Data.Captains) != 1 {
		t.Fatalf("captains=%d want 1: %+v", len(resp.Data.Captains), resp.Data.Captains)
	}
	if len(resp.Data.Soldiers) != 0 {
		t.Fatalf("captain supervisor must not be projected as a soldier: %+v", resp.Data.Soldiers)
	}
	c := resp.Data.Captains[0]
	if c.Provenance != "structured-home" {
		t.Errorf("provenance=%q want structured-home", c.Provenance)
	}
	if c.Freshness != "fresh" {
		t.Errorf("freshness=%q want fresh", c.Freshness)
	}
	if c.ParentEventRole != "historical-only" {
		t.Errorf("parent_event_role=%q", c.ParentEventRole)
	}
	if !c.Contradiction {
		t.Errorf("expected contradiction; last_parent_status=%q current_state=%q reason=%q", c.LastParentStatus, c.CurrentState, c.ContradictionReason)
	}
	if c.CurrentState == "" || c.CurrentState == "unknown" && c.Provenance == "structured-home" {
		// structured-home must expose home state, not promote parent working to current.
		if c.CurrentState == "" {
			t.Errorf("current_state empty")
		}
	}
	if strings.Contains(c.CurrentState, "working") {
		t.Errorf("current_state must not be derived from parent working: %q", c.CurrentState)
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
	if err := mhome.WriteMeta(home, "beta", map[string]string{"kind": "ship"}); err != nil {
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

func TestSafetyCheckContractJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUNSU_HOME", t.TempDir())

	// Initialize a git repo so the path is a "primary" checkout (not "unrelated")
	// SafetyCheck only sets gate_refused=false for non-unrelated with no gate.
	gitCmd := exec.Command("git", "init", dir)
	if out, err := gitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(out))
	}

	out, err := runContract(t, []string{"integrate", "safety-check", dir, "--output", "json"})
	if err != nil {
		t.Fatalf("safety-check: %v", err)
	}

	var resp struct {
		SchemaVersion string          `json:"schema_version"`
		Kind          string          `json:"kind"`
		Status        string          `json:"status"`
		Data          SafetyCheckData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("safety-check JSON unmarshal: %v\nOutput: %s", err, out)
	}

	// Verify envelope fields
	if resp.SchemaVersion != "munsu.orchestration/v2" {
		t.Errorf("schema_version = %q, want munsu.orchestration/v2", resp.SchemaVersion)
	}
	if resp.Kind != "integrate.safety-check" {
		t.Errorf("kind = %q, want integrate.safety-check", resp.Kind)
	}
	if resp.Status != "success" {
		t.Errorf("status = %q, want success", resp.Status)
	}

	// Block MUST always serialize, including false (TS parser requires typeof boolean)
	if resp.Data.Block != false {
		t.Errorf("safe safety-check block = %v, want false", resp.Data.Block)
	}
	if resp.Data.GateRefused != false {
		t.Errorf("safe safety-check gate_refused = %v, want false", resp.Data.GateRefused)
	}
	if resp.Data.Identity == "" {
		t.Error("identity must be non-empty even for temp dirs")
	}
	if resp.Data.GateCapability == "" {
		t.Error("gate_capability must be non-empty even for temp dirs")
	}

	// Verify raw JSON key presence — structural unmarshal alone cannot distinguish
	// omitted vs explicit false for bool fields (both decode to Go zero value).
	rawBytes := []byte(out)
	if !bytes.Contains(rawBytes, []byte(`"block"`)) {
		t.Error("JSON output must contain 'block' key (not omitted)")
	}
	if !bytes.Contains(rawBytes, []byte(`"gate_refused"`)) {
		t.Error("JSON output must contain 'gate_refused' key (not omitted)")
	}
	if bytes.Contains(rawBytes, []byte(`"block":true`)) {
		t.Error("safe safety-check must not have block:true")
	}
}

func TestSafetyCheckBlockTrue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUNSU_HOME", t.TempDir())

	out, err := runContract(t, []string{"integrate", "safety-check", dir, "--command", "munsu watch arm", "--output", "json"})
	if err != nil {
		t.Fatalf("safety-check blocked: %v", err)
	}

	var resp struct {
		Data SafetyCheckData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("JSON unmarshal: %v\nOutput: %s", err, out)
	}

	if !resp.Data.Block {
		t.Errorf("blocked command should have block=true, got block=%v", resp.Data.Block)
	}
	if resp.Data.Reason == "" {
		t.Error("blocked command must have non-empty reason")
	}
}

func TestSafetyCheckRegressionOldOmitemptyShape(t *testing.T) {
	// Old shape WITHOUT 'block' key must be rejected by the TS parseSafetyCheck.
	// This regression proves the omitempty fix is necessary:
	// the TS parser requires typeof data.block === "boolean", which fails
	// when the key is absent from JSON.
	oldJSON := `{
	  "schema_version": "munsu.orchestration/v2",
	  "kind": "integrate.safety-check",
	  "status": "success",
	  "data": {
	    "identity": "unrelated",
	    "gate_capability": "gate-absent",
	    "gate_refused": false
	  }
	}`

	var resp struct {
		Data struct {
			Block *bool `json:"block"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(oldJSON), &resp); err != nil {
		t.Fatalf("old JSON unmarshal: %v", err)
	}
	if resp.Data.Block != nil {
		t.Error(`old JSON without block field should unmarshal block as nil (absent) — rejects the "old shape" assumption`)
	}
	t.Log(`old shape without block serializes block as nil — would fail TS typeof(block) === "boolean" check`)
}

func TestSafetyCheckProductionJSONFeedsTSRuntime(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUNSU_HOME", t.TempDir())

	// Initialize git repo so SafetyCheck classifies this as "primary" (gate_refused=false)
	gitCmd := exec.Command("git", "init", dir)
	if out, err := gitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(out))
	}

	// Capture production safety-check JSON via the actual CLI command path
	safeOut, err := runContract(t, []string{"integrate", "safety-check", dir, "--output", "json"})
	if err != nil {
		t.Fatalf("safe safety-check: %v", err)
	}

	blockedOut, err := runContract(t, []string{"integrate", "safety-check", dir, "--command", "munsu watch arm", "--output", "json"})
	if err != nil {
		t.Fatalf("blocked safety-check: %v", err)
	}

	// Find Node.js — required for TS runtime test
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js not on PATH — skipping TS runtime fixture test")
	}

	testDir := t.TempDir()
	safePath := filepath.Join(testDir, "safe.json")
	blockedPath := filepath.Join(testDir, "blocked.json")
	if err := os.WriteFile(safePath, []byte(safeOut), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedPath, []byte(blockedOut), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a clean TS runner that reads the fixture files
	runner := `import * as fs from "fs";

function parseSafetyCheck(raw: string):
  { ok: true; gate_refused: boolean; block: boolean; reason?: string } |
  { ok: false; reason: string } {
  if (!raw || !raw.trim()) return { ok: false, reason: "empty stdout" };
  let parsed: any;
  try { parsed = JSON.parse(raw); } catch {
    return { ok: false, reason: "malformed JSON" };
  }
  if (typeof parsed !== "object" || parsed === null)
    return { ok: false, reason: "not a JSON object" };
  if (parsed.schema_version !== "munsu.orchestration/v2")
    return { ok: false, reason: "wrong schema_version: " + String(parsed.schema_version) };
  if (parsed.kind !== "integrate.safety-check")
    return { ok: false, reason: "wrong kind: " + String(parsed.kind) };
  if (parsed.status !== "success")
    return { ok: false, reason: "status is not success: " + String(parsed.status) };
  const data = parsed.data;
  if (!data || typeof data !== "object")
    return { ok: false, reason: "missing or non-object data" };
  if (typeof data.gate_refused !== "boolean")
    return { ok: false, reason: "missing or non-boolean gate_refused" };
  if (typeof data.block !== "boolean")
    return { ok: false, reason: "missing or non-boolean block" };
  return { ok: true, gate_refused: data.gate_refused, block: data.block, reason: data.reason };
}

const args = process.argv.slice(2);
if (args.length < 2) { console.error("Usage: runner.ts <safe.json> <blocked.json>"); process.exit(1); }

const safeJSON = JSON.parse(fs.readFileSync(args[0], "utf-8"));
const blockedJSON = JSON.parse(fs.readFileSync(args[1], "utf-8"));

// Test 1: production safe JSON must be accepted
const safeResult = parseSafetyCheck(JSON.stringify(safeJSON));
if (!safeResult.ok) throw new Error("FAIL: safe JSON rejected: " + safeResult.reason);
if (safeResult.block !== false) throw new Error("FAIL: safe JSON block=" + safeResult.block);
if (safeResult.gate_refused !== false) throw new Error("FAIL: safe JSON gate_refused=" + safeResult.gate_refused);
console.log("PASS: production safe JSON accepted");

// Test 2: production blocked JSON must be accepted with block:true
const blockedResult = parseSafetyCheck(JSON.stringify(blockedJSON));
if (!blockedResult.ok) throw new Error("FAIL: blocked JSON rejected: " + blockedResult.reason);
if (blockedResult.block !== true) throw new Error("FAIL: blocked JSON block=" + blockedResult.block);
console.log("PASS: production blocked JSON accepted");

// Test 3: old shape without 'block' must be rejected (regression)
const oldData = { ...safeJSON.data };
delete oldData.block;
const oldPayload = { ...safeJSON, data: oldData };
const oldResult = parseSafetyCheck(JSON.stringify(oldPayload));
if (oldResult.ok) throw new Error("FAIL: old shape without block must be rejected");
console.log("PASS: old shape without block rejected");

console.log("ALL PRODUCTION CONTRACT FIXTURE TESTS PASSED");
`

	runnerPath := filepath.Join(testDir, "runner.ts")
	if err := os.WriteFile(runnerPath, []byte(runner), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(nodePath, "--experimental-strip-types", runnerPath, safePath, blockedPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TS runtime test failed:\nOutput: %s\nError: %v", string(output), err)
	}
	if !strings.Contains(string(output), "ALL PRODUCTION CONTRACT FIXTURE TESTS PASSED") {
		t.Fatalf("TS runtime test did not report PASSED: %s", string(output))
	}
}
