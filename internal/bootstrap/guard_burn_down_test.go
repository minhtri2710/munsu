package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/harness"
)

func TestHarnessHookDirsRejectEmptyProjectCwd(t *testing.T) {
	cases := []struct {
		name string
		call func() error
	}{
		{"agy", func() error { _, err := agyHooksDir(ScopeProject, ""); return err }},
		{"claude", func() error { _, err := claudeSettingsPath(ScopeProject, ""); return err }},
		{"codex", func() error { _, err := codexHooksPath(ScopeProject, ""); return err }},
		{"grok", func() error { _, err := grokHooksDir(ScopeProject, ""); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || !strings.Contains(err.Error(), "cwd is required for project scope") {
				t.Fatalf("error = %v, want empty-cwd refusal", err)
			}
		})
	}
}

func TestAssertSupportedHarnessRejectsKnownHarnessWithoutCapabilities(t *testing.T) {
	original := append([]string(nil), harness.KnownHarnesses...)
	harness.KnownHarnesses = append(harness.KnownHarnesses, "future")
	defer func() { harness.KnownHarnesses = original }()

	err := AssertSupportedHarness("future")
	if err == nil || !strings.Contains(err.Error(), "has no integration capabilities yet") {
		t.Fatalf("AssertSupportedHarness error = %v, want missing-capabilities refusal", err)
	}
}

func TestExpectedTargetPathRejectsEmptyProjectCwd(t *testing.T) {
	_, err := ExpectedTargetPath(ScopeProject, "")
	if err == nil || !strings.Contains(err.Error(), "cwd is required for project scope") {
		t.Fatalf("ExpectedTargetPath error = %v, want empty-cwd refusal", err)
	}
}

func TestExpectedTargetPathRejectsMissingUserHome(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := ExpectedTargetPath(ScopeUser, "")
	if err == nil || !strings.Contains(err.Error(), "cannot determine user extensions directory") {
		t.Fatalf("ExpectedTargetPath error = %v, want missing-home refusal", err)
	}
}

func TestProbePiAPIsRejectsEmptyBinaryPath(t *testing.T) {
	err := probePiAPIs("")
	if err == nil || !strings.Contains(err.Error(), "pi binary path required for API probe") {
		t.Fatalf("probePiAPIs error = %v, want empty-binary refusal", err)
	}
}

func TestProbePiAPIsRejectsMissingSuccessMarker(t *testing.T) {
	previous := SetCapabilityCommandRunner(func(string, []string, string, time.Duration) (string, error) {
		return "API probe failed\n", nil
	})
	defer SetCapabilityCommandRunner(previous)

	err := probePiAPIs("/fake/pi")
	if err == nil || !strings.Contains(err.Error(), "API probe did not complete") {
		t.Fatalf("probePiAPIs error = %v, want missing-success-marker refusal", err)
	}
}

func TestValidateStrictRejectsManifestMismatches(t *testing.T) {
	caps := []Capability{CapSessionStart}
	valid := Manifest{
		SchemaVersion: "munsu.integrate/v1",
		Harness:       "pi",
		Version:       "1.0.0",
		Scope:         "user",
		TargetPaths:   []string{"/tmp/munsu-pi-integration.ts"},
		Capabilities:  []string{"session-start"},
		ContentDigest: "digest",
	}
	cases := []struct {
		name string
		edit func(*Manifest)
		want string
	}{
		{"harness", func(m *Manifest) { m.Harness = "claude" }, "harness mismatch"},
		{"scope", func(m *Manifest) { m.Scope = "project" }, "scope mismatch"},
		{"version", func(m *Manifest) { m.Version = "2.0.0" }, "version mismatch"},
		{"target count", func(m *Manifest) { m.TargetPaths = nil }, "target path count mismatch"},
		{"relative target", func(m *Manifest) { m.TargetPaths = []string{"relative/path"} }, "is not absolute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := valid
			tc.edit(&manifest)
			err := ValidateStrict(manifest, "pi", "user", "1.0.0", caps, 1)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateStrict error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOpencodePluginsDirRejectsEmptyProjectCwd(t *testing.T) {
	_, err := opencodePluginsDir(ScopeProject, "")
	if err == nil || !strings.Contains(err.Error(), "cwd is required for project scope") {
		t.Fatalf("opencodePluginsDir error = %v, want empty-cwd refusal", err)
	}
}

func TestOpencodeInstallRejectsUnknownPluginContent(t *testing.T) {
	original := append([]string(nil), opencodePluginFileNames...)
	opencodePluginFileNames = []string{"unknown.js"}
	defer func() { opencodePluginFileNames = original }()

	SetMunsuPathResolver(testMunsuResolver{path: "/fake/munsu"})
	defer ResetMunsuPathResolver()
	_, _, _, err := (&OpencodeAdapter{Cwd: t.TempDir(), Scope: string(ScopeProject), DryRun: true}).InstallOpencodePlugins()
	if err == nil || !strings.Contains(err.Error(), "cannot generate content for plugin unknown.js") {
		t.Fatalf("InstallOpencodePlugins error = %v, want unknown-content refusal", err)
	}
}

func TestPiInstallRejectsEmptyUserExtensionDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	bin := t.TempDir()
	writeTestExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\necho 0.79.0\n")
	writeTestExecutable(t, filepath.Join(bin, "node"), "#!/bin/sh\necho 'API probe passed'\n")
	t.Setenv("PATH", bin+string(filepath.ListSeparator)+os.Getenv("PATH"))

	_, _, _, err := (&PiAdapter{Scope: string(ScopeUser), DryRun: true}).InstallPiExtension()
	if err == nil || !strings.Contains(err.Error(), "cannot determine extension directory") {
		t.Fatalf("InstallPiExtension error = %v, want empty-extension-directory refusal", err)
	}
}

func TestPiInstallRejectsUnownedTarget(t *testing.T) {
	dir := t.TempDir()
	extDir := ProjectExtensionsDir(dir)
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(extDir, harness.CanonicalPiIntegrationName)
	if err := os.WriteFile(target, []byte("// user-owned\n"), 0644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "munsu")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	SetMunsuPathResolver(testMunsuResolver{path: bin})
	defer ResetMunsuPathResolver()
	callCount := 0
	previousRunner := SetCapabilityCommandRunner(func(_ string, _ []string, _ string, _ time.Duration) (string, error) {
		callCount++
		if callCount == 1 {
			return "0.79.0\n", nil
		}
		return "API probe passed\n", nil
	})
	defer SetCapabilityCommandRunner(previousRunner)

	_, _, _, err := (&PiAdapter{Cwd: dir, Scope: string(ScopeProject), DryRun: true}).InstallPiExtension()
	if err == nil || !strings.Contains(err.Error(), "exists and is not owned by munsu") {
		t.Fatalf("InstallPiExtension error = %v, want unowned-target refusal", err)
	}
}
