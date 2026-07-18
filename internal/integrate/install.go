package integrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
			artifactDir := homePathForScope(homeDir, harnessName, scope)
			if err := os.MkdirAll(artifactDir, 0755); err != nil {
				return nil, fmt.Errorf("create artifact dir: %w", err)
			}

			manifestPath := ManifestPath(homeDir, harnessName, scope)
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
	manifestPath := ManifestPath(homeDir, harnessName, scope)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		result.State = "absent"
		result.Message = "no integration manifest found — not installed"
		return result, nil
	}

	// Decode with DisallowUnknownFields to reject unknown JSON fields.
	var manifest Manifest
	dec := json.NewDecoder(bytesReader(manifestData))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		result.State = "drifted"
		result.Message = "manifest corrupted or contains unknown fields: " + err.Error()
		result.Drifted = true
		return result, nil
	}

	// Strict validation against expected values.
	expectedCaps := caps
	if err := ValidateStrict(manifest, harnessName, string(scope), "1.0.0", expectedCaps, len(manifest.TargetPaths)); err != nil {
		result.State = "drifted"
		result.Message = fmt.Sprintf("manifest validation: %v", err)
		result.Drifted = true
		return result, nil
	}

	// Verify all target paths exist, have ownership markers, and digest matches.
	allPresent := true
	for _, tp := range manifest.TargetPaths {
		if _, err := os.Stat(tp); err != nil {
			allPresent = false
			continue
		}
		if !FileContainsOwnershipMarker(tp) {
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
func bytesReader(b []byte) *bytesReaderT { return &bytesReaderT{b: b} }

type bytesReaderT struct{ b []byte; off int }

func (r *bytesReaderT) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, fmt.Errorf("EOF")
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

// WriteManifestBackup writes the manifest to a backup path.
func WriteManifestBackup(homeDir, harnessName string, scope Scope, manifest Manifest) (string, error) {
	artifactDir := homePathForScope(homeDir, harnessName, scope)
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
