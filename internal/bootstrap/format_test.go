package bootstrap

import (
	"errors"
	"testing"
)

func TestToolDiagnostic_String_Found(t *testing.T) {
	d := ToolDiagnostic{Tool: "git", Status: ToolFound, Path: "/usr/bin/git"}
	want := "FOUND: git at /usr/bin/git"
	if got := d.String(); got != want {
		t.Errorf("ToolDiagnostic.String() = %q, want %q", got, want)
	}
}

func TestToolDiagnostic_String_Missing(t *testing.T) {
	d := ToolDiagnostic{Tool: "tmux", Status: ToolMissing}
	want := "MISSING: tmux (install instructions vary)"
	if got := d.String(); got != want {
		t.Errorf("ToolDiagnostic.String() = %q, want %q", got, want)
	}
}

func TestToolDiagnostic_String_InstallFailed(t *testing.T) {
	d := ToolDiagnostic{Tool: "treehouse", Status: ToolInstallFailed, Err: errors.New("timeout")}
	want := "INSTALL_FAILED: treehouse — timeout"
	if got := d.String(); got != want {
		t.Errorf("ToolDiagnostic.String() = %q, want %q", got, want)
	}
}

func TestToolDiagnostic_String_Installed(t *testing.T) {
	d := ToolDiagnostic{Tool: "no-mistakes", Status: ToolInstalled}
	want := "INSTALLED: no-mistakes"
	if got := d.String(); got != want {
		t.Errorf("ToolDiagnostic.String() = %q, want %q", got, want)
	}
}

func TestAuthDiagnostic_String_Authenticated(t *testing.T) {
	d := AuthDiagnostic{Status: AuthAuthenticated}
	want := "GH_AUTH: authenticated"
	if got := d.String(); got != want {
		t.Errorf("AuthDiagnostic.String() = %q, want %q", got, want)
	}
}

func TestAuthDiagnostic_String_Failed(t *testing.T) {
	d := AuthDiagnostic{Status: AuthFailed}
	want := "NEEDS_GH_AUTH: gh auth status failed (run gh auth login)"
	if got := d.String(); got != want {
		t.Errorf("AuthDiagnostic.String() = %q, want %q", got, want)
	}
}

func TestConfigDiagnostic_String_WithSource(t *testing.T) {
	d := ConfigDiagnostic{Key: "BACKEND_RESOLVED", Value: "tmux", Source: "config pin"}
	want := "BACKEND_RESOLVED: tmux (source: config pin)"
	if got := d.String(); got != want {
		t.Errorf("ConfigDiagnostic.String() = %q, want %q", got, want)
	}
}

func TestConfigDiagnostic_String_WithoutSource(t *testing.T) {
	d := ConfigDiagnostic{Key: "BACKEND_CONFIG", Value: "auto"}
	want := "BACKEND_CONFIG: auto"
	if got := d.String(); got != want {
		t.Errorf("ConfigDiagnostic.String() = %q, want %q", got, want)
	}
}

func TestGCDiagnostic_String(t *testing.T) {
	d := GCDiagnostic{Removed: 2, Dirs: []string{"abc123", "def456"}}
	want := "GC: removed 2 orphan data dir(s): [abc123 def456]"
	if got := d.String(); got != want {
		t.Errorf("GCDiagnostic.String() = %q, want %q", got, want)
	}
}
