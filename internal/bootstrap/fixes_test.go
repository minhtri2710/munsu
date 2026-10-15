package bootstrap

import (
	"testing"
)

func TestFixCommand(t *testing.T) {
	tests := []struct {
		tool string
		want string // empty means no fix expected
	}{
		{"git", "Install git: https://git-scm.com/downloads"},
		{"gh-axi", "npm install -g gh-axi"},
		{"gh", "Install GitHub CLI: brew install gh  |  https://cli.github.com/"},
		{"tmux", "Install tmux: brew install tmux  |  apt install tmux  |  pacman -S tmux"},
		{"fuser", "Install psmisc: apt install psmisc  |  dnf install psmisc (macOS ships fuser)"},
		{"herdr", "Install herdr from the herdr repository"},
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

func TestToolDiagnostic_Fix_Found_NoFix(t *testing.T) {
	d := ToolDiagnostic{Tool: "git", Status: ToolFound, Path: "/usr/bin/git"}
	got := d.Fix()
	if got != "" {
		t.Errorf("ToolDiagnostic{Fix} for found tool = %q, want empty", got)
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
