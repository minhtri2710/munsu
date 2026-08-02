package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/taskauthorityfs"
)

func TestTaskShowFailsClosedOnMalformedParentHomeDuringRecovery(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "config", "parent-home"), []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runMigrateCommand([]string{"--home", homeDir, "task", "show", "TASK-1"}); err == nil {
		t.Fatal("expected task show to fail closed on malformed parent-home")
	}
}

func TestTaskShowRecoversPendingHandoffAndReadsCanonicalState(t *testing.T) {
	if os.Getenv("MUNSU_CLI_HANDOFF_HELPER") == "1" {
		restore := fleet.SetHandoffCrashHookForTest(func(boundary string) {
			if boundary == "prepared" {
				os.Exit(92)
			}
		})
		defer restore()
		if err := fleet.Handoff(os.Getenv("MUNSU_CLI_HANDOFF_SOURCE"), os.Getenv("MUNSU_CLI_HANDOFF_DEST"), []string{"TASK-1"}); err == nil {
			os.Exit(0)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(91)
	}

	parent := t.TempDir()
	captain := filepath.Join(parent, "captains", "api")
	if err := os.MkdirAll(captain, 0755); err != nil {
		t.Fatal(err)
	}
	if err := fleet.SeedProvenance(captain, "api"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set(captain, "parent-home", parent); err != nil {
		t.Fatal(err)
	}
	// The Task 6.2 saga operates on v2 homes: seed the source's canonical
	// authority state instead of the legacy v1 aggregate.
	store, err := taskauthorityfs.NewStore(parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskauthority.New(store).Create(taskauthority.CreateRequest{
		OperationID: "cli-handoff-seed",
		Actor:       taskauthority.Actor{ID: "general", Rank: "general"},
		TaskID:      "TASK-1",
		Owner:       "general",
		Description: "CLI recovery",
		Kind:        "ship",
		Reason:      "test seed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(parent, "TASK-1", map[string]string{"description": "CLI recovery", "generation": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(parent, "TASK-1", "queued: ready"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "data", "TASK-1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "TASK-1", "brief.md"), []byte("# CLI recovery\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(captain, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "data", "backlog.md"), []byte("# Backlog\n\n- [ ] TASK-1 - CLI recovery\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captain, "data", "backlog.md"), []byte("# Backlog\n\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(parent, "tasks-axi")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nif [ \"$1\" = show ]; then echo 'state: queued'; exit 0; fi\nif [ \"$1\" = mv ]; then\n  to=\"\"; file=\"\"; shift\n  while [ \"$#\" -gt 0 ]; do case \"$1\" in --to) to=\"$2\"; shift 2;; --file) file=\"$2\"; shift 2;; *) shift;; esac; done\n  printf '# Backlog\\n\\n' > \"$file\"; exit 0\nfi\nexit 2\n"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run", "^TestTaskShowRecoversPendingHandoffAndReadsCanonicalState$", "--")
	cmd.Env = append(os.Environ(), "MUNSU_CLI_HANDOFF_HELPER=1", "MUNSU_CLI_HANDOFF_SOURCE="+parent, "MUNSU_CLI_HANDOFF_DEST="+captain, "PATH="+filepath.Dir(fake)+":"+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper crash")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, output)
	}
	// Seed the captain home's canonical v2 Authority with the transferred task
	// (Task 6.2 makes the handoff saga install this atomically). task show then
	// triggers handoff recovery of the pending journal and serves the
	// canonical record.
	store, err = taskauthorityfs.NewStore(captain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskauthority.New(store).Create(taskauthority.CreateRequest{
		OperationID: "cli-handoff-recovery-seed",
		Actor:       taskauthority.Actor{ID: "captain:api", Rank: "captain"},
		TaskID:      "TASK-1",
		Owner:       "captain:api",
		Description: "CLI recovery",
		Kind:        "ship",
		Reason:      "test seed",
	}); err != nil {
		t.Fatal(err)
	}
	out, err := runMigrateCommand([]string{"--home", captain, "task", "show", "TASK-1"})
	if err != nil || !strings.Contains(out, "owner: captain:api") || !strings.Contains(out, "description: CLI recovery") {
		t.Fatalf("task show recovery err=%v out=%s", err, out)
	}
}

// seedCLIHandoffTaskV2 creates a canonical v2 task at a home for CLI handoff
// resolution tests.
func seedCLIHandoffTaskV2(t *testing.T, homeDir, taskID, owner, description string) {
	t.Helper()
	store, err := taskauthorityfs.NewStore(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskauthority.New(store).Create(taskauthority.CreateRequest{
		OperationID: "cli-seed-" + taskID + "-" + owner,
		Actor:       taskauthority.Actor{ID: owner, Rank: "general"},
		TaskID:      taskID,
		Owner:       owner,
		Description: description,
		Kind:        "ship",
		Reason:      "test seed",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCaptainHandoffAmbiguousIDCorrectionsPreserveDestinationAndSourceHome(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captains", "api")
	other := filepath.Join(parent, "captains", "other")
	for _, scoped := range []string{parent, captain, other} {
		if err := os.MkdirAll(scoped, 0755); err != nil {
			t.Fatal(err)
		}
	}
	canonicalCaptain, err := filepath.EvalSymlinks(captain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captain, ".munsu-captain-home"), []byte("munsu-v2\napi\n"+canonicalCaptain+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	seedCLIHandoffTaskV2(t, parent, "TASK-1", "general", "source")
	seedCLIHandoffTaskV2(t, captain, "TASK-1", "captain:api", "captain")
	seedCLIHandoffTaskV2(t, other, "TASK-1", "captain:other", "other")
	canonicalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	canonicalOther, err := filepath.EvalSymlinks(other)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runMigrateCommand([]string{"--home", parent, "captain", "handoff", captain, "TASK-1"})
	if err == nil {
		t.Fatal("expected ambiguous task ID error")
	}
	contractErr, ok := err.(*contractError)
	if !ok || contractErr.value.Error.ErrorCode != "ambiguous_task_id" {
		t.Fatalf("error = %#v, want ambiguous_task_id contract", err)
	}
	message := contractErr.value.Error.Message + " " + contractErr.value.Error.Action
	for _, candidate := range []string{canonicalParent, canonicalOther} {
		want := "munsu --home " + candidate + " captain handoff " + captain + " TASK-1"
		if !strings.Contains(message, want) {
			t.Fatalf("missing executable correction %q in %q", want, message)
		}
	}
	if strings.Contains(message, "munsu --home "+canonicalCaptain+" captain handoff "+captain) {
		t.Fatal("corrections must preserve the requested captain destination")
	}
}
