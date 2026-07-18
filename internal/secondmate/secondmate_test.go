package secondmate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// --- BuildLaunchArgs tests (preserved from PR1) ---

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
	_ = wantArgs
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

// --- Seed tests ---

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

	markerData, err := os.ReadFile(filepath.Join(homePath, ProvenanceMarkerName))
	if err != nil {
		t.Fatal("provenance marker was not created:", err)
	}
	if !strings.Contains(string(markerData), "test-sm") {
		t.Errorf("provenance marker should contain id, got: %q", string(markerData))
	}
	if !strings.Contains(string(markerData), ProvenanceVersion) {
		t.Errorf("provenance marker should contain version, got: %q", string(markerData))
	}
}

func TestSeed_InvalidPath(t *testing.T) {
	err := Seed("test-sm", "/nonexistent/parent/sm", "# charter")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

// --- Provenance tests ---

func TestProvenance_SeedAndValidate(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(tmp, 0755)

	_, err := ValidateProvenance(tmp)
	if err == nil {
		t.Fatal("expected error for missing marker")
	}
	if !strings.Contains(err.Error(), "no .munsu-secondmate-home marker") {
		t.Errorf("error = %v", err)
	}

	if err := SeedProvenance(tmp, "test-id"); err != nil {
		t.Fatal(err)
	}

	id, err := ValidateProvenance(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if id != "test-id" {
		t.Errorf("id = %q, want %q", id, "test-id")
	}
}

func TestProvenance_InvalidFormat(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ProvenanceMarkerName), []byte("only-id\n"), 0644)
	_, err := ValidateProvenance(tmp)
	if err == nil {
		t.Fatal("expected error for malformed marker")
	}
}

func TestProvenance_WrongVersion(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ProvenanceMarkerName), []byte("old-v0\nsome-id\nsome/home\n"), 0644)
	_, err := ValidateProvenance(tmp)
	if err == nil {
		t.Fatal("expected error for wrong version")
	}
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("error = %v", err)
	}
}

// --- Validate / Migrate tests ---

func TestValidate_PassesForSeededHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	Seed("test-sm", smHome, "# charter")

	err := Validate(smHome, tmp)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidate_RefusesFakeName(t *testing.T) {
	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "fake")
	Seed("fake-sm", fakeHome, "# charter")

	err := Validate(fakeHome, tmp)
	if err == nil {
		t.Fatal("expected error for reserved name 'fake'")
	}
	if !strings.Contains(err.Error(), "reserved name") {
		t.Errorf("error = %v", err)
	}
}

func TestValidate_RefusesPrimaryName(t *testing.T) {
	tmp := t.TempDir()
	primaryHome := filepath.Join(tmp, "primary")
	Seed("primary-sm", primaryHome, "# charter")

	err := Validate(primaryHome, tmp)
	if err == nil {
		t.Fatal("expected error for reserved name 'primary'")
	}
}

func TestValidate_RefusesSelfParent(t *testing.T) {
	tmp := t.TempDir()
	Seed("test-sm", tmp, "# charter")

	err := Validate(tmp, tmp)
	if err == nil {
		t.Fatal("expected error for being parent home itself")
	}
	if !strings.Contains(err.Error(), "is the parent home itself") {
		t.Errorf("error = %v", err)
	}
}

func TestValidate_RefusesMissingDirs(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	SeedProvenance(smHome, "test-sm")

	err := Validate(smHome, tmp)
	if err == nil {
		t.Fatal("expected error for missing AGENTS.md")
	}
}

func TestMigrate_WritesMarkerToSeededHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")

	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.MkdirAll(filepath.Join(smHome, "data"), 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)

	if err := Migrate(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}

	id, err := ValidateProvenance(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if id != "test-sm" {
		t.Errorf("id = %q, want %q", id, "test-sm")
	}
}

func TestMigrate_RefusesReservedName(t *testing.T) {
	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "fake")
	os.MkdirAll(fakeHome, 0755)
	err := Migrate(fakeHome, "fake-sm")
	if err == nil {
		t.Fatal("expected error for reserved name")
	}
	if !strings.Contains(err.Error(), "reserved name") {
		t.Errorf("error = %v", err)
	}
}

// --- Registry tests ---

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

func TestList_WithRegistryFile(t *testing.T) {
	parent := t.TempDir()
	registryDir := filepath.Join(parent, "data")
	os.MkdirAll(registryDir, 0755)
	registryContent := `# Secondmates
- sm-alpha - Some charter (home: /home/sm-alpha; scope: domain dispatch; projects: project-a; added: 2026-07-18)
- sm-beta - Another charter (home: /home/sm-beta; scope: other domain; projects: project-b; added: 2026-07-17)
`
	os.WriteFile(filepath.Join(registryDir, "secondmates.md"), []byte(registryContent), 0644)

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
		if m.ID == "sm-alpha" {
			if m.Home != "/home/sm-alpha" {
				t.Errorf("sm-alpha home = %q, want %q", m.Home, "/home/sm-alpha")
			}
			if m.Scope != "domain dispatch" {
				t.Errorf("sm-alpha scope = %q", m.Scope)
			}
			if m.Project != "project-a" {
				t.Errorf("sm-alpha project = %q", m.Project)
			}
			if m.Added != "2026-07-18" {
				t.Errorf("sm-alpha added = %q", m.Added)
			}
		}
	}
	if !found["sm-alpha"] {
		t.Error("sm-alpha not found in list")
	}
	if !found["sm-beta"] {
		t.Error("sm-beta not found in list")
	}
}

func TestList_SkipsCommentLines(t *testing.T) {
	parent := t.TempDir()
	registryDir := filepath.Join(parent, "data")
	os.MkdirAll(registryDir, 0755)
	registryContent := `# Secondmates
# This is a comment
- valid-sm - Some charter (home: /home/valid-sm; scope: test; projects: test; added: 2026-07-18)
`
	os.WriteFile(filepath.Join(registryDir, "secondmates.md"), []byte(registryContent), 0644)

	mates, err := List(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 1 {
		t.Errorf("expected 1 secondmate, got %d", len(mates))
	}
	if mates[0].ID != "valid-sm" {
		t.Errorf("expected valid-sm, got %q", mates[0].ID)
	}
}

func TestParseRegistry_FullEntry(t *testing.T) {
	tmp := t.TempDir()
	registryPath := filepath.Join(tmp, "secondmates.md")
	content := `# Secondmates
- monitor-z - # Monitoring secondmate (home: /home/monitor-z; scope: infra monitoring; projects: monitoring; added: 2026-07-18)
`
	os.WriteFile(registryPath, []byte(content), 0644)

	mates, err := ParseRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 1 {
		t.Fatalf("expected 1, got %d", len(mates))
	}
	if mates[0].ID != "monitor-z" {
		t.Errorf("id = %q", mates[0].ID)
	}
	if mates[0].Home != "/home/monitor-z" {
		t.Errorf("home = %q", mates[0].Home)
	}
	if mates[0].Scope != "infra monitoring" {
		t.Errorf("scope = %q", mates[0].Scope)
	}
	if mates[0].Project != "monitoring" {
		t.Errorf("project = %q", mates[0].Project)
	}
	if mates[0].Added != "2026-07-18" {
		t.Errorf("added = %q", mates[0].Added)
	}
}

func TestParseRegistry_MissingFile(t *testing.T) {
	mates, err := ParseRegistry("/nonexistent/secondmates.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(mates) != 0 {
		t.Errorf("expected 0, got %d", len(mates))
	}
}

// --- ConfigPush tests ---

func TestConfigPush_RefusesUnmarkedHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)

	err := ConfigPush(parent, smHome)
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-secondmate-home marker") {
		t.Errorf("error should mention missing marker, got: %v", err)
	}
}

func TestConfigPush_Basic(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	configDir := filepath.Join(parent, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "crew-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(configDir, "crew-dispatch.json"), []byte("{}\n"), 0644)
	os.WriteFile(filepath.Join(configDir, "model"), []byte("claude-sonnet\n"), 0644)

	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

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
	SeedProvenance(smHome, "test-sm")

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
	SeedProvenance(smHome, "test-sm")

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

func TestConfigPush_CaptainShared(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	SeedProvenance(smHome, "test-sm")

	os.MkdirAll(filepath.Join(parent, "data"), 0755)
	sharedContent := "# Captain shared\n\nkey: value\n"
	os.WriteFile(filepath.Join(parent, "data", "captain-shared.md"), []byte(sharedContent), 0644)

	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	dstShared := filepath.Join(smHome, "data", "captain-shared.md")
	data, err := os.ReadFile(dstShared)
	if err != nil {
		t.Fatalf("captain-shared.md was not pushed: %v", err)
	}
	if string(data) != sharedContent {
		t.Errorf("captain-shared.md content = %q, want %q", string(data), sharedContent)
	}

	info, err := os.Stat(dstShared)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0444 {
		t.Errorf("captain-shared.md mode = %v, want 0444", info.Mode().Perm())
	}
}

func TestConfigPush_CaptainSharedMirrorDeletion(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.MkdirAll(filepath.Join(smHome, "data"), 0755)
	SeedProvenance(smHome, "test-sm")

	os.WriteFile(filepath.Join(smHome, "data", "captain-shared.md"), []byte("old\n"), 0644)

	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(smHome, "data", "captain-shared.md")); !os.IsNotExist(err) {
		t.Error("captain-shared.md should have been deleted (mirror deletion)")
	}
}

func TestConfigPush_RejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	outside := t.TempDir()
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(smHome, "config")); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "config", "crew-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ConfigPush(parent, smHome)
	if err == nil || !strings.Contains(err.Error(), "escapes secondmate container") {
		t.Fatalf("ConfigPush error = %v, want symlink-escape refusal", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "crew-harness")); !os.IsNotExist(err) {
		t.Fatalf("outside destination was mutated: %v", err)
	}
}

func TestConfigPush_IdempotentPreservesMtime(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	if err := os.MkdirAll(filepath.Join(smHome, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(smHome, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "config", "crew-harness"), []byte("pi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(smHome, "config", "crew-harness")
	first, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := ConfigPush(parent, smHome); err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Fatalf("idempotent push rewrote unchanged file: %s -> %s", first.ModTime(), second.ModTime())
	}
}

// --- getInheritableList tests ---

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

// --- ShQuote / buildLaunchScript tests ---

func TestShQuote_Basic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"path/with/slashes", "'path/with/slashes'"},
		{"dollar$ign", "'dollar$ign'"},
		{"back`tick", "'back`tick'"},
		{"double\"quote", "'double\"quote'"},
	}
	for _, tt := range tests {
		got := shQuote(tt.input)
		if got != tt.want {
			t.Errorf("shQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShQuote_EmbeddedSingleQuote(t *testing.T) {
	got := shQuote("it's")
	// Expected: 'it'\''s'
	want := "'it'\\''s'"
	if got != want {
		t.Errorf("shQuote with single quote = %q, want %q", got, want)
	}
}

func TestShQuote_NewlinesAndSpecials(t *testing.T) {
	input := "line1\nline2"
	got := shQuote(input)
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("shQuote should wrap in single quotes, got: %q", got)
	}
	// The newline inside the single quotes should be preserved in the quoted form.
	if len(got) < len(input)+2 {
		t.Errorf("shQuote too short: %q", got)
	}
}

func TestShQuote_ShellExecutableCharacters(t *testing.T) {
	// Characters like $() should be inside single quotes, not executed.
	input := "$(echo pwned)"
	got := shQuote(input)
	if got != "'$(echo pwned)'" {
		t.Errorf("shQuote should escape $() by wrapping in single quotes, got: %q", got)
	}
}

func TestBuildLaunchScript(t *testing.T) {
	binPath := "/usr/local/bin/pi"
	args := []string{"--model", "gpt-5", "--", "/home/sm", "# charter"}
	cwd := "/home/sm"

	script, err := buildLaunchScript(binPath, args, cwd)
	if err != nil {
		t.Fatalf("buildLaunchScript error: %v", err)
	}

	// Should start with cd command.
	if !strings.HasPrefix(script, "cd ") {
		t.Errorf("script should start with 'cd ', got: %s", script)
	}
	// Should contain exec.
	if !strings.Contains(script, "exec ") {
		t.Errorf("script should contain 'exec ', got: %s", script)
	}
	// Should have bin path.
	if !strings.Contains(script, binPath) {
		t.Errorf("script should contain bin path %q, got: %s", binPath, script)
	}
	// Should have cwd.
	if !strings.Contains(script, shQuote(cwd)) {
		t.Errorf("script should contain cwd, got: %s", script)
	}
	// All args should be represented.
	for _, arg := range args {
		if !strings.Contains(script, shQuote(arg)) {
			t.Errorf("script should contain quoted arg %q, got: %s", arg, script)
		}
	}
}

func TestBuildLaunchScript_SafeQuoting(t *testing.T) {
	// Test with dangerous characters.
	binPath := "/usr/local/bin/pi"
	args := []string{"--prompt", "# charter with $HOME and `backticks` and $(whoami)"}
	cwd := "/tmp/sm test"

	script, err := buildLaunchScript(binPath, args, cwd)
	if err != nil {
		t.Fatalf("buildLaunchScript error: %v", err)
	}

	// The dangerous characters should be inside single quotes.
	if strings.Contains(script, "$HOME") && !strings.Contains(script, "'$HOME'") && !strings.Contains(script, shQuote("# charter")) {
		t.Logf("script: %s", script)
	}

	// Verify the quoted form protects the special chars.
	if strings.Contains(script, "$(whoami)") {
		// $(whoami) should be inside quotes.
		if !strings.Contains(script, shQuote("# charter with $HOME and `backticks` and $(whoami)")) {
			t.Errorf("dangerous arg not properly quoted in: %s", script)
		}
	}
}

func TestBuildLaunchScript_ShellExecution(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "sm test")
	os.MkdirAll(smHome, 0755)

	recorder := filepath.Join(tmp, "recorded.txt")

	// Create a small shell script that writes its cwd and argv to a file.
	testBin := filepath.Join(tmp, "test-recorder")
	// The script: write cwd, then write argv count, then write each arg.
	binContent := "#!/bin/sh\n"
	binContent += "pwd > '" + recorder + "'\n"
	binContent += "echo \"argv $#\" >> '" + recorder + "'\n"
	binContent += "for a in \"$@\"; do echo \"  [$a]\" >> '" + recorder + "'; done\n"
	if err := os.WriteFile(testBin, []byte(binContent), 0755); err != nil {
		t.Fatal(err)
	}

	// Build a launch script with special characters.
	args := []string{"--prompt", "# charter with $HOME and `backticks` and $(whoami)", "--", smHome}
	script, err := buildLaunchScript(testBin, args, smHome)
	if err != nil {
		t.Fatalf("buildLaunchScript error: %v", err)
	}

	// Execute via /bin/sh -c.
	cmd := exec.Command("/bin/sh", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell execution failed: %v\noutput: %s", err, string(out))
	}

	// Read recorded output.
	data, err := os.ReadFile(recorder)
	if err != nil {
		t.Fatalf("reading recorded output: %v", err)
	}
	recorded := string(data)

	// Verify cwd is the secondmate home.
	if !strings.Contains(recorded, smHome) {
		t.Errorf("recorded output should contain smHome %q, got: %s", smHome, recorded)
	}
	// Verify special characters are preserved as literal strings in argv.
	if !strings.Contains(recorded, "$HOME") {
		t.Errorf("recorded output should contain literal $HOME, got: %s", recorded)
	}
	if !strings.Contains(recorded, "$(whoami)") {
		t.Errorf("recorded output should contain literal $(whoami), got: %s", recorded)
	}
	if !strings.Contains(recorded, "`backticks`") {
		t.Errorf("recorded output should contain literal backticks, got: %s", recorded)
	}
}

// --- sha256Content tests ---

func TestSha256Content_Deterministic(t *testing.T) {
	data := []byte("test content")
	h1 := sha256Content(data)
	h2 := sha256Content(data)
	if h1 != h2 {
		t.Errorf("sha256Content should be deterministic, got %q vs %q", h1, h2)
	}
}

func TestSha256Content_Different(t *testing.T) {
	h1 := sha256Content([]byte("content A"))
	h2 := sha256Content([]byte("content B"))
	if h1 == h2 {
		t.Errorf("sha256Content should differ for different content")
	}
}

func TestSha256Content_Empty(t *testing.T) {
	h := sha256Content([]byte(""))
	if h == "" {
		t.Errorf("sha256Content should return non-empty for empty input")
	}
}

// --- Launch tests using fake backend ---

// fakeBackend is a test session backend that records calls and optionally
// returns configured results.
type fakeBackend struct {
	NewWindowID string
	NewWindowFn func(session, name string) (string, error)
	SendKeysFn  func(windowID, text string) error
	CaptureFn   func(windowID string, lines int) (string, error)
	AliveFn     func(windowID string) bool
	TeardownFn  func(windowID string) error
	ExtraMeta   map[string]string
}

func (f *fakeBackend) NewWindow(session, name string) (string, error) {
	if f.NewWindowFn != nil {
		return f.NewWindowFn(session, name)
	}
	return "test-window", nil
}

func (f *fakeBackend) SendKeys(windowID, text string) error {
	if f.SendKeysFn != nil {
		return f.SendKeysFn(windowID, text)
	}
	return nil
}

func (f *fakeBackend) Capture(windowID string, lines int) (string, error) {
	if f.CaptureFn != nil {
		return f.CaptureFn(windowID, lines)
	}
	return "", nil
}

func (f *fakeBackend) Alive(windowID string) bool {
	if f.AliveFn != nil {
		return f.AliveFn(windowID)
	}
	return true
}

func (f *fakeBackend) Teardown(windowID string) error {
	if f.TeardownFn != nil {
		return f.TeardownFn(windowID)
	}
	return nil
}

func (f *fakeBackend) MetaExtras() map[string]string {
	return f.ExtraMeta
}

func TestLaunch_RefusesUnmarkedHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)

	err := Launch(smHome, tmp)
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-secondmate-home marker") {
		t.Errorf("error should mention missing marker, got: %v", err)
	}
}

func TestLaunch_FailsGracefullyOnLookPathFailure(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	Seed("test-sm", smHome, "# Test")

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("pi\n"), 0644)

	origBK := newSessionBackend
	origLP := lookPath
	defer func() {
		newSessionBackend = origBK
		lookPath = origLP
	}()

	// Inject fake backend that creates a window.
	fakeBK := &fakeBackend{
		NewWindowFn: func(session, name string) (string, error) {
			return "test-window", nil
		},
		TeardownFn: func(windowID string) error {
			return nil
		},
	}
	newSessionBackend = func(parentHome string) (session.Backend, string, error) {
		return fakeBK, "tmux", nil
	}

	// lookPath returns not-found — should fail closed.
	lookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}

	err := Launch(smHome, tmp)
	if err == nil {
		t.Fatal("expected error when harness binary not on PATH")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error should mention PATH, got: %v", err)
	}
}

func TestLaunch_SessionBackedWithMeta(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	Seed("test-sm", smHome, "# charter")

	// Setup secondmate-harness config so harness resolution works.
	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("pi\n"), 0644)

	// Save originals and restore in defer.
	origBK := newSessionBackend
	origLP := lookPath
	defer func() {
		newSessionBackend = origBK
		lookPath = origLP
	}()

	recordedSends := make([]string, 0)
	fakeBK := &fakeBackend{
		NewWindowFn: func(session, name string) (string, error) {
			return "secondmate:test-window", nil
		},
		SendKeysFn: func(windowID, text string) error {
			recordedSends = append(recordedSends, text)
			return nil
		},
		AliveFn: func(windowID string) bool {
			return true
		},
		TeardownFn: func(windowID string) error {
			return nil
		},
		ExtraMeta: map[string]string{
			"herdr_session":      "test-session",
			"herdr_workspace_id": "test-ws",
			"herdr_tab_id":       "test-tab",
			"herdr_pane_id":      "test-pane",
		},
	}

	newSessionBackend = func(parentHome string) (session.Backend, string, error) {
		return fakeBK, "herdr", nil
	}

	lookPath = func(name string) (string, error) {
		return "/usr/local/bin/pi", nil
	}

	// No launchCmd override — uses production buildLaunchScript.

	err := Launch(smHome, tmp)
	if err != nil {
		t.Fatalf("Launch() error: %v", err)
	}

	// Check that meta was written with kind=secondmate.
	taskID := "secondmate:test-sm"
	metaPath := filepath.Join(tmp, "state", taskID+".meta")
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("meta file not created: %v", err)
	}
	metaContent := string(metaData)
	if !strings.Contains(metaContent, "kind=secondmate") {
		t.Errorf("meta should contain kind=secondmate, got: %s", metaContent)
	}
	canonicalSM, err := canonicalHome(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metaContent, "home="+canonicalSM) {
		t.Errorf("meta should contain canonical home=%s", canonicalSM)
	}
	if !strings.Contains(metaContent, "window=secondmate:test-window") {
		t.Errorf("meta should contain window=secondmate:test-window")
	}
	if !strings.Contains(metaContent, "backend=herdr") {
		t.Errorf("meta should contain backend=herdr")
	}
	if !strings.Contains(metaContent, "sm_id=test-sm") {
		t.Errorf("meta should contain sm_id=test-sm")
	}
	// Check backend extras.
	if !strings.Contains(metaContent, "herdr_session=test-session") {
		t.Errorf("meta should contain herdr_session=test-session")
	}
	if !strings.Contains(metaContent, "herdr_pane_id=test-pane") {
		t.Errorf("meta should contain herdr_pane_id=test-pane")
	}

	// Verify launch was sent with production buildLaunchScript quoting.
	if len(recordedSends) == 0 {
		t.Fatal("no commands were sent via SendKeys")
	}

	// Assert recorded SendKeys matches production buildLaunchScript output.
	expectedCmd, err := buildLaunchScript("/usr/local/bin/pi", []string{"--", smHome, "# charter"}, smHome)
	if err != nil {
		t.Fatalf("buildLaunchScript error: %v", err)
	}
	if recordedSends[0] != expectedCmd {
		t.Errorf("sent command = %q, want %q", recordedSends[0], expectedCmd)
	}

	// Verify canonical cwd and charter in the script.
	if !strings.Contains(recordedSends[0], shQuote(smHome)) {
		t.Errorf("script should contain canonical cwd %q", smHome)
	}
	if !strings.Contains(recordedSends[0], shQuote("# charter")) {
		t.Error("script should contain charter content")
	}
}

func TestLaunch_MetaOnlyAfterSuccess(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	Seed("test-sm", smHome, "# charter")

	configDir := filepath.Join(tmp, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "secondmate-harness"), []byte("pi\n"), 0644)

	origBK := newSessionBackend
	origLP := lookPath
	defer func() {
		newSessionBackend = origBK
		lookPath = origLP
	}()

	fakeBK := &fakeBackend{
		NewWindowFn: func(session, name string) (string, error) {
			return "test-window", nil
		},
		SendKeysFn: func(windowID, text string) error {
			return nil
		},
		TeardownFn: func(windowID string) error {
			return nil
		},
	}

	newSessionBackend = func(parentHome string) (session.Backend, string, error) {
		return fakeBK, "tmux", nil
	}

	lookPath = func(name string) (string, error) {
		return "/usr/local/bin/pi", nil
	}

	// No launchCmd override — uses production buildLaunchScript.

	if err := Launch(smHome, tmp); err != nil {
		t.Fatalf("Launch() error: %v", err)
	}

	// Meta should exist.
	taskID := "secondmate:test-sm"
	metaPath := filepath.Join(tmp, "state", taskID+".meta")
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Fatal("meta should exist after successful launch")
	}

	// Verify meta content.
	meta, err := task.ReadMeta(tmp, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta["kind"] != "secondmate" {
		t.Errorf("meta kind = %q", meta["kind"])
	}
	if meta["sm_id"] != "test-sm" {
		t.Errorf("meta sm_id = %q", meta["sm_id"])
	}
	canonicalSM, err := canonicalHome(smHome)
	if err != nil {
		t.Fatal(err)
	}
	if meta["home"] != canonicalSM {
		t.Errorf("meta home = %q, want %q", meta["home"], canonicalSM)
	}
}

// --- Handoff tests ---

func TestHandoff_RefusesUnmarkedHome(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(sm, 0755)

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-secondmate-home marker") {
		t.Errorf("error should mention missing marker, got: %v", err)
	}
}

func TestHandoff_RequiresTasksAxi(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(sm, 0755)
	SeedProvenance(sm, "test-sm")

	origPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", os.ErrNotExist
	}
	defer func() { lookPath = origPath }()

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected error for missing tasks-axi")
	}
	if !strings.Contains(err.Error(), "tasks-axi not found") {
		t.Errorf("error should mention missing tasks-axi, got: %v", err)
	}
}

func TestHandoff_RefusesSelfParent(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(parent, 0755)
	SeedProvenance(parent, "parent-sm")

	err := Handoff(parent, parent, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected error for same home")
	}
	if !strings.Contains(err.Error(), "destination is parent home itself") {
		t.Errorf("error should mention parent home, got: %v", err)
	}
}

func TestHandoff_RefusesTasksAxiWithoutAtomicStateCapability(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}

	origPath := lookPath
	origBackend := isTasksAxiBackend
	defer func() {
		lookPath = origPath
		isTasksAxiBackend = origBackend
	}()

	fakeTasksAxi := filepath.Join(parent, "fake-tasks-axi")
	fakeScript := "#!/bin/sh\nif [ \"$1\" = mv ] && [ \"$2\" = --help ]; then echo 'usage: tasks-axi mv <id> --to <path>'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(fakeTasksAxi, []byte(fakeScript), 0755); err != nil {
		t.Fatal(err)
	}
	lookPath = func(name string) (string, error) { return fakeTasksAxi, nil }
	isTasksAxiBackend = func(string) bool { return true }

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil || !strings.Contains(err.Error(), "--require-state") {
		t.Fatalf("Handoff error = %v, want atomic state capability refusal", err)
	}
}

func TestHandoffPassesAtomicQueuedPrecondition(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")
	if err := os.MkdirAll(sm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(sm, "test-sm"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "data"), 0755); err != nil {
		t.Fatal(err)
	}

	origPath := lookPath
	origBackend := isTasksAxiBackend
	defer func() {
		lookPath = origPath
		isTasksAxiBackend = origBackend
	}()

	argsPath := filepath.Join(parent, "args.txt")
	fakeTasksAxi := filepath.Join(parent, "fake-tasks-axi")
	fakeScript := "#!/bin/sh\nif [ \"$1\" = mv ] && [ \"$2\" = --help ]; then echo '  --require-state queued|in_flight|done'; exit 0; fi\nif [ \"$1\" = show ]; then echo 'state: queued'; exit 0; fi\nprintf '%s\\n' \"$@\" > " + shQuote(argsPath) + "\n"
	if err := os.WriteFile(fakeTasksAxi, []byte(fakeScript), 0755); err != nil {
		t.Fatal(err)
	}
	lookPath = func(name string) (string, error) { return fakeTasksAxi, nil }
	isTasksAxiBackend = func(string) bool { return true }

	if err := Handoff(parent, sm, []string{"TASK-1", "TASK-2"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"mv", "TASK-1", "TASK-2",
		"--to", filepath.Join(sm, "data", "backlog.md"),
		"--require-state", "queued",
		"--file", filepath.Join(parent, "data", "backlog.md"),
	}
	if len(args) != len(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestHandoff_RefusesManualBackend(t *testing.T) {
	parent := t.TempDir()
	sm := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(sm, 0755)
	SeedProvenance(sm, "test-sm")

	origPath := lookPath
	origBackend := isTasksAxiBackend
	defer func() {
		lookPath = origPath
		isTasksAxiBackend = origBackend
	}()

	// Override isTasksAxiBackend to return false (manual backend).
	isTasksAxiBackend = func(string) bool { return false }

	lookPath = func(name string) (string, error) {
		return "/usr/bin/tasks-axi", nil
	}

	err := Handoff(parent, sm, []string{"TASK-1"})
	if err == nil {
		t.Fatal("expected error for manual backend")
	}
	if !strings.Contains(err.Error(), "backlog backend is not set to tasks-axi") {
		t.Errorf("error should mention backend mismatch, got: %v", err)
	}
}

// --- Retire tests ---

func TestRetire_RefusesUnmarkedHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)

	err := Retire(smHome, tmp, false)
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "no .munsu-secondmate-home marker") {
		t.Errorf("error should mention missing marker, got: %v", err)
	}
}

func TestRetire_RefusesUnmarkedWithRemoveHome(t *testing.T) {
	tmp := t.TempDir()
	smHome := filepath.Join(tmp, "secondmates", "test-sm")
	if err := os.MkdirAll(smHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smHome, "sentinel"), []byte("keep\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := Retire(smHome, tmp, true)
	if err == nil {
		t.Fatal("expected ownership refusal for unmarked destructive retire")
	}
	if _, err := os.Stat(filepath.Join(smHome, "sentinel")); err != nil {
		t.Fatalf("unowned home was mutated: %v", err)
	}
}

func TestRetire_RemoveHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	if err := Retire(smHome, parent, true); err != nil {
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
	SeedProvenance(smHome, "test-sm")

	if err := Retire(smHome, parent, false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(smHome); os.IsNotExist(err) {
		t.Error("secondmate home should have been retained")
	}
}

func TestRetire_NonexistentHomeRefused(t *testing.T) {
	parent := t.TempDir()
	if err := Retire("/nonexistent/sm", parent, true); err == nil {
		t.Fatal("expected nonexistent unowned home refusal")
	}
}

// --- Retire meta validation tests ---

func TestRetire_RefusesWrongKindMeta(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	// Write bad meta.
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.WriteFile(filepath.Join(parent, "state", "secondmate:test-sm.meta"),
		[]byte("kind=not-secondmate\nsm_id=test-sm\nhome="+smHome+"\nwindow=w\nbackend=tmux\n"), 0644)

	err := Retire(smHome, parent, false)
	if err == nil {
		t.Fatal("expected error for wrong meta kind")
	}
	if !strings.Contains(err.Error(), "kind=") {
		t.Errorf("error should mention kind mismatch, got: %v", err)
	}
}

func TestRetire_RefusesMismatchedID(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	// Write meta with different sm_id.
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.WriteFile(filepath.Join(parent, "state", "secondmate:test-sm.meta"),
		[]byte("kind=secondmate\nsm_id=wrong-id\nhome="+smHome+"\nwindow=w\nbackend=tmux\n"), 0644)

	err := Retire(smHome, parent, false)
	if err == nil {
		t.Fatal("expected error for mismatched sm_id")
	}
	if !strings.Contains(err.Error(), "sm_id") {
		t.Errorf("error should mention sm_id mismatch, got: %v", err)
	}
}

func TestRetire_RefusesMismatchedHome(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# charter\n"), 0644)
	SeedProvenance(smHome, "test-sm")

	// Write meta with different home.
	os.MkdirAll(filepath.Join(parent, "state"), 0755)
	os.WriteFile(filepath.Join(parent, "state", "secondmate:test-sm.meta"),
		[]byte("kind=secondmate\nsm_id=test-sm\nhome=/some/other/path\nwindow=w\nbackend=tmux\n"), 0644)

	err := Retire(smHome, parent, false)
	if err == nil {
		t.Fatal("expected error for mismatched home")
	}
	if !strings.Contains(err.Error(), "home=") {
		t.Errorf("error should mention home mismatch, got: %v", err)
	}
}

// --- acquireExclusiveLock tests ---

func TestAcquireExclusiveLock(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}
	if release == nil {
		t.Fatal("expected non-nil release function")
	}

	// Lock file should exist.
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("lock file was not created")
	}

	// Release.
	release()

	// Lock file should be removed.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file was not removed after release")
	}
}

func TestAcquireExclusiveLock_ConcurrentRefusal(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release1, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Second acquire with LOCK_NB should fail immediately.
	// Use channel + timeout to prove non-blocking behavior.
	done := make(chan struct{})
	var secondErr error
	go func() {
		_, secondErr = acquireExclusiveLock(lockPath)
		close(done)
	}()

	select {
	case <-done:
		if secondErr == nil {
			t.Fatal("second concurrent lock should have failed with LOCK_NB")
		}
		if !strings.Contains(secondErr.Error(), "held by another process") {
			t.Logf("second lock error (expected): %v", secondErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second lock acquisition blocked for 5s — LOCK_NB not working")
	}

	release1()

	// Acquire again after release (generation-safe).
	release2, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

// --- Nudge marker tests ---

func TestNudgeMarkerPath(t *testing.T) {
	parent := t.TempDir()
	path := nudgeMarkerPath(parent, "test-sm")
	want := filepath.Join(parent, "state", ".secondmate-nudge-pending", "test-sm.pending")
	if path != want {
		t.Errorf("nudgeMarkerPath = %q, want %q", path, want)
	}
}

func TestWriteAndReadNudgeMarker(t *testing.T) {
	parent := t.TempDir()
	smID := "test-sm"
	smHome := "/home/test-sm"
	instructions := "# charter content"
	message := "reread"

	err := writeNudgeMarker(parent, smID, smHome, "abc123", instructions, message)
	if err != nil {
		t.Fatalf("writeNudgeMarker error: %v", err)
	}

	marker, err := readNudgeMarker(parent, smID)
	if err != nil {
		t.Fatalf("readNudgeMarker error: %v", err)
	}
	if marker == nil {
		t.Fatal("nudge marker not found")
	}
	if marker["id"] != smID {
		t.Errorf("marker id = %q, want %q", marker["id"], smID)
	}
	if marker["home"] != smHome {
		t.Errorf("marker home = %q, want %q", marker["home"], smHome)
	}
	if marker["instructions"] != instructions {
		t.Errorf("marker instructions = %q, want %q", marker["instructions"], instructions)
	}
	if marker["message"] != message {
		t.Errorf("marker message = %q, want %q", marker["message"], message)
	}
}

func TestRemoveNudgeMarker(t *testing.T) {
	parent := t.TempDir()
	smID := "test-sm"

	writeNudgeMarker(parent, smID, "/home/test-sm", "abc", "# charter", "reread")
	if _, err := readNudgeMarker(parent, smID); err != nil || true {
		// Marker exists.
		marker, _ := readNudgeMarker(parent, smID)
		if marker == nil {
			t.Fatal("expected marker to exist after write")
		}
	}

	removeNudgeMarker(parent, smID)
	marker, _ := readNudgeMarker(parent, smID)
	if marker != nil {
		t.Error("marker should have been removed")
	}
}

func TestReadNudgeMarker_Nonexistent(t *testing.T) {
	parent := t.TempDir()
	marker, err := readNudgeMarker(parent, "nonexistent-sm")
	if err != nil {
		t.Fatalf("readNudgeMarker error: %v", err)
	}
	if marker != nil {
		t.Errorf("expected nil for nonexistent marker, got %v", marker)
	}
}

// --- safeFF tests (real git repos) ---

type safeFFFixture struct {
	parent     string
	secondmate string
	before     string
	after      string
}

func gitTestRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newSafeFFFixture(t *testing.T) safeFFFixture {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	parent := filepath.Join(root, "parent")
	secondmate := filepath.Join(root, "secondmate")
	for _, dst := range []string{parent, secondmate} {
		if out, err := exec.Command("git", "clone", remote, dst).CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v\n%s", err, out)
		}
		gitTestRun(t, dst, "config", "user.name", "Munsu Test")
		gitTestRun(t, dst, "config", "user.email", "munsu@example.invalid")
	}
	gitTestRun(t, parent, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("state/ignored\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, parent, "add", ".gitignore", "AGENTS.md")
	gitTestRun(t, parent, "commit", "-m", "initial")
	before := gitTestRun(t, parent, "rev-parse", "HEAD")
	gitTestRun(t, parent, "push", "-u", "origin", "main")
	gitTestRun(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	gitTestRun(t, secondmate, "fetch", "origin", "main")
	gitTestRun(t, secondmate, "checkout", "-B", "main", before)
	gitTestRun(t, secondmate, "remote", "set-head", "origin", "main")
	gitTestRun(t, parent, "remote", "set-head", "origin", "main")

	if err := os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, parent, "commit", "-am", "advance instructions")
	after := gitTestRun(t, parent, "rev-parse", "HEAD")
	gitTestRun(t, parent, "push", "origin", "main")
	// Seed the already-local object without changing the secondmate checkout.
	gitTestRun(t, secondmate, "fetch", "origin", "main")
	gitTestRun(t, secondmate, "reset", "--hard", before)
	return safeFFFixture{parent: parent, secondmate: secondmate, before: before, after: after}
}

func TestSafeFF_OffBranchRefused(t *testing.T) {
	f := newSafeFFFixture(t)
	gitTestRun(t, f.secondmate, "checkout", "-b", "feature")
	if _, _, err := safeFF(f.secondmate, f.parent); err == nil || !strings.Contains(err.Error(), "expected \"main\"") {
		t.Fatalf("safeFF error = %v, want off-default-branch refusal", err)
	}
}

func TestSafeFF_MissingOriginHEADRefused(t *testing.T) {
	f := newSafeFFFixture(t)
	gitTestRun(t, f.parent, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	if _, _, err := safeFF(f.secondmate, f.parent); err == nil || !strings.Contains(err.Error(), "origin/HEAD") {
		t.Fatalf("safeFF error = %v, want missing origin/HEAD refusal", err)
	}
}

func TestAcquireExclusiveLock_OldReleasePreservesReplacement(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")
	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lockPath, lockPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte(strings.Repeat("a", 64)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("old generation release removed replacement lock: %v", err)
	}
}

func TestSafeFF_TrackedChangesRefused(t *testing.T) {
	f := newSafeFFFixture(t)
	if err := os.WriteFile(filepath.Join(f.secondmate, "AGENTS.md"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := safeFF(f.secondmate, f.parent); err == nil || !strings.Contains(err.Error(), "tracked changes") {
		t.Fatalf("safeFF error = %v, want tracked-change refusal", err)
	}
}

func TestSafeFF_UnignoredUntrackedRefused(t *testing.T) {
	f := newSafeFFFixture(t)
	if err := os.WriteFile(filepath.Join(f.secondmate, "rogue.txt"), []byte("rogue\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := safeFF(f.secondmate, f.parent); err == nil || !strings.Contains(err.Error(), "unignored untracked") {
		t.Fatalf("safeFF error = %v, want unignored-file refusal", err)
	}
}

func TestSafeFF_GitignoredArtifactAllowed(t *testing.T) {
	f := newSafeFFFixture(t)
	if err := os.MkdirAll(filepath.Join(f.secondmate, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.secondmate, "state", "ignored"), []byte("local\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, after, err := safeFF(f.secondmate, f.parent)
	if err != nil {
		t.Fatalf("safeFF: %v", err)
	}
	if before != f.before || after != f.after {
		t.Fatalf("safeFF = (%s, %s), want (%s, %s)", before, after, f.before, f.after)
	}
}

func TestSafeFF_ParentFeatureCheckoutStillTargetsDefaultBranch(t *testing.T) {
	f := newSafeFFFixture(t)
	gitTestRun(t, f.parent, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(f.parent, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, f.parent, "add", "feature.txt")
	gitTestRun(t, f.parent, "commit", "-m", "feature only")
	_, after, err := safeFF(f.secondmate, f.parent)
	if err != nil {
		t.Fatalf("safeFF: %v", err)
	}
	if after != f.after {
		t.Fatalf("after = %s, want default-branch commit %s", after, f.after)
	}
}

// --- acquireExclusiveLock token tests ---

func TestAcquireExclusiveLock_TokenGeneration(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("acquireExclusiveLock error: %v", err)
	}

	// Lock file should exist with hex token content.
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.TrimSpace(string(data))
	if len(content) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("expected 64 hex chars, got %d: %q", len(content), content)
	}

	// Release should clean up.
	release()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after release")
	}
}

func TestAcquireExclusiveLock_NoRemoveOnFailure(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, "test.lock")

	release1, err := acquireExclusiveLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Create a file with lock-like content to simulate the file still existing.
	// Write a marker so we can detect if it's removed.
	os.WriteFile(lockPath, []byte("other-content\n"), 0644)

	// Second acquire should fail (LOCK_NB) but NOT remove the file.
	_, err = acquireExclusiveLock(lockPath)
	if err == nil {
		t.Fatal("expected second acquire to fail")
	}

	// The file should still exist with its original content (not removed).
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal("lock file was removed — bug: os.Remove on LOCK_NB failure")
	}
	if string(data) != "other-content\n" {
		t.Errorf("lock file content changed: %q", string(data))
	}

	release1()
}

// --- Converge tests ---

func TestConverge_EmptyRegistry(t *testing.T) {
	parent := t.TempDir()
	err := Converge(parent, nil)
	if err != nil {
		t.Fatalf("Converge(nil) error: %v", err)
	}
	err = Converge(parent, []Info{})
	if err != nil {
		t.Fatalf("Converge(empty) error: %v", err)
	}
}

func TestConverge_RefusesUnmarkedHome(t *testing.T) {
	parent := t.TempDir()

	err := Converge(parent, []Info{
		{ID: "test-sm", Home: "/nonexistent"},
	})
	if err == nil {
		t.Fatal("expected error for unmarked home")
	}
	if !strings.Contains(err.Error(), "provenance validation failed") {
		t.Errorf("error = %v", err)
	}
}

func TestConverge_ValidMarkersWithConfigPush(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "config"), 0755)
	os.WriteFile(filepath.Join(parent, "config", "crew-harness"), []byte("pi\n"), 0644)
	os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("# Parent charter\n"), 0644)

	// Create two secondmates with provenance markers.
	sm1 := filepath.Join(parent, "secondmates", "sm-alpha")
	os.MkdirAll(filepath.Join(sm1, "state"), 0755)
	os.MkdirAll(filepath.Join(sm1, "config"), 0755)
	os.MkdirAll(filepath.Join(sm1, "data"), 0755)
	os.WriteFile(filepath.Join(sm1, "AGENTS.md"), []byte("# Alpha\n"), 0644)
	SeedProvenance(sm1, "sm-alpha")

	sm2 := filepath.Join(parent, "secondmates", "sm-beta")
	os.MkdirAll(filepath.Join(sm2, "state"), 0755)
	os.MkdirAll(filepath.Join(sm2, "config"), 0755)
	os.MkdirAll(filepath.Join(sm2, "data"), 0755)
	os.WriteFile(filepath.Join(sm2, "AGENTS.md"), []byte("# Beta\n"), 0644)
	SeedProvenance(sm2, "sm-beta")

	// Run converge.
	err := Converge(parent, []Info{
		{ID: "sm-alpha", Home: sm1},
		{ID: "sm-beta", Home: sm2},
	})

	// Since these are not git repos, safeFF will fail. Accept that.
	if err == nil {
		t.Fatal("expected converge errors (no git repos)")
	}

	// But config push should have succeeded for both.
	// Check that crew-harness was pushed.
	data1, err := os.ReadFile(filepath.Join(sm1, "config", "crew-harness"))
	if err != nil {
		t.Errorf("sm-alpha crew-harness not pushed: %v", err)
	} else if string(data1) != "pi\n" {
		t.Errorf("sm-alpha crew-harness content = %q", string(data1))
	}

	data2, err := os.ReadFile(filepath.Join(sm2, "config", "crew-harness"))
	if err != nil {
		t.Errorf("sm-beta crew-harness not pushed: %v", err)
	} else if string(data2) != "pi\n" {
		t.Errorf("sm-beta crew-harness content = %q", string(data2))
	}
}

func TestConverge_RefusesRegistryIDMismatch(t *testing.T) {
	parent := t.TempDir()
	smHome := filepath.Join(parent, "secondmates", "test-sm")
	os.MkdirAll(smHome, 0755)
	os.MkdirAll(filepath.Join(smHome, "state"), 0755)
	os.MkdirAll(filepath.Join(smHome, "config"), 0755)
	os.MkdirAll(filepath.Join(smHome, "data"), 0755)
	os.WriteFile(filepath.Join(smHome, "AGENTS.md"), []byte("# Test\n"), 0644)
	// Seed with id "actual-id"
	SeedProvenance(smHome, "actual-id")

	// But registry says "wrong-id".
	err := Converge(parent, []Info{
		{ID: "wrong-id", Home: smHome},
	})
	if err == nil {
		t.Fatal("expected error for ID mismatch")
	}
	if !strings.Contains(err.Error(), "does not match registry id") {
		t.Errorf("error should mention ID mismatch, got: %v", err)
	}
}

// --- taskIDForSecondmate tests ---

func TestTaskIDForSecondmate(t *testing.T) {
	id := taskIDForSecondmate("test-sm")
	if id != "secondmate:test-sm" {
		t.Errorf("taskIDForSecondmate = %q, want %q", id, "secondmate:test-sm")
	}
}
