package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestRun_BackendDiagnostics_AutoWithActiveTMUX(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMUX", "/tmp/tmux-xxx/default")
	t.Setenv("HERDR_ENV", "")

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: tmux (source: active TMUX)")
}

func TestRun_BackendDiagnostics_AutoWithActiveHERDRENV(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "1")

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: herdr (source: active HERDR_ENV)")
}

func TestRun_BackendDiagnostics_ColdStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")

	// Create a fake tmux on a temporary PATH to guarantee cold-start detection
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte("#!/bin/sh\nexit 0"), 0755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: tmux (source: cold-start)")
}

func TestRun_BackendDiagnostics_ExplicitConfigHerdr(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: herdr")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: herdr (source: config pin)")
}

func TestRun_BackendDiagnostics_ExplicitConfigTmux(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("tmux\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: tmux")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: tmux (source: config pin)")
}

func TestRun_BackendDiagnostics_UnrelatedOutputStable(t *testing.T) {
	// Verify that other Configs entries (like CREW_HARNESS, CREW_DISPATCH)
	// are not broken by the backend changes.
	home := t.TempDir()
	t.Setenv("TMUX", "/tmp/tmux-xxx")
	t.Setenv("HERDR_ENV", "")

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should not have any backend-unrelated Configs in a bare home
	// (CREW_HARNESS and CREW_DISPATCH require specific files).
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

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertConfigContains(t, result.Configs, "BACKEND_CONFIG: auto")
	assertConfigContains(t, result.Configs, "BACKEND_RESOLVED: none (source: no backend available)")
}

func setDirMtime(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(dir, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestGCOrphanDataDirs_EmptyDirOlderThanGrace(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "orphan-id"), 0755); err != nil {
		t.Fatal(err)
	}
	// Make it older than the grace period
	setDirMtime(t, filepath.Join(dataDir, "orphan-id"), 48*time.Hour)

	cleaned := gcOrphanDataDirs(home)
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

	cleaned := gcOrphanDataDirs(home)
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

func TestGCOrphanDataDirs_WithBriefKept(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "with-brief"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create brief.md — should protect from GC
	if err := os.WriteFile(filepath.Join(dataDir, "with-brief", "brief.md"), []byte("brief"), 0644); err != nil {
		t.Fatal(err)
	}
	// Make it older than the grace period so only the brief keeps it
	setDirMtime(t, filepath.Join(dataDir, "with-brief"), 48*time.Hour)

	cleaned := gcOrphanDataDirs(home)
	for _, id := range cleaned {
		if id == "with-brief" {
			t.Errorf("expected dir with brief.md to be kept, but it was removed")
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "with-brief")); os.IsNotExist(err) {
		t.Errorf("expected dir with brief.md to still exist")
	}
}

func TestGCOrphanDataDirs_RecentDirKept(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "recent-id"), 0755); err != nil {
		t.Fatal(err)
	}
	// Leave it at its original mtime (current time) — should be too recent to GC

	cleaned := gcOrphanDataDirs(home)
	for _, id := range cleaned {
		if id == "recent-id" {
			t.Errorf("expected recent dir to be kept, but it was removed")
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "recent-id")); os.IsNotExist(err) {
		t.Errorf("expected recent dir to still exist")
	}
}
