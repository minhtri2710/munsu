package configmigration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

func TestNeedsConfigMigration_NoLegacyFiles(t *testing.T) {
	homeDir := t.TempDir()
	needed, cmd := NeedsConfigMigration(homeDir)
	if needed {
		t.Errorf("expected no migration needed, got cmd=%s", cmd)
	}
}

func TestNeedsConfigMigration_LegacyFilesPresent(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte("# Captains\n"), 0644)

	needed, cmd := NeedsConfigMigration(homeDir)
	if !needed {
		t.Fatal("expected migration needed")
	}
	if !strings.Contains(cmd, "munsu migrate config") {
		t.Errorf("expected migrate command, got %s", cmd)
	}
}

func TestNeedsConfigMigration_TypedAlreadyPresent(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte("# Captains\n"), 0644)

	// Create typed documents (simulating already migrated).
	base := config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion}
	if err := config.StoreFleetBase(homeDir, base); err != nil {
		t.Fatal(err)
	}
	captains := config.CaptainRegistryDocument{SchemaVersion: config.CaptainRegistrySchemaVersion}
	if err := config.StoreCaptainRegistry(homeDir, captains); err != nil {
		t.Fatal(err)
	}
	projects := config.ProjectRegistryDocument{SchemaVersion: config.ProjectRegistrySchemaVersion}
	if err := config.StoreProjectRegistry(homeDir, projects); err != nil {
		t.Fatal(err)
	}

	needed, _ := NeedsConfigMigration(homeDir)
	if needed {
		t.Error("expected no migration needed when typed docs already present")
	}
}

func TestPlanConfigMigration_NoLegacyFiles(t *testing.T) {
	homeDir := t.TempDir()
	_, err := PlanConfigMigration(homeDir)
	if err == nil {
		t.Fatal("expected error for no legacy files")
	}
}

func TestPlanConfigMigration_SingleFile(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	content := "- my-project - A test project (added 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(content), 0644)

	plan, err := PlanConfigMigration(homeDir)
	if err != nil {
		t.Fatalf("PlanConfigMigration: %v", err)
	}
	if len(plan.LegacyFiles) != 1 {
		t.Fatalf("expected 1 legacy file, got %d", len(plan.LegacyFiles))
	}
	if plan.LegacyFiles[0].Name != "projects.md" {
		t.Errorf("expected projects.md, got %s", plan.LegacyFiles[0].Name)
	}
	if plan.ProjectRegistry == nil {
		t.Fatal("expected project registry in plan")
	}
	if len(plan.ProjectRegistry.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(plan.ProjectRegistry.Projects))
	}
	if plan.ProjectRegistry.Projects[0].Name != "my-project" {
		t.Errorf("project name = %q, want my-project", plan.ProjectRegistry.Projects[0].Name)
	}
}

func TestPlanConfigMigration_LegacyCaptains(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	capContent := "- captain-1 - (home: /home/captain-1; scope: test; projects: project-a; added: 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte(capContent), 0644)
	projContent := "- project-a - /tmp/project-a (added 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(projContent), 0644)

	plan, err := PlanConfigMigration(homeDir)
	if err != nil {
		t.Fatalf("PlanConfigMigration: %v", err)
	}
	if len(plan.LegacyFiles) != 2 {
		t.Fatalf("expected 2 legacy files, got %d", len(plan.LegacyFiles))
	}
	if plan.CaptainRegistry == nil {
		t.Fatal("expected captain registry in plan")
	}
	if len(plan.CaptainRegistry.Captains) != 1 {
		t.Fatalf("expected 1 captain, got %d", len(plan.CaptainRegistry.Captains))
	}
	if plan.CaptainRegistry.Captains[0].ID != "captain-1" {
		t.Errorf("captain id = %q, want captain-1", plan.CaptainRegistry.Captains[0].ID)
	}
}

func TestPlanConfigMigration_ConflictingBindings(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	// Captain references unknown project.
	content := "- captain-1 - (home: /home/captain-1; scope: test; projects: unknown-project; added: 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte(content), 0644)
	projContent := "- my-project - /tmp/my-project (added 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(projContent), 0644)

	_, err := PlanConfigMigration(homeDir)
	if err == nil {
		t.Fatal("expected error for conflicting bindings")
	}
	if !strings.Contains(err.Error(), "source untouched") && !strings.Contains(err.Error(), "unknown project") {
		t.Errorf("error = %v, want binding conflict error", err)
	}
}

func TestPlanConfigMigration_ConflictingDuplicate(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	// Two captains owning the same project.
	content := "- captain-1 - (home: /home/captain-1; scope: test; projects: project-a; added: 2026-01-15)\n" +
		"- captain-2 - (home: /home/captain-2; scope: test; projects: project-a; added: 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte(content), 0644)
	projContent := "- project-a - /tmp/project-a (added 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(projContent), 0644)

	_, err := PlanConfigMigration(homeDir)
	if err == nil {
		t.Fatal("expected error for duplicate project ownership")
	}
	if !strings.Contains(err.Error(), "source untouched") && !strings.Contains(err.Error(), "already owned") {
		t.Errorf("error = %v, want duplicate ownership error", err)
	}
}

func TestApplyConfigMigration_PlanAndApply(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)

	// Create legacy files.
	capContent := "# Captains\n- captain-1 - (home: /home/captain-1; scope: test; projects: project-a; added: 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte(capContent), 0644)
	projContent := "- project-a - /tmp/project-a (added 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(projContent), 0644)

	// Plan migration.
	plan, err := PlanConfigMigration(homeDir)
	if err != nil {
		t.Fatalf("PlanConfigMigration: %v", err)
	}

	// Apply migration.
	receipt, err := ApplyConfigMigration(plan)
	if err != nil {
		t.Fatalf("ApplyConfigMigration: %v", err)
	}

	// Verify legacy files are removed.
	if _, err := os.Stat(filepath.Join(homeDir, "data", "captains.md")); !os.IsNotExist(err) {
		t.Error("captains.md should be removed")
	}
	if _, err := os.Stat(filepath.Join(homeDir, "data", "projects.md")); !os.IsNotExist(err) {
		t.Error("projects.md should be removed")
	}

	// Verify archive exists.
	if receipt.ArchivePath == "" {
		t.Fatal("expected archive path")
	}
	if _, err := os.Stat(receipt.ArchivePath); os.IsNotExist(err) {
		t.Fatal("archive directory should exist")
	}
	if _, err := os.Stat(filepath.Join(receipt.ArchivePath, "receipt.json")); os.IsNotExist(err) {
		t.Fatal("receipt should exist in archive")
	}

	// Verify typed documents are installed.
	if _, err := os.Stat(filepath.Join(homeDir, config.BaseDocumentPath)); os.IsNotExist(err) {
		t.Error("fleet base document should exist")
	}
	if _, err := os.Stat(filepath.Join(homeDir, config.CaptainDocumentPath)); os.IsNotExist(err) {
		t.Error("captain registry document should exist")
	}
	if _, err := os.Stat(filepath.Join(homeDir, config.ProjectDocumentPath)); os.IsNotExist(err) {
		t.Error("project registry document should exist")
	}

	// Verify typed documents are valid.
	base, err := config.LoadFleetBase(homeDir)
	if err != nil {
		t.Fatalf("loading fleet base: %v", err)
	}
	if base.SchemaVersion != config.FleetBaseSchemaVersion {
		t.Errorf("schema version = %q, want %q", base.SchemaVersion, config.FleetBaseSchemaVersion)
	}

	captains, err := config.LoadCaptainRegistry(homeDir)
	if err != nil {
		t.Fatalf("loading captain registry: %v", err)
	}
	if len(captains.Captains) != 1 {
		t.Fatalf("expected 1 captain, got %d", len(captains.Captains))
	}

	projects, err := config.LoadProjectRegistry(homeDir)
	if err != nil {
		t.Fatalf("loading project registry: %v", err)
	}
	if len(projects.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects.Projects))
	}

	// Verify receipt has correct counts.
	if len(receipt.LegacyFiles) != 2 {
		t.Fatalf("expected 2 legacy files in receipt, got %d", len(receipt.LegacyFiles))
	}
}

func TestApplyConfigMigration_Idempotent(t *testing.T) {
	// Run migration twice - second run should be a no-op.
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "config"), 0755)

	capContent := "- captain-1 - (home: /home/captain-1; scope: test; projects: project-a; added: 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte(capContent), 0644)
	projContent := "- project-a - /tmp/project-a (added 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(projContent), 0644)

	// First migration.
	plan, err := PlanConfigMigration(homeDir)
	if err != nil {
		t.Fatalf("first PlanConfigMigration: %v", err)
	}
	_, err = ApplyConfigMigration(plan)
	if err != nil {
		t.Fatalf("first ApplyConfigMigration: %v", err)
	}

	// Get snapshot of typed documents.
	base1, _ := config.LoadFleetBase(homeDir)
	captains1, _ := config.LoadCaptainRegistry(homeDir)
	projects1, _ := config.LoadProjectRegistry(homeDir)

	// Second migration - should be idempotent.
	plan2, err := PlanConfigMigration(homeDir)
	if err == nil {
		t.Fatal("expected error for second plan (no legacy files)")
	}
	_ = plan2 // unused; second plan is expected to fail

	// Verify typed documents are unchanged.
	base2, _ := config.LoadFleetBase(homeDir)
	captains2, _ := config.LoadCaptainRegistry(homeDir)
	projects2, _ := config.LoadProjectRegistry(homeDir)

	if len(base1.Config.SoldierHarness) != len(base2.Config.SoldierHarness) {
		t.Error("fleet base changed after second migration attempt")
	}
	if len(captains1.Captains) != len(captains2.Captains) {
		t.Error("captain registry changed after second migration attempt")
	}
	if len(projects1.Projects) != len(projects2.Projects) {
		t.Error("project registry changed after second migration attempt")
	}
}

func TestApplyConfigMigration_EmptyCaptainRegistry(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)

	// Legacy captains.md with only a header line: valid but empty registry.
	os.WriteFile(filepath.Join(homeDir, "data", "captains.md"), []byte("# Captains\n"), 0644)

	plan, err := PlanConfigMigration(homeDir)
	if err != nil {
		t.Fatalf("PlanConfigMigration: %v", err)
	}

	receipt, err := ApplyConfigMigration(plan)
	if err != nil {
		t.Fatalf("ApplyConfigMigration: %v", err)
	}

	// Legacy file must be archived and removed.
	if _, err := os.Stat(filepath.Join(homeDir, "data", "captains.md")); !os.IsNotExist(err) {
		t.Error("captains.md should be removed")
	}
	if _, err := os.Stat(filepath.Join(receipt.ArchivePath, "captains.md")); os.IsNotExist(err) {
		t.Error("legacy captains.md should be archived")
	}
	if _, err := os.Stat(filepath.Join(receipt.ArchivePath, "receipt.json")); os.IsNotExist(err) {
		t.Error("receipt should exist in archive")
	}

	// Canonical document must be materialized even for an empty registry.
	path := filepath.Join(homeDir, config.CaptainDocumentPath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("canonical captain registry not materialized: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}

	captains, err := config.LoadCaptainRegistry(homeDir)
	if err != nil {
		t.Fatalf("loading captain registry: %v", err)
	}
	if captains.SchemaVersion != config.CaptainRegistrySchemaVersion {
		t.Errorf("schema = %q, want %q", captains.SchemaVersion, config.CaptainRegistrySchemaVersion)
	}
	if len(captains.Captains) != 0 {
		t.Fatalf("expected 0 captains, got %d", len(captains.Captains))
	}
	if !strings.Contains(string(data), `"captains": []`) {
		t.Errorf("expected literal `captains: []` in canonical document, got:\n%s", data)
	}

	// Receipt records the digest of the written document.
	if receipt.CaptainDigest == "" {
		t.Error("expected captain digest in receipt")
	}

	// Session-start paths must load cleanly after apply.
	if needed, _ := NeedsConfigMigration(homeDir); needed {
		t.Error("expected no migration needed after apply")
	}
	if _, _, _, err := config.LoadDocuments(homeDir); err != nil {
		t.Fatalf("LoadDocuments after apply: %v", err)
	}

	// Reapply is idempotent: a second plan fails and documents are unchanged.
	if _, err := PlanConfigMigration(homeDir); err == nil {
		t.Fatal("expected second plan to fail (no legacy files)")
	}
	reload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(reload) != string(data) {
		t.Error("canonical document changed between reads")
	}
}

func TestApplyConfigMigration_StalePlan(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "data"), 0755)
	projContent := "- project-a - /tmp/project-a (added 2026-01-15)\n"
	os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte(projContent), 0644)

	plan, err := PlanConfigMigration(homeDir)
	if err != nil {
		t.Fatalf("PlanConfigMigration: %v", err)
	}

	// Change the source file after plan.
	os.WriteFile(filepath.Join(homeDir, "data", "projects.md"), []byte("- project-b - /tmp/project-b (added 2026-01-15)\n"), 0644)

	_, err = ApplyConfigMigration(plan)
	if err == nil {
		t.Fatal("expected error for stale plan")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error = %v, want stale plan error", err)
	}
}

func TestLegacyConfigError(t *testing.T) {
	err := LegacyConfigCheckError("/tmp/test-home")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "munsu migrate config") {
		t.Errorf("error = %v, want migrate command", err)
	}
}

func TestMigrationCommand(t *testing.T) {
	cmd := MigrationCommand("/tmp/test-home")
	if !strings.Contains(cmd, "munsu migrate config") {
		t.Errorf("command = %q, want munsu migrate config", cmd)
	}
}