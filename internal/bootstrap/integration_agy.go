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
	"sort"
	"strings"
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

	// Safety check: PreToolUse for run_command plus the native file-write
	// tools, which bypass the shell entirely and would otherwise reach the
	// filesystem with no guard at all.
	safetyCommand := hookCommand(munsuBin, "integrate", "safety-check", "--harness", "agy")
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
			{
				"matcher": writeToolMatcher(agyWriteToolNames),
				"hooks": []map[string]interface{}{
					{
						"command": safetyCommand,
					},
				},
			},
		},
	}

	// Turn-end guard: Stop (no matcher needed)
	guardCommand := hookCommand(munsuBin, "guard", "--harness", "agy")
	hooks["munsu-turnend-guard"] = map[string]interface{}{
		"Stop": []map[string]interface{}{
			{
				"command": guardCommand,
			},
		},
	}

	// Session-start nudge: PreInvocation (no matcher needed)
	nudgeCommand := hookCommand(munsuBin, "integrate", "sessionstart-nudge")
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

// AgyHooksTargetPath returns the expected path to .agents/hooks.json for the given scope+cwd.
func AgyHooksTargetPath(scope Scope, cwd string) (string, error) {
	dir, err := agyHooksDir(scope, cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks.json"), nil
}

// agyHookEvents is the on-disk shape of one munsu-owned agy hook: event name
// to entries, each carrying its command directly or nested in hooks[].
type agyHookEvents map[string][]struct {
	Matcher string `json:"matcher"`
	Command string `json:"command"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
}

// agyHookEntries flattens the munsu-owned hooks of an agy hooks.json into
// comparable "name|event|matcher|command" keys. User-owned keys are never
// decoded, so their shape cannot fail the check.
func agyHookEntries(data []byte) ([]string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var entries []string
	for _, name := range agyHooksFileNames {
		raw, ok := doc[name]
		if !ok {
			continue
		}
		var events agyHookEvents
		if err := json.Unmarshal(raw, &events); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for event, list := range events {
			for _, e := range list {
				if e.Command != "" {
					entries = append(entries, name+"|"+event+"|"+e.Matcher+"|"+e.Command)
				}
				for _, h := range e.Hooks {
					entries = append(entries, name+"|"+event+"|"+e.Matcher+"|"+h.Command)
				}
			}
		}
	}
	return entries, nil
}

// AgyHooksHasOwnedHooks checks whether hooks.json in hooksDir carries every
// munsu-owned event+matcher+command entry that AgyHooksContent generates for
// munsuBin. A command that merely mentions the binary path (another
// --harness, a sibling binary) is drift and gets repaired.
func AgyHooksHasOwnedHooks(hooksDir, munsuBin string) (bool, string, error) {
	data, err := os.ReadFile(filepath.Join(hooksDir, "hooks.json"))
	if err != nil {
		return false, "hooks.json not found or unreadable", nil
	}
	actual, err := agyHookEntries(data)
	if err != nil {
		return false, "hooks.json is invalid JSON: " + err.Error(), nil
	}
	expected, err := agyHookEntries([]byte(AgyHooksContent(munsuBin)))
	if err != nil {
		return false, "", fmt.Errorf("parsing generated agy hooks: %w", err)
	}

	present := make(map[string]bool)
	for _, entry := range actual {
		present[entry] = true
	}
	var missing []string
	for _, want := range expected {
		if !present[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return false, fmt.Sprintf("missing or mismatched munsu-owned hooks: %s", strings.Join(missing, ", ")), nil
	}
	return true, "all munsu-owned agy hooks present", nil
}

// AgyAdapter implements hook generation and installation for the Agy harness.
type AgyAdapter struct {
	Cwd    string
	Scope  string // "user" or "project"
	DryRun bool
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

	// Read-merge: replace munsu-owned keys, keep user-owned keys.
	existing, err := readExistingHookFile(targetPath)
	if err != nil {
		return nil, false, "", err
	}
	if existing != "" {
		content, err = mergeAgyHooksFile(targetPath, existing, content)
		if err != nil {
			return nil, false, "", err
		}
	}

	sum := sha256.Sum256([]byte(content))
	combinedDigest = hex.EncodeToString(sum[:])

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
func mergeAgyHooksFile(path, existing, generated string) (string, error) {
	existingJSON, err := parseExistingHookJSON(path, existing)
	if err != nil {
		return "", err
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
