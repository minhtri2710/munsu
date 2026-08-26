package cli

import (
	"io/fs"
	"os"
	"path"
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

func TestReadEmbeddedSkillAll(t *testing.T) {
	names, err := embeddedSkillNames()
	if err != nil {
		t.Fatalf("embeddedSkillNames() error: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one embedded skill")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			content, err := readEmbeddedSkill(name)
			if err != nil {
				t.Fatalf("readEmbeddedSkill(%q) error: %v", name, err)
			}
			if len(content) == 0 {
				t.Fatalf("readEmbeddedSkill(%q) returned empty content", name)
			}
		})
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

	// Second install with overwrite=false should not install
	ok, err = installOneSkill(dest, "munsu-ops", false)
	if err != nil {
		t.Fatalf("installOneSkill (second): %v", err)
	}
	if ok {
		t.Error("expected false for second install without overwrite")
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

// agentMirrorSkills have .agents/skills/<name> machine-compared to the embedded
// canonical by TestAgentSkillMirrorsMatchCanonical.
var agentMirrorSkills = []string{"captain-provisioning", "munsu-ops", "munsu-update"}

// referenceDocSkills have docs/skills/<name>.md machine-compared to the embedded
// REFERENCE.md by TestAgentSkillReferencesMatchEmbeddedCanonical.
var referenceDocSkills = []string{"bootstrap-diagnostics", "harness-adapters", "stuck-soldier-recovery"}

// embeddedOnlySkills are explicitly embedded-only: no .agents/skills mirror and
// at most the declared active secondary doc under docs/skills/. Any other external
// copy must move the skill to a machine-compared set.
var embeddedOnlySkills = []struct {
	name string
	doc  string // optional active secondary doc (basename under docs/skills/); "" = none
}{
	{name: "afk", doc: "afk.md"},
	{name: "ask-user-authority"},
	{name: "decision-hold-lifecycle", doc: "decision-hold-lifecycle.md"},
	{name: "diagnostic-reasoning"},
}

// TestEmbeddedSkillParityCoverage is the parity gate over every embedded skill
// name: each name must carry exactly one disposition — a machine-compared
// canonical mirror/reference (agentMirrorSkills, referenceDocSkills) or an
// explicit embedded-only disposition (embeddedOnlySkills).
func TestEmbeddedSkillParityCoverage(t *testing.T) {
	names, err := embeddedSkillNames()
	if err != nil {
		t.Fatal(err)
	}

	classified := map[string]string{}
	for _, n := range agentMirrorSkills {
		classified[n] = "agent-mirror"
	}
	for _, n := range referenceDocSkills {
		classified[n] = "reference-doc"
	}
	for _, s := range embeddedOnlySkills {
		classified[s.name] = "embedded-only"
	}

	embedded := make(map[string]bool, len(names))
	for _, n := range names {
		embedded[n] = true
		if _, ok := classified[n]; !ok {
			t.Errorf("embedded skill %q has no parity disposition; add it to agentMirrorSkills, referenceDocSkills, or embeddedOnlySkills", n)
		}
	}
	for n := range classified {
		if !embedded[n] {
			t.Errorf("parity disposition names %q, but no such embedded skill exists", n)
		}
	}

	repo := os.DirFS(filepath.Join("..", ".."))
	for _, s := range embeddedOnlySkills {
		t.Run(s.name, func(t *testing.T) {
			if _, err := fs.Stat(repo, path.Join(".agents", "skills", s.name)); err == nil {
				t.Errorf("embedded-only skill %q has an .agents/skills mirror; move it to agentMirrorSkills or remove the mirror", s.name)
			}
			docPath := path.Join("docs", "skills", s.name+".md")
			_, docErr := fs.Stat(repo, docPath)
			if s.doc == "" {
				if docErr == nil {
					t.Errorf("undeclared secondary doc %s exists; declare it in embeddedOnlySkills or move %q to referenceDocSkills", docPath, s.name)
				}
				return
			}
			if docErr != nil {
				t.Errorf("declared secondary doc %s is missing: %v", docPath, docErr)
				return
			}
			refPath := path.Join("internal", "cli", "skills", s.name, "REFERENCE.md")
			doc, docReadErr := fs.ReadFile(repo, docPath)
			ref, refReadErr := fs.ReadFile(repo, refPath)
			if docReadErr == nil && refReadErr == nil && string(doc) == string(ref) {
				t.Errorf("%s is byte-identical to %s; move %q to referenceDocSkills instead of embedded-only", docPath, refPath, s.name)
			}
		})
	}
}

func TestAgentSkillMirrorsMatchCanonical(t *testing.T) {
	repo := os.DirFS(filepath.Join("..", ".."))
	for _, name := range agentMirrorSkills {
		t.Run(name, func(t *testing.T) {
			canonical := path.Join("internal", "cli", "skills", name)
			mirror := path.Join(".agents", "skills", name)
			compareSkillDirectories(t, repo, canonical, mirror)
		})
	}
}

func TestAgentSkillReferencesMatchEmbeddedCanonical(t *testing.T) {
	repo := os.DirFS(filepath.Join("..", ".."))
	for _, name := range referenceDocSkills {
		t.Run(name, func(t *testing.T) {
			docPath := path.Join("docs", "skills", name+".md")
			referencePath := path.Join("internal", "cli", "skills", name, "REFERENCE.md")
			doc, err := fs.ReadFile(repo, docPath)
			if err != nil {
				t.Fatal(err)
			}
			reference, err := fs.ReadFile(repo, referencePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(doc) != string(reference) {
				t.Errorf("agent reference %s differs from embedded canonical %s", docPath, referencePath)
			}
		})
	}
}

func TestInstalledMunsuOpsReferencesResolve(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills")
	installed, err := installSkills(dest, "", nil)
	if err != nil {
		t.Fatalf("installSkills: %v", err)
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
		files[strings.TrimPrefix(filename, root+"/")] = data
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
