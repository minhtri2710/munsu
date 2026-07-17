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

// DoctorFix returns a human-readable fix string for a bootstrap diagnostic line.
func DoctorFix(diagnostic string) string {
	// Handle the known diagnostic prefixes
	switch {
	case len(diagnostic) < 8:
		return ""
	case diagnostic[:7] == "MISSING":
		tool := diagnostic[8:]
		if idx := indexOfAny(tool, " (\t"); idx >= 0 {
			tool = tool[:idx]
		}
		if cmd := FixCommand(tool); cmd != "" {
			return fmt.Sprintf("    Fix: %s", cmd)
		}
		return "    Fix: install " + tool + " (see its documentation)"
	case diagnostic == "NEEDS_GH_AUTH: gh auth status failed (run gh auth login)":
		return "    Fix: gh auth login"
	case len(diagnostic) >= 13 && diagnostic[:13] == "NEEDS_GH_AUTH":
		return "    Fix: gh auth login"
	}
	return ""
}

// indexOfAny returns the index of the first occurrence of any of the runes in chars.
func indexOfAny(s string, chars string) int {
	for i, r := range s {
		for _, c := range chars {
			if r == c {
				return i
			}
		}
	}
	return -1
}
