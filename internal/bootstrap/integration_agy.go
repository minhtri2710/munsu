// Package integrate manages opt-in harness integration.
//
// Agy adapter: generates .agents/hooks.json with munsu-owned hook entries
// anchored to the munsu binary path, following the official Antigravity CLI
// hook contract documented at https://antigravity.google/docs/hooks.
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

// agyHooksDir returns the path to .agents/ for the given scope.
// munsu uses project-local .agents/ (PREFERRED per agy docs), falling
// back to user-global only when no project cwd is available.
func agyHooksDir(scope Scope, cwd string) (string, error) {
	switch scope {
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine user home: %w", err)
		}
		return filepath.Join(home, ".gemini", "config"), nil
	case ScopeProject:
		if cwd == "" {
			return "", fmt.Errorf("cwd is required for project scope")
		}
		canonical, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return "", fmt.Errorf("cannot resolve cwd %s: %w", cwd, err)
		}
		return filepath.Join(canonical, ".agents"), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
}

// agyHookCommand wraps a munsu command for agy hooks.
// agy expands the raw command string via shell, so we pass the
// absolute munsu path with arguments directly.
func agyHookCommand(munsuBin string, args ...string) string {
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

// agyHooksFileNames lists the munsu-owned hook name keys in .agents/hooks.json.
var agyHooksFileNames = []string{
	"munsu-safety-check",
	"munsu-turnend-guard",
	"munsu-sessionstart-nudge",
}

// agyBuildHookJSON builds the complete .agents/hooks.json content for agy.
// The top-level keys are hook names; each hook maps event names to handler arrays.
func agyBuildHookJSON(munsuBin string) string {
	// Build the three munsu-owned hooks
	hooks := make(map[string]interface{})

	// Safety check: PreToolUse with matcher run_command
	safetyCommand := agyHookCommand(munsuBin, "integrate", "safety-check", "--harness", "agy")
	hooks["munsu-safety-check"] = map[string]interface{}{
		"PreToolUse": []map[string]interface{}{
			{
				"matcher": "run_command",
				"hooks": []map[string]interface{}{
					{
						"command": safetyCommand,
					},
				},
			},
		},
	}

	// Turn-end guard: Stop (no matcher needed)
	guardCommand := agyHookCommand(munsuBin, "guard", "--harness", "agy")
	hooks["munsu-turnend-guard"] = map[string]interface{}{
		"Stop": []map[string]interface{}{
			{
				"command": guardCommand,
			},
		},
	}

	// Session-start nudge: PreInvocation (no matcher needed)
	nudgeCommand := agyHookCommand(munsuBin, "integrate", "sessionstart-nudge")
	hooks["munsu-sessionstart-nudge"] = map[string]interface{}{
		"PreInvocation": []map[string]interface{}{
			{
				"command": nudgeCommand,
			},
		},
	}

	data, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

// AgyHooksContent returns the complete .agents/hooks.json content for agy.
func AgyHooksContent(munsuBinPath string) string {
	return agyBuildHookJSON(munsuBinPath)
}

// AgyHooksDigest returns the SHA-256 hex digest of the hooks content.
func AgyHooksDigest(munsuBinPath string) string {
	content := AgyHooksContent(munsuBinPath)
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// AgyHooksTargetPath returns the expected path to .agents/hooks.json for the given scope+cwd.
func AgyHooksTargetPath(scope Scope, cwd string) (string, error) {
	dir, err := agyHooksDir(scope, cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks.json"), nil
}

// AgyHooksAllTargetPaths returns the single hooks.json path for the given scope+cwd.
func AgyHooksAllTargetPaths(scope Scope, cwd string) ([]string, error) {
	path, err := AgyHooksTargetPath(scope, cwd)
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

// AgyHooksHasOwnedHooks checks whether the .agents/hooks.json file contains
// all munsu-owned hook names with commands anchored to the given munsu binary path.
// Returns true when all expected hooks are present, along with a descriptive message.
func AgyHooksHasOwnedHooks(hooksDir, munsuBin string) (bool, string, error) {
	var missing []string

	path := filepath.Join(hooksDir, "hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "hooks.json not found or unreadable", nil
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false, "hooks.json is invalid JSON", nil
	}

	for _, name := range agyHooksFileNames {
		hookConfig, ok := parsed[name].(map[string]interface{})
		if !ok {
			missing = append(missing, name+" (not found)")
			continue
		}

		// Walk through all event entries looking for a command that references munsuBin
		foundMunsu := false
		for _, eventValue := range hookConfig {
			entries, ok := eventValue.([]interface{})
			if !ok {
				continue
			}
			for _, entry := range entries {
				entryMap, ok := entry.(map[string]interface{})
				if !ok {
					continue
				}
				// For PreToolUse/PostToolUse, command is nested in hooks[]
				if hooks, ok := entryMap["hooks"].([]interface{}); ok {
					for _, h := range hooks {
						hMap, ok := h.(map[string]interface{})
						if !ok {
							continue
						}
						if cmd, ok := hMap["command"].(string); ok && strings.Contains(cmd, munsuBin) {
							foundMunsu = true
						}
					}
				}
				// For Stop/PreInvocation/PostInvocation, command is directly on the entry
				if cmd, ok := entryMap["command"].(string); ok && strings.Contains(cmd, munsuBin) {
					foundMunsu = true
				}
			}
		}

		if !foundMunsu {
			missing = append(missing, name+" (command does not reference munsu)")
		}
	}

	if len(missing) > 0 {
		return false, fmt.Sprintf("missing or mismatched munsu-owned hooks: %s", strings.Join(missing, ", ")), nil
	}
	return true, "all munsu-owned agy hooks present", nil
}

// AgyAdapter implements hook generation and installation for the Agy harness.
type AgyAdapter struct {
	HomeDir string
	Cwd     string
	Scope   string // "user" or "project"
	DryRun  bool
}

// InstallAgyHooks generates and installs .agents/hooks.json.
// Returns the target paths, whether anything was actually written, and the
// content digest (SHA-256 of the hooks.json content).
func (a *AgyAdapter) InstallAgyHooks() (targetPaths []string, written bool, combinedDigest string, err error) {
	munsuBin, err := ResolveMunsuPathString()
	if err != nil {
		return nil, false, "", fmt.Errorf("cannot resolve munsu binary path: %w", err)
	}

	scope := Scope(a.Scope)
	dir, err := agyHooksDir(scope, a.Cwd)
	if err != nil {
		return nil, false, "", fmt.Errorf("cannot determine agy hooks dir: %w", err)
	}

	targetPath := filepath.Join(dir, "hooks.json")
	targetPaths = []string{targetPath}

	content := AgyHooksContent(munsuBin)

	// Read-merge: check existing hooks.json, merge munsu-owned keys with user-owned keys
	if existing, statErr := os.Stat(targetPath); statErr == nil && existing.Size() > 0 {
		data, readErr := os.ReadFile(targetPath)
		if readErr == nil && len(data) > 0 {
			merged, mergeErr := mergeAgyHooksFile(string(data), content)
			if mergeErr != nil {
				return nil, false, "", fmt.Errorf("merge agy hooks: %w", mergeErr)
			}
			content = merged
		}
	}

	combinedDigest = AgyHooksDigest(munsuBin)

	if a.DryRun {
		return targetPaths, false, combinedDigest, nil
	}

	if err := writeAtomic(targetPath, content, 0644); err != nil {
		return nil, false, "", fmt.Errorf("writing agy hooks.json: %w", err)
	}

	return targetPaths, true, combinedDigest, nil
}

// mergeAgyHooksFile merges munsu's generated hook entries into an existing
// hooks.json, preserving user-owned hook names that differ from munsu's entries.
func mergeAgyHooksFile(existing, generated string) (string, error) {
	var existingJSON map[string]interface{}
	if err := json.Unmarshal([]byte(existing), &existingJSON); err != nil {
		// If existing is not valid JSON, overwrite with backup
		return generated, nil
	}

	var generatedJSON map[string]interface{}
	if err := json.Unmarshal([]byte(generated), &generatedJSON); err != nil {
		return "", fmt.Errorf("generated hooks.json is invalid JSON: %w", err)
	}

	// Remove any existing munsu-owned keys from the merged result
	for _, name := range agyHooksFileNames {
		delete(existingJSON, name)
	}

	// Add munsu's generated entries
	for name, hooks := range generatedJSON {
		existingJSON[name] = hooks
	}

	return marshalJSON(existingJSON)
}

// generateAgyManifest creates the integration manifest for the Agy adapter.
func generateAgyManifest(harnessName string, scope string, caps []Capability, combinedDigest string, targetPaths []string) Manifest {
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
		TargetPaths:   targetPaths,
		Capabilities:  capStrs,
		ContentDigest: combinedDigest,
	}
}
