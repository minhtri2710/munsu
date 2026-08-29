package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const CaptainProvenanceMarkerName = ".munsu-captain-home"
const CaptainProvenanceVersion = "munsu-v2"

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
	return os.WriteFile(filepath.Join(homePath, CaptainProvenanceMarkerName), []byte(content), 0600)
}

func ValidateCaptainProvenance(homePath string) (string, error) {
	markerPath := filepath.Join(homePath, CaptainProvenanceMarkerName)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("captain home %s has no %s marker — run 'munsu captain seed' or 'munsu captain migrate'", homePath, CaptainProvenanceMarkerName)
		}
		return "", fmt.Errorf("reading provenance marker %s: %w", markerPath, err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 4)
	if len(lines) < 3 {
		return "", fmt.Errorf("provenance marker %s is malformed: expected exactly 3 lines (version, id, canonical-home), got %d", markerPath, len(lines))
	}
	if len(lines) > 3 {
		return "", fmt.Errorf("provenance marker %s has extra content — expected exactly 3 lines", markerPath)
	}
	if strings.TrimSpace(lines[0]) != CaptainProvenanceVersion {
		return "", fmt.Errorf("provenance marker %s has unsupported version %q (expected %q)", markerPath, lines[0], CaptainProvenanceVersion)
	}
	id := strings.TrimSpace(lines[1])
	if id == "" {
		return "", fmt.Errorf("provenance marker %s has empty id", markerPath)
	}
	stored := strings.TrimSpace(lines[2])
	actual, err := CanonicalCaptainHome(homePath)
	if err != nil {
		return "", fmt.Errorf("cannot verify canonical home for copied/move check: %w", err)
	}
	if actual != stored {
		return "", fmt.Errorf("provenance marker home %s does not match actual canonical home %s — captain may have been copied/moved", stored, actual)
	}
	return id, nil
}
