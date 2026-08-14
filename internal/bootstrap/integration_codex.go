// Package integrate manages opt-in harness integration.
//
// Codex adapter: generates .codex/hooks.json with hooks anchored to the munsu
// binary path, mirroring the munsu Codex hook contract.

package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// codexHooksPath returns the path to .codex/hooks.json for the given scope.
func codexHooksPath(scope Scope, cwd string) (string, error) {
	switch scope {
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine user home: %w", err)
		}
		return filepath.Join(home, ".codex", "hooks.json"), nil
	case ScopeProject:
		if cwd == "" {
			return "", fmt.Errorf("cwd is required for project scope")
		}
		canonical, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return "", fmt.Errorf("cannot resolve cwd %s: %w", cwd, err)
		}
		return filepath.Join(canonical, ".codex", "hooks.json"), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
}

// codexHookCommand builds a JSON-marshaled command string for Codex hooks.
func codexHookCommand(munsuBin string, args ...string) string {
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

// codexHooksJSON builds the complete hooks JSON as a byte slice,
// embedding the munsu binary path into each command.
func codexHooksJSON(munsuBin string) ([]byte, error) {
	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookMatcher struct {
		Matcher string      `json:"matcher,omitempty"`
		Hooks   []hookEntry `json:"hooks"`
	}

	sessionStartNudge := codexHookCommand(munsuBin, "integrate", "sessionstart-nudge")
	safetyCheck := codexHookCommand(munsuBin, "integrate", "safety-check", "--harness", "codex")
	guardCmd := codexHookCommand(munsuBin, "guard", "--harness", "codex")

	hooks := struct {
		Hooks map[string][]hookMatcher `json:"hooks"`
	}{
		Hooks: map[string][]hookMatcher{
			"SessionStart": {
				{
					Matcher: "startup|resume|clear",
					Hooks: []hookEntry{
						{Type: "command", Command: sessionStartNudge},
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
					Matcher: writeToolMatcher(codexWriteToolNames),
					Hooks: []hookEntry{
						{Type: "command", Command: safetyCheck},
					},
				},
			},
			"Stop": {
				{
					Hooks: []hookEntry{
						{Type: "command", Command: guardCmd},
					},
				},
			},
		},
	}

	return json.MarshalIndent(hooks, "", "  ")
}

// CodexHooksContent returns the hooks JSON with the munsu binary path baked in.
func CodexHooksContent(munsuBinPath string) string {
	data, err := codexHooksJSON(munsuBinPath)
	if err != nil {
		return `{"hooks":{}}`
	}
	return string(data)
}

// CodexHooksDigest returns the SHA-256 hex digest of the generated hooks content.
func CodexHooksDigest(munsuBinPath string) string {
	content := CodexHooksContent(munsuBinPath)
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// CodexHooksHasOwnedHooks checks whether hooks.json at hooksPath contains
// all expected munsu-owned hook commands anchored to the given munsu binary path.
// Returns true when all expected hooks are present, along with a descriptive message.
// The message lists which hooks are missing when the check fails.
// Uses structural ownership (JSON content check) since .codex/hooks.json cannot
// carry a first-line comment marker.
func CodexHooksHasOwnedHooks(hooksPath, munsuBin string) (bool, string, error) {
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		return false, "", fmt.Errorf("reading codex hooks: %w", err)
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
		return false, "", fmt.Errorf("parsing codex hooks JSON: %w", err)
	}

	sessionStartCmd := codexHookCommand(munsuBin, "integrate", "sessionstart-nudge")
	safetyCheckCmd := codexHookCommand(munsuBin, "integrate", "safety-check", "--harness", "codex")
	guardCmd := codexHookCommand(munsuBin, "guard", "--harness", "codex")

	hooksByEvent := parsed.Hooks
	if hooksByEvent == nil {
		return false, "no hooks section in hooks.json", nil
	}

	var missing []string

	// Check SessionStart hook
	foundSessionStart := false
	if matchers, ok := hooksByEvent["SessionStart"]; ok {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if h.Type == "command" && h.Command == sessionStartCmd {
					foundSessionStart = true
					break
				}
			}
			if foundSessionStart {
				break
			}
		}
	}
	if !foundSessionStart {
		missing = append(missing, "SessionStart")
	}

	// Check PreToolUse hooks. Both matchers must be present: an install that
	// predates the native-write matcher still has the Bash one, and reporting
	// it as healthy would leave the file-write path unguarded forever.
	foundBashPreToolUse := false
	foundWritePreToolUse := false
	writeMatcher := writeToolMatcher(codexWriteToolNames)
	if matchers, ok := hooksByEvent["PreToolUse"]; ok {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if h.Type != "command" || h.Command != safetyCheckCmd {
					continue
				}
				switch m.Matcher {
				case "Bash":
					foundBashPreToolUse = true
				case writeMatcher:
					foundWritePreToolUse = true
				}
			}
		}
	}
	if !foundBashPreToolUse {
		missing = append(missing, "PreToolUse(Bash)")
	}
	if !foundWritePreToolUse {
		missing = append(missing, "PreToolUse("+writeMatcher+")")
	}

	// Check Stop hook
	foundStop := false
	if matchers, ok := hooksByEvent["Stop"]; ok {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if h.Type == "command" && h.Command == guardCmd {
					foundStop = true
					break
				}
			}
			if foundStop {
				break
			}
		}
	}
	if !foundStop {
		missing = append(missing, "Stop")
	}

	if len(missing) > 0 {
		return false, fmt.Sprintf("missing munsu-owned hooks: %s", strings.Join(missing, ", ")), nil
	}
	return true, "all munsu-owned hooks present", nil
}

// CodexAdapter implements hook generation and installation for the Codex harness.
type CodexAdapter struct {
	HomeDir string
	Cwd     string
	Scope   string // "user" or "project"
	DryRun  bool
}

// InstallCodexHooks generates and installs the .codex/hooks.json file.
// Returns the target path, whether it was actually written, and the content digest.
func (a *CodexAdapter) InstallCodexHooks() (targetPath string, written bool, digest string, err error) {
	munsuBin, err := ResolveMunsuPathString()
	if err != nil {
		return "", false, "", fmt.Errorf("cannot resolve munsu binary path: %w", err)
	}

	targetPath, err = codexHooksPath(Scope(a.Scope), a.Cwd)
	if err != nil {
		return "", false, "", fmt.Errorf("cannot determine codex hooks path: %w", err)
	}

	// Check existing file for read-merge
	var existingContent string
	if existing, statErr := os.Stat(targetPath); statErr == nil && existing.Size() > 0 {
		data, readErr := os.ReadFile(targetPath)
		if readErr == nil {
			existingContent = string(data)
		}
	}

	content := CodexHooksContent(munsuBin)
	digest = CodexHooksDigest(munsuBin)

	if existingContent != "" {
		merged, mergeErr := mergeCodexHooks(existingContent, content)
		if mergeErr != nil {
			return "", false, "", fmt.Errorf("merge codex hooks: %w", mergeErr)
		}
		content = merged
		sum := sha256.Sum256([]byte(content))
		digest = hex.EncodeToString(sum[:])
	}

	if a.DryRun {
		return targetPath, false, digest, nil
	}

	if err := writeAtomic(targetPath, content, 0644); err != nil {
		return "", false, "", fmt.Errorf("writing codex hooks: %w", err)
	}

	return targetPath, true, digest, nil
}

// mergeCodexHooks merges munsu's generated hook entries into existing hooks.json,
// preserving any user-owned hooks. It takes the full existing file content and the
// full munsu-generated content, then merges the hooks arrays.
func mergeCodexHooks(existing, generated string) (string, error) {
	var existingJSON map[string]interface{}
	if err := json.Unmarshal([]byte(existing), &existingJSON); err != nil {
		return generated, nil
	}

	var generatedJSON map[string]interface{}
	if err := json.Unmarshal([]byte(generated), &generatedJSON); err != nil {
		return "", fmt.Errorf("generated hooks is invalid JSON: %w", err)
	}

	genHooks, _ := generatedJSON["hooks"].(map[string]interface{})

	existingHooks, hasExistingHooks := existingJSON["hooks"].(map[string]interface{})
	if !hasExistingHooks || existingHooks == nil {
		existingJSON["hooks"] = genHooks
		return marshalJSON(existingJSON)
	}

	for eventKey, genHookList := range genHooks {
		genList, ok := genHookList.([]interface{})
		if !ok {
			continue
		}

		existingList, hasExisting := existingHooks[eventKey].([]interface{})
		if !hasExisting || existingList == nil {
			existingHooks[eventKey] = genList
			continue
		}

		merged := append(genList, existingList...)
		existingHooks[eventKey] = merged
	}

	return marshalJSON(existingJSON)
}

// generateCodexManifest creates the integration manifest for the Codex adapter.
func generateCodexManifest(harnessName string, scope string, caps []Capability, contentDigest string, targetPath string) Manifest {
	capStrs := make([]string, len(caps))
	for i, c := range caps {
		capStrs[i] = string(c)
	}
	return Manifest{
		SchemaVersion: "munsu.integrate/v1",
		Harness:       harnessName,
		Version:       "1.0.0",
		Scope:         scope,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		TargetPaths:   []string{targetPath},
		Capabilities:  capStrs,
		ContentDigest: contentDigest,
	}
}

// CodexHooksTargetPath returns the expected target path for a given scope+cwd.
func CodexHooksTargetPath(scope Scope, cwd string) (string, error) {
	return codexHooksPath(scope, cwd)
}
