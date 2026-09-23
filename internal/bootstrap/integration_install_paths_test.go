package bootstrap

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestProjectScopeInstallPathsMatchesInstalledFiles pins that the derived
// paths are exactly the files a project-scope Install writes, per harness.
func TestProjectScopeInstallPathsMatchesInstalledFiles(t *testing.T) {
	bin := t.TempDir()
	writeTestExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\necho 0.79.0\n")
	writeTestExecutable(t, filepath.Join(bin, "node"), "#!/bin/sh\necho 'API probe passed'\n")
	writeTestExecutable(t, filepath.Join(bin, "munsu"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))
	SetMunsuPathResolver(testMunsuResolver{path: testutil.FakeExecutablePath(filepath.Join(bin, "munsu"))})
	t.Cleanup(ResetMunsuPathResolver)

	for _, name := range []string{harness.Pi, harness.Claude, harness.Codex, harness.Grok, harness.Opencode, harness.Agy} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Install(dir, dir, name, ScopeProject, false); err != nil {
				t.Fatal(err)
			}
			var written []string
			if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, err := filepath.Rel(dir, path)
				written = append(written, filepath.ToSlash(rel))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			got, err := ProjectScopeInstallPaths(dir, name)
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(written)
			slices.Sort(got)
			if len(written) == 0 || !slices.Equal(got, written) {
				t.Fatalf("ProjectScopeInstallPaths = %v, Install wrote %v", got, written)
			}
		})
	}
}

func TestProjectScopeInstallPathsUnsupportedHarnessWritesNothing(t *testing.T) {
	got, err := ProjectScopeInstallPaths(t.TempDir(), "no-such-harness")
	if err != nil || got != nil {
		t.Fatalf("ProjectScopeInstallPaths = %v, %v; want nil, nil", got, err)
	}
}

func TestProjectScopeInstallPathsRefusesUnresolvableCwd(t *testing.T) {
	if _, err := ProjectScopeInstallPaths(filepath.Join(t.TempDir(), "missing"), harness.Pi); err == nil {
		t.Fatal("want error for a cwd that does not exist")
	}
}
