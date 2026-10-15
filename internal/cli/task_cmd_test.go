package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"charm.land/huh/v2"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
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
// line to the .status projection but leaves the authoritative Phase and
// Revision untouched (Task 3.4 criteria 1 and 2).
func TestTaskStatusAppendsAuditInputWithoutPhaseChange(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	registerCLITestProject(t, homeDir, "munsu")
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "ship beta", "--repo", "munsu", "--home", homeDir}); err != nil {
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
	registerCLITestProject(t, homeDir, "munsu")
	if out, err := runTaskCommand(t, []string{"task", "add", "beta", "ship beta", "--repo", "munsu", "--home", homeDir}); err != nil {
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
// scout` without a scope is refused and commits no task. The CLI defaults
// `--scope` to "", so without this guard the ordinary invocation would create a
// scout with no investigation scope.
func TestTaskAddRejectsScoutMissingScoutContract(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"no scope", []string{"--kind", "scout"}, "requires non-empty scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			registerCLITestProject(t, homeDir, "munsu")

			args := append([]string{"task", "add", "recon", "investigate the thing", "--repo", "munsu"}, tc.args...)
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
// CLI path with the scope present commits, and the scope reaches the
// authoritative aggregate.
func TestTaskAddAcceptsCompleteScoutContract(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	registerCLITestProject(t, homeDir, "munsu")

	out, err := runTaskCommand(t, []string{
		"task", "add", "recon", "investigate the thing", "--repo", "munsu",
		"--kind", "scout", "--scope", "investigate the thing",
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
		agg.Definition.ScoutScope != "investigate the thing" {
		t.Fatalf("scout definition = %+v, want the --scope flag preserved", agg.Definition)
	}
}

// TestTaskAddProjectResolution: with no --repo, task add takes the registered
// project at the current repository root and writes its brief; outside any
// repository no project resolves and nothing is created. On an interactive
// terminal task add asks for missing args and for a registered project, with
// no validation the args path lacks; an aborted prompt returns "aborted".
func TestTaskAddProjectResolution(t *testing.T) {
	for _, tc := range []struct {
		name       string
		inRepo     bool
		registered bool
		tty        bool
		args       []string
		answers    string
		desc       string
		wantErr    string
	}{
		{"blocks when no project resolves", false, true, false, []string{"t1", "do the work"}, "", "", "task t1 has no project"},
		{"allows a registered project resolved from the repository", true, true, false, []string{"t1", "do the work"}, "", "do the work", ""},
		{"blocks missing args off a terminal", true, true, false, []string{"t1"}, "", "", "add accepts 2 arg(s), received 1"},
		{"allows missing args asked at the terminal", true, true, true, nil, "t1\ndo the work\n", "do the work", ""},
		{"allows an empty description asked at the terminal, as the args path does", true, true, true, nil, "t1\n\nre-asked\n", "", ""},
		{"blocks at the terminal when no project is registered", false, false, true, []string{"t1", "do the work"}, "", "", "task t1 has no project"},
		{"allows a project picked at the terminal", false, true, true, []string{"t1", "do the work"}, "1\n", "do the work", ""},
		{"blocks with a plain error when the project prompt is aborted", false, true, true, []string{"t1", "do the work"}, "abort", "", "aborted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := t.TempDir()
			initCLITestHome(t, homeDir)
			repoDir := t.TempDir()
			if tc.registered {
				runGit(t, repoDir, "init", "-q")
				if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion, Config: config.FleetBaseConfig{Backend: "tmux"}}); err != nil {
					t.Fatal(err)
				}
				if out, err := runRoot(t, "project", "add", "alpha", repoDir, "--home", homeDir); err != nil {
					t.Fatalf("project add: %v\n%s", err, out)
				}
			}
			cwd := t.TempDir()
			if tc.inRepo {
				cwd = repoDir
			}
			t.Chdir(cwd)
			oldTerminals, oldPrompt := terminals, runPrompt
			t.Cleanup(func() { terminals, runPrompt = oldTerminals, oldPrompt })
			terminals = func() (bool, bool) { return tc.tty, tc.tty }
			runPrompt = func(f *huh.Form) error {
				if tc.answers == "" {
					t.Error("task add prompted where it must not")
				}
				if tc.answers == "abort" {
					return huh.ErrUserAborted
				}
				return f.WithAccessible(true).WithInput(iotest.OneByteReader(strings.NewReader(tc.answers))).WithOutput(io.Discard).Run()
			}
			_, err := runTaskCommand(t, append([]string{"task", "add"}, append(tc.args, "--home", homeDir)...))
			agg, getErr := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "t1"))
			if tc.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Fatalf("task add error = %v, want %q", err, tc.wantErr)
				}
				if !errors.Is(getErr, taskauthority.ErrNotFound) {
					t.Fatalf("refused task add left a task: %v", getErr)
				}
				return
			}
			if err != nil || getErr != nil || agg.Definition.Project != "alpha" || agg.Definition.Description != tc.desc {
				t.Fatalf("task add = %v, get = %v, definition %+v, want project alpha, description %q", err, getErr, agg.Definition, tc.desc)
			}
			if tc.desc == "" {
				return
			}
			brief, err := os.ReadFile(filepath.Join(homeDir, "data", "t1", "brief.md"))
			if err != nil || !strings.Contains(string(brief), "do the work") || strings.Contains(string(brief), "{TASK}") {
				t.Fatalf("brief = %q (%v), want the description in place of {TASK}", brief, err)
			}
		})
	}
}
