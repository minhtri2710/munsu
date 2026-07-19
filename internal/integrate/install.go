package integrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/minhtri2710/munsu/internal/harness"
)

// Install installs integration artifacts for the given harness.
// Returns an IntegrationResult describing what was done.
//
// Semantics:
//   - Fresh install: creates all artifacts, writes manifest.
//   - Re-install (idempotent): identical content is a no-op; changed content
//     is a repair. User files without ownership markers are backed up.
//   - Unknown harness: no filesystem mutation.
//   - Dry-run: reports what would happen without writing.
func Install(homeDir, cwd, harnessName string, scope Scope, dryRun bool) (*IntegrationResult, error) {
	adapter, ok := harness.GetAdapter(harnessName)
	if !ok {
		return &IntegrationResult{
			Harness: harnessName,
			State:   "unsupported",
			Message: fmt.Sprintf("harness %q is not in the adapter registry", harnessName),
		}, nil
	}

	caps := EnabledCapabilities(harnessName)
	if len(caps) == 0 {
		return &IntegrationResult{
			Harness: harnessName,
			State:   "unsupported",
			Message: fmt.Sprintf("harness %q has no integration capabilities", harnessName),
		}, nil
	}

	result := &IntegrationResult{
		Harness: adapter.Name,
		Scope:   scope,
		State:   "installed",
	}

	switch harnessName {
	case harness.Pi:
		piAdpt := &PiAdapter{
			HomeDir: homeDir,
			Cwd:     cwd,
			Scope:   string(scope),
			DryRun:  dryRun,
		}
		target, written, digest, err := piAdpt.InstallPiExtension()
		if err != nil {
			return nil, fmt.Errorf("pi extension install: %w", err)
		}
		result.Message = fmt.Sprintf("pi extension: %s", target)

		if !dryRun && written {
			// Write manifest to per-harness per-scope path.
			manifest := GenerateManifest(harnessName, string(scope), caps, digest)
			manifest.TargetPaths = []string{target}
			artifactDir := homePathForScope(homeDir, harnessName, scope, cwd)
			if err := os.MkdirAll(artifactDir, 0755); err != nil {
				return nil, fmt.Errorf("create artifact dir: %w", err)
			}

			manifestPath := ManifestPath(homeDir, harnessName, scope, cwd)
			manifestData, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("marshal manifest: %w", err)
			}
			if err := writeAtomic(manifestPath, string(manifestData), 0644); err != nil {
				return nil, fmt.Errorf("write manifest: %w", err)
			}

			result.Version = manifest.Version
			result.InstalledAt = manifest.InstalledAt
		} else if dryRun {
			result.Message = fmt.Sprintf("[dry-run] would write: %s", target)
			result.State = "fresh"
		} else {
			result.State = "fresh"
			result.Message = "no changes needed"
		}

	case harness.Claude:
		claudeAdpt := &ClaudeAdapter{
			HomeDir: homeDir,
			Cwd:     cwd,
			Scope:   string(scope),
			DryRun:  dryRun,
		}
		target, written, digest, err := claudeAdpt.InstallClaudeSettings()
		if err != nil {
			return nil, fmt.Errorf("claude settings install: %w", err)
		}
		result.Message = fmt.Sprintf("claude settings: %s", target)

		if !dryRun && written {
			manifest := generateClaudeManifest(harnessName, string(scope), caps, digest, target)
			artifactDir := homePathForScope(homeDir, harnessName, scope, cwd)
			if err := os.MkdirAll(artifactDir, 0755); err != nil {
				return nil, fmt.Errorf("create artifact dir: %w", err)
			}

			manifestPath := ManifestPath(homeDir, harnessName, scope, cwd)
			manifestData, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("marshal manifest: %w", err)
			}
			if err := writeAtomic(manifestPath, string(manifestData), 0644); err != nil {
				return nil, fmt.Errorf("write manifest: %w", err)
			}

			result.Version = manifest.Version
			result.InstalledAt = manifest.InstalledAt
		} else if dryRun {
			result.Message = fmt.Sprintf("[dry-run] would write: %s", target)
			result.State = "fresh"
		} else {
			result.State = "fresh"
			result.Message = "no changes needed"
		}

	case harness.Grok:
		grokAdpt := &GrokAdapter{
			HomeDir: homeDir,
			Cwd:     cwd,
			Scope:   string(scope),
			DryRun:  dryRun,
		}
		targets, written, digest, err := grokAdpt.InstallGrokHooks()
		if err != nil {
			return nil, fmt.Errorf("grok hooks install: %w", err)
		}
		result.Message = fmt.Sprintf("grok hooks: %d files in %s", len(targets), filepath.Dir(targets[0]))

		if !dryRun && written {
			manifest := generateGrokManifest(harnessName, string(scope), caps, digest, targets)
			artifactDir := homePathForScope(homeDir, harnessName, scope, cwd)
			if err := os.MkdirAll(artifactDir, 0755); err != nil {
				return nil, fmt.Errorf("create artifact dir: %w", err)
			}

			manifestPath := ManifestPath(homeDir, harnessName, scope, cwd)
			manifestData, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("marshal manifest: %w", err)
			}
			if err := writeAtomic(manifestPath, string(manifestData), 0644); err != nil {
				return nil, fmt.Errorf("write manifest: %w", err)
			}

			result.Version = manifest.Version
			result.InstalledAt = manifest.InstalledAt
		} else if dryRun {
			result.Message = fmt.Sprintf("[dry-run] would write: %d files to %s", len(targets), filepath.Dir(targets[0]))
			result.State = "fresh"
		} else {
			result.State = "fresh"
			result.Message = "no changes needed"
		}

	case harness.Codex:
		codexAdpt := &CodexAdapter{
			HomeDir: homeDir,
			Cwd:     cwd,
			Scope:   string(scope),
			DryRun:  dryRun,
		}
		target, written, digest, err := codexAdpt.InstallCodexHooks()
		if err != nil {
			return nil, fmt.Errorf("codex hooks install: %w", err)
		}
		result.Message = fmt.Sprintf("codex hooks: %s", target)

		if !dryRun && written {
			manifest := generateCodexManifest(harnessName, string(scope), caps, digest, target)
			artifactDir := homePathForScope(homeDir, harnessName, scope, cwd)
			if err := os.MkdirAll(artifactDir, 0755); err != nil {
				return nil, fmt.Errorf("create artifact dir: %w", err)
			}

			manifestPath := ManifestPath(homeDir, harnessName, scope, cwd)
			manifestData, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("marshal manifest: %w", err)
			}
			if err := writeAtomic(manifestPath, string(manifestData), 0644); err != nil {
				return nil, fmt.Errorf("write manifest: %w", err)
			}

			result.Version = manifest.Version
			result.InstalledAt = manifest.InstalledAt
		} else if dryRun {
			result.Message = fmt.Sprintf("[dry-run] would write: %s", target)
			result.State = "fresh"
		} else {
			result.State = "fresh"
			result.Message = "no changes needed"
		}

	case harness.Opencode:
		opencodeAdpt := &OpencodeAdapter{
			HomeDir: homeDir,
			Cwd:     cwd,
			Scope:   string(scope),
			DryRun:  dryRun,
		}
		targets, written, digest, err := opencodeAdpt.InstallOpencodePlugins()
		if err != nil {
			return nil, fmt.Errorf("opencode plugins install: %w", err)
		}
		result.Message = fmt.Sprintf("opencode plugins: %d files in %s", len(targets), filepath.Dir(targets[0]))

		if !dryRun && written {
			manifest := generateOpencodeManifest(harnessName, string(scope), caps, digest, targets)
			artifactDir := homePathForScope(homeDir, harnessName, scope, cwd)
			if err := os.MkdirAll(artifactDir, 0755); err != nil {
				return nil, fmt.Errorf("create artifact dir: %w", err)
			}

			manifestPath := ManifestPath(homeDir, harnessName, scope, cwd)
			manifestData, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("marshal manifest: %w", err)
			}
			if err := writeAtomic(manifestPath, string(manifestData), 0644); err != nil {
				return nil, fmt.Errorf("write manifest: %w", err)
			}

			result.Version = manifest.Version
			result.InstalledAt = manifest.InstalledAt
		} else if dryRun {
			result.Message = fmt.Sprintf("[dry-run] would write: %d files to %s", len(targets), filepath.Dir(targets[0]))
			result.State = "fresh"
		} else {
			result.State = "fresh"
			result.Message = "no changes needed"
		}

	case harness.Agy:
		agyAdpt := &AgyAdapter{
			HomeDir: homeDir,
			Cwd:     cwd,
			Scope:   string(scope),
			DryRun:  dryRun,
		}
		targets, written, digest, err := agyAdpt.InstallAgyHooks()
		if err != nil {
			return nil, fmt.Errorf("agy hooks install: %w", err)
		}
		result.Message = fmt.Sprintf("agy hooks: %s", targets[0])

		if !dryRun && written {
			manifest := generateAgyManifest(harnessName, string(scope), caps, digest, targets)
			artifactDir := homePathForScope(homeDir, harnessName, scope, cwd)
			if err := os.MkdirAll(artifactDir, 0755); err != nil {
				return nil, fmt.Errorf("create artifact dir: %w", err)
			}

			manifestPath := ManifestPath(homeDir, harnessName, scope, cwd)
			manifestData, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return nil, fmt.Errorf("marshal manifest: %w", err)
			}
			if err := writeAtomic(manifestPath, string(manifestData), 0644); err != nil {
				return nil, fmt.Errorf("write manifest: %w", err)
			}

			result.Version = manifest.Version
			result.InstalledAt = manifest.InstalledAt
		} else if dryRun {
			result.Message = fmt.Sprintf("[dry-run] would write: %s", targets[0])
			result.State = "fresh"
		} else {
			result.State = "fresh"
			result.Message = "no changes needed"
		}

	default:
		return &IntegrationResult{
			Harness: harnessName,
			State:   "unsupported",
			Message: fmt.Sprintf("harness %q integration not yet implemented", harnessName),
		}, nil
	}

	return result, nil
}

// Repair checks for drift and repairs integration artifacts.
func Repair(homeDir, cwd, harnessName string, scope Scope, dryRun bool) (*IntegrationResult, error) {
	status, err := Status(homeDir, cwd, harnessName, scope)
	if err != nil {
		return nil, fmt.Errorf("pre-repair status: %w", err)
	}

	if status.State == "unsupported" {
		return status, nil
	}

	if status.State == "installed" && !status.Drifted {
		return &IntegrationResult{
			Harness: status.Harness,
			Scope:   status.Scope,
			State:   "installed",
			Message: "no drift detected, integration is healthy",
			Version: status.Version,
		}, nil
	}

	result, err := Install(homeDir, cwd, harnessName, scope, dryRun)
	if err != nil {
		return nil, fmt.Errorf("repair install: %w", err)
	}

	if !dryRun {
		result.State = "repair"
		result.Message = "drift corrected, integration re-installed"
	} else {
		result.State = "repairable"
		result.Message = "[dry-run] drift would be corrected"
	}

	return result, nil
}

// Status checks the current integration state for the given harness.
// It detects drift by comparing installed artifacts against the manifest
// and verifying ownership markers.
func Status(homeDir, cwd, harnessName string, scope Scope) (*IntegrationResult, error) {
	adapter, ok := harness.GetAdapter(harnessName)
	if !ok {
		return &IntegrationResult{
			Harness: harnessName,
			State:   "unsupported",
			Message: fmt.Sprintf("harness %q is not in the adapter registry", harnessName),
		}, nil
	}

	caps := EnabledCapabilities(harnessName)
	if len(caps) == 0 {
		return &IntegrationResult{
			Harness: harnessName,
			State:   "unsupported",
			Message: fmt.Sprintf("harness %q has no integration capabilities", harnessName),
		}, nil
	}

	result := &IntegrationResult{
		Harness: adapter.Name,
		Scope:   scope,
	}

	// Read manifest from per-harness per-scope path.
	manifestPath := ManifestPath(homeDir, harnessName, scope, cwd)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		result.State = "absent"
		result.Message = "no integration manifest found — not installed"
		return result, nil
	}

	// Decode with DisallowUnknownFields to reject unknown JSON fields.
	// Reject trailing JSON values after the main object.
	var manifest Manifest
	dec := json.NewDecoder(reader(manifestData))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		result.State = "drifted"
		result.Message = "manifest corrupted or contains unknown fields: " + err.Error()
		result.Drifted = true
		return result, nil
	}
	// Check for trailing non-whitespace after the main object.
	// The general decode must specifically return io.EOF to be clean.
	// Any non-EOF error or a successful decode is drift.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != nil {
		if !errors.Is(err, io.EOF) {
			result.State = "drifted"
			result.Message = "manifest trailing data decode error: " + err.Error()
			result.Drifted = true
			return result, nil
		}
	} else {
		result.State = "drifted"
		result.Message = "manifest contains trailing JSON values after the main object"
		result.Drifted = true
		return result, nil
	}

	// Strict validation against expected values.
	expectedCaps := caps
	expectedTargets := 1
	if harnessName == harness.Grok || harnessName == harness.Opencode {
		expectedTargets = 4
	}
	if err := ValidateStrict(manifest, harnessName, string(scope), "1.0.0", expectedCaps, expectedTargets); err != nil {
		result.State = "drifted"
		result.Message = fmt.Sprintf("manifest validation: %v", err)
		result.Drifted = true
		return result, nil
	}

	// Verify that TargetPaths contains the expected canonical targets for this scope+cwd.
	if harnessName == harness.Grok || harnessName == harness.Opencode {
		// For Grok and OpenCode, verify all 4 expected paths are present.
		var expectedPaths []string
		var pathErr error
		if harnessName == harness.Grok {
			expectedPaths, pathErr = GrokHooksAllTargetPaths(scope, cwd)
		} else {
			expectedPaths, pathErr = OpencodePluginsAllTargetPaths(scope, cwd)
		}
		if pathErr != nil {
			result.State = "drifted"
			result.Message = fmt.Sprintf("cannot compute %s targets: %v", harnessName, pathErr)
			result.Drifted = true
			return result, nil
		}
		for _, expected := range expectedPaths {
			found := false
			canonicalExpected, resolveErr := filepath.EvalSymlinks(expected)
			if resolveErr != nil {
				canonicalExpected = filepath.Clean(expected)
			}
			for _, tp := range manifest.TargetPaths {
				canonicalTP, resolveErr := filepath.EvalSymlinks(tp)
				if resolveErr != nil {
					continue
				}
				canonicalTP = filepath.Clean(canonicalTP)
				if canonicalTP == canonicalExpected {
					found = true
					break
				}
			}
			if !found {
				result.State = "drifted"
				result.Message = fmt.Sprintf("target path mismatch: expected %q not found in manifest target paths", expected)
				result.Drifted = true
				return result, nil
			}
		}
	} else {
		var expectedTarget string
		if harnessName == harness.Claude {
			expectedTarget, err = ClaudeSettingsTargetPath(scope, cwd)
		} else if harnessName == harness.Codex {
			expectedTarget, err = CodexHooksTargetPath(scope, cwd)
		} else if harnessName == harness.Agy {
			expectedTarget, err = AgyHooksTargetPath(scope, cwd)
		} else {
			expectedTarget, err = ExpectedTargetPath(scope, cwd)
		}
		if err != nil {
			result.State = "drifted"
			result.Message = fmt.Sprintf("cannot compute expected target: %v", err)
			result.Drifted = true
			return result, nil
		}
		targetFound := false
		for _, tp := range manifest.TargetPaths {
			canonicalTP, resolveErr := filepath.EvalSymlinks(tp)
			if resolveErr != nil {
				continue
			}
			canonicalTP = filepath.Clean(canonicalTP)
			canonicalExpected, resolveErr := filepath.EvalSymlinks(expectedTarget)
			if resolveErr != nil {
				continue
			}
			canonicalExpected = filepath.Clean(canonicalExpected)
			if canonicalTP == canonicalExpected {
				targetFound = true
				break
			}
		}
		if !targetFound {
			result.State = "drifted"
			result.Message = fmt.Sprintf("target path mismatch: expected %q not found in manifest target paths", expectedTarget)
			result.Drifted = true
			return result, nil
		}
	}

	// Verify the target file exists, has ownership marker, and digest matches.
	allPresent := true
	for _, tp := range manifest.TargetPaths {
		if _, err := os.Stat(tp); err != nil {
			allPresent = false
			continue
		}
		if harnessName == harness.Claude {
			// Structural ownership check for Claude settings.json —
			// JSON cannot carry a first-line comment marker.
			munsuBin, resolveErr := ResolveMunsuPathString()
			if resolveErr != nil {
				allPresent = false
				continue
			}
			present, _, hookErr := ClaudeSettingsHasOwnedHooks(tp, munsuBin)
			if hookErr != nil || !present {
				allPresent = false
				continue
			}
		} else if harnessName == harness.Codex {
			// Structural ownership check for Codex hooks.json —
			// JSON cannot carry a first-line comment marker.
			munsuBin, resolveErr := ResolveMunsuPathString()
			if resolveErr != nil {
				allPresent = false
				continue
			}
			present, _, hookErr := CodexHooksHasOwnedHooks(tp, munsuBin)
			if hookErr != nil || !present {
				allPresent = false
				continue
			}
		} else if harnessName == harness.Grok {
			// Structural ownership check for Grok hook files.
			munsuBin, resolveErr := ResolveMunsuPathString()
			if resolveErr != nil {
				allPresent = false
				continue
			}
			hooksDir := filepath.Dir(tp)
			present, _, hookErr := GrokHooksHasOwnedHooks(hooksDir, munsuBin)
			if hookErr != nil || !present {
				allPresent = false
				continue
			}
		} else if harnessName == harness.Agy {
			// Structural ownership check for agy hooks.json —
			// JSON cannot carry a first-line comment marker, so
			// check for munsu-owned hook names with correct commands.
			munsuBin, resolveErr := ResolveMunsuPathString()
			if resolveErr != nil {
				allPresent = false
				continue
			}
			hooksDir := filepath.Dir(tp)
			present, _, hookErr := AgyHooksHasOwnedHooks(hooksDir, munsuBin)
			if hookErr != nil || !present {
				allPresent = false
				continue
			}
		} else if harnessName == harness.Opencode {
			// Structural ownership check for OpenCode plugin files.
			// JS files cannot carry a reliable first-line marker, so
			// check content for the munsu binary path and expected
			// export function names.
			munsuBin, resolveErr := ResolveMunsuPathString()
			if resolveErr != nil {
				allPresent = false
				continue
			}
			pluginsDir := filepath.Dir(tp)
			present, _, hookErr := OpencodePluginsHasOwnedHooks(pluginsDir, munsuBin)
			if hookErr != nil || !present {
				allPresent = false
				continue
			}
		} else if !FileContainsOwnershipMarker(tp) {
			allPresent = false
			continue
		}

		// Verify content digest if manifest has one.
		if manifest.ContentDigest != "" {
			currentData, readErr := os.ReadFile(tp)
			if readErr != nil {
				allPresent = false
				continue
			}
			sum := sha256.Sum256(currentData)
			currentDigest := hex.EncodeToString(sum[:])
			if currentDigest != manifest.ContentDigest {
				allPresent = false
			}
		}
	}

	result.Version = manifest.Version
	result.InstalledAt = manifest.InstalledAt

	if allPresent {
		result.State = "installed"
		result.Message = "integration is healthy"
		result.Drifted = false
	} else {
		result.State = "drifted"
		result.Drifted = true
		result.Message = "one or more integration artifacts are missing or modified"
	}

	return result, nil
}

// bytesReader returns a reader for a byte slice.
func reader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// WriteManifestBackup writes the manifest to a backup path.
func WriteManifestBackup(homeDir, harnessName string, scope Scope, manifest Manifest, cwd ...string) (string, error) {
	artifactDir := homePathForScope(homeDir, harnessName, scope, cwd...)
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	backupPath := filepath.Join(artifactDir, "manifest."+time.Now().Format("20060102-150405")+".json")
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal backup: %w", err)
	}
	if err := writeAtomic(backupPath, string(data), 0644); err != nil {
		return "", fmt.Errorf("write backup: %w", err)
	}
	return backupPath, nil
}
