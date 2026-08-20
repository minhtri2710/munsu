// Issue #546 Slice 6 — topology policy hardening. The Fleet boundary names an
// explicit dispatch policy for every soldier dispatch before any supervision
// read, config resolution, or mutation:
//
//   - DispatchPolicyGeneralDirect (General → Soldier): the operation home is a
//     General-owned home and the config surface is the fleet base document
//     (or the legacy flat path when no typed documents exist). The parent
//     identity sent to the soldier stays the "general" sentinel.
//   - DispatchPolicyCaptainMediated (Captain → Soldier): the operation home is
//     the Captain's own provenanced home (.munsu-captain-home) and the config
//     surface is the Captain's assigned published snapshot. A Captain never
//     re-resolves, never reads the General's files, and never falls back to
//     local/base configuration (ADR-0008 §6).
//
// ResolveDispatchPolicy is the single authority for the choice. A parent rank,
// home provenance, or config surface that contradicts the other two is an
// ambiguous combination and fails closed with a typed DispatchPolicyError that
// names the rejected matrix row. In particular a General dispatch is refused
// at the boundary the moment the operation home is Captain-owned, so General
// code never reads or mutates Captain-owned state (ADR-0008 §4).
package fleet

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// DispatchPolicy is the explicit Fleet-boundary topology choice for one
// soldier dispatch operation.
type DispatchPolicy string

const (
	// DispatchPolicyGeneralDirect dispatches the soldier directly from a
	// General-owned home. The General is the parent.
	DispatchPolicyGeneralDirect DispatchPolicy = "general-direct"

	// DispatchPolicyCaptainMediated dispatches the soldier through a Captain
	// running inside its own provenanced home. The Captain is the parent.
	DispatchPolicyCaptainMediated DispatchPolicy = "captain-mediated"
)

// DispatchPolicyProblem names the policy-matrix row that rejected a
// parent/home/config combination at the Fleet boundary.
type DispatchPolicyProblem string

const (
	// DispatchPolicyProblemGeneralOnCaptainHome: parent rank general, but the
	// operation home carries Captain provenance. The General never dispatches
	// inside a Captain-owned home (ADR-0008 §4).
	DispatchPolicyProblemGeneralOnCaptainHome DispatchPolicyProblem = "general-parent-on-captain-home"

	// DispatchPolicyProblemCaptainOnUnprovenHome: parent rank captain, but the
	// operation home has no Captain provenance marker. A Captain dispatches
	// only from its own proven home.
	DispatchPolicyProblemCaptainOnUnprovenHome DispatchPolicyProblem = "captain-parent-on-unproven-home"

	// DispatchPolicyProblemCaptainWithoutAssignedSnapshot: parent rank captain
	// in a proven home that carries no published config snapshot. A Captain
	// never re-resolves or spawns without its assigned snapshot (ADR-0008 §6).
	DispatchPolicyProblemCaptainWithoutAssignedSnapshot DispatchPolicyProblem = "captain-without-assigned-snapshot"

	// DispatchPolicyProblemConfigSurfaceContradiction: the home's config
	// surface contradicts the resolved home provenance and parent rank — a
	// captain assignment surface (published snapshot) in a General home, or a
	// fleet base document inside a Captain home.
	DispatchPolicyProblemConfigSurfaceContradiction DispatchPolicyProblem = "config-surface-contradiction"

	// DispatchPolicyProblemUnknownParentRank: the parent rank is not general
	// or captain. authorizeSpawn refuses unknown roles before the boundary;
	// this row backstops direct callers.
	DispatchPolicyProblemUnknownParentRank DispatchPolicyProblem = "unknown-parent-rank"
)

// DispatchPolicyError is the typed fail-closed outcome of ResolveDispatchPolicy.
// It names the rejected policy-matrix row and the exact parent/home/config
// evidence, so a refusal is never an ambiguous error string.
type DispatchPolicyError struct {
	Problem DispatchPolicyProblem
	Parent  string
	HomeDir string
	Detail  string
}

func (e *DispatchPolicyError) Error() string {
	return fmt.Sprintf("dispatch policy: %s (parent=%q home=%q): %s",
		e.Problem, e.Parent, e.HomeDir, e.Detail)
}

func policyError(problem DispatchPolicyProblem, parent, homeDir, detail string) error {
	return &DispatchPolicyError{Problem: problem, Parent: parent, HomeDir: homeDir, Detail: detail}
}

// generalOnCaptainHomeError builds the typed fail-closed refusal for a General
// (or unranked) parent aimed at a Captain-owned operation home. It is the
// single construction point for the row so the Runner's early boundary and
// ResolveDispatchPolicy cannot drift on message or evidence.
func generalOnCaptainHomeError(parent, captainID, homeDir string) error {
	return policyError(DispatchPolicyProblemGeneralOnCaptainHome, parent, homeDir,
		fmt.Sprintf("the operation home is Captain-owned (captain %s, .munsu-captain-home); the General never reads or mutates Captain-owned state — run the dispatch from the General home or as the Captain (ADR-0008 §4)", captainID))
}

// ResolveDispatchPolicy resolves the explicit dispatch policy for one
// operation home and parent rank. parentRank is the resolved parent role
// evidence ("general", "captain", or "" = general, matching authorizeSpawn).
// It returns the policy and the parent identity dispatched under ("general"
// sentinel for GeneralDirect, the provenance captain ID for CaptainMediated).
//
// Policy matrix (rows fall closed on contradiction):
//
//	parent   | home            | config surface        | policy
//	---------+-----------------+-----------------------+-------------------
//	general  | general home    | fleet base/legacy     | general-direct
//	general  | general home    | published snapshot    | refuse (contradiction)
//	general  | captain home    | any                   | refuse (captain-owned)
//	captain  | captain home    | published snapshot    | captain-mediated
//	captain  | captain home    | fleet base document   | refuse (contradiction)
//	captain  | captain home    | no snapshot           | refuse (no assignment)
//	captain  | general home    | any                   | refuse (unproven)
func ResolveDispatchPolicy(homeDir, parentRank string) (DispatchPolicy, string, error) {
	captainID := ""
	provenanced := false
	markerPath := filepath.Join(homeDir, home.CaptainProvenanceMarkerName)
	switch _, err := os.Stat(markerPath); {
	case err == nil:
		id, verr := home.ValidateCaptainProvenance(homeDir)
		if verr != nil {
			return "", "", policyError(DispatchPolicyProblemCaptainOnUnprovenHome, parentRank, homeDir,
				fmt.Sprintf("captain provenance marker is malformed or unreadable: %v", verr))
		}
		captainID, provenanced = id, true
	case os.IsNotExist(err):
		// General home — no provenance.
	default:
		return "", "", policyError(DispatchPolicyProblemCaptainOnUnprovenHome, parentRank, homeDir,
			fmt.Sprintf("reading captain provenance marker: %v", err))
	}

	// Config surface evidence in the operation home.
	published := config.PublishedSnapshotAvailable(homeDir)
	basePresent := false
	if _, err := os.Stat(filepath.Join(homeDir, config.BaseDocumentPath)); err == nil {
		basePresent = true
	}

	switch parentRank {
	case "captain":
		if !provenanced {
			return "", "", policyError(DispatchPolicyProblemCaptainOnUnprovenHome, parentRank, homeDir,
				"a Captain dispatches only from its own home; the operation home has no .munsu-captain-home provenance marker")
		}
		if basePresent {
			return "", "", policyError(DispatchPolicyProblemConfigSurfaceContradiction, parentRank, homeDir,
				"the Captain home carries a fleet base document; a Captain never reads or resolves base/local configuration (ADR-0008 §6)")
		}
		if !published {
			return "", "", policyError(DispatchPolicyProblemCaptainWithoutAssignedSnapshot, parentRank, homeDir,
				"the proven Captain home has no assigned published config snapshot; run 'munsu captain config-push' from the General before dispatching")
		}
		return DispatchPolicyCaptainMediated, captainID, nil

	case "general", "":
		if provenanced {
			return "", "", generalOnCaptainHomeError(parentRank, captainID, homeDir)
		}
		if published {
			return "", "", policyError(DispatchPolicyProblemConfigSurfaceContradiction, parentRank, homeDir,
				"a Captain assignment surface (published config snapshot) is present in a General home; the General never consumes the Captain assignment surface")
		}
		return DispatchPolicyGeneralDirect, "general", nil

	default:
		return "", "", policyError(DispatchPolicyProblemUnknownParentRank, parentRank, homeDir,
			fmt.Sprintf("unknown parent rank %q; expected general or captain", parentRank))
	}
}
