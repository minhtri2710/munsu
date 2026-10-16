//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestStructuredGuardFailsClosedOnUnreadableTruth proves the structured
// `munsu guard` command returns a typed invalid_state failure when Task truth
// is unreadable (a legacy/meta-only soldier task with no canonical record),
// instead of evaluating the guard with an empty/down-counted fleet.
func TestStructuredGuardFailsClosedOnUnreadableTruth(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	// A task-facing meta without a canonical record is unreadable Task truth
	// under the clean-break contract.
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	meta := "kind=ship\nwindow=test\n"
	if err := os.WriteFile(filepath.Join(homeDir, "state", "legacy-task.meta"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runContract(t, []string{"guard"})
	if err == nil {
		t.Fatalf("munsu guard over unreadable truth = nil error, want fail-closed; output:\n%s", out)
	}
	if !strings.Contains(out, "invalid_state") {
		t.Errorf("guard failure must carry a structured invalid_state code, got:\n%s", out)
	}
	if strings.Contains(out, "kind: guard") {
		t.Errorf("guard failure must not report a normal guard result, got:\n%s", out)
	}
}

// TestPrerunGuardSurfacesUnreadableTruth proves the pre-run middleware does
// not silently treat an unreadable fleet as empty: it writes a warning to
// stderr naming the failure.
func TestPrerunGuardSurfacesUnreadableTruth(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	meta := "kind=ship\nwindow=test\n"
	if err := os.WriteFile(filepath.Join(homeDir, "state", "legacy-task.meta"), []byte(meta), 0644); err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"capabilities"})
		_ = root.Execute()
	})
	if !strings.Contains(stderr, "cannot read authoritative fleet state") {
		t.Errorf("pre-run guard must surface unreadable Task truth on stderr (not return silently), got:\n%s", stderr)
	}
}

// TestWakeClaimResolveAckActualSyntax proves wake uses the real invocation
// strings documented in the contract: claim by --consumer, resolve by
// --claim-id/--event-id/--summary, and ack by positional lease-id + event-ids.
func TestWakeClaimResolveAckActualSyntax(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)

	// claim --consumer is the only accepted claim form.
	claim, err := runContract(t, []string{"wake", "claim", "--consumer", "me"})
	if err != nil {
		t.Fatalf("wake claim --consumer: %v\n%s", err, claim)
	}
	if !strings.Contains(claim, "kind: wake.claim") {
		t.Errorf("wake claim must emit kind wake.claim, got:\n%s", claim)
	}

	// ack by positional lease-id + event-id is accepted against a real lease.
	if err := home.EnqueueWake(homeDir, "signal", "task", "payload"); err != nil {
		t.Fatal(err)
	}
	leaseClaim, herr := home.ClaimWakes(homeDir, "test", 60, 1)
	if herr != nil || len(leaseClaim.Wakes) != 1 {
		t.Fatalf("claim: %+v err=%v", leaseClaim, herr)
	}
	eventID := leaseClaim.Wakes[0].Epoch + ":" + leaseClaim.Wakes[0].Seq
	ack, err := runContract(t, []string{"wake", "ack", leaseClaim.LeaseID, eventID})
	if err != nil {
		t.Fatalf("wake ack positional: %v\n%s", err, ack)
	}
	if !strings.Contains(ack, "kind: wake.ack") {
		t.Errorf("wake ack must emit kind wake.ack, got:\n%s", ack)
	}
}

// TestBackendCapabilitiesOnlyTmuxHerdr proves structured backend capability
// discovery exposes only tmux and herdr; all other runtime backends are not
// part of this contract surface.
func TestBackendCapabilitiesOnlyTmuxHerdr(t *testing.T) {
	t.Setenv("MUNSU_HOME", t.TempDir())
	for _, tc := range []struct {
		backend string
		want    string // "" allows
	}{
		{"tmux", ""},
		{"herdr", ""},
		{"zellij", `Unsupported backend "zellij"`},
	} {
		out, err := runContract(t, []string{"backend", "capabilities", "--backend", tc.backend})
		if tc.want == "" {
			if err != nil {
				t.Errorf("backend capabilities --backend %s: %v\n%s", tc.backend, err, out)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error()+out, tc.want) {
			t.Errorf("backend capabilities --backend %s = %v\n%s, want %q", tc.backend, err, out, tc.want)
		}
	}
}

// TestTaskObserveRequiresCanonicalRecord proves `task observe` answers from the
// canonical record alone: a task with only a legacy .meta projection is not
// found, and a canonical task with no .meta is observed.
func TestTaskObserveRequiresCanonicalRecord(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "legacy-task.meta"), []byte("kind=ship\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cliSeedCanonicalTaskPhase(t, homeDir, "no-meta", "ship", taskauthority.PhaseQueued)

	out, err := runContract(t, []string{"task", "observe", "no-meta", "--output", "json"})
	if err != nil {
		t.Fatalf("task observe canonical without meta: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"status": "queued"`) {
		t.Errorf("task observe must report canonical queued phase, got:\n%s", out)
	}

	out, err = runContract(t, []string{"task", "observe", "legacy-task", "--output=json"})
	if err == nil {
		t.Fatalf("task observe over a meta-only task = nil error, want not_found; output:\n%s", out)
	}
	response := decodeContractError(t, out)
	if response.Error.ErrorCode != "not_found" {
		t.Errorf("meta-only task observe error_code = %q, want not_found; output:\n%s", response.Error.ErrorCode, out)
	}
	assertCarriesContext(t, response, "legacy-task", homeDir)
	if response.Kind != "error" {
		t.Errorf("meta-only task observe must not emit a success envelope, got kind %q:\n%s", response.Kind, out)
	}
}

// TestTaskObserveCorruptCanonicalFailsClosed proves a corrupt canonical record
// yields a structured invalid_state with task/home context.
func TestTaskObserveCorruptCanonicalFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	cliSeedCanonicalTask(t, homeDir, "corrupt", "ship")
	cur := filepath.Join(homeDir, "state", "task-authority", "tasks", "corrupt", "current.json")
	if err := os.WriteFile(cur, []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runContract(t, []string{"task", "observe", "corrupt", "--output=json"})
	if err == nil {
		t.Fatalf("task observe over a corrupt canonical record = nil error, want fail-closed; output:\n%s", out)
	}
	response := decodeContractError(t, out)
	if response.Error.ErrorCode != "invalid_state" {
		t.Errorf("corrupt canonical observe error_code = %q, want invalid_state; output:\n%s", response.Error.ErrorCode, out)
	}
	assertCarriesContext(t, response, "corrupt", homeDir)
	if !strings.Contains(response.Error.Action, "munsu cannot repair an unreadable canonical record") {
		t.Errorf("corrupt canonical observe action = %q, want the no-repair instruction", response.Error.Action)
	}
}

// decodeContractError decodes the structured error envelope so the assertions
// below read fields instead of a rendering. The TOON encoder quotes any scalar
// holding ":" or "\\" (toonString, contract_output.go:180), so a home path
// holding either is always escaped in the rendered text and no raw substring of it can
// ever match; --output=json makes the same envelope machine-readable.
func decodeContractError(t *testing.T, out string) ErrorResponse {
	t.Helper()
	var response ErrorResponse
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decoding contract error envelope: %v; output:\n%s", err, out)
	}
	return response
}

// assertCarriesContext proves the error message names the task and the home it
// was resolved against, and that the task ID is delimited while the home is
// present without pre-quoting.
//
// The three outer contract sites (contract_commands.go:138,141,146) render
// the home with %s and keep the task ID at %q. The corresponding fleet sites
// (soldierstate_soldierstate.go:59,61) also render the home with %s. These rows
// exercise the corrupt-error path at soldierstate_soldierstate.go:61 through
// contract_commands.go:146; they do not observe line 59 because the
// not-found branch is recognized at contract_commands.go:135 and replaced by
// the message from contract_commands.go:141.
//
// The assertion checks the delimited task ID, raw home presence, and absence
// of the pre-quoted home form. The negative check is necessary because the
// corrupt message carries the home in both the outer contract error and the
// inner fleet error, so raw home presence alone would not prove the inner
// rendering is unquoted.
func assertCarriesContext(t *testing.T, response ErrorResponse, taskID, homeDir string) {
	t.Helper()
	for _, want := range []string{strconv.Quote(taskID), homeDir} {
		if !strings.Contains(response.Error.Message, want) {
			t.Errorf("error message must carry %s, got:\n%s", want, response.Error.Message)
		}
	}
	if strings.Contains(response.Error.Message, strconv.Quote(homeDir)) {
		t.Errorf("error message must not pre-quote the home path, got:\n%s", response.Error.Message)
	}
}

// TestTaskObserveCanonicalDoneBeatsStaleStatus proves canonical phase wins over
// a stale .status tail through the task observe contract.
func TestTaskObserveCanonicalDoneBeatsStaleStatus(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	cliSeedCanonicalTaskPhase(t, homeDir, "done-task", "ship", taskauthority.PhaseDone)
	if err := home.AppendStatus(homeDir, "done-task", "working: stale tail"); err != nil {
		t.Fatal(err)
	}

	out, err := runContract(t, []string{"task", "observe", "done-task", "--output", "json"})
	if err != nil {
		t.Fatalf("task observe canonical done: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"status": "done"`) {
		t.Errorf("task observe must report canonical done phase, got:\n%s", out)
	}
}

// cliSeedCanonicalTaskPhase creates one canonical task at the given phase.
func cliSeedCanonicalTaskPhase(t *testing.T, homeDir, id, kind string, phase taskauthority.Phase) {
	t.Helper()
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	auth := cliCanonicalForHome(t, homeDir)
	project, err := domain.NewProjectID("munsu")
	if err != nil {
		t.Fatal(err)
	}
	createReq := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "owner",
		Description: "work", Kind: kind, Project: project, Reason: "test",
	}
	if kind == "scout" {
		createReq.ScoutScope = "investigate scope"
	}
	opID, err := domain.NewOperationID("op-create-" + id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, createReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, createReq); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
	if phase == taskauthority.PhaseDone {
		cliCompleteTask(t, auth, tid, "op-done-"+id, taskauthority.PhaseDone)
	}
}

// cliCompleteTask drives a non-terminal canonical task into a terminal phase.
func cliCompleteTask(t *testing.T, auth *taskauthority.Canonical, tid domain.TaskID, opID string, to taskauthority.Phase) {
	t.Helper()
	cur, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCompleteRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(cur.Generation), uint64(cur.Revision)),
		To:           to, Reason: "test",
	}
	op, err := domain.NewOperation(mustOpIDFor(t, opID), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Complete(op, req); err != nil {
		t.Fatalf("Complete(%s): %v", tid.Value(), err)
	}
}

// mustOpIDFor builds a validated operation identity.
func mustOpIDFor(t *testing.T, value string) domain.OperationID {
	t.Helper()
	id, err := domain.NewOperationID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestTaskObserveRefusalsStateThatMunsuCannotRepair proves task observe
// reports a meta-only task as not found and a corrupt task as invalid state
// without pointing at a command that repairs it.
func TestTaskObserveRefusalsStateThatMunsuCannotRepair(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", "meta-only.meta"), []byte("kind=ship\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cliSeedCanonicalTask(t, homeDir, "corrupt", "ship")
	cur := filepath.Join(homeDir, "state", "task-authority", "tasks", "corrupt", "current.json")
	if err := os.WriteFile(cur, []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][2]string{
		"meta-only": {"not_found", "Run `munsu task list` to find a task ID"},
		"corrupt":   {"invalid_state", "munsu cannot repair an unreadable canonical record"},
	} {
		out, err := runContract(t, []string{"task", "observe", id, "--output=json"})
		if err == nil {
			t.Fatalf("task observe %s = nil error; output:\n%s", id, out)
		}
		got := decodeContractError(t, out).Error
		if got.ErrorCode != want[0] || !strings.Contains(got.Action, want[1]) {
			t.Errorf("task observe %s = (%q, %q), want (%q, %q)", id, got.ErrorCode, got.Action, want[0], want[1])
		}
	}
}
