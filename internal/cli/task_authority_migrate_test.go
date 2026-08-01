package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateTaskAuthorityPlanAndApplyCommands(t *testing.T) {
	home := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "1.json"), `{
  "schema_version": "munsu.task-aggregate/v1",
  "task_id": "ship-1",
  "generation": "1",
  "current": true,
  "owner": "captain:api",
  "definition": "Implement API",
  "state": "working"
}
`)
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "current"), "1\n")
	planPath := filepath.Join(t.TempDir(), "task-authority-plan.json")

	out, err := runMigrateCommand([]string{"migrate", "task-authority", "plan", "--home", home, "--plan-out", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "migrate.task_authority.plan") || !strings.Contains(out, "Planned 1 v1 record") {
		t.Fatalf("plan output = %s", out)
	}
	if !strings.Contains(out, "apply=munsu migrate task-authority apply --plan") {
		t.Fatalf("plan output missing next action: %s", out)
	}

	out, err = runMigrateCommand([]string{"migrate", "task-authority", "apply", "--plan", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "migrate.task_authority.apply") || !strings.Contains(out, "Migrated 1 v1 record") {
		t.Fatalf("apply output = %s", out)
	}
	// The v1 source is archived and the v2 store serves the converted state.
	if _, err := os.Stat(filepath.Join(home, "state", ".task-authority", "aggregates")); !os.IsNotExist(err) {
		t.Fatalf("v1 aggregates still present after apply")
	}
	if _, err := os.Stat(filepath.Join(home, "state", ".task-authority", "v2", "aggregates", "ship-1", "1.json")); err != nil {
		t.Fatalf("v2 aggregate not installed: %v", err)
	}

	// Re-running apply reports typed already_migrated without rewriting.
	out, err = runMigrateCommand([]string{"migrate", "task-authority", "apply", "--plan", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("re-apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "migrate.task_authority.apply.already_migrated") || !strings.Contains(out, "already migrated") {
		t.Fatalf("re-apply output = %s", out)
	}
}

func TestMigrateTaskAuthorityPlanRequiresPlanOut(t *testing.T) {
	out, err := runMigrateCommand([]string{"migrate", "task-authority", "plan", "--home", t.TempDir(), "--output", "json"})
	if err == nil || !strings.Contains(err.Error(), "--plan-out is required") {
		t.Fatalf("plan without --plan-out should fail, err=%v out=%s", err, out)
	}
}

func TestMigrateTaskAuthorityApplyRequiresPlan(t *testing.T) {
	out, err := runMigrateCommand([]string{"migrate", "task-authority", "apply", "--home", t.TempDir(), "--output", "json"})
	if err == nil || !strings.Contains(err.Error(), "--plan is required") {
		t.Fatalf("apply without --plan should fail, err=%v out=%s", err, out)
	}
}

func TestMigrateTaskAuthorityPlanAlreadyMigrated(t *testing.T) {
	home := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "1.json"), `{
  "schema_version": "munsu.task-aggregate/v1",
  "task_id": "ship-1",
  "generation": "1",
  "current": true,
  "owner": "captain:api",
  "state": "queued"
}
`)
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "current"), "1\n")
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if _, err := runMigrateCommand([]string{"migrate", "task-authority", "plan", "--home", home, "--plan-out", planPath, "--output", "json"}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := runMigrateCommand([]string{"migrate", "task-authority", "apply", "--plan", planPath, "--output", "json"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, err := runMigrateCommand([]string{"migrate", "task-authority", "plan", "--home", home, "--plan-out", filepath.Join(t.TempDir(), "plan2.json"), "--output", "json"})
	if err != nil {
		t.Fatalf("re-plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "migrate.task_authority.plan.already_migrated") {
		t.Fatalf("re-plan output = %s", out)
	}
}

func TestMigrateTaskAuthorityPlanWritesPrivateDeterministicPlan(t *testing.T) {
	home := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "1.json"), `{
  "schema_version": "munsu.task-aggregate/v1",
  "task_id": "ship-1",
  "generation": "1",
  "current": true,
  "owner": "captain:api",
  "state": "working"
}
`)
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "current"), "1\n")
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if _, err := runMigrateCommand([]string{"migrate", "task-authority", "plan", "--home", home, "--plan-out", planPath, "--output", "json"}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	info, err := os.Stat(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("plan mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		SchemaVersion string `json:"schema_version"`
		Command       string `json:"command"`
		SourceDigest  string `json:"source_digest"`
		RecordCount   int    `json:"record_count"`
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != "munsu.task-authority-migration/v1" || plan.Command == "" || len(plan.SourceDigest) != 64 || plan.RecordCount != 1 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestMigrateTaskAuthorityApplyRefusesQuarantine(t *testing.T) {
	home := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "1.json"), `{
  "schema_version": "munsu.task-aggregate/v1",
  "task_id": "ship-1",
  "generation": "1",
  "current": true,
  "owner": "captain:api",
  "state": "paused"
}
`)
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "current"), "1\n")
	planPath := filepath.Join(t.TempDir(), "plan.json")
	out, err := runMigrateCommand([]string{"migrate", "task-authority", "plan", "--home", home, "--plan-out", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "quarantined=1") {
		t.Fatalf("plan output did not report quarantine: %s", out)
	}
	out, err = runMigrateCommand([]string{"migrate", "task-authority", "apply", "--plan", planPath, "--output", "json"})
	if err == nil || !strings.Contains(err.Error(), "quarantines 1 source record") {
		t.Fatalf("apply on quarantined plan should fail, err=%v out=%s", err, out)
	}
	// Source remains untouched.
	if _, err := os.Stat(filepath.Join(home, "state", ".task-authority", "aggregates", "ship-1", "1.json")); err != nil {
		t.Fatalf("v1 source was touched: %v", err)
	}
}
