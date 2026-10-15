package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

func TestEmbeddedSkillReferencesResolve(t *testing.T) {
	if err := validateSkillBundle(skillFiles, "skills"); err != nil {
		t.Fatal(err)
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
		{name: "absolute path", files: fstest.MapFS{
			"skills/main/SKILL.md": {Data: []byte("See [passwd](/etc/passwd).")},
		}, wantErr: "absolute paths are not allowed"},
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

func TestSkillCmdRegistration(t *testing.T) {
	t.Setenv("MUNSU_HOME", t.TempDir())
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"skill", "list", "--output", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("skill list: %v", err)
	}
	var resp Response[MessageResult]
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode skill list output %q: %v", out.String(), err)
	}
	names, err := embeddedSkillNames()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	if got := strings.Split(resp.Data.Message, "\n"); !slices.Equal(got, names) {
		t.Errorf("skill list printed %q, want %q", got, names)
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
		if len(cells) == 4 {
			rows[cells[0]] = line
		}
	}
	for name, adapter := range harness.Adapters {
		tmpl := adapter.LaunchTemplate
		extra := "none"
		if len(tmpl.ExtraArgs) > 0 {
			extra = "`" + strings.Join(tmpl.ExtraArgs, " ") + "`"
		}
		want := fmt.Sprintf("| %s | %s | %s | %s |", name, flag(tmpl.ModelFlag), flag(tmpl.EffortFlag), extra)
		if rows[name] != want {
			t.Errorf("Launch templates row for %s = %q, want %q", name, rows[name], want)
		}
	}
}
