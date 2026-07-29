// Package integrate manages opt-in harness integration.
//
// Claude adapter: generates .claude/settings.json with hooks anchored to
// the munsu binary path, mirroring the munsu Claude hook contract.

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

// claudeSettingsPath returns the path to .claude/settings.json for the given scope.
func claudeSettingsPath(scope Scope, cwd string) (string, error) {
	switch scope {
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine user home: %w", err)
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	case ScopeProject:
		if cwd == "" {
			return "", fmt.Errorf("cwd is required for project scope")
		}
		canonical, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return "", fmt.Errorf("cannot resolve cwd %s: %w", cwd, err)
		}
		return filepath.Join(canonical, ".claude", "settings.json"), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
}

// claudeHookCommand builds a JSON-marshaled command string for Claude hooks.
func claudeHookCommand(munsuBin string, args ...string) string {
	// Build the full quoted command string: "/path/to/munsu" arg1 arg2
	// The path is JSON-marshaled to handle spaces/special chars.
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

// claudeSettingsJSON builds the complete hooks JSON as a byte slice,
// embedding the munsu binary path into each command.
func claudeSettingsJSON(munsuBin string) ([]byte, error) {
	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookMatcher struct {
		Matcher string      `json:"matcher,omitempty"`
		Hooks   []hookEntry `json:"hooks"`
	}

	sessionStartNudge := claudeHookCommand(munsuBin, "integrate", "sessionstart-nudge")
	safetyCheck := claudeHookCommand(munsuBin, "integrate", "safety-check", "--harness", "claude")
	guardCmd := claudeHookCommand(munsuBin, "guard", "--harness", "claude")

	settings := struct {
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

	return json.MarshalIndent(settings, "", "  ")
}

// ClaudeSettingsContent returns the settings JSON with the munsu binary path baked in.
func ClaudeSettingsContent(munsuBinPath string) string {
	data, err := claudeSettingsJSON(munsuBinPath)
	if err != nil {
		// Fallback: shouldn't happen
		return `{"hooks":{}}`
	}
	return string(data)
}

// ClaudeSettingsDigest returns the SHA-256 hex digest of the generated settings content.
func ClaudeSettingsDigest(munsuBinPath string) string {
	content := ClaudeSettingsContent(munsuBinPath)
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ClaudeSettingsHasOwnedHooks checks whether the settings.json at settingsPath contains
// all expected munsu-owned hook commands anchored to the given munsu binary path.
// Returns true when all expected hooks are present, along with a descriptive message.
// The message lists which hooks are missing when the check fails.
func ClaudeSettingsHasOwnedHooks(settingsPath, munsuBin string) (bool, string, error) {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false, "", fmt.Errorf("reading claude settings: %w", err)
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
		return false, "", fmt.Errorf("parsing claude settings JSON: %w", err)
	}

	sessionStartCmd := claudeHookCommand(munsuBin, "integrate", "sessionstart-nudge")
	safetyCheckCmd := claudeHookCommand(munsuBin, "integrate", "safety-check", "--harness", "claude")
	guardCmd := claudeHookCommand(munsuBin, "guard", "--harness", "claude")

	hooksByEvent := parsed.Hooks
	if hooksByEvent == nil {
		return false, "no hooks section in settings", nil
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

	// Check PreToolUse hook
	foundPreToolUse := false
	if matchers, ok := hooksByEvent["PreToolUse"]; ok {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if h.Type == "command" && h.Command == safetyCheckCmd {
					foundPreToolUse = true
					break
				}
			}
			if foundPreToolUse {
				break
			}
		}
	}
	if !foundPreToolUse {
		missing = append(missing, "PreToolUse")
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

// ClaudeAdapter implements settings generation and installation for the Claude harness.
type ClaudeAdapter struct {
	HomeDir string
	Cwd     string
	Scope   string // "user" or "project"
	DryRun  bool
}

// InstallClaudeSettings generates and installs the .claude/settings.json file.
// Returns the target path, whether it was actually written, and the content digest.
func (a *ClaudeAdapter) InstallClaudeSettings() (targetPath string, written bool, digest string, err error) {
	munsuBin, err := ResolveMunsuPathString()
	if err != nil {
		return "", false, "", fmt.Errorf("cannot resolve munsu binary path: %w", err)
	}

	targetPath, err = claudeSettingsPath(Scope(a.Scope), a.Cwd)
	if err != nil {
		return "", false, "", fmt.Errorf("cannot determine claude settings path: %w", err)
	}

	// Check existing file: if it exists, read-merge instead of overwrite
	var existingContent string
	if existing, statErr := os.Stat(targetPath); statErr == nil && existing.Size() > 0 {
		data, readErr := os.ReadFile(targetPath)
		if readErr == nil {
			existingContent = string(data)
		}
	}

	content := ClaudeSettingsContent(munsuBin)
	digest = ClaudeSettingsDigest(munsuBin)

	if existingContent != "" {
		// Read-merge: preserve user-owned hooks, replace munsu's hook entries
		merged, mergeErr := mergeClaudeSettings(existingContent, content)
		if mergeErr != nil {
			return "", false, "", fmt.Errorf("merge claude settings: %w", mergeErr)
		}
		content = merged
		// Recalculate digest for the merged content
		sum := sha256.Sum256([]byte(content))
		digest = hex.EncodeToString(sum[:])
	}

	if a.DryRun {
		return targetPath, false, digest, nil
	}

	if err := writeAtomic(targetPath, content, 0644); err != nil {
		return "", false, "", fmt.Errorf("writing claude settings: %w", err)
	}

	return targetPath, true, digest, nil
}

// mergeClaudeSettings merges munsu's generated hook entries into existing settings,
// preserving any user-owned hooks. It takes the full existing file content and the
// full munsu-generated content, then merges the hooks arrays.
func mergeClaudeSettings(existing, generated string) (string, error) {
	var existingJSON map[string]interface{}
	if err := json.Unmarshal([]byte(existing), &existingJSON); err != nil {
		// If existing content is not valid JSON, overwrite with backup
		return generated, nil
	}

	var generatedJSON map[string]interface{}
	if err := json.Unmarshal([]byte(generated), &generatedJSON); err != nil {
		return "", fmt.Errorf("generated settings is invalid JSON: %w", err)
	}

	// Get munsu's hooks map
	genHooks, _ := generatedJSON["hooks"].(map[string]interface{})

	// Ensure hooks section exists in the merge target
	existingHooks, hasExistingHooks := existingJSON["hooks"].(map[string]interface{})
	if !hasExistingHooks || existingHooks == nil {
		existingJSON["hooks"] = genHooks
		return marshalJSON(existingJSON)
	}

	// For each hook event type, merge the arrays
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

		// Prepend munsu's hook entries to the existing list
		// This ensures munsu's hooks run first
		merged := append(genList, existingList...)
		existingHooks[eventKey] = merged
	}

	return marshalJSON(existingJSON)
}

func marshalJSON(v interface{}) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// generateClaudeManifest creates the integration manifest for the Claude adapter.
func generateClaudeManifest(harnessName string, scope string, caps []Capability, contentDigest string, targetPath string) Manifest {
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

// ClaudeSettingsTargetPath returns the expected target path for a given scope+cwd.
func ClaudeSettingsTargetPath(scope Scope, cwd string) (string, error) {
	return claudeSettingsPath(scope, cwd)
}
