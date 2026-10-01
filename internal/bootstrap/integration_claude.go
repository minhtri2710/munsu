// Package integrate manages opt-in harness integration.
//
// JSON-hook adapter: generates one hooks JSON file (Claude's
// .claude/settings.json, Codex's .codex/hooks.json) with hooks anchored to
// the munsu binary path, merged into any user-owned hooks already there.

package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
)

// jsonHookHarness is a harness whose munsu hooks live in one JSON file under
// a dot directory, shared with user-owned hooks.
type jsonHookHarness struct {
	name       string // harness name, also the --harness hook argument
	dir        string // dot directory under the home or project root
	file       string // hooks file name inside dir
	writeTools []string
}

var (
	claudeHooks = jsonHookHarness{name: harness.Claude, dir: ".claude", file: "settings.json", writeTools: claudeWriteToolNames}
	codexHooks  = jsonHookHarness{name: harness.Codex, dir: ".codex", file: "hooks.json", writeTools: codexWriteToolNames}
)

// path returns the hooks file path for the given scope.
func (h jsonHookHarness) path(scope Scope, cwd string) (string, error) {
	switch scope {
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine user home: %w", err)
		}
		return filepath.Join(home, h.dir, h.file), nil
	case ScopeProject:
		if cwd == "" {
			return "", fmt.Errorf("cwd is required for project scope")
		}
		canonical, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return "", fmt.Errorf("cannot resolve cwd %s: %w", cwd, err)
		}
		return filepath.Join(canonical, h.dir, h.file), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
}

// jsonHookCommand builds a hook command string: the JSON-marshaled munsu
// path (spaces and special characters survive) followed by the arguments.
func jsonHookCommand(munsuBin string, args ...string) string {
	binJSON, err := json.Marshal(munsuBin)
	if err != nil {
		binJSON = []byte(fmt.Sprintf("%q", munsuBin))
	}
	full := string(binJSON)
	for _, arg := range args {
		full += " " + arg
	}
	return full
}

func (h jsonHookHarness) sessionStartCommand(munsuBin string) string {
	return jsonHookCommand(munsuBin, "integrate", "sessionstart-nudge")
}

func (h jsonHookHarness) safetyCheckCommand(munsuBin string) string {
	return jsonHookCommand(munsuBin, "integrate", "safety-check", "--harness", h.name)
}

func (h jsonHookHarness) guardCommand(munsuBin string) string {
	return jsonHookCommand(munsuBin, "guard", "--harness", h.name)
}

// content returns the generated hooks JSON with the munsu binary path baked in.
func (h jsonHookHarness) content(munsuBin string) string {
	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookMatcher struct {
		Matcher string      `json:"matcher,omitempty"`
		Hooks   []hookEntry `json:"hooks"`
	}

	safetyCheck := h.safetyCheckCommand(munsuBin)
	hooks := struct {
		Hooks map[string][]hookMatcher `json:"hooks"`
	}{
		Hooks: map[string][]hookMatcher{
			"SessionStart": {
				{
					Matcher: "startup|resume|clear",
					Hooks: []hookEntry{
						{Type: "command", Command: h.sessionStartCommand(munsuBin)},
					},
				},
			},
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks: []hookEntry{
						{Type: "command", Command: safetyCheck},
					},
				},
				{
					// Native file-write tools bypass the shell entirely; without
					// this matcher they reach the filesystem with no guard at all.
					Matcher: writeToolMatcher(h.writeTools),
					Hooks: []hookEntry{
						{Type: "command", Command: safetyCheck},
					},
				},
			},
			"Stop": {
				{
					Hooks: []hookEntry{
						{Type: "command", Command: h.guardCommand(munsuBin)},
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		return `{"hooks":{}}`
	}
	return string(data)
}

// hasOwnedHooks checks whether the hooks file at path contains all expected
// munsu-owned hook commands anchored to the given munsu binary path. The
// message lists which hooks are missing when the check fails. Ownership is
// structural because JSON cannot carry a first-line comment marker.
func (h jsonHookHarness) hasOwnedHooks(path, munsuBin string) (bool, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", fmt.Errorf("reading %s %s: %w", h.name, h.file, err)
	}

	var parsed struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher,omitempty"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false, "", fmt.Errorf("parsing %s %s: %w", h.name, h.file, err)
	}

	hooksByEvent := parsed.Hooks
	if hooksByEvent == nil {
		return false, "no hooks section in " + h.file, nil
	}

	hasCommand := func(event, command string) bool {
		for _, m := range hooksByEvent[event] {
			for _, hook := range m.Hooks {
				if hook.Type == "command" && hook.Command == command {
					return true
				}
			}
		}
		return false
	}

	var missing []string
	if !hasCommand("SessionStart", h.sessionStartCommand(munsuBin)) {
		missing = append(missing, "SessionStart")
	}

	// Both PreToolUse matchers must be present: an install that predates the
	// native-write matcher still has the Bash one, and reporting it as healthy
	// would leave the file-write path unguarded forever.
	safetyCheck := h.safetyCheckCommand(munsuBin)
	writeMatcher := writeToolMatcher(h.writeTools)
	foundBash, foundWrite := false, false
	for _, m := range hooksByEvent["PreToolUse"] {
		for _, hook := range m.Hooks {
			if hook.Type != "command" || hook.Command != safetyCheck {
				continue
			}
			switch m.Matcher {
			case "Bash":
				foundBash = true
			case writeMatcher:
				foundWrite = true
			}
		}
	}
	if !foundBash {
		missing = append(missing, "PreToolUse(Bash)")
	}
	if !foundWrite {
		missing = append(missing, "PreToolUse("+writeMatcher+")")
	}

	if !hasCommand("Stop", h.guardCommand(munsuBin)) {
		missing = append(missing, "Stop")
	}

	if len(missing) > 0 {
		return false, fmt.Sprintf("missing munsu-owned hooks: %s", strings.Join(missing, ", ")), nil
	}
	return true, "all munsu-owned hooks present", nil
}

// install generates the hooks file and merges it into any existing one,
// preserving user-owned hooks. It returns the target path, whether it was
// written, and the digest of the content written.
func (h jsonHookHarness) install(scope Scope, cwd string, dryRun bool) (targetPath string, written bool, digest string, err error) {
	munsuBin, err := ResolveMunsuPathString()
	if err != nil {
		return "", false, "", fmt.Errorf("cannot resolve munsu binary path: %w", err)
	}

	targetPath, err = h.path(scope, cwd)
	if err != nil {
		return "", false, "", fmt.Errorf("cannot determine %s %s path: %w", h.name, h.file, err)
	}

	content := h.content(munsuBin)
	existing, err := readExistingHookFile(targetPath)
	if err != nil {
		return "", false, "", err
	}
	if existing != "" {
		content, err = mergeHookEventArrays(targetPath, existing, content)
		if err != nil {
			return "", false, "", err
		}
	}
	sum := sha256.Sum256([]byte(content))
	digest = hex.EncodeToString(sum[:])

	if dryRun {
		return targetPath, false, digest, nil
	}

	if err := writeAtomic(targetPath, content, 0644); err != nil {
		return "", false, "", fmt.Errorf("writing %s %s: %w", h.name, h.file, err)
	}

	return targetPath, true, digest, nil
}
