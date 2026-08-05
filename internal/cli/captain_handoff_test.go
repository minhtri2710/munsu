package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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

// TestTaskShowRecoversPendingTransferAndReadsCanonicalState proves the public
// task-show entry point converges an interrupted journaled Task Transfer
// (RecoverTaskHandoffs) and then serves the canonical record: a crash after
// the durable journal intent leaves the source owning the task; task show at
// the captain resumes the SAME transfer and serves the received/activated
// canonical record.
func TestTaskShowRecoversPendingTransferAndReadsCanonicalState(t *testing.T) {
	if os.Getenv("MUNSU_CLI_HANDOFF_HELPER") == "1" {
		restore := fleet.SetHandoffCrashHookForTest(func(boundary string) {
			if boundary == "journal" {
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
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	if _, err := home.Init(captain); err != nil {
		t.Fatal(err)
	}
	if err := fleet.SeedProvenance(captain, "api"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set(captain, "parent-home", parent); err != nil {
		t.Fatal(err)
	}
	sourceAuth := seedCLICanonicalHome(t, parent)
	seedCLICanonicalQueuedTask(t, sourceAuth, "TASK-1", "general")

	cmd := exec.Command(os.Args[0], "-test.run", "^TestTaskShowRecoversPendingTransferAndReadsCanonicalState$", "--")
	cmd.Env = append(os.Environ(), "MUNSU_CLI_HANDOFF_HELPER=1", "MUNSU_CLI_HANDOFF_SOURCE="+parent, "MUNSU_CLI_HANDOFF_DEST="+captain)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected helper crash")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
		t.Fatalf("helper exit = %v\n%s", err, output)
	}

	// task show at the captain recovers the pending transfer (the journal
	// lives at the configured parent) and serves the canonical record.
	out, err := runMigrateCommand([]string{"--home", captain, "task", "show", "TASK-1"})
	if err != nil {
		t.Fatalf("task show recovery err=%v out=%s", err, out)
	}
	if !strings.Contains(out, "owner: captain:api") || !strings.Contains(out, "description: CLI recovery") {
		t.Fatalf("task show recovery output missing canonical record: %s", out)
	}
	// The source no longer owns the task after recovery.
	captainC, err := cliCanonical(t, parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captainC.Get(mustCLITaskID(t, "TASK-1")); err == nil {
		t.Fatal("source still owns task after recovery")
	}
}

// seedCLICanonicalHome initializes a canonical home and returns its authority.
func seedCLICanonicalHome(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func seedCLICanonicalQueuedTask(t *testing.T, c *taskauthority.Canonical, taskID, owner string) {
	t.Helper()
	req := taskauthority.CanonicalCreateRequest{
		HomeID:      c.HomeID(),
		TaskID:      mustCLITaskID(t, taskID),
		Owner:       owner,
		Description: "CLI recovery",
		Kind:        "ship",
		Project:     mustCLIProjectID(t, "munsu"),
		Reason:      "test seed",
	}
	if _, err := c.Create(mustCLIOp(t, "cli-seed-"+taskID, req), req); err != nil {
		t.Fatal(err)
	}
}

func cliCanonical(t *testing.T, homeDir string) (*taskauthority.Canonical, error) {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		return nil, err
	}
	return taskauthority.NewCanonical(h)
}

func mustCLITaskID(t *testing.T, value string) domain.TaskID {
	t.Helper()
	id, err := domain.NewTaskID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustCLIProjectID(t *testing.T, value string) domain.ProjectID {
	t.Helper()
	id, err := domain.NewProjectID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustCLIOp(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestCaptainHandoffAmbiguousIDCorrectionsPreserveDestinationAndSourceHome(t *testing.T) {
	parent := t.TempDir()
	captain := filepath.Join(parent, "captains", "api")
	other := filepath.Join(parent, "captains", "other")
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	for _, scoped := range []string{captain, other} {
		if err := os.MkdirAll(scoped, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := home.Init(captain); err != nil {
		t.Fatal(err)
	}
	if _, err := home.Init(other); err != nil {
		t.Fatal(err)
	}
	canonicalCaptain, err := filepath.EvalSymlinks(captain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captain, ".munsu-captain-home"), []byte("munsu-v2\napi\n"+canonicalCaptain+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	seedCLICanonicalQueuedTask(t, seedCLICanonicalHome(t, parent), "TASK-1", "general")
	seedCLICanonicalQueuedTask(t, seedCLICanonicalHome(t, captain), "TASK-1", "captain:api")
	seedCLICanonicalQueuedTask(t, seedCLICanonicalHome(t, other), "TASK-1", "captain:other")
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
