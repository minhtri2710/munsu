package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedSkillNames(t *testing.T) {
	names, err := embeddedSkillNames()
	if err != nil {
		t.Fatalf("embeddedSkillNames() returned error: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one embedded skill, got none")
	}
	// Expect the entry-point skill
	found := false
	for _, n := range names {
		if n == "munsu-ops" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'munsu-ops' in embedded skills, got %v", names)
	}
	// Expect auxiliary skills
	aux := []string{"bootstrap-diagnostics", "harness-adapters", "munsu-update", "captain-provisioning", "stuck-soldier-recovery"}
	for _, a := range aux {
		found = false
		for _, n := range names {
			if n == a {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected auxiliary skill %q in embedded skills, got %v", a, names)
		}
	}
}

func TestReadEmbeddedSkill(t *testing.T) {
	// Read munsu-ops
	content, err := readEmbeddedSkill("munsu-ops")
	if err != nil {
		t.Fatalf("readEmbeddedSkill('munsu-ops'): %v", err)
	}
	if !strings.Contains(content, "fleet orchestration") {
		t.Errorf("expected munsu-ops skill to contain 'fleet orchestration', got: %s", content[:100])
	}

	// Read bootstrap-diagnostics
	content, err = readEmbeddedSkill("bootstrap-diagnostics")
	if err != nil {
		t.Fatalf("readEmbeddedSkill('bootstrap-diagnostics'): %v", err)
	}
	if !strings.Contains(content, "bootstrap-diagnostics") {
		t.Errorf("expected bootstrap-diagnostics content, got: %s", content[:100])
	}

	// Non-existent skill
	_, err = readEmbeddedSkill("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent skill, got nil")
	}
}

func TestSkillExistsAt(t *testing.T) {
	tmpDir := t.TempDir()

	// Dir doesn't exist yet
	if skillExistsAt(tmpDir, "test-skill") {
		t.Error("expected false for non-existent skill")
	}

	// Create the dir
	if err := os.MkdirAll(filepath.Join(tmpDir, "test-skill"), 0755); err != nil {
		t.Fatal(err)
	}
	if !skillExistsAt(tmpDir, "test-skill") {
		t.Error("expected true for existing skill")
	}

	// File isn't a dir
	f, err := os.Create(filepath.Join(tmpDir, "not-a-dir"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if skillExistsAt(tmpDir, "not-a-dir") {
		t.Error("expected false for non-directory")
	}
}

func TestInstallOneSkill(t *testing.T) {
	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "skills")

	// Install munsu-ops
	ok, err := installOneSkill(dest, "munsu-ops", false)
	if err != nil {
		t.Fatalf("installOneSkill: %v", err)
	}
	if !ok {
		t.Fatal("expected at least one file installed")
	}

	// Check SKILL.md was written
	skillPath := filepath.Join(dest, "munsu-ops", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		t.Fatalf("expected SKILL.md at %s", skillPath)
	}

	// Captain install with overwrite=false should not install
	ok, err = installOneSkill(dest, "munsu-ops", false)
	if err != nil {
		t.Fatalf("installOneSkill (captain): %v", err)
	}
	if ok {
		t.Error("expected false for captain install without overwrite")
	}

	// Install with overwrite=true
	ok, err = installOneSkill(dest, "munsu-ops", true)
	if err != nil {
		t.Fatalf("installOneSkill (overwrite): %v", err)
	}
	if !ok {
		t.Error("expected true for overwrite install")
	}
}

func TestInstallOneSkillNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	ok, err := installOneSkill(tmpDir, "nonexistent", false)
	if err != nil {
		t.Fatalf("unexpected error for non-existent skill name: %v", err)
	}
	if ok {
		t.Error("expected false for non-existent skill name")
	}
}

// TestBuildWithEmbed verifies that the package compiles with the //go:embed directive.
func TestBuildWithEmbed(t *testing.T) {
	// This test is intentionally minimal — it verifies the embed builds via
	// the other tests that call embeddedSkillNames() and readEmbeddedSkill().
	// If the embed package compiles, these tests pass. The real embed is tested
	// in TestEmbeddedSkillNames and TestReadEmbeddedSkill above.
}

// TestInitCmdHasSkillFlag verifies newInitCmd exposes --skill.
func TestInitCmdHasSkillFlag(t *testing.T) {
	cmd := newInitCmd()
	flag := cmd.Flags().Lookup("skill")
	if flag == nil {
		t.Fatal("init command missing --skill flag")
	}
	if flag.DefValue != "" {
		t.Errorf("expected empty default for --skill, got %q", flag.DefValue)
	}
}

func TestEmbeddedSkillReferencesResolve(t *testing.T) {
	if err := validateSkillBundle(skillFiles, "skills"); err != nil {
		t.Fatal(err)
	}
}

func TestMunsuOpsAgentMirrorMatchesCanonical(t *testing.T) {
	canonical := filepath.Join("internal", "cli", "skills", "munsu-ops")
	mirror := filepath.Join(".agents", "skills", "munsu-ops")
	compareSkillDirectories(t, os.DirFS(filepath.Join("..", "..")), canonical, mirror)
}

func TestInstalledMunsuOpsReferencesResolve(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills")
	installed, err := installAllSkills(dest, nil)
	if err != nil {
		t.Fatalf("installAllSkills: %v", err)
	}
	if len(installed) == 0 {
		t.Fatal("expected embedded skills to install")
	}
	for _, name := range []string{"SKILL.md", "COMMANDS.md", "SUPERVISION.md"} {
		if _, err := os.Stat(filepath.Join(dest, "munsu-ops", name)); err != nil {
			t.Fatalf("installed companion %s: %v", name, err)
		}
	}
	if err := validateSkillBundle(os.DirFS(dest), "."); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSkillBundleReferences(t *testing.T) {
	tests := []struct {
		name    string
		files   fstest.MapFS
		wantErr string
	}{
		{name: "valid companion", files: fstest.MapFS{
			"skills/main/SKILL.md":     {Data: []byte("See `REFERENCE.md` and [commands](COMMANDS.md).")},
			"skills/main/REFERENCE.md": {Data: []byte("reference")},
			"skills/main/COMMANDS.md":  {Data: []byte("commands")},
		}},
		{name: "missing reference", files: fstest.MapFS{
			"skills/main/SKILL.md": {Data: []byte("See `REFERENCE.md`.")},
		}, wantErr: "resolves to missing"},
		{name: "path escape", files: fstest.MapFS{
			"skills/main/SKILL.md": {Data: []byte("See [outside](../outside.md).")},
		}, wantErr: "escapes skill module"},
		{name: "external and anchor ignored", files: fstest.MapFS{
			"skills/main/SKILL.md": {Data: []byte("[web](https://example.com/x.md) [section](#local)")},
		}},
		{name: "inline reference after fence", files: fstest.MapFS{
			"skills/main/SKILL.md": {Data: []byte("```sh\necho docs/ignored.md\n```\nSee `docs/missing.md`.")},
		}, wantErr: "resolves to missing"},
		{name: "unknown skill show target", files: fstest.MapFS{
			"skills/main/SKILL.md": {Data: []byte("Run `munsu skill show absent-skill`.")},
		}, wantErr: "is not embedded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSkillBundle(tt.files, "skills")
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error=%v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func compareSkillDirectories(t *testing.T, fsys fs.FS, left, right string) {
	t.Helper()
	leftFiles := readSkillDirectory(t, fsys, left)
	rightFiles := readSkillDirectory(t, fsys, right)
	if len(leftFiles) != len(rightFiles) {
		t.Fatalf("file count differs: canonical=%d mirror=%d", len(leftFiles), len(rightFiles))
	}
	for name, leftData := range leftFiles {
		rightData, ok := rightFiles[name]
		if !ok {
			t.Errorf("mirror missing %s", name)
			continue
		}
		if string(leftData) != string(rightData) {
			t.Errorf("mirror content differs for %s", name)
		}
	}
}

func readSkillDirectory(t *testing.T, fsys fs.FS, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := fs.WalkDir(fsys, root, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSkillCmdRegistration(t *testing.T) {
	cmd := newSkillCmd()
	if cmd.Use != "skill" {
		t.Errorf("expected Use='skill', got %q", cmd.Use)
	}
	subs := cmd.Commands()
	if len(subs) == 0 {
		t.Fatal("expected skill subcommands")
	}
	hasList, hasShow := false, false
	for _, s := range subs {
		if s.Name() == "list" {
			hasList = true
		}
		if s.Name() == "show" {
			hasShow = true
		}
	}
	if !hasList {
		t.Error("expected 'skill list' subcommand")
	}
	if !hasShow {
		t.Error("expected 'skill show' subcommand")
	}
}
