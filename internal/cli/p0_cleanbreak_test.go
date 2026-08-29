//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	mhome "github.com/minhtri2710/munsu/internal/home"
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
// The legacy positional-claim / --owner / --claim forms are rejected.
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
	if err := mhome.EnqueueWake(homeDir, "signal", "task", "payload"); err != nil {
		t.Fatal(err)
	}
	leaseClaim, herr := mhome.ClaimWakes(homeDir, "test", 60, 1)
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

	// Legacy positional claim form is rejected.
	if out, err := runContract(t, []string{"wake", "claim", "wake-1"}); err == nil {
		t.Errorf("wake claim with positional wake-id must be rejected, got:\n%s", out)
	}
	// Legacy --owner flag is rejected.
	if out, err := runContract(t, []string{"wake", "claim", "--owner", "x"}); err == nil {
		t.Errorf("wake claim --owner must be rejected (unknown flag), got:\n%s", out)
	}
	// Legacy ack --claim form is rejected.
	if out, err := runContract(t, []string{"wake", "ack", "--claim", "claim-1"}); err == nil {
		t.Errorf("wake ack --claim must be rejected (requires positional lease-id + event-ids), got:\n%s", out)
	}
}

// TestBackendCapabilitiesOnlyTmuxHerdr proves structured backend capability
// discovery exposes only tmux and herdr; all other runtime backends are not
// part of this contract surface.
func TestBackendCapabilitiesOnlyTmuxHerdr(t *testing.T) {
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "tmux"}); err != nil {
		t.Errorf("backend capabilities --backend tmux: %v\n%s", err, out)
	}
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "herdr"}); err != nil {
		t.Errorf("backend capabilities --backend herdr: %v\n%s", err, out)
	}
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "zellij"}); err == nil {
		t.Errorf("backend capabilities --backend zellij must be rejected (not exposed), got:\n%s", out)
	}
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "cmux"}); err == nil {
		t.Errorf("backend capabilities --backend cmux must be rejected (not exposed), got:\n%s", out)
	}
	if out, err := runContract(t, []string{"backend", "capabilities", "--backend", "orca"}); err == nil {
		t.Errorf("backend capabilities --backend orca must be rejected (not exposed), got:\n%s", out)
	}
}

// TestTaskObserveMetaOnlyFailsClosed proves `task observe` on an ordinary
// meta-only task (no canonical record) returns a structured invalid_state with
// task/home context, never a projection fallback.
func TestTaskObserveMetaOnlyFailsClosed(t *testing.T) {
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

	out, err := runContract(t, []string{"task", "observe", "legacy-task", "--output=json"})
	if err == nil {
		t.Fatalf("task observe over a meta-only task = nil error, want fail-closed; output:\n%s", out)
	}
	response := decodeContractError(t, out)
	if response.Error.ErrorCode != "invalid_state" {
		t.Errorf("meta-only task observe error_code = %q, want invalid_state; output:\n%s", response.Error.ErrorCode, out)
	}
	assertCarriesContext(t, response, "legacy-task", homeDir)
	if response.Kind != "error" {
		t.Errorf("meta-only task observe must not emit a success envelope, got kind %q:\n%s", response.Kind, out)
	}
}

// TestTaskObserveMissingTaskNotFound proves `task observe` on a completely
// missing task returns a structured not_found with task/home context.
func TestTaskObserveMissingTaskNotFound(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)

	out, err := runContract(t, []string{"task", "observe", "does-not-exist", "--output=json"})
	if err == nil {
		t.Fatalf("task observe over a missing task = nil error, want not_found; output:\n%s", out)
	}
	response := decodeContractError(t, out)
	if response.Error.ErrorCode != "not_found" {
		t.Errorf("missing task observe error_code = %q, want not_found; output:\n%s", response.Error.ErrorCode, out)
	}
	assertCarriesContext(t, response, "does-not-exist", homeDir)
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
}

// decodeContractError decodes the structured error envelope so the assertions
// below read fields instead of a rendering. The TOON encoder quotes any scalar
// holding ":" or "\\" (toonString, contract_output.go:180), so a windows home
// path is always escaped in the rendered text and no raw substring of it can
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
// was resolved against, and that each is delimited rather than merely present.
//
// Today both operands are interpolated with %q (contract_commands.go:138,141,146),
// which on windows escapes the separators in the path, so the quoted form is
// what the message carries. The corrupt-canonical caller carries the home
// twice: contract_commands.go:146 wraps the inner error with %v and that inner
// error quotes the home again (soldierstate_soldierstate.go:59,61).
//
// #708 changes the home verb to %s and leaves the task ID quoted. When it
// lands, exactly one expectation below changes -- strconv.Quote(homeDir)
// becomes homeDir -- and strconv.Quote(taskID) and the strconv import both
// stay. Dropping the taskID quote too would be silent, not loud: the bare word
// is a substring of its own quoted form and of much else in the message, so the
// assertion would stay green while no longer proving the ID is delimited. The
// inner site is load-bearing for the same reason from the other side: if #708
// changed only the three outer sites, this row would keep matching
// strconv.Quote(homeDir) against the inner occurrence and pass while the two
// others failed. Both halves of that were wrong in the #706 M8 commit message.
func assertCarriesContext(t *testing.T, response ErrorResponse, taskID, homeDir string) {
	t.Helper()
	for _, want := range []string{strconv.Quote(taskID), strconv.Quote(homeDir)} {
		if !strings.Contains(response.Error.Message, want) {
			t.Errorf("error message must carry %s, got:\n%s", want, response.Error.Message)
		}
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
	if err := mhome.AppendStatus(homeDir, "done-task", "working: stale tail"); err != nil {
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

// TestTaskObserveCanonicalWithoutMetaSucceeds proves a canonical task with no
// .meta projection is still observable from its authoritative phase.
func TestTaskObserveCanonicalWithoutMetaSucceeds(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	cliSeedCanonicalTaskPhase(t, homeDir, "no-meta", "ship", taskauthority.PhaseQueued)

	out, err := runContract(t, []string{"task", "observe", "no-meta", "--output", "json"})
	if err != nil {
		t.Fatalf("task observe canonical without meta: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"status": "queued"`) {
		t.Errorf("task observe must report canonical queued phase, got:\n%s", out)
	}
}
