package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestTaskShowDisplaysCurrentAggregateAuthority(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=legacy\nowner=captain:api\ngeneration=7\nstate=working\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}

	out, err := runMigrateCommand([]string{"task", "show", "ship-1", "--home", homeDir})
	if err != nil {
		t.Fatalf("task show: %v\n%s", err, out)
	}
	if !strings.Contains(out, "owner: captain:api") || !strings.Contains(out, "generation: 7") {
		t.Fatalf("task show output = %s", out)
	}
}

func TestAggregateOnlyTaskWorksInListShowAndSoldierState(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=aggregate only\nowner=captain:api\ngeneration=7\nstate=working\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(homeDir, "state", "ship-1.meta")); err != nil {
		t.Fatal(err)
	}

	listOut, err := runMigrateCommand([]string{"task", "list", "--home", homeDir, "--output", "json"})
	if err != nil || !strings.Contains(listOut, "ship-1") {
		t.Fatalf("task list err=%v out=%s", err, listOut)
	}
	showOut, err := runMigrateCommand([]string{"task", "show", "ship-1", "--home", homeDir})
	if err != nil || !strings.Contains(showOut, "owner: captain:api") {
		t.Fatalf("task show err=%v out=%s", err, showOut)
	}
	stateOut, err := runMigrateCommand([]string{"soldier-state", "ship-1", "--home", homeDir, "--output", "json"})
	if err != nil || !strings.Contains(stateOut, "working") {
		t.Fatalf("soldier-state err=%v out=%s", err, stateOut)
	}
}

func TestTaskAddCreatesAggregateAuthority(t *testing.T) {
	homeDir := t.TempDir()
	out, err := runMigrateCommand([]string{"task", "add", "ship-1", "new work", "--home", homeDir, "--output", "json"})
	if err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	agg, ok, err := home.ReadCurrentTaskAggregate(homeDir, "ship-1")
	if err != nil || !ok || agg.Generation != "1" || agg.Definition != "new work" || agg.State != "queued" {
		t.Fatalf("aggregate = %+v ok=%v err=%v", agg, ok, err)
	}
}

func TestTaskAddRollsBackAggregateWhenMetaProjectionFails(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, "state", "ship-1.meta"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := runMigrateCommand([]string{"task", "add", "ship-1", "new work", "--home", homeDir, "--output", "json"})
	if err == nil {
		t.Fatalf("task add unexpectedly succeeded: %s", out)
	}
	if _, ok, readErr := home.ReadCurrentTaskAggregate(homeDir, "ship-1"); readErr != nil || ok {
		t.Fatalf("aggregate should be rolled back ok=%v err=%v", ok, readErr)
	}
}

func TestTaskStatusUpdatesAggregateAuthorityBeforeProjection(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=legacy\nowner=captain:api\ngeneration=7\nstate=working\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	if out, err := runMigrateCommand([]string{"task", "status", "ship-1", "blocked", "waiting", "--home", homeDir, "--output", "json"}); err != nil {
		t.Fatalf("task status: %v\n%s", err, out)
	}
	agg, ok, err := home.ReadCurrentTaskAggregate(homeDir, "ship-1")
	if err != nil || !ok || agg.State != "blocked" || agg.StateDetail != "waiting" {
		t.Fatalf("aggregate = %+v ok=%v err=%v", agg, ok, err)
	}
}

func TestTaskStatusFailsBeforeProjectionWhenAggregateAuthorityIsInvalid(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=legacy\nowner=captain:api\ngeneration=7\nstate=working\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", ".task-authority", "aggregates", "ship-1", "current"), []byte("bad-generation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := runMigrateCommand([]string{"task", "status", "ship-1", "done", "should not project", "--home", homeDir, "--output", "json"})
	if err == nil {
		t.Fatalf("task status unexpectedly succeeded: %s", out)
	}
	if _, statErr := os.Stat(filepath.Join(homeDir, "state", "ship-1.status")); !os.IsNotExist(statErr) {
		t.Fatalf("status projection should not be written after aggregate failure: %v", statErr)
	}
}

func TestRejectedBacklogTransitionDoesNotChangeAggregateAuthority(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=legacy\nowner=captain:api\ngeneration=7\nstate=done\n")
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "data", "backlog.md"), "# Backlog\n\n- [x] ship-1 - legacy\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	out, err := runMigrateCommand([]string{"backlog", "ready", "ship-1", "--home", homeDir})
	if err == nil {
		t.Fatalf("backlog ready unexpectedly succeeded: %s", out)
	}
	agg, ok, err := home.ReadCurrentTaskAggregate(homeDir, "ship-1")
	if err != nil || !ok || agg.State != "done" {
		t.Fatalf("aggregate = %+v ok=%v err=%v", agg, ok, err)
	}
}

func TestBacklogDoneUpdatesAggregateAuthority(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=legacy\nowner=captain:api\ngeneration=7\nstate=working\n")
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "data", "backlog.md"), "# Backlog\n\n- [-] ship-1 - legacy\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	if out, err := runMigrateCommand([]string{"backlog", "done", "ship-1", "--home", homeDir}); err != nil {
		t.Fatalf("backlog done: %v\n%s", err, out)
	}
	agg, ok, err := home.ReadCurrentTaskAggregate(homeDir, "ship-1")
	if err != nil || !ok || agg.State != "done" {
		t.Fatalf("aggregate = %+v ok=%v err=%v", agg, ok, err)
	}
}

func TestReadyUsesAggregateGenerationWithoutLegacyMeta(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=current\ngeneration=7\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(homeDir, "state", "ship-1.meta")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-1")
	out, err := runMigrateCommand([]string{"ready", "--event-id", "evt-agg-only", "--output", "json"})
	if err != nil {
		t.Fatalf("ready: %v\n%s", err, out)
	}
	if !strings.Contains(out, "gen=7") {
		t.Fatalf("ready output = %s", out)
	}
}

func TestConsumeReadyUsesAggregateGenerationWithoutLegacyMeta(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=current\ngeneration=7\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(homeDir, "state", "ship-1.meta")); err != nil {
		t.Fatal(err)
	}
	out, err := runMigrateCommand([]string{"consume-ready", "ship-1", "--home", homeDir, "--sender", "captain", "--output", "json"})
	if err != nil {
		t.Fatalf("consume-ready aggregate-only: %v\n%s", err, out)
	}
}

func TestReadyUsesAggregateGenerationOverLegacyMeta(t *testing.T) {
	homeDir := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.meta"), "description=legacy\ngeneration=1\ncurrent=false\n")
	writeTaskAggregateCLIFile(t, filepath.Join(homeDir, "state", "ship-1.gen7.meta"), "task_id=ship-1\ndescription=current\ngeneration=7\n")
	plan, err := home.PlanTaskAggregateMigration(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.ApplyTaskAggregateMigration(plan); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-1")

	out, err := runMigrateCommand([]string{"ready", "--event-id", "evt-1", "--output", "json"})
	if err != nil {
		t.Fatalf("ready: %v\n%s", err, out)
	}
	if !strings.Contains(out, "gen=7") {
		t.Fatalf("ready output = %s", out)
	}
}
