package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptainIntegrationEnsureMissingPiFailsClosed(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "munsu"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin)

	err := (captainIntegrationAdapter{}).EnsureCaptain(home)
	if err == nil || !strings.Contains(err.Error(), "installing canonical Pi integration") {
		t.Fatalf("EnsureCaptain() error = %v, want fail-closed canonical integration error", err)
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
	if err := os.WriteFile(aliasPath, []byte("// legacy alias\n"), 0644); err != nil {
		t.Fatal(err)
	}

	adapter := captainIntegrationAdapter{}
	if err := adapter.EnsureCaptain(home); err != nil {
		t.Fatalf("EnsureCaptain() error: %v", err)
	}
	if err := adapter.EnsureCaptain(home); err != nil {
		t.Fatalf("second EnsureCaptain() error: %v", err)
	}

	extensions, err := os.ReadDir(filepath.Join(home, ".pi", "extensions"))
	if err != nil {
		t.Fatalf("ReadDir() error: %v", err)
	}
	if len(extensions) != 2 || extensions[0].Name() != "munsu-captain-pi-watch.ts" || extensions[1].Name() != "munsu-pi-integration.ts" {
		t.Fatalf("extensions = %v, want preserved legacy alias plus canonical integration", entryNames(extensions))
	}
	status, err := adapter.Status(home, "pi")
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if status.State != "installed" || status.Scope != "project" {
		t.Fatalf("Status() = %+v, want installed project integration", status)
	}
}

func TestRequireCaptainIntegrationIgnoresNonPiHarness(t *testing.T) {
	if err := requireCaptainIntegration(t.TempDir(), "claude"); err != nil {
		t.Fatalf("non-Pi integration check error: %v", err)
	}
}

func TestRequireCaptainIntegrationRejectsDigestDrift(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\necho 0.79.0\n")
	writeExecutable(t, filepath.Join(bin, "node"), "#!/bin/sh\necho 'API probe passed'\n")
	writeExecutable(t, filepath.Join(bin, "munsu"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))
	adapter := captainIntegrationAdapter{}
	if err := adapter.EnsureCaptain(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".pi", "extensions", "munsu-pi-integration.ts")
	if err := os.WriteFile(path, []byte("// modified\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := requireCaptainIntegration(home, "pi")
	if err == nil || !strings.Contains(err.Error(), "drifted") || !strings.Contains(err.Error(), "integrate repair") {
		t.Fatalf("requireCaptainIntegration() error = %v, want actionable drift refusal", err)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
