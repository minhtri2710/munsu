package secondmate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

func TestBuildLaunchArgs_VerifiedHarnesses(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)

	tests := []struct {
		name      string
		harness   string
		wantBin   string
	}{
		{"claude", harness.Claude, "claude"},
		{"codex", harness.Codex, "codex"},
		{"opencode", harness.Opencode, "opencode"},
		{"pi", harness.Pi, "pi"},
		{"grok", harness.Grok, "grok"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binName, args, err := buildLaunchArgs(smHome, tt.harness, tmp)
			if err != nil {
				t.Fatalf("buildLaunchArgs(%q) error: %v", tt.harness, err)
			}
			if binName != tt.wantBin {
				t.Errorf("binName = %q, want %q", binName, tt.wantBin)
			}
			// Verify secondmate home is in args
			found := false
			for _, a := range args {
				if a == smHome {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("args should contain secondmate home %q, got %v", smHome, args)
			}
			// Verify AGENTS.md is referenced in args
			hasPrompt := false
			for _, a := range args {
				if strings.Contains(a, "AGENTS.md") {
					hasPrompt = true
					break
				}
			}
			if !hasPrompt {
				t.Error("args should reference AGENTS.md")
			}
			// Verify -- separator is present
			hasSep := false
			for _, a := range args {
				if a == "--" {
					hasSep = true
					break
				}
			}
			if !hasSep {
				t.Error("args should contain -- separator")
			}
		})
	}
}

func TestBuildLaunchArgs_UnknownHarness(t *testing.T) {
	_, _, err := buildLaunchArgs("/tmp", "unknown_harness", "/tmp")
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if !strings.Contains(err.Error(), "not a verified harness") {
		t.Errorf("error should mention unverified harness, got: %v", err)
	}
}

func TestBuildLaunchArgs_ConfigModelPropagation(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)

	// Set model config
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "model"), []byte("claude-sonnet-4-20250515\n"), 0644)

	binName, args, err := buildLaunchArgs(smHome, harness.Claude, tmp)
	if err != nil {
		t.Fatalf("buildLaunchArgs error: %v", err)
	}
	if binName != "claude" {
		t.Errorf("binName = %q, want %q", binName, "claude")
	}

	// Verify --model flag and value are in args
	foundModel := false
	for i, a := range args {
		if a == "--model" && i+1 < len(args) && args[i+1] == "claude-sonnet-4-20250515" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Errorf("args should contain --model claude-sonnet-4-20250515, got: %v", args)
	}
}

func TestLaunch_HarnessBinaryNotFound(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)

	// Configure secondmate-harness = claude
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("claude\n"), 0644)

	// Use a PATH that definitely doesn't have claude
	t.Setenv("PATH", tmp)

	err := Launch(smHome, tmp)
	if err == nil {
		t.Fatal("expected error for harness binary not on PATH")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error should mention PATH, got: %v", err)
	}
}


func TestSeed_CreatesDirectoryStructure(t *testing.T) {
	tmp := t.TempDir()
	homePath := filepath.Join(tmp, "secondmates", "test-sm")
	charter := "# Secondmate charter\n\nPersistent domain supervisor.\n"

	if err := Seed("test-sm", homePath, charter); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(homePath); os.IsNotExist(err) {
		t.Fatalf("home dir %s was not created", homePath)
	}

	for _, dir := range []string{"state", "data", "config", "projects"} {
		p := filepath.Join(homePath, dir)
		if fi, err := os.Stat(p); err != nil {
			t.Errorf("subdirectory %s not created: %v", dir, err)
		} else if !fi.IsDir() {
			t.Errorf("%s exists but is not a directory", p)
		}
	}

	agentsPath := filepath.Join(homePath, "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != charter {
		t.Errorf("AGENTS.md content = %q, want %q", string(data), charter)
	}
}

func TestSeed_InvalidPath(t *testing.T) {
	err := Seed("test-sm", "/nonexistent/parent/sm", "# charter")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestHandoff_Basic(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(filepath.Join(sm, "state"), 0755)

	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	metaContent := "window=@0\nkind=ship\n"
	if err := os.WriteFile(filepath.Join(parent, "state", "TASK-1.meta"), []byte(metaContent), 0644); err != nil {
		t.Fatal(err)
	}
	statusContent := "working: spawned\ndone: completed\n"
	if err := os.WriteFile(filepath.Join(parent, "state", "TASK-1.status"), []byte(statusContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(sm, "state", "TASK-1.meta")); os.IsNotExist(err) {
		t.Error("TASK-1.meta was not copied to secondmate")
	}
	if _, err := os.Stat(filepath.Join(sm, "state", "TASK-1.status")); os.IsNotExist(err) {
		t.Error("TASK-1.status was not copied to secondmate")
	}

	if _, err := os.Stat(filepath.Join(parent, "state", "TASK-1.meta")); !os.IsNotExist(err) {
		t.Error("TASK-1.meta was not removed from parent")
	}
	if _, err := os.Stat(filepath.Join(parent, "state", "TASK-1.status")); !os.IsNotExist(err) {
		t.Error("TASK-1.status was not removed from parent")
	}
}

func TestHandoff_MultipleItems(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(filepath.Join(sm, "state"), 0755)

	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	for _, id := range []string{"TASK-1", "TASK-2", "TASK-3"} {
		os.WriteFile(filepath.Join(parent, "state", id+".meta"), []byte("kind=ship\n"), 0644)
		os.WriteFile(filepath.Join(parent, "state", id+".status"), []byte("working: spawned\n"), 0644)
	}

	if err := Handoff(parent, sm, []string{"TASK-1", "TASK-2", "TASK-3"}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"TASK-1", "TASK-2", "TASK-3"} {
		if _, err := os.Stat(filepath.Join(sm, "state", id+".meta")); os.IsNotExist(err) {
			t.Errorf("%s.meta was not copied to secondmate", id)
		}
		if _, err := os.Stat(filepath.Join(parent, "state", id+".meta")); !os.IsNotExist(err) {
			t.Errorf("%s.meta was not removed from parent", id)
		}
	}
}

func TestHandoff_PartialMissingMeta(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(filepath.Join(sm, "state"), 0755)

	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.WriteFile(filepath.Join(parent, "state", "TASK-1.meta"), []byte("kind=ship\n"), 0644)
	os.WriteFile(filepath.Join(parent, "state", "TASK-1.status"), []byte("working: spawned\n"), 0644)

	if err := Handoff(parent, sm, []string{"TASK-1", "TASK-2"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(sm, "state", "TASK-1.meta")); os.IsNotExist(err) {
		t.Error("TASK-1 should have been handed off despite TASK-2 missing meta")
	}
	if _, err := os.Stat(filepath.Join(parent, "state", "TASK-1.meta")); !os.IsNotExist(err) {
		t.Error("TASK-1 meta should have been removed from parent")
	}
}

func TestHandoff_AutoCreatesStateDir(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")

	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.WriteFile(filepath.Join(parent, "state", "TASK-1.meta"), []byte("kind=ship\n"), 0644)

	if err := Handoff(parent, sm, []string{"TASK-1"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(sm, "state")); os.IsNotExist(err) {
		t.Error("secondmate state dir should have been created automatically")
	}
	if _, err := os.Stat(filepath.Join(sm, "state", "TASK-1.meta")); os.IsNotExist(err) {
		t.Error("TASK-1.meta should exist in secondmate")
	}
}

func TestList_Empty(t *testing.T) {
	parent := t.TempDir()
	mates, err := List(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 0 {
		t.Errorf("expected empty list, got %d entries", len(mates))
	}
}

func TestList_WithSecondmates(t *testing.T) {
	parent := t.TempDir()
	smDir := filepath.Join(parent, "secondmates")
	os.MkdirAll(smDir, 0755)

	for _, name := range []string{"sm-alpha", "sm-beta"} {
		os.MkdirAll(filepath.Join(smDir, name), 0755)
	}

	mates, err := List(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 2 {
		t.Errorf("expected 2 secondmates, got %d", len(mates))
	}

	found := map[string]bool{}
	for _, m := range mates {
		found[m.ID] = true
	}
	if !found["sm-alpha"] {
		t.Error("sm-alpha not found in list")
	}
	if !found["sm-beta"] {
		t.Error("sm-beta not found in list")
	}
}

func TestList_IgnoresFiles(t *testing.T) {
	parent := t.TempDir()
	smDir := filepath.Join(parent, "secondmates")
	os.MkdirAll(smDir, 0755)

	os.MkdirAll(filepath.Join(smDir, "valid-sm"), 0755)
	os.WriteFile(filepath.Join(smDir, "README.md"), []byte("not a secondmate\n"), 0644)

	mates, err := List(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 1 {
		t.Errorf("expected 1 secondmate (ignoring files), got %d", len(mates))
	}
	if mates[0].ID != "valid-sm" {
		t.Errorf("expected valid-sm, got %q", mates[0].ID)
	}
}

func TestRetire_RemoveHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)

	if err := Retire(smHome, true); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(smHome); !os.IsNotExist(err) {
		t.Error("secondmate home should have been removed")
	}
}

func TestRetire_KeepHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)

	if err := Retire(smHome, false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(smHome); os.IsNotExist(err) {
		t.Error("secondmate home should have been retained")
	}
}

func TestRetire_NonexistentHome(t *testing.T) {
	err := Retire("/nonexistent/sm", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRetire_WithLockFile(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.WriteFile(filepath.Join(smHome, "state", ".lock"), []byte("999999\n"), 0644)

	if err := Retire(smHome, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigPush_Basic(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)

	configDir := filepath.Join(parent, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "crew-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(configDir, "crew-dispatch.json"), []byte("{}\n"), 0644)
	os.WriteFile(filepath.Join(configDir, "model"), []byte("claude-sonnet\n"), 0644)

	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	// Only check files that exist in the parent config
	for _, name := range []string{"crew-harness", "crew-dispatch.json"} {
		_, err := os.Stat(filepath.Join(smHome, "config", name))
		if err != nil {
			t.Errorf("inheritable config %q was not copied: %v", name, err)
		}
	}

	if _, err := os.Stat(filepath.Join(smHome, "config", "model")); !os.IsNotExist(err) {
		t.Error("non-inheritable config 'model' should not have been copied")
	}
}

func TestConfigPush_MirrorDeletions(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")

	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.WriteFile(filepath.Join(smHome, "config", "crew-harness"), []byte("old\n"), 0644)

	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(smHome, "config", "crew-harness")); !os.IsNotExist(err) {
		t.Error("crew-harness should have been deleted (mirror deletion)")
	}
}

func TestConfigPush_OnlyInheritableDeleted(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)

	os.WriteFile(filepath.Join(smHome, "config", "crew-harness"), []byte("old\n"), 0644)
	os.WriteFile(filepath.Join(smHome, "config", "model"), []byte("some-model\n"), 0644)

	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(smHome, "config", "crew-harness")); !os.IsNotExist(err) {
		t.Error("inheritable crew-harness should have been deleted")
	}

	if _, err := os.Stat(filepath.Join(smHome, "config", "model")); os.IsNotExist(err) {
		t.Error("non-inheritable model should NOT have been deleted")
	}
}

func TestConfigPush_WritesLog(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)

	configDir := filepath.Join(parent, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "crew-harness"), []byte("pi\n"), 0644)

	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	logData, err := os.ReadFile(filepath.Join(smHome, "state", "config-push.log"))
	if err != nil {
		t.Fatalf("config-push.log not found: %v", err)
	}
	if !strings.Contains(string(logData), "pushed") {
		t.Errorf("log should contain 'pushed' action, got: %s", string(logData))
	}
}

func TestGetInheritableList_Default(t *testing.T) {
	os.Unsetenv("MUNSU_INHERITABLE_CONFIG")
	list := getInheritableList()
	expected := []string{"crew-harness", "crew-dispatch.json", "backlog-backend"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d items, got %d: %v", len(expected), len(list), list)
	}
	for i, v := range expected {
		if list[i] != v {
			t.Errorf("list[%d] = %q, want %q", i, list[i], v)
		}
	}
}

func TestGetInheritableList_EnvOverride(t *testing.T) {
	t.Setenv("MUNSU_INHERITABLE_CONFIG", "crew-harness:model:custom-config")
	list := getInheritableList()
	expected := []string{"crew-harness", "model", "custom-config"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d items, got %d: %v", len(expected), len(list), list)
	}
	for i, v := range expected {
		if list[i] != v {
			t.Errorf("list[%d] = %q, want %q", i, list[i], v)
		}
	}
}

func TestGetInheritableList_EmptyEnv(t *testing.T) {
	// Empty string env means "not set" — falls through to default list
	t.Setenv("MUNSU_INHERITABLE_CONFIG", "")
	list := getInheritableList()
	expected := []string{"crew-harness", "crew-dispatch.json", "backlog-backend"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d items, got %d: %v", len(expected), len(list), list)
	}
	for i, v := range expected {
		if list[i] != v {
			t.Errorf("list[%d] = %q, want %q", i, list[i], v)
		}
	}
}
