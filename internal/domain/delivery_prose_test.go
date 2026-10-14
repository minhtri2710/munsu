package domain

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDeliveryDiffProseOnly(t *testing.T) {
	one := func(path, status string) []DeliveryChange { return []DeliveryChange{{Path: path, Status: status}} }
	cases := []struct {
		name string
		diff DeliveryDiff
		want bool
	}{
		{"docs markdown modified", DeliveryDiff{Listed: true, Changes: one("docs/guide.md", "M")}, true},
		{"root markdown added", DeliveryDiff{Listed: true, Changes: one("README.md", "A")}, true},
		{"text file deleted", DeliveryDiff{Listed: true, Changes: one("notes.txt", "D")}, true},
		{"docs shell script is not prose", DeliveryDiff{Listed: true, Changes: one("docs/build.sh", "M")}, false},
		{"docs makefile is not prose", DeliveryDiff{Listed: true, Changes: one("docs/Makefile", "M")}, false},
		{"docs python script is not prose", DeliveryDiff{Listed: true, Changes: one("docs/deploy.py", "A")}, false},
		{"docs symlink is not prose", DeliveryDiff{Listed: true, Changes: []DeliveryChange{{Path: "docs/guide.md", Status: "A", Typed: true}}}, false},
		{"go source is not prose", DeliveryDiff{Listed: true, Changes: one("main.go", "M")}, false},
		{"prose mixed with code", DeliveryDiff{Listed: true, Changes: []DeliveryChange{{Path: "docs/a.md", Status: "M"}, {Path: "main.go", Status: "M"}}}, false},
		{"embedded skill is excluded", DeliveryDiff{Listed: true, Changes: one("internal/cli/skills/munsu-ops/SKILL.md", "M")}, false},
		{"agents skill is excluded", DeliveryDiff{Listed: true, Changes: one(".agents/skills/munsu-ops/SKILL.md", "M")}, false},
		{"embedded manual is excluded", DeliveryDiff{Listed: true, Changes: one("internal/cli/seed_orchestrator_manual.md", "M")}, false},
		{"AGENTS.md is excluded", DeliveryDiff{Listed: true, Changes: one("AGENTS.md", "M")}, false},
		{"nested CLAUDE.md is excluded", DeliveryDiff{Listed: true, Changes: one("internal/x/CLAUDE.md", "M")}, false},
		{"gating github text is excluded", DeliveryDiff{Listed: true, Changes: one(".github/flake-ledger.md", "M")}, false},
		{"test fixture text is excluded", DeliveryDiff{Listed: true, Changes: one("internal/domain/testdata/codex.txt", "M")}, false},
		{"binary under docs is not prose", DeliveryDiff{Listed: true, Changes: []DeliveryChange{{Path: "docs/shot.png", Status: "A", Binary: true}}}, false},
		{"rename is not prose", DeliveryDiff{Listed: true, Changes: one("docs/a.md", "R")}, false},
		{"type change is not prose", DeliveryDiff{Listed: true, Changes: one("docs/a.md", "T")}, false},
		{"unlisted diff is not prose", DeliveryDiff{Changes: one("docs/a.md", "M")}, false},
		{"empty diff is not prose", DeliveryDiff{Listed: true}, false},
		{"path escaping the tree is not prose", DeliveryDiff{Listed: true, Changes: one("docs/../main.go", "M")}, false},
		{"absolute path is not prose", DeliveryDiff{Listed: true, Changes: one("/docs/a.md", "M")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.diff.ProseOnly(); got != tc.want {
				t.Fatalf("ProseOnly() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Every Go-embedded file must stay outside the prose class, so embedded
// doctrine can never ride a prose-only delivery. The walk covers every
// //go:embed directive under internal/ and cmd/.
func TestEmbeddedFilesAreNeverProse(t *testing.T) {
	root := filepath.Join("..", "..")
	embedLine := regexp.MustCompile(`^//go:embed\s+(.+)$`)
	var embedded []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(p) != ".go" {
				return err
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(src), "\n") {
				m := embedLine.FindStringSubmatch(strings.TrimSpace(line))
				if m == nil {
					continue
				}
				for _, pattern := range strings.Fields(m[1]) {
					matches, err := filepath.Glob(filepath.Join(filepath.Dir(p), pattern))
					if err != nil {
						return err
					}
					for _, match := range matches {
						embedded = append(embedded, embeddedFiles(t, match)...)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(embedded) == 0 {
		t.Fatal("found no //go:embed files; the walk is broken")
	}
	for _, p := range embedded {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		diff := DeliveryDiff{Listed: true, Changes: []DeliveryChange{{Path: filepath.ToSlash(rel), Status: "M"}}}
		if diff.ProseOnly() {
			t.Errorf("embedded file %s is classified as prose", rel)
		}
	}
}

// embeddedFiles lists the files a go:embed match embeds: the file itself, or
// every regular file under a matched directory that Go would include.
func embeddedFiles(t *testing.T, match string) []string {
	t.Helper()
	info, err := os.Stat(match)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		return []string{match}
	}
	var out []string
	err = filepath.WalkDir(match, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if name := d.Name(); strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
