package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestTaskStatusCannotMutateAuthoritativePhase proves `task status` is
// audit-only: appending a status line must never change the authoritative
// Task Aggregate phase (Task 3.4 criterion 1). Authoritative transitions
// belong to named operations owned by the parent rank, not to arbitrary
// status text.
func TestTaskStatusCannotMutateAuthoritativePhase(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	auth := testAuthorityFor(t, homeDir)
	seedAuthorityTask(t, auth, "legacy")

	if _, err := runTaskCommand(t, []string{"task", "status", "legacy", "done", "completed", "--home", homeDir}); err != nil {
		t.Fatalf("task status: %v", err)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseQueued || agg.Revision != taskauthority.FirstRevision {
		t.Fatalf("task status mutated the authoritative aggregate: %+v", agg)
	}
	statusLines, err := home.ReadStatus(homeDir, "legacy")
	if err != nil || len(statusLines) != 1 || statusLines[0] != "done: completed" {
		t.Fatalf("status projection = %v err=%v", statusLines, err)
	}
}

// TestTaskStatusAppendsAuditInputWithoutPhaseChange proves that on a task
// created through the canonical Authority, `task status` appends the status
// line to the .status projection and the typed event log but leaves the
// authoritative Phase and Revision untouched (Task 3.4 criteria 1 and 2).
func TestTaskStatusAppendsAuditInputWithoutPhaseChange(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "ship beta", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	if out, err := runTaskCommand(t, []string{"task", "status", "beta", "working", "building the thing [key=build-1]", "--home", homeDir}); err != nil {
		t.Fatalf("task status: %v\n%s", err, out)
	}

	agg, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "beta"))
	if err != nil {
		t.Fatalf("authority Get: %v", err)
	}
	if agg.Phase != taskauthority.PhaseQueued || agg.Revision != taskauthority.FirstRevision {
		t.Fatalf("task status changed canonical aggregate = %+v", agg)
	}

	statusLines, err := home.ReadStatus(homeDir, "beta")
	if err != nil || len(statusLines) != 2 || statusLines[1] != "working: building the thing [key=build-1]" {
		t.Fatalf("status projection = %v err=%v", statusLines, err)
	}

	// Typed event translation stays deterministic: one status line produces
	// one task.status event with the task as producer and the key preserved.
	events, err := orchestrator.ReadAll(homeDir)
	if err != nil {
		t.Fatalf("reading event log: %v", err)
	}
	if len(events) != 1 || events[0].Type != "task.status" || events[0].Producer != "beta" ||
		events[0].Key != "build-1" || events[0].Payload != "working: building the thing" {
		t.Fatalf("event log = %+v", events)
	}
}

// TestTaskStatusHelpIsAuditOnly pins the operator-facing contract: help and
// typed output must not imply that arbitrary status text mutates the
// authoritative lifecycle (Task 3.4 criterion 4).
func TestTaskStatusHelpIsAuditOnly(t *testing.T) {
	homeDir := t.TempDir()
	out, err := runTaskCommand(t, []string{"task", "status", "--help", "--home", homeDir})
	if err != nil {
		t.Fatalf("task status --help: %v", err)
	}
	if !strings.Contains(out, "audit") || !strings.Contains(out, "never changes") {
		t.Fatalf("task status help must describe audit-only semantics:\n%s", out)
	}

	initCLITestHome(t, homeDir)
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "ship beta", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	out, err = runTaskCommand(t, []string{"task", "status", "beta", "working", "building", "--output", "json", "--home", homeDir})
	if err != nil {
		t.Fatalf("task status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "audit") {
		t.Fatalf("task status output must mark the append as audit-only:\n%s", out)
	}
}

// TestTaskStatusMissingTaskStillAuditOnly proves that appending a status line
// for a task with no canonical aggregate neither errors the command nor
// creates an authoritative record; it only appends projection/audit input.
func TestTaskStatusMissingTaskStillAuditOnly(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	if out, err := runTaskCommand(t, []string{"task", "status", "ghost", "working", "note", "--home", homeDir}); err != nil {
		t.Fatalf("task status on missing task: %v\n%s", err, out)
	}
	// No authoritative aggregate may appear as a side effect.
	if _, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "ghost")); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("task status created an authoritative record for a missing task")
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", "task-authority", "tasks", "ghost")); !os.IsNotExist(err) {
		t.Fatalf("task status created authoritative state for a missing task")
	}
	lines, err := home.ReadStatus(homeDir, "ghost")
	if err != nil || len(lines) != 1 {
		t.Fatalf("status projection = %v err=%v", lines, err)
	}
}

// TestTaskAddRejectsScoutMissingScoutContract proves the scout contract is
// enforced on the CLI path, not only inside the Authority: `task add --kind
// scout` without a scope, and with a non-positive budget, are both refused and
// commit no task. The CLI defaults `--scope` to "" and `--budget` to 0, so
// without this guard the ordinary invocation would create a scout with no
// investigation scope and no runtime ceiling.
func TestTaskAddRejectsScoutMissingScoutContract(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"no scope", []string{"--kind", "scout", "--budget", "300"}, "requires non-empty scope"},
		{"no budget", []string{"--kind", "scout", "--scope", "investigate"}, "requires positive runtime budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)

			args := append([]string{"task", "add", "recon", "investigate the thing"}, tc.args...)
			out, err := runTaskCommand(t, append(args, "--home", homeDir))
			if err == nil {
				t.Fatalf("task add %v succeeded, want the scout contract to refuse it\n%s", tc.args, out)
			}
			if !errors.Is(err, taskauthority.ErrInvalidInput) {
				t.Errorf("task add %v error = %v, want ErrInvalidInput", tc.args, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("task add %v error = %v, want it to mention %q", tc.args, err, tc.wantMsg)
			}
			if _, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "recon")); !errors.Is(err, taskauthority.ErrNotFound) {
				t.Errorf("refused scout create left a task behind: Get = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestTaskAddAcceptsCompleteScoutContract is the positive control: the same
// CLI path with both scout flags present commits, and the flag values reach the
// authoritative aggregate.
func TestTaskAddAcceptsCompleteScoutContract(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)

	out, err := runTaskCommand(t, []string{
		"task", "add", "recon", "investigate the thing",
		"--kind", "scout", "--scope", "investigate the thing", "--budget", "300",
		"--home", homeDir,
	})
	if err != nil {
		t.Fatalf("task add (valid scout): %v\n%s", err, out)
	}
	agg, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "recon"))
	if err != nil {
		t.Fatalf("authority Get: %v", err)
	}
	if agg.Definition.Kind != "scout" ||
		agg.Definition.ScoutScope != "investigate the thing" ||
		agg.Definition.ScoutRuntimeBudgetSecs != 300 {
		t.Fatalf("scout definition = %+v, want the --scope/--budget flags preserved", agg.Definition)
	}
}
