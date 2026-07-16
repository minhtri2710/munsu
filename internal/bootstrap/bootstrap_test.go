package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertContains fails t if slice does not contain a string matching want.
func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("expected %q in slice, got %v", want, slice)
}

func TestRun_BackendDiagnostics_AutoWithActiveTMUX(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMUX", "/tmp/tmux-xxx/default")
	t.Setenv("HERDR_ENV", "")

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, result.ConfigDetails, "BACKEND_CONFIG: auto")
	assertContains(t, result.ConfigDetails, "BACKEND_RESOLVED: tmux (source: active TMUX)")
}

func TestRun_BackendDiagnostics_AutoWithActiveHERDRENV(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "1")

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	assertContains(t, result.ConfigDetails, "BACKEND_CONFIG: auto")
	assertContains(t, result.ConfigDetails, "BACKEND_RESOLVED: herdr (source: active HERDR_ENV)")
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

	assertContains(t, result.ConfigDetails, "BACKEND_CONFIG: auto")
	assertContains(t, result.ConfigDetails, "BACKEND_RESOLVED: tmux (source: cold-start)")
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

	assertContains(t, result.ConfigDetails, "BACKEND_CONFIG: herdr")
	assertContains(t, result.ConfigDetails, "BACKEND_RESOLVED: herdr (source: config pin)")
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

	assertContains(t, result.ConfigDetails, "BACKEND_CONFIG: tmux")
	assertContains(t, result.ConfigDetails, "BACKEND_RESOLVED: tmux (source: config pin)")
}

func TestRun_BackendDiagnostics_UnrelatedOutputStable(t *testing.T) {
	// Verify that other ConfigDetails entries (like CREW_HARNESS, CREW_DISPATCH)
	// are not broken by the backend changes.
	home := t.TempDir()
	t.Setenv("TMUX", "/tmp/tmux-xxx")
	t.Setenv("HERDR_ENV", "")

	result, err := Run(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should not have any backend-unrelated ConfigDetails in a bare home
	// (CREW_HARNESS and CREW_DISPATCH require specific files).
	for _, c := range result.ConfigDetails {
		if strings.HasPrefix(c, "BACKEND_") {
			continue
		}
		t.Errorf("unexpected ConfigDetails entry from bare home: %q", c)
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

	assertContains(t, result.ConfigDetails, "BACKEND_CONFIG: auto")
	assertContains(t, result.ConfigDetails, "BACKEND_RESOLVED: none (no backend available)")

}
