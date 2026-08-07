package bootstrap

import (
	"errors"
	"testing"
)

func TestFixCommand(t *testing.T) {
	tests := []struct {
		tool string
		want string // empty means no fix expected
	}{
		{"git", "Install git: https://git-scm.com/downloads"},
		{"treehouse", "go install github.com/kunchenguid/treehouse@latest"},
		{"no-mistakes", "go install github.com/kunchenguid/no-mistakes@latest"},
		{"gh-axi", "npm install -g gh-axi"},
		{"gh", "Install GitHub CLI: brew install gh  |  https://cli.github.com/"},
		{"tmux", "Install tmux: brew install tmux  |  apt install tmux  |  pacman -S tmux"},
		{"herdr", "Install herdr from the herdr repository, or set HERDR_ENV=1 if already installed"},
		{"zellij", "Install zellij: brew install zellij  |  see https://zellij.dev/documentation/installation" + "\n\tExperimental: set config/backend=zellij or --backend zellij. Not auto-detected."},
	}

	for _, tt := range tests {
		got := FixCommand(tt.tool)
		if got != tt.want {
			t.Errorf("FixCommand(%q) = %q, want %q", tt.tool, got, tt.want)
		}
	}
}

func TestToolDiagnostic_Fix_Missing(t *testing.T) {
	d := ToolDiagnostic{Tool: "git", Status: ToolMissing}
	got := d.Fix()
	want := "Install git: https://git-scm.com/downloads"
	if got != want {
		t.Errorf("ToolDiagnostic{Fix} for missing git = %q, want %q", got, want)
	}
}

func TestToolDiagnostic_Fix_InstallFailed(t *testing.T) {
	d := ToolDiagnostic{Tool: "no-mistakes", Status: ToolInstallFailed, Err: errors.New("timeout")}
	got := d.Fix()
	want := "go install github.com/kunchenguid/no-mistakes@latest"
	if got != want {
		t.Errorf("ToolDiagnostic{Fix} for install-failed no-mistakes = %q, want %q", got, want)
	}
}

func TestToolDiagnostic_Fix_Found_NoFix(t *testing.T) {
	d := ToolDiagnostic{Tool: "git", Status: ToolFound, Path: "/usr/bin/git"}
	got := d.Fix()
	if got != "" {
		t.Errorf("ToolDiagnostic{Fix} for found tool = %q, want empty", got)
	}
}

func TestToolDiagnostic_Fix_Installed_NoFix(t *testing.T) {
	d := ToolDiagnostic{Tool: "treehouse", Status: ToolInstalled}
	got := d.Fix()
	if got != "" {
		t.Errorf("ToolDiagnostic{Fix} for installed tool = %q, want empty", got)
	}
}

func TestToolDiagnostic_Fix_UnknownToolMissing(t *testing.T) {
	d := ToolDiagnostic{Tool: "unknownthing", Status: ToolMissing}
	got := d.Fix()
	// FixCommand returns "" for unknown tools; missing returns FixCommand directly
	if got != "" {
		t.Errorf("ToolDiagnostic{Fix} for missing unknown tool = %q, want empty", got)
	}
}

func TestToolDiagnostic_Fix_UnknownToolInstallFailed_Fallback(t *testing.T) {
	d := ToolDiagnostic{Tool: "unknownthing", Status: ToolInstallFailed, Err: errors.New("fail")}
	got := d.Fix()
	want := "install unknownthing (see its documentation)"
	if got != want {
		t.Errorf("ToolDiagnostic{Fix} for install-failed unknown tool = %q, want %q", got, want)
	}
}

func TestAuthDiagnostic_Fix_Authenticated(t *testing.T) {
	d := AuthDiagnostic{Status: AuthAuthenticated}
	got := d.Fix()
	if got != "" {
		t.Errorf("AuthDiagnostic{Fix} for authenticated = %q, want empty", got)
	}
}

func TestAuthDiagnostic_Fix_Failed(t *testing.T) {
	d := AuthDiagnostic{Status: AuthFailed}
	got := d.Fix()
	want := "gh auth login"
	if got != want {
		t.Errorf("AuthDiagnostic{Fix} for failed = %q, want %q", got, want)
	}
}
