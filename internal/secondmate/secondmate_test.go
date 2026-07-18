package secondmate

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

func TestBuildLaunchArgs_VerifiedSecondmateHarness(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	charter := []byte("# Test charter\n\nFollow this exactly.\n")
	if err := os.WriteFile(filepath.Join(smHome, "AGENTS.md"), charter, 0644); err != nil {
		t.Fatal(err)
	}

	binName, args, err := buildLaunchArgs(smHome, harness.Pi, tmp)
	if err != nil {
		t.Fatalf("buildLaunchArgs() error: %v", err)
	}
	if binName != "pi" {
		t.Fatalf("binName = %q, want pi", binName)
	}
	wantArgs := []string{"--", smHome, string(charter)}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], wantArgs[i])
		}
	}
	if strings.Contains(args[len(args)-1], "$(cat") {
		t.Fatalf("prompt contains shell expression: %q", args[len(args)-1])
	}
}

func TestBuildLaunchArgs_UnverifiedSecondmateHarnesses(t *testing.T) {
	for _, name := range []string{harness.Claude, harness.Codex, harness.Opencode, harness.Grok, harness.Agy} {
		t.Run(name, func(t *testing.T) {
			_, _, err := buildLaunchArgs(t.TempDir(), name, t.TempDir())
			if err == nil {
				t.Fatal("expected unverified secondmate contract error")
			}
			if !strings.Contains(err.Error(), "does not have a verified secondmate launch contract") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBuildLaunchArgs_MissingCharterFailsClosed(t *testing.T) {
	_, _, err := buildLaunchArgs(t.TempDir(), harness.Pi, t.TempDir())
	if err == nil {
		t.Fatal("expected missing AGENTS.md error")
	}
	if !strings.Contains(err.Error(), "reading secondmate charter") {
		t.Fatalf("error = %v", err)
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
	model := "opencode-go/deepseek-v4-flash"
	os.WriteFile(filepath.Join(configDir, "model"), []byte(model+"\n"), 0644)

	binName, args, err := buildLaunchArgs(smHome, harness.Pi, tmp)
	if err != nil {
		t.Fatalf("buildLaunchArgs error: %v", err)
	}
	if binName != "pi" {
		t.Errorf("binName = %q, want %q", binName, "pi")
	}

	wantPrefix := []string{"--model", model, "--"}
	if len(args) < len(wantPrefix) {
		t.Fatalf("args = %v, want prefix %v", args, wantPrefix)
	}
	for i := range wantPrefix {
		if args[i] != wantPrefix[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], wantPrefix[i])
		}
	}
}

func TestLaunch_HarnessBinaryNotFound(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)

	// Configure secondmate-harness = pi
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("pi\n"), 0644)

	// Use a PATH that definitely doesn't have pi
	t.Setenv("PATH", tmp)

	err := Launch(smHome, tmp)
	if err == nil {
		t.Fatal("expected error for harness binary not on PATH")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error should mention PATH, got: %v", err)
	}
}

func TestLaunch_DeliversCharterAndWorkingDirectory(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	charter := []byte("# Secondmate\n\nObserve exactly.\n")
	if err := os.WriteFile(filepath.Join(smHome, "AGENTS.md"), charter, 0644); err != nil {
		t.Fatal(err)
	}

	configDir := filepath.Join(tmp, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	captureDir := filepath.Join(tmp, "capture")
	if err := os.MkdirAll(captureDir, 0755); err != nil {
		t.Fatal(err)
	}
	fakePi := filepath.Join(tmp, "pi")
	script := "#!/bin/sh\npwd > \"$CAPTURE_DIR/cwd\"\nprintf '%s\\0' \"$@\" > \"$CAPTURE_DIR/argv\"\n"
	if err := os.WriteFile(fakePi, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)
	t.Setenv("CAPTURE_DIR", captureDir)
	originalStart := startSecondmateProcess
	startSecondmateProcess = func(binPath string, args []string, dir string) (int, error) {
		cmd := exec.Command(binPath, args...)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		if err := cmd.Run(); err != nil {
			return 0, err
		}
		return os.Getpid(), nil
	}
	t.Cleanup(func() { startSecondmateProcess = originalStart })

	if err := Launch(smHome, tmp); err != nil {
		t.Fatalf("Launch() error: %v", err)
	}

	cwd, err := os.ReadFile(filepath.Join(captureDir, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	capturedDir := strings.TrimSpace(string(cwd))
	capturedInfo, err := os.Stat(capturedDir)
	if err != nil {
		t.Fatal(err)
	}
	wantInfo, err := os.Stat(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(capturedInfo, wantInfo) {
		t.Errorf("cwd = %q, want same directory as %q", capturedDir, smHome)
	}
	argv, err := os.ReadFile(filepath.Join(captureDir, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Join([][]byte{[]byte("--"), []byte(smHome), charter, nil}, []byte{0})
	if !bytes.Equal(argv, want) {
		t.Errorf("argv = %q, want %q", argv, want)
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
