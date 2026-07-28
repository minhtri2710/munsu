package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- ValidateCheck tests ---

func TestValidateCheck_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	writeExecScript(t, path, "#!/bin/bash\necho hello\n")
	if err := ValidateCheck(path); err != nil {
		t.Errorf("expected valid check, got: %v", err)
	}
}

func TestValidateCheck_NotFound(t *testing.T) {
	err := ValidateCheck("/nonexistent/check.check")
	if err == nil {
		t.Fatal("expected error for missing check")
	}
}

func TestValidateCheck_NotExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	if err := os.WriteFile(path, []byte("#!/bin/bash\necho\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := ValidateCheck(path)
	if err == nil {
		t.Fatal("expected error for non-executable check")
	}
}

func TestValidateCheck_MissingShebang(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	writeExecScript(t, path, "echo hello\n")
	err := ValidateCheck(path)
	if err == nil {
		t.Fatal("expected error for missing shebang")
	}
}

func TestValidateCheck_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	writeExecScript(t, path, "")
	err := ValidateCheck(path)
	if err == nil {
		t.Fatal("expected error for empty check")
	}
}

func TestValidateCheck_DirectoryInsteadOfFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	err := ValidateCheck(path)
	if err == nil {
		t.Fatal("expected error for directory")
	}
}

// --- MigrateOrRefuseStale tests ---

func TestMigrateOrRefuseStale_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task-1.check")
	writeExecScript(t, path, "#!/bin/bash\necho poll\n")
	migrated, err := MigrateOrRefuseStale(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Error("expected valid check to be accepted (migrated=true)")
	}
}

func TestMigrateOrRefuseStale_ZeroLength(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.check")
	writeExecScript(t, path, "")
	migrated, err := MigrateOrRefuseStale(path)
	if err == nil {
		t.Fatal("expected error for zero-length check")
	}
	if migrated {
		t.Error("expected zero-length check to be refused")
	}
	// File should have been removed
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("expected zero-length check to be removed")
	}
}

func TestMigrateOrRefuseStale_NoShebang(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.check")
	writeExecScript(t, path, "echo hello\n")
	migrated, err := MigrateOrRefuseStale(path)
	if err == nil {
		t.Fatal("expected error for no-shebang check")
	}
	if migrated {
		t.Error("expected no-shebang check to be refused")
	}
	// File should remain (no valid shebang means unsafe to remove)
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		t.Error("expected no-shebang check to remain for inspection")
	}
}

func TestMigrateOrRefuseStale_MetaNewer(t *testing.T) {
	dir := t.TempDir()
	checkPath := filepath.Join(dir, "task-1.check")
	writeExecScript(t, checkPath, "#!/bin/bash\necho poll\n")

	// Write a .meta file with newer modification time
	metaPath := filepath.Join(dir, "task-1.meta")
	if err := os.WriteFile(metaPath, []byte("kind=ship\npr_url=...\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Set meta modification time to be after check
	checkFI, _ := os.Stat(checkPath)
	future := checkFI.ModTime().Add(time.Hour)
	os.Chtimes(metaPath, future, future)

	migrated, err := MigrateOrRefuseStale(checkPath)
	if err == nil {
		t.Fatal("expected error for stale check (meta newer)")
	}
	if migrated {
		t.Error("expected stale check to be refused (meta newer)")
	}
}

func TestMigrateOrRefuseStale_NotStale(t *testing.T) {
	dir := t.TempDir()

	// Write meta BEFORE check so check mod time is newer
	metaPath := filepath.Join(dir, "task-2.meta")
	if err := os.WriteFile(metaPath, []byte("kind=ship\npr_url=...\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Sleep briefly so check mod time is definitely after meta
	time.Sleep(10 * time.Millisecond)

	checkPath := filepath.Join(dir, "task-2.check")
	writeExecScript(t, checkPath, "#!/bin/bash\necho poll\n")

	migrated, err := MigrateOrRefuseStale(checkPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Error("expected valid non-stale check to be accepted")
	}
}

// --- DiscoverPerTaskChecks tests ---

func TestDiscoverPerTaskChecks_FindsCheckFiles(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0755)

	writeExecScript(t, filepath.Join(stateDir, "task-1.check"), "#!/bin/bash\necho 1\n")
	writeExecScript(t, filepath.Join(stateDir, "task-2.check"), "#!/bin/bash\necho 2\n")

	plugins, err := DiscoverPerTaskChecks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}
	// Verify labels
	labels := map[string]bool{}
	for _, p := range plugins {
		if p.Kind != CheckPerTask {
			t.Errorf("expected CheckPerTask kind, got %v", p.Kind)
		}
		labels[p.Label] = true
	}
	if !labels["task-1"] || !labels["task-2"] {
		t.Errorf("expected task-1 and task-2, got %v", labels)
	}
}

func TestDiscoverPerTaskChecks_IgnoresNonCheckFiles(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0755)

	writeExecScript(t, filepath.Join(stateDir, "task-1.check"), "#!/bin/bash\necho 1\n")
	os.WriteFile(filepath.Join(stateDir, "task-1.meta"), []byte("kind=ship\n"), 0644)
	os.WriteFile(filepath.Join(stateDir, "task-1.status"), []byte("working: yes\n"), 0644)

	plugins, err := DiscoverPerTaskChecks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
}

func TestDiscoverPerTaskChecks_NoStateDir(t *testing.T) {
	plugins, err := DiscoverPerTaskChecks(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected 0 plugins without state dir, got %d", len(plugins))
	}
}

func TestDiscoverPerTaskChecks_NoCheckFiles(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0755)

	plugins, err := DiscoverPerTaskChecks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected 0 plugins without .check files, got %d", len(plugins))
	}
}

func TestDiscoverPerTaskChecks_IgnoresDotfilePrefixed(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0755)

	writeExecScript(t, filepath.Join(stateDir, ".hidden.check"), "#!/bin/bash\necho\n")

	plugins, err := DiscoverPerTaskChecks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected 0 plugins for dotfile check, got %d", len(plugins))
	}
}

// --- DiscoverGlobalChecks tests ---

func TestDiscoverGlobalChecks_FindsCheckFiles(t *testing.T) {
	dir := t.TempDir()
	checksDir := filepath.Join(dir, "state", "checks")
	os.MkdirAll(checksDir, 0755)

	writeExecScript(t, filepath.Join(checksDir, "health.check"), "#!/bin/bash\necho health\n")
	writeExecScript(t, filepath.Join(checksDir, "ci.check"), "#!/bin/bash\necho ci\n")

	plugins, err := DiscoverGlobalChecks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("expected 2 global plugins, got %d", len(plugins))
	}
	labels := map[string]bool{}
	for _, p := range plugins {
		if p.Kind != CheckGlobal {
			t.Errorf("expected CheckGlobal kind, got %v", p.Kind)
		}
		labels[p.Label] = true
	}
	if !labels["global:health"] || !labels["global:ci"] {
		t.Errorf("expected global:health and global:ci, got %v", labels)
	}
}

func TestDiscoverGlobalChecks_NoChecksDir(t *testing.T) {
	plugins, err := DiscoverGlobalChecks(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected 0 plugins without checks dir, got %d", len(plugins))
	}
}

func TestDiscoverGlobalChecks_IgnoresNonCheck(t *testing.T) {
	dir := t.TempDir()
	checksDir := filepath.Join(dir, "state", "checks")
	os.MkdirAll(checksDir, 0755)

	writeExecScript(t, filepath.Join(checksDir, "active.check"), "#!/bin/bash\necho active\n")
	os.WriteFile(filepath.Join(checksDir, "readme.txt"), []byte("notes\n"), 0644)

	plugins, err := DiscoverGlobalChecks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected 1 global plugin, got %d", len(plugins))
	}
}

// --- DiscoverAllChecks tests ---

func TestDiscoverAllChecks_CombinesBoth(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	checksDir := filepath.Join(stateDir, "checks")
	os.MkdirAll(checksDir, 0755)

	writeExecScript(t, filepath.Join(stateDir, "task-1.check"), "#!/bin/bash\necho 1\n")
	writeExecScript(t, filepath.Join(checksDir, "global-1.check"), "#!/bin/bash\necho g1\n")

	plugins, err := DiscoverAllChecks(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("expected 2 total plugins, got %d", len(plugins))
	}
	// Check we have one per-task and one global
	var perTask, global int
	for _, p := range plugins {
		switch p.Kind {
		case CheckPerTask:
			perTask++
		case CheckGlobal:
			global++
		}
	}
	if perTask != 1 {
		t.Errorf("expected 1 per-task, got %d", perTask)
	}
	if global != 1 {
		t.Errorf("expected 1 global, got %d", global)
	}
}

// --- helpers ---

// writeExecScript writes a script with 0755 permissions for use as a check plugin.
func writeExecScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("writing exec script: %v", err)
	}
}
