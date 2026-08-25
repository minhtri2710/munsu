// Package integrate manages opt-in harness integration.
//
// Grok adapter: generates .grok/hooks/*.json hook files anchored to the munsu
// binary path, mirroring the munsu Grok hook contract.
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

// grokHooksDir returns the path to .grok/hooks/ for the given scope.
func grokHooksDir(scope Scope, cwd string) (string, error) {
	switch scope {
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine user home: %w", err)
		}
		return filepath.Join(home, ".grok", "hooks"), nil
	case ScopeProject:
		if cwd == "" {
			return "", fmt.Errorf("cwd is required for project scope")
		}
		canonical, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return "", fmt.Errorf("cannot resolve cwd %s: %w", cwd, err)
		}
		return filepath.Join(canonical, ".grok", "hooks"), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
}

// grokHookCommand wraps a munsu command in bash -lc for Grok hooks.
// Grok requires the command to be wrapped in bash -lc because Grok expands
// the raw hook command string before bash runs it.
func grokHookCommand(munsuBin string, args ...string) string {
	binJSON, err := json.Marshal(munsuBin)
	if err != nil {
		binJSON = []byte(fmt.Sprintf("%q", munsuBin))
	}
	full := string(binJSON)
	for _, arg := range args {
		full += " " + arg
	}
	return "bash -lc 'exec " + full + "'"
}

// grokHookFileNames lists the 4 Grok hook file names, matching the munsu contract.
var grokHookFileNames = []string{
	"fm-primary-sessionstart-nudge.json",
	"fm-primary-pretool-check.json",
	"fm-primary-cd-check.json",
	"fm-primary-turnend-guard.json",
}

// grokHookFile returns the JSON content for a single Grok hook file.
func grokHookFile(munsuBin string, hookName string) string {
	var command string
	var event string
	var matchers []string
	var timeout int

	switch hookName {
	case "fm-primary-sessionstart-nudge.json":
		event = "SessionStart"
		command = grokHookCommand(munsuBin, "integrate", "sessionstart-nudge")
		timeout = 10
	case "fm-primary-pretool-check.json":
		event = "PreToolUse"
		// Bash plus the native file-write tools: the latter bypass the shell
		// entirely and would otherwise reach the filesystem with no guard.
		// A second matcher in the same file keeps the four-file manifest
		// contract intact.
		matchers = []string{"Bash", writeToolMatcher(grokWriteToolNames)}
		command = grokHookCommand(munsuBin, "integrate", "safety-check", "--harness", "grok")
		timeout = 10
	case "fm-primary-cd-check.json":
		event = "PreToolUse"
		matchers = []string{"Bash"}
		command = grokHookCommand(munsuBin, "integrate", "safety-check", "--harness", "grok")
		timeout = 10
	case "fm-primary-turnend-guard.json":
		event = "Stop"
		command = grokHookCommand(munsuBin, "guard", "--harness", "grok")
		timeout = 180
	default:
		return ""
	}

	return grokBuildHookJSON(event, matchers, command, timeout)
}

// grokBuildHookJSON builds the complete hook JSON for a single Grok hook file.
// An empty matcher list produces one unmatched entry (the Stop hook shape).
func grokBuildHookJSON(event string, matcherNames []string, command string, timeout int) string {
	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}

	type hookMatcher struct {
		Matcher string      `json:"matcher,omitempty"`
		Hooks   []hookEntry `json:"hooks"`
	}

	entries := []hookEntry{
		{Type: "command", Command: command, Timeout: timeout},
	}

	var matchers []hookMatcher
	for _, name := range matcherNames {
		if name == "" {
			continue
		}
		matchers = append(matchers, hookMatcher{Matcher: name, Hooks: entries})
	}
	if len(matchers) == 0 {
		matchers = []hookMatcher{
			{
				Hooks: entries,
			},
		}
	}

	hookData := struct {
		Hooks map[string][]hookMatcher `json:"hooks"`
	}{
		Hooks: map[string][]hookMatcher{
			event: matchers,
		},
	}

	data, err := json.MarshalIndent(hookData, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

// GrokHooksContent returns the complete hook JSON content for the given hook file name.
func GrokHooksContent(munsuBinPath string, hookName string) string {
	return grokHookFile(munsuBinPath, hookName)
}

// GrokHooksAllTargetPaths returns all 4 hook file paths for the given scope+cwd.
func GrokHooksAllTargetPaths(scope Scope, cwd string) ([]string, error) {
	dir, err := grokHooksDir(scope, cwd)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range grokHookFileNames {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

// GrokHooksHasOwnedHooks checks whether all 4 Grok hook files in the hooksDir
// contain munsu-owned commands anchored to the given munsu binary path.
// Returns true when all expected hooks are present, along with a descriptive message.
//
// grokHookDocument is the on-disk shape of one Grok hook file. Both the
// installed file and the freshly generated expectation are decoded into it so
// the two can be compared entry by entry.
type grokHookDocument struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher,omitempty"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// grokHookEntries flattens a hook document into comparable
// "event|matcher|command" keys.
func grokHookEntries(doc grokHookDocument) []string {
	var entries []string
	for event, matchers := range doc.Hooks {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if h.Type != "" && h.Type != "command" {
					continue
				}
				entries = append(entries, event+"|"+m.Matcher+"|"+h.Command)
			}
		}
	}
	return entries
}

func GrokHooksHasOwnedHooks(hooksDir, munsuBin string) (bool, string, error) {
	var missing []string

	for _, name := range grokHookFileNames {
		path := filepath.Join(hooksDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			missing = append(missing, name+" (not found)")
			continue
		}

		var parsed grokHookDocument
		if err := json.Unmarshal(data, &parsed); err != nil {
			missing = append(missing, name+" (invalid JSON)")
			continue
		}

		var expectedParsed grokHookDocument
		if err := json.Unmarshal([]byte(grokHookFile(munsuBin, name)), &expectedParsed); err != nil {
			missing = append(missing, name+" (cannot parse expected)")
			continue
		}

		// Compare the full set of event+matcher+command entries, not just the
		// first command found. Taking the first match reports an installation
		// that predates a newly added matcher as healthy, so it is never
		// repaired and the new matcher never reaches the machines running today.
		actual := make(map[string]bool)
		for _, entry := range grokHookEntries(parsed) {
			actual[entry] = true
		}
		var absent []string
		for _, want := range grokHookEntries(expectedParsed) {
			if !actual[want] {
				absent = append(absent, want)
			}
		}
		if len(absent) > 0 {
			missing = append(missing, name+" (missing hook entries: "+strings.Join(absent, ", ")+")")
			continue
		}
	}

	if len(missing) > 0 {
		return false, fmt.Sprintf("missing or mismatched munsu-owned hooks: %s", strings.Join(missing, ", ")), nil
	}
	return true, "all munsu-owned grok hooks present", nil
}

// GrokAdapter implements hook generation and installation for the Grok harness.
type GrokAdapter struct {
	HomeDir string
	Cwd     string
	Scope   string // "user" or "project"
	DryRun  bool
}

// InstallGrokHooks generates and installs all 4 .grok/hooks/*.json files.
// Returns the target paths, whether anything was actually written, and the
// combined content digest (SHA-256 of all 4 hook files concatenated).
func (a *GrokAdapter) InstallGrokHooks() (targetPaths []string, written bool, combinedDigest string, err error) {
	munsuBin, err := ResolveMunsuPathString()
	if err != nil {
		return nil, false, "", fmt.Errorf("cannot resolve munsu binary path: %w", err)
	}

	scope := Scope(a.Scope)
	dir, err := grokHooksDir(scope, a.Cwd)
	if err != nil {
		return nil, false, "", fmt.Errorf("cannot determine grok hooks dir: %w", err)
	}

	var allTargets []string
	var allContents []string
	anyWritten := false

	for _, name := range grokHookFileNames {
		targetPath := filepath.Join(dir, name)
		allTargets = append(allTargets, targetPath)

		// Check existing file: read-merge if present
		var existingContent string
		if existing, statErr := os.Stat(targetPath); statErr == nil && existing.Size() > 0 {
			data, readErr := os.ReadFile(targetPath)
			if readErr == nil {
				existingContent = string(data)
			}
		}

		content := GrokHooksContent(munsuBin, name)

		if existingContent != "" {
			// Read-merge: preserve user-owned hooks, replace munsu's hook entries
			merged, mergeErr := mergeGrokHookFile(existingContent, content)
			if mergeErr != nil {
				return nil, false, "", fmt.Errorf("merge grok hook %s: %w", name, mergeErr)
			}
			content = merged
		}

		allContents = append(allContents, content)

		if a.DryRun {
			continue
		}

		if err := writeAtomic(targetPath, content, 0644); err != nil {
			return nil, false, "", fmt.Errorf("writing grok hook %s: %w", name, err)
		}
		anyWritten = true
	}

	// Compute combined digest of all 4 files
	combined := strings.Join(allContents, "\x00")
	sum := sha256.Sum256([]byte(combined))
	combinedDigest = hex.EncodeToString(sum[:])

	return allTargets, anyWritten, combinedDigest, nil
}

// mergeGrokHookFile merges munsu's generated hook entries into an existing file,
// preserving user-owned hooks that differ from munsu's entries.
func mergeGrokHookFile(existing, generated string) (string, error) {
	var existingJSON map[string]interface{}
	if err := json.Unmarshal([]byte(existing), &existingJSON); err != nil {
		// If existing content is not valid JSON, overwrite with backup
		return generated, nil
	}

	var generatedJSON map[string]interface{}
	if err := json.Unmarshal([]byte(generated), &generatedJSON); err != nil {
		return "", fmt.Errorf("generated hook is invalid JSON: %w", err)
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
		merged := append(genList, existingList...)
		existingHooks[eventKey] = merged
	}

	return marshalJSON(existingJSON)
}

// generateGrokManifest creates the integration manifest for the Grok adapter.
func generateGrokManifest(harnessName string, scope string, caps []Capability, combinedDigest string, targetPaths []string) Manifest {
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
