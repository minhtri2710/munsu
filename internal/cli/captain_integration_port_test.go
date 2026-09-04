package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestCaptainIntegrationEnsureMissingPiFailsClosed(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "munsu"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin)

	err := (captainIntegrationAdapter{}).EnsureCaptain(home, "pi")
	if err == nil || !strings.Contains(err.Error(), "installing captain pi integration") {
		t.Fatalf("EnsureCaptain() error = %v, want fail-closed integration error", err)
	}
}

func TestCaptainIntegrationEnsureWritesOnlyCanonicalPiExtension(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\necho 0.79.0\n")
	writeExecutable(t, filepath.Join(bin, "node"), "#!/bin/sh\necho 'API probe passed'\n")
	writeExecutable(t, filepath.Join(bin, "munsu"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))

	aliasPath := filepath.Join(home, ".pi", "extensions", "munsu-captain-pi-watch.ts")
	if err := os.MkdirAll(filepath.Dir(aliasPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aliasPath, []byte("// munsu-integrate v1 -- do not edit this section\n// legacy alias\n"), 0644); err != nil {
		t.Fatal(err)
	}

	adapter := captainIntegrationAdapter{}
	if err := adapter.EnsureCaptain(home, "pi"); err != nil {
		t.Fatalf("EnsureCaptain() error: %v", err)
	}
	if err := adapter.EnsureCaptain(home, "pi"); err != nil {
		t.Fatalf("second EnsureCaptain() error: %v", err)
	}

	extensions, err := os.ReadDir(filepath.Join(home, ".pi", "extensions"))
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	if len(extensions) != 1 || extensions[0].Name() != "munsu-pi-integration.ts" {
		t.Fatalf("extensions = %v, want only canonical integration", entryNames(extensions))
	}
	status, err := adapter.Status(home, "pi")
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status.State != "installed" || status.Scope != "project" {
		t.Fatalf("Status() = %+v, want installed project integration", status)
	}
}

func TestCaptainIntegrationEnsureRefusesUnownedCompatibilityAlias(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\necho 0.79.0\n")
	writeExecutable(t, filepath.Join(bin, "node"), "#!/bin/sh\necho 'API probe passed'\n")
	writeExecutable(t, filepath.Join(bin, "munsu"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))

	aliasPath := filepath.Join(home, ".pi", "extensions", "munsu-captain-turnend-guard.ts")
	if err := os.MkdirAll(filepath.Dir(aliasPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aliasPath, []byte("// user-owned extension\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := (captainIntegrationAdapter{}).EnsureCaptain(home, "pi")
	if err == nil || !strings.Contains(err.Error(), aliasPath) || !strings.Contains(err.Error(), "not owned by munsu") {
		t.Fatalf("EnsureCaptain() error = %v, want actionable unowned alias refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".pi", "extensions", "munsu-pi-integration.ts")); !os.IsNotExist(statErr) {
		t.Fatalf("canonical integration created before alias conflict was resolved: %v", statErr)
	}
}

func TestCaptainIntegrationStatusDetectsDigestDrift(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\necho 0.79.0\n")
	writeExecutable(t, filepath.Join(bin, "node"), "#!/bin/sh\necho 'API probe passed'\n")
	writeExecutable(t, filepath.Join(bin, "munsu"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))
	adapter := captainIntegrationAdapter{}
	if err := adapter.EnsureCaptain(home, "pi"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".pi", "extensions", "munsu-pi-integration.ts")
	if err := os.WriteFile(path, []byte("// modified\n"), 0644); err != nil {
		t.Fatal(err)
	}

	status, err := adapter.Status(home, "pi")
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status.State != "drifted" {
		t.Fatalf("Status() = %+v, want drifted after modifying installed extension", status)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	testutil.WriteFakeExecutable(t, path, content)
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
