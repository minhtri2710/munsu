//go:build integration

package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestInvalidClosedSetInputsRefuse(t *testing.T) {
	t.Run("install tool", func(t *testing.T) {
		err := installTool("unknown-tool")
		if err == nil || !strings.Contains(err.Error(), "no automatic install") {
			t.Fatalf("installTool error = %v, want unsupported tool refusal", err)
		}
	})

	scopeCases := []struct {
		name string
		call func() error
	}{
		{"agy", func() error { _, err := agyHooksDir(Scope("invalid"), ""); return err }},
		{"claude", func() error { _, err := claudeSettingsPath(Scope("invalid"), ""); return err }},
		{"codex", func() error { _, err := codexHooksPath(Scope("invalid"), ""); return err }},
		{"grok", func() error { _, err := grokHooksDir(Scope("invalid"), ""); return err }},
		{"opencode", func() error { _, err := opencodePluginsDir(Scope("invalid"), ""); return err }},
		{"pi", func() error { _, err := ExpectedTargetPath(Scope("invalid"), ""); return err }},
	}
	for _, tc := range scopeCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || !strings.Contains(err.Error(), "unsupported scope") {
				t.Fatalf("scope error = %v, want unsupported scope refusal", err)
			}
		})
	}

	t.Run("doctor role", func(t *testing.T) {
		_, err := Doctor(t.TempDir(), Role("invalid"))
		if err == nil || !strings.Contains(err.Error(), "unknown role") {
			t.Fatalf("Doctor error = %v, want unknown role refusal", err)
		}
	})
}

// assertConfigContains fails t if result.Configs does not contain a ConfigDiagnostic
// whose String() output matches want.
func assertConfigContains(t *testing.T, configs []ConfigDiagnostic, want string) {
	t.Helper()
	for _, c := range configs {
		if c.String() == want {
			return
		}
	}
	var got []string
	for _, c := range configs {
		got = append(got, c.String())
	}
	t.Errorf("expected %q in Configs, got %v", want, got)
}

func TestRun_BackendDiagnostics_NoPersistedIdentityWithActiveTMUX(t *testing.T) {
	home := t.TempDir()
	// An active TMUX env must NOT resolve a backend for diagnostics: no
	// persisted snapshot identity exists, so the report is typed missing-input.
	t.Setenv("TMUX", "/tmp/tmux-xxx/default")
	t.Setenv("HERDR_ENV", "")

	result, err := Run(home, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: none (source: no persisted backend identity (set backend in the fleet base config))")
}

func TestRun_BackendDiagnostics_NoPersistedIdentityWithActiveHERDRENV(t *testing.T) {
	home := t.TempDir()
	// An active HERDR_ENV must NOT resolve a backend for diagnostics.
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "1")

	result, err := Run(home, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: none (source: no persisted backend identity (set backend in the fleet base config))")
}

func TestRun_BackendDiagnostics_NoPersistedIdentityWithTmuxOnPATH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")

	// A fake tmux on PATH must NOT be treated as a backend selection: PATH
	// probing is not a selection contract for diagnostics.
	fakeBin := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(fakeBin, "tmux"), "#!/bin/sh\nexit 0")
	testutil.PrependPath(t, fakeBin)

	result, err := Run(home, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: none (source: no persisted backend identity (set backend in the fleet base config))")
}

func TestRun_BackendDiagnostics_LegacyPinAloneIsNotAnIdentity(t *testing.T) {
	// A legacy config file pin is not a typed snapshot identity: without a
	// fleet base document or published snapshot, BACKEND_RESOLVED is typed
	// missing-input rather than the pin value.
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Run(home, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: herdr")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: none (source: no persisted backend identity (set backend in the fleet base config))")
}

func TestRun_BackendDiagnostics_PersistedFleetBaseBackend(t *testing.T) {
	home := t.TempDir()
	if err := config.StoreFleetBase(home, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux"},
	}); err != nil {
		t.Fatal(err)
	}
	// Env must not shadow the persisted typed Backend.
	t.Setenv("HERDR_ENV", "1")

	result, err := Run(home, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: tmux (source: fleet base document)")
}

func TestRun_BackendDiagnostics_PersistedPublishedSnapshotWins(t *testing.T) {
	home := t.TempDir()
	// The published snapshot is the composed typed truth and wins over the
	// fleet base document backend.
	if err := config.StoreFleetBase(home, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.StorePublishedSnapshot(home, config.ResolvedProjectConfig{
		Project:     "sample",
		ProjectPath: "/tmp/sample",
		Digest:      "abc",
		Backend:     "herdr",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := Run(home, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: herdr (source: published snapshot)")
}

func TestRun_BackendDiagnostics_UnrelatedOutputStable(t *testing.T) {
	// Verify that other Configs entries (like SOLDIER_HARNESS, SOLDIER_DISPATCH)
	// are not broken by the backend changes.
	home := t.TempDir()
	t.Setenv("TMUX", "/tmp/tmux-xxx")
	t.Setenv("HERDR_ENV", "")

	result, err := Run(home, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should not have any backend-unrelated Configs in a bare home
	// (SOLDIER_HARNESS and SOLDIER_DISPATCH require specific files).
	for _, c := range result.Configs {
		if strings.HasPrefix(c.String(), "BACKEND_") {
			continue
		}
		t.Errorf("unexpected Configs entry from bare home: %q", c.String())
	}
}

func TestRun_BackendDiagnostics_AutoConfigFileWithNothingAvailable(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write "auto" explicitly
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("auto\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")

	// Scrub PATH so no tmux is findable
	t.Setenv("PATH", "/dev/null")

	result, err := Run(home, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: none (source: no persisted backend identity (set backend in the fleet base config))")
}

func reclaimNone(_ string, reclaim func() error) (bool, error) { return true, reclaim() }
func reclaimEvery(string, func() error) (bool, error)          { return false, nil }

func setDirMtime(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(dir, at, at); err != nil {
		t.Fatal(err)
	}
}

// TestRun_RequireNoMistakesDiagnosticFromTypedBase verifies the bootstrap
// REQUIRE_NO_MISTAKES diagnostic reads the typed fleet base document, and that
// a legacy flat config file alone does not produce it.
func TestRun_RequireNoMistakesDiagnosticFromTypedBase(t *testing.T) {
	t.Run("typed base requireNoMistakes=true", func(t *testing.T) {
		home := t.TempDir()
		if err := config.StoreFleetBase(home, config.FleetBaseDocument{
			SchemaVersion: config.FleetBaseSchemaVersion,
			Config:        config.ProjectOverlay{RequireNoMistakes: &[]bool{true}[0]},
		}); err != nil {
			t.Fatal(err)
		}

		result, err := Run(home, false, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertConfigContains(t, result.Configs, "REQUIRE_NO_MISTAKES: strict")
	})

	t.Run("legacy flat file alone does not gate", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "config"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config", "require-no-mistakes"), []byte("true\n"), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := Run(home, false, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range result.Configs {
			if c.Key == "REQUIRE_NO_MISTAKES" {
				t.Fatalf("legacy flat file produced REQUIRE_NO_MISTAKES diagnostic: %+v", c)
			}
		}
	})
}

func TestGCOrphanDataDirs_EmptyDirOlderThanGrace(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "orphan-id"), 0755); err != nil {
		t.Fatal(err)
	}
	// Make it older than the grace period
	setDirMtime(t, filepath.Join(dataDir, "orphan-id"), 48*time.Hour)

	cleaned := gcOrphanDataDirs(home, reclaimNone)
	if len(cleaned) != 1 || cleaned[0] != "orphan-id" {
		t.Errorf("expected [orphan-id], got %v", cleaned)
	}
	// Verify dir was actually removed
	if _, err := os.Stat(filepath.Join(dataDir, "orphan-id")); !os.IsNotExist(err) {
		t.Errorf("expected dir to be removed, stat err: %v", err)
	}
}

func TestGCOrphanDataDirs_WithReportKept(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "with-report"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create report.md — should protect from GC
	if err := os.WriteFile(filepath.Join(dataDir, "with-report", "report.md"), []byte("report"), 0644); err != nil {
		t.Fatal(err)
	}
	// Make it older than the grace period so only the report keeps it
	setDirMtime(t, filepath.Join(dataDir, "with-report"), 48*time.Hour)

	cleaned := gcOrphanDataDirs(home, reclaimNone)
	// Should not remove dir with report.md
	for _, id := range cleaned {
		if id == "with-report" {
			t.Errorf("expected dir with report.md to be kept, but it was removed")
		}
	}
	// Verify dir still exists
	if _, err := os.Stat(filepath.Join(dataDir, "with-report")); os.IsNotExist(err) {
		t.Errorf("expected dir with report.md to still exist")
	}
}

func TestGCOrphanDataDirs_BriefOfOwnedTaskKept(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "with-brief"), 0755); err != nil {
		t.Fatal(err)
	}
	// A brief written before the task was ever spawned: no meta, no status,
	// and nothing else on disk says the task is still coming.
	if err := os.WriteFile(filepath.Join(dataDir, "with-brief", "brief.md"), []byte("brief"), 0644); err != nil {
		t.Fatal(err)
	}
	setDirMtime(t, filepath.Join(dataDir, "with-brief"), 48*time.Hour)

	cleaned := gcOrphanDataDirs(home, reclaimEvery)
	for _, id := range cleaned {
		if id == "with-brief" {
			t.Errorf("expected brief of an owned task to be kept, but it was removed")
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "with-brief")); os.IsNotExist(err) {
		t.Errorf("expected brief of an owned task to still exist")
	}
}

func TestGCOrphanDataDirs_BriefOfReleasedTaskReclaimed(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "torn-down"), 0755); err != nil {
		t.Fatal(err)
	}
	// Byte-identical to the case above. Only ownership separates a brief
	// waiting for its soldier from one left by a teardown that wrote no
	// report, which is what the forced stuck-soldier relaunch leaves behind.
	if err := os.WriteFile(filepath.Join(dataDir, "torn-down", "brief.md"), []byte("brief"), 0644); err != nil {
		t.Fatal(err)
	}
	setDirMtime(t, filepath.Join(dataDir, "torn-down"), 48*time.Hour)

	cleaned := gcOrphanDataDirs(home, reclaimNone)
	if len(cleaned) != 1 || cleaned[0] != "torn-down" {
		t.Fatalf("expected [torn-down], got %v", cleaned)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "torn-down")); !os.IsNotExist(err) {
		t.Errorf("expected released brief dir to be removed, stat err: %v", err)
	}
}

func TestGCOrphanDataDirs_ArchivedReportKept(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "retired-scout"), 0755); err != nil {
		t.Fatal(err)
	}
	// What a teardown leaves for the operator to read: the retired
	// generation's report, under that generation's name.
	if err := os.WriteFile(filepath.Join(dataDir, "retired-scout", "report-g1.md"), []byte("findings"), 0644); err != nil {
		t.Fatal(err)
	}
	setDirMtime(t, filepath.Join(dataDir, "retired-scout"), 48*time.Hour)

	cleaned := gcOrphanDataDirs(home, reclaimNone)
	for _, id := range cleaned {
		if id == "retired-scout" {
			t.Errorf("expected archived report to be kept, but the dir was removed")
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "retired-scout", "report-g1.md")); err != nil {
		t.Errorf("expected archived report to survive the sweep: %v", err)
	}
}

func TestGCOrphanDataDirs_RecentDirKept(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "recent-id"), 0755); err != nil {
		t.Fatal(err)
	}
	// Leave it at its original mtime (current time) — should be too recent to GC

	cleaned := gcOrphanDataDirs(home, reclaimNone)
	for _, id := range cleaned {
		if id == "recent-id" {
			t.Errorf("expected recent dir to be kept, but it was removed")
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "recent-id")); os.IsNotExist(err) {
		t.Errorf("expected recent dir to still exist")
	}
}

func TestRunSkipsGCWithoutATaskOwnershipSource(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "orphan-id"), 0755); err != nil {
		t.Fatal(err)
	}
	setDirMtime(t, filepath.Join(dataDir, "orphan-id"), 48*time.Hour)

	result, err := Run(home, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.GC == nil || result.GC.SkippedReason != "no task ownership source" {
		t.Fatalf("GC = %+v, want the sweep skipped for want of an ownership source", result.GC)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "orphan-id")); err != nil {
		t.Fatalf("a sweep that cannot ask about ownership must remove nothing: %v", err)
	}
}
