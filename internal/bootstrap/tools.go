package bootstrap

// ToolSpec defines a tool that bootstrap checks for presence.
type ToolSpec struct {
	Name     string
	Required bool
}

// checkedTools is the single source of truth for bootstrap tool classification.
var checkedTools = []ToolSpec{
	{"git", true},
	{"tmux", true},
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
