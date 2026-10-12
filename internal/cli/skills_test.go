package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/minhtri2710/munsu/internal/harness"
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
	if !strings.Contains(content, "- For a ship brief, replace the `{TEST_IMPACT}` placeholder with the test-impact map: for each path the task will change, the existing tests that exercise it, found by code search (`semble` or `zg`).\n") {
		t.Error("expected munsu-ops skill step 4 to tell the orchestrator to fill {TEST_IMPACT}")
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

func TestReplaceSkill(t *testing.T) {
	embedded, err := skillFiles.ReadFile("skills/munsu-ops/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	write := func(t *testing.T, path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name         string
		setup        func(t *testing.T, dest string)
		wantReplaced bool
	}{
		{name: "replaces an existing dir and drops files absent from the embed", setup: func(t *testing.T, dest string) {
			write(t, filepath.Join(dest, "munsu-ops", "SKILL.md"), "old")
			write(t, filepath.Join(dest, "munsu-ops", "STALE.md"), "old")
		}, wantReplaced: true},
		{name: "installs into an empty destination", setup: func(t *testing.T, dest string) {}},
		{name: "leftover work dirs from a crashed run do not break the run", setup: func(t *testing.T, dest string) {
			write(t, filepath.Join(dest, "munsu-ops", "SKILL.md"), "old")
			write(t, filepath.Join(dest, ".munsu-ops.new", "munsu-ops", "STALE.md"), "partial")
			write(t, filepath.Join(dest, ".munsu-ops.old", "munsu-ops", "SKILL.md"), "aside")
		}, wantReplaced: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "skills")
			tc.setup(t, dest)
			replaced, err := replaceSkill(dest, "munsu-ops")
			if err != nil {
				t.Fatalf("replaceSkill: %v", err)
			}
			if replaced != tc.wantReplaced {
				t.Errorf("replaced = %v, want %v", replaced, tc.wantReplaced)
			}
			got, err := os.ReadFile(filepath.Join(dest, "munsu-ops", "SKILL.md"))
			if err != nil || string(got) != string(embedded) {
				t.Errorf("SKILL.md is not the embedded copy (err %v)", err)
			}
			for _, p := range []string{filepath.Join("munsu-ops", "STALE.md"), ".munsu-ops.new", ".munsu-ops.old"} {
				if _, err := os.Lstat(filepath.Join(dest, p)); !os.IsNotExist(err) {
					t.Errorf("%s still present after replace (err %v)", p, err)
				}
			}
		})
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
// canonical by TestAgentSkillMirrorsMatchCanonical. A skill belongs here whenever
// the mirror exists at all: an unlisted mirror is a copy nothing compares.
var agentMirrorSkills = []string{
	"bootstrap-diagnostics",
	"captain-provisioning",
	"harness-adapters",
	"munsu-ops",
	"munsu-update",
	"stuck-soldier-recovery",
}

// embeddedOnlySkills are explicitly embedded-only: no .agents/skills mirror.
var embeddedOnlySkills = []string{"afk", "ask-user-authority", "decision-hold-lifecycle", "diagnostic-reasoning"}

func validateAgentMirrorCoverage(repo fs.FS, embeddedNames, declaredNames []string) error {
	declared := make(map[string]bool, len(declaredNames))
	for _, name := range declaredNames {
		declared[name] = true
	}

	var errs []error
	for _, name := range embeddedNames {
		mirror := path.Join(".agents", "skills", name)
		info, err := fs.Stat(repo, mirror)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("stat mirror %s: %w", mirror, err))
			}
			continue
		}
		if !info.IsDir() {
			continue
		}
		if !declared[name] {
			errs = append(errs, fmt.Errorf("embedded skill %q has an .agents/skills mirror; add it to agentMirrorSkills or delete the mirror", name))
		}
	}

	for _, name := range declaredNames {
		mirror := path.Join(".agents", "skills", name)
		info, err := fs.Stat(repo, mirror)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("declared mirror %s is missing", mirror))
			} else {
				errs = append(errs, fmt.Errorf("stat declared mirror %s: %w", mirror, err))
			}
			continue
		}
		if !info.IsDir() {
			errs = append(errs, fmt.Errorf("declared mirror %s is not a directory", mirror))
		}
	}
	return errors.Join(errs...)
}

func TestAgentMirrorCoverage(t *testing.T) {
	repo := fstest.MapFS{
		".agents/skills/embedded/SKILL.md":  {Data: []byte("skill")},
		".agents/skills/declared/SKILL.md":  {Data: []byte("skill")},
		".agents/skills/unrelated/SKILL.md": {Data: []byte("skill")},
	}

	if err := validateAgentMirrorCoverage(repo, []string{"embedded", "declared"}, []string{"declared"}); err == nil || !strings.Contains(err.Error(), `embedded skill "embedded"`) {
		t.Fatalf("expected undeclared mirror error, got %v", err)
	}
	if err := validateAgentMirrorCoverage(repo, nil, []string{"missing"}); err == nil || !strings.Contains(err.Error(), "declared mirror .agents/skills/missing is missing") {
		t.Fatalf("expected missing declared mirror error, got %v", err)
	}
	// The fixture also contains an unrelated mirror; only embedded names are checked.
	if err := validateAgentMirrorCoverage(repo, []string{"declared"}, []string{"declared"}); err != nil {
		t.Fatalf("expected declared mirror to pass and unrelated mirror to be ignored: %v", err)
	}
}

// TestEmbeddedSkillParityCoverage is the parity gate over every embedded skill
// name. Each name must carry a disposition: a machine-compared mirror
// (agentMirrorSkills pins .agents/skills/<name>) or an explicit embedded-only
// disposition. For embedded names, on-disk mirrors must correspond exactly to
// agentMirrorSkills. embedded-only is exclusive: it asserts the absence of the
// mirror agentMirrorSkills pins, so holding both is a contradiction.
func TestEmbeddedSkillParityCoverage(t *testing.T) {
	names, err := embeddedSkillNames()
	if err != nil {
		t.Fatal(err)
	}

	classified := map[string][]string{}
	for _, n := range agentMirrorSkills {
		classified[n] = append(classified[n], "agent-mirror")
	}
	for _, s := range embeddedOnlySkills {
		classified[s] = append(classified[s], "embedded-only")
	}

	embedded := make(map[string]bool, len(names))
	for _, n := range names {
		embedded[n] = true
		if len(classified[n]) == 0 {
			t.Errorf("embedded skill %q has no parity disposition; add it to agentMirrorSkills or embeddedOnlySkills", n)
		}
	}
	for n, dispositions := range classified {
		if !embedded[n] {
			t.Errorf("parity disposition names %q, but no such embedded skill exists", n)
		}
		if len(dispositions) > 1 && slices.Contains(dispositions, "embedded-only") {
			t.Errorf("skill %q is declared embedded-only and %v; embedded-only asserts the absence of the mirror agentMirrorSkills pins", n, dispositions)
		}
	}

	repo := os.DirFS(filepath.Join("..", ".."))
	if err := validateAgentMirrorCoverage(repo, names, agentMirrorSkills); err != nil {
		t.Error(err)
	}
	for _, name := range embeddedOnlySkills {
		t.Run(name, func(t *testing.T) {
			if _, err := fs.Stat(repo, path.Join(".agents", "skills", name)); err == nil {
				t.Errorf("embedded-only skill %q has an .agents/skills mirror; move it to agentMirrorSkills or remove the mirror", name)
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

func TestInstalledMunsuOpsReferencesResolve(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills")
	installed, err := installSkills(dest, "")
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

// TestHarnessAdaptersLaunchTemplateTable pins each Launch templates row of the
// harness-adapters skill to the adapter registry, one row per verified adapter.
func TestHarnessAdaptersLaunchTemplateTable(t *testing.T) {
	content, err := readEmbeddedSkill("harness-adapters")
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(content, "## Launch templates\n")
	if !ok {
		t.Fatal("harness-adapters has no Launch templates section")
	}
	table, _, _ = strings.Cut(table, "\n\n")
	flag := func(v string) string {
		if v == "" {
			return "none"
		}
		return "`" + v + "`"
	}
	rows := map[string]string{}
	for _, line := range strings.Split(table, "\n") {
		cells := strings.Split(strings.Trim(line, "| "), " | ")
		if len(cells) == 5 {
			rows[cells[0]] = line
		}
	}
	for name, adapter := range harness.Adapters {
		tmpl := adapter.LaunchTemplate
		defaults := "none"
		if tmpl.DefaultModel != "" {
			defaults = "`" + tmpl.DefaultModel + "` / `" + tmpl.DefaultEffort + "`"
		}
		extra := "none"
		if len(tmpl.ExtraArgs) > 0 {
			extra = "`" + strings.Join(tmpl.ExtraArgs, " ") + "`"
		}
		want := fmt.Sprintf("| %s | %s | %s | %s | %s |", name, flag(tmpl.ModelFlag), flag(tmpl.EffortFlag), defaults, extra)
		if rows[name] != want {
			t.Errorf("Launch templates row for %s = %q, want %q", name, rows[name], want)
		}
	}
}
