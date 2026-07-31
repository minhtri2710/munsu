package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
)

func TestMigrateWakeResolutionsPlanWritesReviewedPlanWithoutMutatingHome(t *testing.T) {
	homeDir := writeCLILegacyWakeResolutions(t)
	planPath := filepath.Join(t.TempDir(), "wake-plan.json")
	before := snapshotCLITree(t, homeDir)

	out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "plan", "--home", homeDir, "--plan-out", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("migrate wake-resolutions plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"kind": "migrate.wake_resolutions.plan"`) || !strings.Contains(out, `"status": "success"`) {
		t.Fatalf("unexpected migration output: %s", out)
	}
	if !strings.Contains(out, "apply=munsu migrate wake-resolutions apply --plan") {
		t.Fatalf("plan output missing exact apply command: %s", out)
	}
	if after := snapshotCLITree(t, homeDir); after != before {
		t.Fatalf("plan mutated target home\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := home.ReadWakeResolutionMigrationPlan(planPath); err != nil {
		t.Fatalf("reviewed plan not readable at %s: %v", planPath, err)
	}
}

func TestMigrateWakeResolutionsPlanRequiresPlanOut(t *testing.T) {
	homeDir := writeCLILegacyWakeResolutions(t)
	out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "plan", "--home", homeDir, "--output", "json"})
	if err == nil || !strings.Contains(err.Error(), "--plan-out is required") {
		t.Fatalf("plan without --plan-out should fail, err=%v out=%s", err, out)
	}
}

func TestMigrateWakeResolutionsApplyRequiresReviewedPlan(t *testing.T) {
	homeDir := writeCLILegacyWakeResolutions(t)

	out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "apply", "--home", homeDir, "--output", "json"})
	if err == nil || !strings.Contains(err.Error(), "--plan is required") {
		t.Fatalf("apply without --plan should fail with usage error, err=%v out=%s", err, out)
	}
}

func TestMigrateWakeResolutionsApplyCommandUsesExactPlan(t *testing.T) {
	homeDir := writeCLILegacyWakeResolutions(t)
	planPath := filepath.Join(t.TempDir(), "wake-plan.json")
	if out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "plan", "--home", homeDir, "--plan-out", planPath, "--output", "json"}); err != nil {
		t.Fatalf("migrate wake-resolutions plan: %v\n%s", err, out)
	}

	out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "apply", "--plan", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("migrate wake-resolutions apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"kind": "migrate.wake_resolutions.apply"`) || !strings.Contains(out, `"status": "success"`) {
		t.Fatalf("unexpected migration output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", ".wake-resolution-migration", "receipt.json")); err != nil {
		t.Fatalf("receipt missing: %v", err)
	}
}

func TestMigrateWakeResolutionsApplyRejectsChangedSourceAfterPlan(t *testing.T) {
	homeDir := writeCLILegacyWakeResolutions(t)
	planPath := filepath.Join(t.TempDir(), "wake-plan.json")
	if out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "plan", "--home", homeDir, "--plan-out", planPath, "--output", "json"}); err != nil {
		t.Fatalf("migrate wake-resolutions plan: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", ".wake-resolutions"), []byte("100\tlease-1\t100:1\tchanged\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "apply", "--plan", planPath, "--output", "json"})
	if err == nil || !strings.Contains(err.Error(), "source digest changed") {
		t.Fatalf("apply should reject changed source, err=%v out=%s", err, out)
	}
}

func TestMigrateWakeResolutionsFleetApplyReportsPartialStaleSource(t *testing.T) {
	parent := t.TempDir()
	good := writeCLILegacyWakeResolutions(t)
	stale := writeCLILegacyWakeResolutions(t)
	if err := fleet.Register(parent, "good", good, "captain", "project-a"); err != nil {
		t.Fatal(err)
	}
	if err := fleet.Register(parent, "stale", stale, "captain", "project-b"); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "fleet-plan.json")
	if out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "fleet-plan", "--home", parent, "--plan-out", planPath, "--output", "json"}); err != nil {
		t.Fatalf("fleet plan: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(stale, "state", ".wake-resolutions"), []byte("100\tlease-1\t100:1\tchanged\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runMigrateCommand([]string{"migrate", "wake-resolutions", "fleet-apply", "--plan", planPath, "--output", "json"})
	if err != nil {
		t.Fatalf("fleet apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"kind": "migrate.wake_resolutions.fleet_apply"`) || !strings.Contains(out, `"status": "applied"`) || !strings.Contains(out, "source digest changed") {
		t.Fatalf("fleet apply missing partial outcomes: %s", out)
	}
	if _, err := os.Stat(filepath.Join(good, "state", ".wake-resolution-migration", "receipt.json")); err != nil {
		t.Fatalf("good home did not apply independently: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stale, "state", ".wake-resolution-migration", "receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("stale home should not have receipt: %v", err)
	}
}

func writeCLILegacyWakeResolutions(t *testing.T) string {
	t.Helper()
	homeDir := filepath.Join(t.TempDir(), "general")
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, "state", ".wake-resolutions"), []byte("100\tlease-1\t100:1\tchecked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return homeDir
}

func runMigrateCommand(args []string) (string, error) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func snapshotCLITree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		b.WriteString(rel)
		if d.IsDir() {
			b.WriteString("/\n")
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.WriteString(" ")
		b.WriteString(sha256HexCLI(data))
		b.WriteString("\n")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func sha256HexCLI(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
