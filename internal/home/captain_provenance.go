package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const CaptainProvenanceMarkerName = ".munsu-captain-home"
const CaptainProvenanceVersion = "munsu-v2"

// CaptainProvenanceName is the provenance metadata file of a managed
// worktree captain home. Either it or the marker makes a home a captain home.
const CaptainProvenanceName = ".captain-provenance"

// ErrCaptainMarkerUnusable marks a provenance marker that is missing or cannot
// name a captain (malformed, wrong version, blank id). A marker that names
// another home is not unusable in this sense: that is the copy/move check.
var ErrCaptainMarkerUnusable = errors.New("captain provenance marker is missing or malformed")

// ErrCaptainHomeMismatch marks a well-formed marker that names another home
// (a copied or moved captain home). ValidateCaptainProvenance returns the
// marker's id alongside it.
var ErrCaptainHomeMismatch = errors.New("captain provenance marker names another home")

func CanonicalCaptainHome(homePath string) (string, error) {
	canonical, err := filepath.EvalSymlinks(homePath)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks: %w", err)
	}
	absolute, err := filepath.Abs(canonical)
	if err != nil {
		return "", fmt.Errorf("resolving absolute path: %w", err)
	}
	return absolute, nil
}

func SeedCaptainProvenance(homePath, id string) error {
	canonical, err := CanonicalCaptainHome(homePath)
	if err != nil {
		return fmt.Errorf("cannot determine canonical home for %s: %w", homePath, err)
	}
	content := fmt.Sprintf("%s\n%s\n%s\n", CaptainProvenanceVersion, id, canonical)
	return AtomicWrite(filepath.Join(homePath, CaptainProvenanceMarkerName), []byte(content), 0600)
}

// ValidateCaptainProvenance returns the captain id the marker in homePath
// names. A refusal that wraps ErrCaptainMarkerUnusable names the seed command
// that rewrites the marker.
func ValidateCaptainProvenance(homePath string) (string, error) {
	id, err := validateCaptainProvenance(homePath)
	if errors.Is(err, ErrCaptainMarkerUnusable) {
		err = fmt.Errorf("%w; fix: run 'munsu captain seed <id> <home> --repo <repo>'", err)
	}
	return id, err
}

func validateCaptainProvenance(homePath string) (string, error) {
	markerPath := filepath.Join(homePath, CaptainProvenanceMarkerName)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("captain home %s has no %s marker: %w", homePath, CaptainProvenanceMarkerName, ErrCaptainMarkerUnusable)
		}
		return "", fmt.Errorf("reading provenance marker %s: %w", markerPath, err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 4)
	if len(lines) < 3 {
		return "", fmt.Errorf("provenance marker %s is malformed: expected exactly 3 lines (version, id, canonical-home), got %d: %w", markerPath, len(lines), ErrCaptainMarkerUnusable)
	}
	if len(lines) > 3 {
		return "", fmt.Errorf("provenance marker %s has extra content — expected exactly 3 lines: %w", markerPath, ErrCaptainMarkerUnusable)
	}
	if strings.TrimSpace(lines[0]) != CaptainProvenanceVersion {
		return "", fmt.Errorf("provenance marker %s has unsupported version %q (expected %q): %w", markerPath, lines[0], CaptainProvenanceVersion, ErrCaptainMarkerUnusable)
	}
	id := strings.TrimSpace(lines[1])
	if id == "" {
		return "", fmt.Errorf("provenance marker %s has empty id: %w", markerPath, ErrCaptainMarkerUnusable)
	}
	stored := strings.TrimSpace(lines[2])
	actual, err := CanonicalCaptainHome(homePath)
	if err != nil {
		return "", fmt.Errorf("cannot verify canonical home for copied/move check: %w", err)
	}
	if actual != stored {
		return id, fmt.Errorf("provenance marker home %s does not match actual canonical home %s — captain may have been copied/moved: %w", stored, actual, ErrCaptainHomeMismatch)
	}
	return id, nil
}
