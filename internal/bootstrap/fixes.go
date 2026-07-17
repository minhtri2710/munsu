package bootstrap

import "fmt"

// FixCommand returns the recommended fix command for a missing tool.
// Returns empty string if no standard fix exists.
func FixCommand(tool string) string {
	switch tool {
	case "git":
		return "Install git: https://git-scm.com/downloads"
	case "treehouse":
		return "go install github.com/kunchenguid/treehouse@latest"
	case "no-mistakes":
		return "go install github.com/kunchenguid/no-mistakes@latest"
	case "tasks-axi":
		return "npm install -g tasks-axi"
	case "gh-axi":
		return "npm install -g gh-axi"
	case "gh":
		return "Install GitHub CLI: brew install gh  |  https://cli.github.com/"
	case "tmux":
		return "Install tmux: brew install tmux  |  apt install tmux  |  pacman -S tmux"
	case "herdr":
		return "Install herdr from the herdr repository, or set HERDR_ENV=1 if already installed"
	default:
		return ""
	}
}

// Fix returns a human-readable fix string for a tool that is missing or failed to install.
func (d ToolDiagnostic) Fix() string {
	switch d.Status {
	case ToolMissing:
		return FixCommand(d.Tool)
	case ToolInstallFailed:
		if cmd := FixCommand(d.Tool); cmd != "" {
			return cmd
		}
		return fmt.Sprintf("install %s (see its documentation)", d.Tool)
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

