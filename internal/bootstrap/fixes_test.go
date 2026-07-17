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
		{"treehouse", "go install github.com/kunchenguid/treehouse@latest"},
		{"no-mistakes", "go install github.com/kunchenguid/no-mistakes@latest"},
		{"tasks-axi", "npm install -g tasks-axi"},
		{"gh-axi", "npm install -g gh-axi"},
		{"gh", "Install GitHub CLI: brew install gh  |  https://cli.github.com/"},
		{"tmux", "Install tmux: brew install tmux  |  apt install tmux  |  pacman -S tmux"},
		{"herdr", "Install herdr from the herdr repository, or set HERDR_ENV=1 if already installed"},
		{"unknown-tool", ""},
	}

	for _, tt := range tests {
		got := FixCommand(tt.tool)
		if got != tt.want {
			t.Errorf("FixCommand(%q) = %q, want %q", tt.tool, got, tt.want)
		}
	}
}

func TestDoctorFix(t *testing.T) {
	tests := []struct {
		diag string
		want string
	}{
		{"MISSING git (install instructions vary)", "    Fix: Install git: https://git-scm.com/downloads"},
		{"MISSING tmux (install instructions vary)", "    Fix: Install tmux: brew install tmux  |  apt install tmux  |  pacman -S tmux"},
		{"MISSING no-mistakes (install instructions vary)", "    Fix: go install github.com/kunchenguid/no-mistakes@latest"},
		{"NEEDS_GH_AUTH: gh auth status failed (run gh auth login)", "    Fix: gh auth login"},
		{"FOUND: git at /usr/bin/git", ""},
		{"short", ""},
		{"MISSING unknownthing", "    Fix: install unknownthing (see its documentation)"},
	}

	for _, tt := range tests {
		got := DoctorFix(tt.diag)
		if got != tt.want {
			t.Errorf("DoctorFix(%q) = %q, want %q", tt.diag, got, tt.want)
		}
	}
}

func TestDoctorFixNeedsGHAuthVariant(t *testing.T) {
	// Test the length-based NEEDS_GH_AUTH branch
	got := DoctorFix("NEEDS_GH_AUTH: some other message")
	if got != "    Fix: gh auth login" {
		t.Errorf("DoctorFix('NEEDS_GH_AUTH: ...') = %q, want '    Fix: gh auth login'", got)
	}
}
