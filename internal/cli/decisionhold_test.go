package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// runRoot executes one CLI command and returns the serialized output plus the
// raw error.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil {
		WriteContractError(&out, err, args)
	}
	return out.String(), err
}

// decisionHoldAuthority composes the command-context Authority over the given
// home, mirroring what the decision-hold commands do.
func decisionHoldAuthority(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	ctx := Ctx{Home: homeDir}
	auth, err := ctx.TaskAuthority()
	if err != nil {
		t.Fatalf("composing task authority: %v", err)
	}
	return auth
}

func containsAction(actions []taskauthority.DispatchAction, action taskauthority.DispatchAction) bool {
	for _, candidate := range actions {
		if candidate == action {
			return true
		}
	}
	return false
}

// TestDecisionHoldHoldAndListRouteThroughAuthority proves `decision-hold
// hold` creates a durable Authority DispatchHold and appends the needs-decision
// status projection, and that `decision-hold list` reads unresolved holds back
// from the Authority (Task 5.2 criterion 3).
func TestDecisionHoldHoldAndListRouteThroughAuthority(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	out, err := runRoot(t, "decision-hold", "hold", "approach",
		"--reason", "Pick the UI framework", "--from", "scout-r2",
		"--home", homeDir, "--output", "json")
	if err != nil {
		t.Fatalf("hold: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"kind": "decision-hold.hold"`) || !strings.Contains(out, "created") {
		t.Fatalf("hold output = %s", out)
	}

	// The authoritative hold lives in the Authority store, read back by ID.
	auth := decisionHoldAuthority(t, homeDir)
	holds, err := auth.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 || holds[0].ID != "scout-r2-decision-approach" {
		t.Fatalf("authority holds = %+v, want scout-r2-decision-approach", holds)
	}
	hold := holds[0]
	if hold.ReleasedAt != 0 || hold.Reason != "Pick the UI framework" {
		t.Fatalf("hold = %+v", hold)
	}
	if len(hold.Scope.TaskIDs) != 1 || hold.Scope.TaskIDs[0] != "scout-r2" {
		t.Fatalf("hold scope = %+v, want task-scoped to scout-r2", hold.Scope)
	}
	for _, action := range []taskauthority.DispatchAction{
		taskauthority.DispatchActionStart, taskauthority.DispatchActionSpawn, taskauthority.DispatchActionHandoff,
	} {
		if !containsAction(hold.Actions, action) {
			t.Fatalf("hold actions = %v, missing %s", hold.Actions, action)
		}
	}
	// Status projection line appended to the origin task.
	statuses, err := os.ReadFile(filepath.Join(homeDir, "state", "scout-r2.status"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statuses), "needs-decision: Pick the UI framework [key=approach]") {
		t.Fatalf("status = %q", statuses)
	}

	// List reads unresolved holds from the Authority.
	out, err = runRoot(t, "decision-hold", "list", "scout-r2", "--home", homeDir, "--output", "json")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"kind": "decision-hold.list"`) || !strings.Contains(out, `"decision_key": "approach"`) {
		t.Fatalf("list output = %s", out)
	}
}

// TestDecisionHoldHoldIsIdempotent proves repeating `decision-hold hold` with
// the same key and origin is a no-op reported as already existing, keeping
// the typed contract stable.
func TestDecisionHoldHoldIsIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	args := []string{"decision-hold", "hold", "approach",
		"--reason", "Pick the UI framework", "--from", "scout-r2",
		"--home", homeDir, "--output", "json"}

	if out, err := runRoot(t, args...); err != nil {
		t.Fatalf("first hold: %v\n%s", err, out)
	}
	out, err := runRoot(t, args...)
	if err != nil {
		t.Fatalf("second hold: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already exists") || !strings.Contains(out, `"noop": true`) {
		t.Fatalf("idempotent hold output = %s", out)
	}
	auth := decisionHoldAuthority(t, homeDir)
	holds, err := auth.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 {
		t.Fatalf("authority holds = %d, want exactly one", len(holds))
	}
}

// TestDecisionHoldResolveRoutesThroughAuthority proves `decision-hold
// resolve` releases the Authority hold and records the answer in the status
// projection.
func TestDecisionHoldResolveRoutesThroughAuthority(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runRoot(t, "decision-hold", "hold", "approach",
		"--reason", "Pick the UI framework", "--from", "scout-r2",
		"--home", homeDir, "--output", "json"); err != nil {
		t.Fatalf("hold: %v\n%s", err, out)
	}

	out, err := runRoot(t, "decision-hold", "resolve", "approach",
		"--answer", "Choose React", "--from", "scout-r2",
		"--grantor", "human", "--channel", "herdr", "--quote", "go with it",
		"--home", homeDir, "--output", "json")
	if err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"kind": "decision-hold.resolve"`) || !strings.Contains(out, "Choose React") {
		t.Fatalf("resolve output = %s", out)
	}

	auth := decisionHoldAuthority(t, homeDir)
	holds, err := auth.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 || holds[0].ReleasedAt == 0 {
		t.Fatalf("authority holds after resolve = %+v, want one released hold", holds)
	}
	if w := holds[0].ReleaseWords; w == nil || *w != (domain.Words{Grantor: "human", Channel: "herdr", Quote: "go with it"}) {
		t.Fatalf("hold release words = %+v, want the words given on the command line", w)
	}

	statuses, err := os.ReadFile(filepath.Join(homeDir, "state", "scout-r2.status"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(statuses), "resolved: Choose React [key=approach]") {
		t.Fatalf("origin status = %q", statuses)
	}

	// List now reports no unresolved holds.
	out, err = runRoot(t, "decision-hold", "list", "scout-r2", "--home", homeDir, "--output", "json")
	if err != nil {
		t.Fatalf("list after resolve: %v\n%s", err, out)
	}
	var envelope struct {
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("list output not JSON: %v\n%s", err, out)
	}
	if envelope.Data.Count != 0 {
		t.Fatalf("unresolved count = %d, want 0", envelope.Data.Count)
	}
}

// TestDecisionHoldResolveUnblocksThroughAuthority proves --unblock moves a
// blocked dependent to queued in the Authority, and reports a dependent the
// Authority refuses to unblock instead of claiming it was unblocked.
func TestDecisionHoldResolveUnblocksThroughAuthority(t *testing.T) {
	for _, tc := range []struct {
		name    string
		blocked bool
	}{
		{name: "blocked dependent is unblocked", blocked: true},
		{name: "queued dependent is reported", blocked: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			auth := testAuthorityFor(t, homeDir)
			seedAuthorityTask(t, auth, "dep-task-1")
			if tc.blocked {
				agg, err := auth.Get(mustTaskIDFor(t, "dep-task-1"))
				if err != nil {
					t.Fatal(err)
				}
				req := taskauthority.CanonicalBlockRequest{
					HomeID:       auth.HomeID(),
					TaskID:       mustTaskIDFor(t, "dep-task-1"),
					Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
					Detail:       "decision",
					Reason:       "seed",
				}
				if _, err := auth.Block(mustCanonicalOp(t, "seed-block", req), req); err != nil {
					t.Fatal(err)
				}
			}
			if out, err := runRoot(t, "decision-hold", "hold", "approach",
				"--reason", "Pick the UI framework", "--from", "scout-r2",
				"--home", homeDir, "--output", "json"); err != nil {
				t.Fatalf("hold: %v\n%s", err, out)
			}

			out, err := runRoot(t, "decision-hold", "resolve", "approach",
				"--answer", "Choose React", "--from", "scout-r2",
				"--grantor", "human", "--channel", "herdr", "--quote", "go with it",
				"--unblock", "dep-task-1", "--home", homeDir, "--output", "json")

			agg, gerr := auth.Get(mustTaskIDFor(t, "dep-task-1"))
			if gerr != nil || agg.Phase != taskauthority.PhaseQueued {
				t.Fatalf("dependent aggregate = %+v err=%v, want queued", agg, gerr)
			}
			depStatuses, _ := os.ReadFile(filepath.Join(homeDir, "state", "dep-task-1.status"))
			hasUnblockedLine := strings.Contains(string(depStatuses), "unblocked: decision resolved [key=approach]")
			if tc.blocked {
				if err != nil || !strings.Contains(out, "Unblocked: dep-task-1") || !hasUnblockedLine {
					t.Fatalf("resolve err=%v status=%q\n%s", err, depStatuses, out)
				}
				return
			}
			if err == nil || !strings.Contains(out, "Not unblocked: dep-task-1") ||
				!strings.Contains(out, "unblock requires blocked task") ||
				strings.Contains(out, "Unblocked:") || hasUnblockedLine {
				t.Fatalf("resolve err=%v status=%q\n%s", err, depStatuses, out)
			}
		})
	}
}

// TestDecisionHoldCompleteRoutesThroughAuthority proves `decision-hold
// complete` releases each named hold through the Authority and keeps the
// typed contract output stable.
func TestDecisionHoldCompleteRoutesThroughAuthority(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	for _, key := range []string{"approach", "db-schema"} {
		if out, err := runRoot(t, "decision-hold", "hold", key,
			"--reason", "pick "+key, "--from", "scout-r2",
			"--home", homeDir, "--output", "json"); err != nil {
			t.Fatalf("hold %s: %v\n%s", key, err, out)
		}
	}

	out, err := runRoot(t, "decision-hold", "complete", "scout-r2", "approach", "db-schema",
		"--grantor", "human", "--channel", "herdr", "--quote", "go with it",
		"--home", homeDir, "--output", "json")
	if err != nil {
		t.Fatalf("complete: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"kind": "decision-hold.complete"`) || !strings.Contains(out, "Completed 2 decision hold(s)") {
		t.Fatalf("complete output = %s", out)
	}

	auth := decisionHoldAuthority(t, homeDir)
	holds, err := auth.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 2 {
		t.Fatalf("authority holds = %d, want 2", len(holds))
	}
	for _, hold := range holds {
		if hold.ReleasedAt == 0 {
			t.Fatalf("hold %s not released by complete: %+v", hold.ID, hold)
		}
		if w := hold.ReleaseWords; w == nil || *w != (domain.Words{Grantor: "human", Channel: "herdr", Quote: "go with it"}) {
			t.Fatalf("hold %s release words = %+v, want the words given on the command line", hold.ID, w)
		}
	}
}

// TestDecisionHoldVerifyClean proves `decision-hold verify` reports no
// unresolved decisions once every Authority hold is released.
func TestDecisionHoldVerifyClean(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runRoot(t, "decision-hold", "hold", "approach",
		"--reason", "Pick the UI framework", "--from", "scout-r2",
		"--home", homeDir, "--output", "json"); err != nil {
		t.Fatalf("hold: %v\n%s", err, out)
	}
	if out, err := runRoot(t, "decision-hold", "resolve", "approach",
		"--answer", "Choose React", "--from", "scout-r2",
		"--grantor", "human", "--channel", "herdr", "--quote", "go with it",
		"--home", homeDir, "--output", "json"); err != nil {
		t.Fatalf("resolve: %v\n%s", err, out)
	}

	out, err := runRoot(t, "decision-hold", "verify", "scout-r2", "--home", homeDir)
	if err != nil {
		t.Fatalf("verify clean: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No unresolved decisions") {
		t.Fatalf("verify output = %s", out)
	}
}

// TestDecisionHoldCompleteAppendsResolvedProjection proves the full chain
// hold → complete → verify is clean: `decision-hold complete` releases the
// Authority hold AND appends the resolved status projection line, mirroring
// the resolve path, so verify sees no stale needs-decision line.
func TestDecisionHoldCompleteAppendsResolvedProjection(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runRoot(t, "decision-hold", "hold", "approach",
		"--reason", "Pick the UI framework", "--from", "scout-r2",
		"--home", homeDir, "--output", "json"); err != nil {
		t.Fatalf("hold: %v\n%s", err, out)
	}

	out, err := runRoot(t, "decision-hold", "complete", "scout-r2", "approach",
		"--grantor", "human", "--channel", "herdr", "--quote", "go with it",
		"--home", homeDir, "--output", "json")
	if err != nil {
		t.Fatalf("complete: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Completed 1 decision hold(s)") {
		t.Fatalf("complete output = %s", out)
	}

	// The resolved projection line must mirror the resolve path so the
	// needs-decision line is not left stale.
	statuses, err := os.ReadFile(filepath.Join(homeDir, "state", "scout-r2.status"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(statuses)
	if !strings.Contains(content, "needs-decision: Pick the UI framework [key=approach]") {
		t.Fatalf("status missing needs-decision line: %q", content)
	}
	if !strings.Contains(content, "resolved: recorded (decision noted) [key=approach]") {
		t.Fatalf("status missing resolved projection line after complete: %q", content)
	}

	// The Authority hold is released.
	auth := decisionHoldAuthority(t, homeDir)
	holds, err := auth.ListHolds()
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 || holds[0].ReleasedAt == 0 {
		t.Fatalf("authority holds after complete = %+v, want one released hold", holds)
	}

	// verify must now be clean.
	out, err = runRoot(t, "decision-hold", "verify", "scout-r2", "--home", homeDir)
	if err != nil {
		t.Fatalf("verify after complete: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No unresolved decisions") {
		t.Fatalf("verify output = %s", out)
	}
}

// TestDecisionHoldFailsClosedOnUninitializedHome proves decision-hold
// commands on an uninitialized home fail closed instead of silently
// initializing state.
func TestDecisionHoldFailsClosedOnUninitializedHome(t *testing.T) {
	homeDir := t.TempDir()

	_, err := runRoot(t, "decision-hold", "list", "scout-r2", "--home", homeDir, "--output", "json")
	if err == nil {
		t.Fatal("list on uninitialized home succeeded")
	}
	_, err = runRoot(t, "decision-hold", "hold", "approach",
		"--reason", "pick", "--from", "scout-r2", "--home", homeDir, "--output", "json")
	if err == nil {
		t.Fatal("hold on uninitialized home succeeded")
	}
}
