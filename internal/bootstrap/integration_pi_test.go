package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestPiStatusDistinguishesMissingAndInvalidCanonicalIntegration(t *testing.T) {
	home, canonical := installTestPiIntegration(t)
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	missing, err := Status(home, home, harness.Pi, ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(missing.Message, "is missing") {
		t.Fatalf("missing status = %+v", missing)
	}

	if _, err := Install(home, home, harness.Pi, ScopeProject, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("// modified\n"), 0644); err != nil {
		t.Fatal(err)
	}
	invalid, err := Status(home, home, harness.Pi, ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(invalid.Message, "digest is invalid") {
		t.Fatalf("invalid status = %+v", invalid)
	}
}

func installTestPiIntegration(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	bin := t.TempDir()
	writeTestExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\necho 0.79.0\n")
	writeTestExecutable(t, filepath.Join(bin, "node"), "#!/bin/sh\necho 'API probe passed'\n")
	writeTestExecutable(t, filepath.Join(bin, "munsu"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))
	SetMunsuPathResolver(testMunsuResolver{path: testutil.FakeExecutablePath(filepath.Join(bin, "munsu"))})
	t.Cleanup(ResetMunsuPathResolver)
	if _, err := Install(home, home, harness.Pi, ScopeProject, false); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(ProjectExtensionsDir(home), harness.CanonicalPiIntegrationName)
}

func writeTestExecutable(t *testing.T, path, content string) {
	t.Helper()
	testutil.WriteFakeExecutable(t, path, content)
}
