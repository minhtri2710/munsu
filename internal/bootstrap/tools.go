package bootstrap

import (
	"github.com/minhtri2710/munsu/internal/config"
)

// ToolSpec defines a tool that bootstrap checks for presence.
type ToolSpec struct {
	Name     string
	Required bool
}

// checkedTools is the single source of truth for bootstrap tool classification.
var checkedTools = []ToolSpec{
	{"git", true},
	{"tmux", true},
	{"zellij", false},
	{"treehouse", false},
	{"no-mistakes", false},
	{"tasks-axi", false},
	{"gh-axi", false},
	{"gh", false},
}

// IsHardRequired returns true if tool is classified as a hard-required dependency.
// Used by munsu doctor to decide exit code. Unknown tools return false.
func IsHardRequired(tool string) bool {
	for _, t := range checkedTools {
		if t.Name == tool {
			return t.Required
		}
	}
	return false
}

// IsHardRequiredByConfig returns true when the tool is hard-required by the
// typed fleet base config. Currently only handles no-mistakes via the base
// requireNoMistakes field. Presence semantics are preserved: a base document
// that sets requireNoMistakes: true treats no-mistakes as hard-required.
func IsHardRequiredByConfig(homeDir, tool string) bool {
	if tool != "no-mistakes" {
		return false
	}
	base, err := config.LoadFleetBase(homeDir)
	if err != nil {
		return false
	}
	return base.Config.RequireNoMistakes != nil && *base.Config.RequireNoMistakes
}
