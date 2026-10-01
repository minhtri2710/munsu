package bootstrap

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

// harnessIntegration is one harness's row in the integration table.
type harnessIntegration struct {
	label   string // artifact noun in messages, e.g. "claude settings"
	files   int    // number of target files the manifest records
	install func(scope Scope, cwd string, dryRun bool) (targets []string, written bool, digest string, err error)
	targets func(scope Scope, cwd string) ([]string, error)
	// owned reports whether an installed target still carries munsu's
	// ownership: a first-line marker or, for JSON and JS files that cannot
	// carry one, a structural check of the munsu-owned hooks.
	owned func(target string) bool
	// combinedDigest marks a manifest digest taken over all target files
	// together. Status cannot check it file by file and relies on owned,
	// which already verifies every file.
	combinedDigest bool
	// regeneratedDigest, when set, is the digest Status expects instead of
	// the stored one. Only a wholly munsu-owned file sets it: the stored
	// digest only proves the file still matches what the *installing* binary
	// produced, and an installation that predates a change to the generated
	// source would match it forever and never be repaired. Files merged with
	// user-owned hooks keep the stored digest, since a freshly generated file
	// is legitimately not byte-identical; owned covers them.
	regeneratedDigest func(munsuBin string) string
}

// structuralOwner adapts a (path, munsuBin) ownership check to the Status loop.
func structuralOwner(check func(path, munsuBin string) (bool, string, error)) func(string) bool {
	return func(target string) bool {
		munsuBin, err := ResolveMunsuPathString()
		if err != nil {
			return false
		}
		present, _, err := check(target, munsuBin)
		return err == nil && present
	}
}

// inDir runs a directory-level ownership check on the target's directory.
func inDir(check func(dir, munsuBin string) (bool, string, error)) func(path, munsuBin string) (bool, string, error) {
	return func(path, munsuBin string) (bool, string, error) {
		return check(filepath.Dir(path), munsuBin)
	}
}

func singleTargetInstall(install func(scope Scope, cwd string, dryRun bool) (string, bool, string, error)) func(Scope, string, bool) ([]string, bool, string, error) {
	return func(scope Scope, cwd string, dryRun bool) ([]string, bool, string, error) {
		target, written, digest, err := install(scope, cwd, dryRun)
		if err != nil {
			return nil, false, "", err
		}
		return []string{target}, written, digest, nil
	}
}

func singleTargetPath(path func(scope Scope, cwd string) (string, error)) func(Scope, string) ([]string, error) {
	return func(scope Scope, cwd string) ([]string, error) {
		target, err := path(scope, cwd)
		if err != nil {
			return nil, err
		}
		return []string{target}, nil
	}
}

var harnessIntegrations = map[string]harnessIntegration{
	harness.Pi: {
		label: "pi extension",
		files: 1,
		install: singleTargetInstall(func(scope Scope, cwd string, dryRun bool) (string, bool, string, error) {
			return (&PiAdapter{Cwd: cwd, Scope: string(scope), DryRun: dryRun}).InstallPiExtension()
		}),
		targets:           singleTargetPath(ExpectedTargetPath),
		owned:             FileContainsOwnershipMarker,
		regeneratedDigest: PiExtensionContentDigest,
	},
	harness.Claude: {
		label:   "claude settings",
		files:   1,
		install: singleTargetInstall(claudeHooks.install),
		targets: singleTargetPath(claudeHooks.path),
		owned:   structuralOwner(claudeHooks.hasOwnedHooks),
	},
	harness.Codex: {
		label:   "codex hooks",
		files:   1,
		install: singleTargetInstall(codexHooks.install),
		targets: singleTargetPath(codexHooks.path),
		owned:   structuralOwner(codexHooks.hasOwnedHooks),
	},
	harness.Grok: {
		label: "grok hooks",
		files: len(grokHookFileNames),
		install: func(scope Scope, cwd string, dryRun bool) ([]string, bool, string, error) {
			return (&GrokAdapter{Cwd: cwd, Scope: string(scope), DryRun: dryRun}).InstallGrokHooks()
		},
		targets:        GrokHooksAllTargetPaths,
		owned:          structuralOwner(inDir(GrokHooksHasOwnedHooks)),
		combinedDigest: true,
	},
	harness.Opencode: {
		label: "opencode plugins",
		files: len(opencodePluginFileNames),
		install: func(scope Scope, cwd string, dryRun bool) ([]string, bool, string, error) {
			return (&OpencodeAdapter{Cwd: cwd, Scope: string(scope), DryRun: dryRun}).InstallOpencodePlugins()
		},
		targets:        OpencodePluginsAllTargetPaths,
		owned:          structuralOwner(inDir(OpencodePluginsHasOwnedHooks)),
		combinedDigest: true,
	},
	harness.Agy: {
		label: "agy hooks",
		files: 1,
		install: func(scope Scope, cwd string, dryRun bool) ([]string, bool, string, error) {
			return (&AgyAdapter{Cwd: cwd, Scope: string(scope), DryRun: dryRun}).InstallAgyHooks()
		},
		targets:        singleTargetPath(AgyHooksTargetPath),
		owned:          structuralOwner(inDir(AgyHooksHasOwnedHooks)),
		combinedDigest: true,
	},
}

// describeTargets renders the installed targets for an install message.
func describeTargets(targets []string) string {
	if len(targets) == 1 {
		return targets[0]
	}
	return fmt.Sprintf("%d files in %s", len(targets), filepath.Dir(targets[0]))
}

// newManifest builds the integration manifest recorded after an install.
func newManifest(harnessName string, scope Scope, caps []Capability, contentDigest string, targetPaths []string) Manifest {
	capStrs := make([]string, len(caps))
	for i, c := range caps {
		capStrs[i] = string(c)
	}
	return Manifest{
		SchemaVersion: "munsu.integrate/v1",
		Harness:       harnessName,
		Version:       "1.0.0",
		Scope:         string(scope),
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		TargetPaths:   targetPaths,
		Capabilities:  capStrs,
		ContentDigest: contentDigest,
	}
}

// Install installs integration artifacts for the given harness.
// Returns an IntegrationResult describing what was done.
//
// Semantics:
//   - Fresh install: creates all artifacts, writes manifest.
//   - Re-install (idempotent): identical content is a no-op; changed content
//     is a repair. A Pi extension without the ownership marker is a
//     conflict; a hooks JSON file that is not valid JSON is refused.
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

	row, ok := harnessIntegrations[harnessName]
	if !ok {
		return &IntegrationResult{
			Harness: harnessName,
			State:   "unsupported",
			Message: fmt.Sprintf("harness %q integration not yet implemented", harnessName),
		}, nil
	}

	result := &IntegrationResult{
		Harness: adapter.Name,
		Scope:   scope,
		State:   "installed",
	}

	targets, written, digest, err := row.install(scope, cwd, dryRun)
	if err != nil {
		return nil, fmt.Errorf("%s install: %w", row.label, err)
	}
	result.Message = fmt.Sprintf("%s: %s", row.label, describeTargets(targets))

	switch {
	case dryRun:
		result.State = "fresh"
		if len(targets) == 1 {
			result.Message = fmt.Sprintf("[dry-run] would write: %s", targets[0])
		} else {
			result.Message = fmt.Sprintf("[dry-run] would write: %d files to %s", len(targets), filepath.Dir(targets[0]))
		}
	case !written:
		result.State = "fresh"
		result.Message = "no changes needed"
	default:
		manifest := newManifest(harnessName, scope, caps, digest, targets)
		if err := os.MkdirAll(homePathForScope(homeDir, harnessName, scope, cwd), 0755); err != nil {
			return nil, fmt.Errorf("create artifact dir: %w", err)
		}
		manifestData, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal manifest: %w", err)
		}
		if err := writeAtomic(ManifestPath(homeDir, harnessName, scope, cwd), string(manifestData), 0644); err != nil {
			return nil, fmt.Errorf("write manifest: %w", err)
		}
		result.Version = manifest.Version
		result.InstalledAt = manifest.InstalledAt
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

	row, ok := harnessIntegrations[harnessName]
	if !ok {
		result.State = "unsupported"
		result.Message = fmt.Sprintf("harness %q integration not yet implemented", harnessName)
		return result, nil
	}
	expectedPaths, pathErr := row.targets(scope, cwd)

	// Strict validation against expected values.
	if err := ValidateStrict(manifest, harnessName, string(scope), "1.0.0", caps, row.files); err != nil {
		result.State = "drifted"
		result.Message = fmt.Sprintf("manifest validation: %v", err)
		result.Drifted = true
		return result, nil
	}

	// Verify that TargetPaths contains the expected canonical targets for this scope+cwd.
	if row.files > 1 {
		// For multi-file harnesses, every expected path must be present and exist.
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
		if pathErr != nil {
			result.State = "drifted"
			result.Message = fmt.Sprintf("cannot compute expected target: %v", pathErr)
			result.Drifted = true
			return result, nil
		}
		expectedTarget := expectedPaths[0]
		targetFound := false
		for _, tp := range manifest.TargetPaths {
			canonicalTP := canonicalizePossiblyMissingPath(tp)
			canonicalExpected := canonicalizePossiblyMissingPath(expectedTarget)
			if canonicalTP == canonicalExpected {
				targetFound = true
				break
			}
		}
		if !targetFound {
			result.State = "drifted"
			if harnessName == harness.Pi && len(manifest.TargetPaths) == 1 && canonicalizePossiblyMissingPath(manifest.TargetPaths[0]) == canonicalizePossiblyMissingPath(expectedTarget) {
				result.Message = fmt.Sprintf("canonical Pi integration is missing at %s; repair with: munsu integrate repair --harness pi --scope %s", expectedTarget, scope)
			} else {
				result.Message = fmt.Sprintf("target path mismatch: expected %q not found in manifest target paths", expectedTarget)
			}
			result.Drifted = true
			return result, nil
		}
	}

	// Verify each target exists, is still munsu-owned, and matches its digest.
	allPresent := true
	for _, tp := range manifest.TargetPaths {
		if _, err := os.Stat(tp); err != nil {
			allPresent = false
			continue
		}
		if !row.owned(tp) {
			allPresent = false
			continue
		}
		if manifest.ContentDigest == "" || row.combinedDigest {
			continue
		}
		currentData, readErr := os.ReadFile(tp)
		if readErr != nil {
			allPresent = false
			continue
		}
		sum := sha256.Sum256(currentData)
		currentDigest := hex.EncodeToString(sum[:])
		expectedDigest := manifest.ContentDigest
		if row.regeneratedDigest != nil {
			munsuBin, resolveErr := ResolveMunsuPathString()
			if resolveErr != nil {
				allPresent = false
				continue
			}
			expectedDigest = row.regeneratedDigest(munsuBin)
		}
		if currentDigest != expectedDigest {
			allPresent = false
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
		if harnessName == harness.Pi && len(manifest.TargetPaths) == 1 {
			if _, statErr := os.Stat(manifest.TargetPaths[0]); os.IsNotExist(statErr) {
				result.Message = fmt.Sprintf("canonical Pi integration is missing at %s; repair with: munsu integrate repair --harness pi --scope %s", manifest.TargetPaths[0], scope)
			} else {
				result.Message = fmt.Sprintf("canonical Pi integration digest is invalid at %s; repair with: munsu integrate repair --harness pi --scope %s", manifest.TargetPaths[0], scope)
			}
		} else {
			result.Message = "one or more integration artifacts are missing or modified"
		}
	}

	return result, nil
}

func canonicalizePossiblyMissingPath(path string) string {
	parent := filepath.Dir(path)
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		parent = resolved
	}
	return filepath.Join(filepath.Clean(parent), filepath.Base(path))
}

// bytesReader returns a reader for a byte slice.
func reader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// ProjectScopeInstallPaths returns the cwd-relative, slash-separated paths
// that Install(cwd, cwd, harnessName, ScopeProject, false) writes: the
// harness integration targets plus the munsu manifest. An unsupported
// harness writes nothing and returns no paths.
func ProjectScopeInstallPaths(cwd, harnessName string) ([]string, error) {
	canonical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolving project cwd %s: %w", cwd, err)
	}
	row, ok := harnessIntegrations[harnessName]
	if !ok {
		return nil, nil
	}
	targets, err := row.targets(ScopeProject, canonical)
	if err != nil {
		return nil, err
	}
	targets = append(targets, ManifestPath(canonical, harnessName, ScopeProject, canonical))
	rels := make([]string, 0, len(targets))
	for _, target := range targets {
		rel, err := filepath.Rel(canonical, target)
		if err != nil {
			return nil, err
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	return rels, nil
}
