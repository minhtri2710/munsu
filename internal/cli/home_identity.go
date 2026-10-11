package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
)

// homeIdentity is the one owner of "who is this home" for a CLI sender or task
// owner. A home already known to be a captain home (its provenance marker or its
// .captain-provenance file exists) takes its captain id from the validated
// marker, and a missing or malformed marker refuses instead of falling back to
// the directory name. Any other home is the General's, which has no marker by
// design: home.ReadHomeIdentity names it.
func homeIdentity(homeDir string) (string, home.Rank, error) {
	if !isCaptainHome(homeDir) {
		return home.ReadHomeIdentity(homeDir)
	}
	id, err := captainHomeID(homeDir)
	return id, home.RankCaptain, err
}

func isCaptainHome(homeDir string) bool {
	for _, name := range []string{home.CaptainProvenanceMarkerName, fleet.CaptainProvenanceName} {
		if _, err := os.Lstat(filepath.Join(homeDir, name)); err == nil {
			return true
		}
	}
	return false
}

// captainHomeID is a captain home's id from its provenance marker. A copied home
// (a well-formed marker naming another home) keeps the marker's id.
func captainHomeID(homeDir string) (string, error) {
	id, err := home.ValidateCaptainProvenance(homeDir)
	if err == nil || errors.Is(err, home.ErrCaptainHomeMismatch) {
		return id, nil
	}
	if !errors.Is(err, home.ErrCaptainMarkerUnusable) {
		// The seed command only rewrites an unusable marker; it cannot fix a
		// marker that cannot be read.
		return "", fmt.Errorf("captain provenance marker %s cannot be read or verified: %w", filepath.Join(homeDir, home.CaptainProvenanceMarkerName), err)
	}
	if !strings.Contains(err.Error(), "munsu captain seed") {
		err = fmt.Errorf("%w; fix: run 'munsu captain seed <id> <home> --repo <repo>'", err)
	}
	return "", err
}
