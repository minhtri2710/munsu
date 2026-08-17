package bootstrap

import (
	"encoding/json"
	"strings"
)

// Native file-write tool names, per harness.
//
// The lists are separate on purpose: harnesses do not share a tool vocabulary.
// Claude and its hook-compatible cousins use PascalCase (`Bash`, `Write`), Pi
// and OpenCode use lowercase (`bash`, `write`), agy uses snake_case
// (`run_command`). A single shared list would silently be wrong for at least
// three of the six.
//
// The lists are deliberately generous, because the two failure directions are
// not symmetric:
//
//   - A name listed that the harness never emits costs nothing — the hook
//     simply never fires for it.
//   - A name listed that the harness emits for a tool with no file path in its
//     payload also costs nothing: safety-check finds no path and falls through
//     to the pre-existing behaviour. It cannot manufacture a refusal.
//   - A name missing from the list keeps the C2 gap open for that tool.
//
// So over-listing is safe and under-listing is not.
//
// The apply-patch family is deliberately absent from these lists, and leaving a
// name out is NOT what decides whether the guard covers it. Codex declares
// `Write` and `Edit` as matcher aliases of `apply_patch` and matches on regex
// alternation, so `Write|Edit|MultiEdit|NotebookEdit` already selects
// `apply_patch` today; listing the name as well would change nothing. Coverage
// is decided one layer down, where safety-check classifies the payload by
// `tool_name` and routes a patch document to its own channel instead of the
// shell-command one (`internal/cli/integrate_cmd.go`). Measured on pi 0.79.9 and
// agy 1.1.13, neither harness has an `apply_patch` or `patch` tool at all.
var (
	claudeWriteToolNames   = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}
	codexWriteToolNames    = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}
	grokWriteToolNames     = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}
	agyWriteToolNames      = []string{"write_file", "edit_file", "create_file", "replace_in_file", "notebook_edit"}
	piWriteToolNames       = []string{"write", "edit", "multi_edit", "multiedit", "notebook_edit"}
	opencodeWriteToolNames = []string{"write", "edit", "multiedit", "notebook_edit"}
)

// writeToolMatcher renders a hook matcher for harnesses that take an
// alternation pattern (Claude, Codex, Grok, agy).
func writeToolMatcher(names []string) string {
	return strings.Join(names, "|")
}

// writeToolJSArray renders the names as a JSON array literal for embedding in
// the Pi extension and the OpenCode plugin.
func writeToolJSArray(names []string) string {
	data, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}
	return string(data)
}
