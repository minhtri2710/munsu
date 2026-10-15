package bootstrap

// FixCommand returns the recommended fix command for a missing tool.
// Returns empty string if no standard fix exists.
func FixCommand(tool string) string {
	switch tool {
	case "git":
		return "Install git: https://git-scm.com/downloads"
	case "gh-axi":
		return "npm install -g gh-axi"
	case "gh":
		return "Install GitHub CLI: brew install gh  |  https://cli.github.com/"
	case "tmux":
		return "Install tmux: brew install tmux  |  apt install tmux  |  pacman -S tmux"
	case "fuser":
		return "Install psmisc: apt install psmisc  |  dnf install psmisc (macOS ships fuser)"
	case "herdr":
		return "Install herdr from the herdr repository, or set HERDR_ENV=1 if already installed"
	default:
		return ""
	}
}

// Fix returns a human-readable fix string for a missing tool.
func (d ToolDiagnostic) Fix() string {
	if d.Status == ToolMissing {
		return FixCommand(d.Tool)
	}
	return ""
}

// Fix returns a human-readable fix string when GitHub auth has failed.
func (d AuthDiagnostic) Fix() string {
	if d.Status == AuthFailed {
		return "gh auth login"
	}
	return ""
}
