package bootstrap

import (
	"errors"
	"fmt"
	"os"

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

// IsHardRequiredByConfig reports whether the tool is hard-required by the
// typed fleet base config, and an error only when the operational config read
// fails. Currently only handles no-mistakes via the base requireNoMistakes
// field. Presence semantics are preserved: a base document that sets
// requireNoMistakes: true treats no-mistakes as hard-required.
//
// Unsupported tools are never hard-required by config and return false, nil
// without reading any config. An absent base document (fresh home) is treated
// as not required (false, nil). Any other failure to read the base config is
// returned as an error so callers fail closed rather than silently treating
// the tool as optional.
func IsHardRequiredByConfig(homeDir, tool string) (bool, error) {
	if tool != "no-mistakes" {
		return false, nil
	}
	base, err := config.LoadFleetBase(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("reading fleet base config: %w", err)
	}
	return base.Config.RequireNoMistakes != nil && *base.Config.RequireNoMistakes, nil
}
