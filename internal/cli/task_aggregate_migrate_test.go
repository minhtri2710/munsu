package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateTaskAggregatesPlanAndApplyCommands(t *testing.T) {
	home := t.TempDir()
	writeTaskAggregateCLIFile(t, filepath.Join(home, "state", "ship-1.meta"), "description=Implement API\nowner=captain:api\ngeneration=7\n")
	planPath := filepath.Join(t.TempDir(), "task-aggregate-plan.json")

	out, err := runMigrateCommand([]string{"migrate", "task-aggregates", "plan", "--home", home, "--plan-out", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "migrate.task_aggregates.plan") || !strings.Contains(out, "Planned 1 task aggregate") {
		t.Fatalf("plan output = %s", out)
	}

	out, err = runMigrateCommand([]string{"migrate", "task-aggregates", "apply", "--plan", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "migrate.task_aggregates.apply") || !strings.Contains(out, "Migrated 1 task aggregate") {
		t.Fatalf("apply output = %s", out)
	}
}

func writeTaskAggregateCLIFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
