package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestPiCompatibilityAliasesAreRejectedByStatus(t *testing.T) {
	for _, alias := range harness.PiIntegrationAliasNames() {
		t.Run(alias, func(t *testing.T) {
			home, _ := installTestPiIntegration(t)
			aliasPath := filepath.Join(ProjectExtensionsDir(home), alias)
			if err := os.WriteFile(aliasPath, []byte("// munsu-integrate v1 -- do not edit this section\n"), 0644); err != nil {
				t.Fatal(err)
			}

			status, err := Status(home, home, harness.Pi, ScopeProject)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != "drifted" || !strings.Contains(status.Message, aliasPath) {
				t.Fatalf("Status() = %+v, want alias-specific drift", status)
			}
		})
	}
}

func TestPiRepairRemovesOwnedCompatibilityAliases(t *testing.T) {
	home, _ := installTestPiIntegration(t)
	for _, alias := range harness.PiIntegrationAliasNames() {
		aliasPath := filepath.Join(ProjectExtensionsDir(home), alias)
		if err := os.WriteFile(aliasPath, []byte("// munsu-integrate v1 -- do not edit this section\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Repair(home, home, harness.Pi, ScopeProject, false); err != nil {
		t.Fatal(err)
	}
	for _, alias := range harness.PiIntegrationAliasNames() {
		if _, err := os.Stat(filepath.Join(ProjectExtensionsDir(home), alias)); !os.IsNotExist(err) {
			t.Fatalf("alias %s still exists: %v", alias, err)
		}
	}
}

func TestPiRepairRefusesAndPreservesUnownedCompatibilityAlias(t *testing.T) {
	home, canonical := installTestPiIntegration(t)
	aliasPath := filepath.Join(ProjectExtensionsDir(home), harness.PiIntegrationAliasNames()[0])
	if err := os.WriteFile(aliasPath, []byte("// user-owned\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Repair(home, home, harness.Pi, ScopeProject, false)
	if err == nil || !strings.Contains(err.Error(), "not owned by munsu") {
		t.Fatalf("Repair() error = %v, want unowned alias refusal", err)
	}
	if _, statErr := os.Stat(aliasPath); statErr != nil {
		t.Fatalf("unowned alias was removed: %v", statErr)
	}
	after, err := os.ReadFile(canonical)
	if err != nil || string(after) != string(before) {
		t.Fatalf("canonical changed during refused repair: %v", err)
	}
}

func TestPiRepairDryRunPreservesOwnedCompatibilityAlias(t *testing.T) {
	home, _ := installTestPiIntegration(t)
	aliasPath := filepath.Join(ProjectExtensionsDir(home), harness.PiIntegrationAliasNames()[0])
	if err := os.WriteFile(aliasPath, []byte("// munsu-integrate v1 -- do not edit this section\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Repair(home, home, harness.Pi, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(aliasPath); err != nil {
		t.Fatalf("dry-run removed alias: %v", err)
	}
}

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
