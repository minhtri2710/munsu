package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
	tauth "github.com/minhtri2710/munsu/internal/taskauthority"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

const taskHandoffDirName = ".task-handoff"

// Handoff transfers queued tasks from a parent home to a captain home by
// composing the canonical Task Authority transfer primitives (ADR-0008 §3):
// the source task generation is fenced with ReserveTransfer, the destination
// re-creates and activates the generation with ReceiveTransfer/ActivateTransfer
// from the typed Task Definition, and the source is superseded with
// CommitTransfer only after the destination activation is durable. The
// Dispatch-Hold gate is applied to both homes before any staging. This is the
// underlying primitive composition #413 builds its journaled Task Transfer
// on; the cross-home journal/orchestration/ordering/retries is #413's work,
// not implemented here. Post-transfer projection copies (backlog/.meta/.status/
// brief) run after the authoritative transfer; a projection failure returns a
// typed partial result and never rolls back or re-transfers ownership.
func Handoff(parentHome, captainHome string, itemKeys []string) error {
	return durableTaskHandoff(parentHome, captainHome, itemKeys)
}

// HandoffPartialError is the typed outcome of a handoff whose authoritative
// ownership transfer committed (destination received/activated, source
// superseded) but whose post-transfer projection copies could not complete.
// Ownership truth is never rolled back and never re-transferred; the
// destination can reconcile its projections from canonical state.
type HandoffPartialError struct {
	Transferred   []string
	ProjectionErr error
}

func (e *HandoffPartialError) Error() string {
	return fmt.Sprintf("handoff transferred %s but projection copies failed: %v", strings.Join(e.Transferred, ", "), e.ProjectionErr)
}

func (e *HandoffPartialError) Unwrap() error { return e.ProjectionErr }

// RecoverTaskHandoffs is retained for the pre-read public task-safety gates in
// brief/spawn/recovery. The Stage 1A handoff composes the canonical transfer
// primitives directly with no durable journal, so there are no incomplete
// handoff journals to recover; #413 owns the journaled Task Transfer with real
// recovery. This is a no-op.
func RecoverTaskHandoffs(homeDir string) error {
	_ = homeDir
	return nil
}

func durableTaskHandoff(parentHome, captainHome string, itemKeys []string) error {
	source, err := canonicalHandoffHome(parentHome)
	if err != nil {
		return err
	}
	destination, err := canonicalHandoffHome(captainHome)
	if err != nil {
		return err
	}
	if source == destination {
		return fmt.Errorf("refusing handoff: destination is parent home itself")
	}
	captainID, err := ValidateProvenance(destination)
	if err != nil {
		return fmt.Errorf("refusing handoff to unmarked home %s: %w", destination, err)
	}
	if !isTasksAxiBackend(source) {
		return fmt.Errorf("backlog backend is not set to tasks-axi — handoff requires tasks-axi")
	}

	// Supervision gate: handoff fails closed when the watcher lease of either
	// home is degraded (Task 4.3, ADR-0007 §8). Durable Dispatch Holds are
	// evaluated by the fleet holds gate below.
	if err := CheckSupervisionForDispatch(source, mhome.DispatchActionHandoff); err != nil {
		return err
	}
	if err := CheckSupervisionForDispatch(destination, mhome.DispatchActionHandoff); err != nil {
		return err
	}

	sourceAuth, err := handoffCanonical(source)
	if err != nil {
		return err
	}
	destinationAuth, err := handoffCanonical(destination)
	if err != nil {
		return err
	}

	// Fleet-owned holds gate: every applicable durable Dispatch Hold on either
	// home blocks the transfer before any staging (the matching rule lives in
	// tauth.DispatchHold.Matches; only the orchestration stays here).
	if err := checkHandoffHolds(sourceAuth, "", "", "", ""); err != nil {
		return err
	}
	if err := checkHandoffHolds(destinationAuth, "", "", "", ""); err != nil {
		return err
	}

	path, err := captainLookPath("tasks-axi")
	if err != nil {
		return fmt.Errorf("tasks-axi not found: %w", err)
	}
	sourceBacklog := filepath.Join(source, "data", "backlog.md")
	destinationBacklog := filepath.Join(destination, "data", "backlog.md")
	keys, err := resolveHandoffKeys(source, sourceBacklog, path, itemKeys)
	if err != nil {
		return err
	}

	// Destination projection absence preflight: a destination that already
	// has any of the task's projections (backlog-free .meta/.status/brief)
	// quarantines the transfer before any authoritative mutation, so a stale
	// destination projection never overwrites or is overwritten.
	for _, taskID := range keys {
		for _, rel := range taskHandoffProjectionRelPaths(taskID) {
			if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(rel))); err == nil {
				return fmt.Errorf("handoff: destination projection already exists: %s", rel)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}

	// Authoritative transfer: per task, fence the source, receive and activate
	// at the destination, then supersede the source only after the destination
	// activation is durable. A failure at any step fails closed (fenced source
	// generations are recovered by #413's journal).
	for _, taskID := range keys {
		tid, err := domain.NewTaskID(taskID)
		if err != nil {
			return err
		}
		agg, err := sourceAuth.Get(tid)
		if err != nil {
			return fmt.Errorf("handoff: reading source task %s: %w", taskID, err)
		}
		project := agg.Definition.Project
		parentID := agg.Definition.ParentTaskID
		generation := agg.Generation.String()
		if err := checkHandoffHolds(sourceAuth, taskID, project, generation, parentID); err != nil {
			return err
		}
		if err := checkHandoffHolds(destinationAuth, taskID, project, generation, parentID); err != nil {
			return err
		}
		if parentID != "" {
			if err := checkHandoffHolds(sourceAuth, parentID, project, "", parentID); err != nil {
				return err
			}
		}
		// A destination that already owns the task fails closed with a typed
		// conflict and never overwrites destination truth (ADR-0007 §10); the
		// receive operation re-fences the same rule inside its transaction.
		if _, err := destinationAuth.Get(tid); err == nil {
			return handoffDestinationConflict(taskID, agg.Generation)
		} else if !errors.Is(err, tauth.ErrNotFound) {
			return fmt.Errorf("handoff: reading destination ownership for %s: %w", taskID, err)
		}

		reservationID := fmt.Sprintf("handoff-%s-%s", taskID, agg.Generation)
		fenceToken := reservationID + "-fence"

		// 1. Fence the source generation for transfer.
		reserveReq := tauth.CanonicalReserveTransferRequest{
			HomeID:        sourceAuth.HomeID(),
			TaskID:        tid,
			Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
			ReservationID: reservationID,
			Destination:   destinationAuth.HomeID(),
			FenceToken:    fenceToken,
			Reason:        "handoff to " + destinationAuth.HomeID().Value(),
		}
		if _, err := sourceAuth.ReserveTransfer(mustHandoffOperation("handoff-reserve-"+taskID+"-"+generation, reserveReq), reserveReq); err != nil {
			return fmt.Errorf("handoff: reserving source task %s: %w", taskID, err)
		}

		// 2. Re-create the generation at the destination from the typed Task
		// Definition (never from a raw source document), owner bound to the
		// captain.
		definition := tauth.TaskDefinition{
			Owner:        "captain:" + captainID,
			Description:  agg.Definition.Description,
			Kind:         agg.Definition.Kind,
			Project:      agg.Definition.Project,
			ParentTaskID: agg.Definition.ParentTaskID,
		}
		receiveReq := tauth.CanonicalReceiveTransferRequest{
			HomeID:           destinationAuth.HomeID(),
			TaskID:           tid,
			ReservationID:    reservationID,
			SourceHome:       sourceAuth.HomeID(),
			SourceGeneration: agg.Generation,
			Definition:       definition,
			Reason:           "handoff receive",
		}
		if _, err := destinationAuth.ReceiveTransfer(mustHandoffOperation("handoff-receive-"+taskID+"-"+generation, receiveReq), receiveReq); err != nil {
			return fmt.Errorf("handoff: receiving task %s at destination: %w", taskID, err)
		}

		// 3. Activate the received generation as the destination's current
		// owned generation.
		activateReq := tauth.CanonicalActivateTransferRequest{
			HomeID:        destinationAuth.HomeID(),
			TaskID:        tid,
			Precondition:  domain.Of(1, 1),
			ReservationID: reservationID,
			Reason:        "handoff activate",
		}
		activateOp := mustHandoffOperation("handoff-activate-"+taskID+"-"+generation, activateReq)
		destOut, err := destinationAuth.ActivateTransfer(activateOp, activateReq)
		if err != nil {
			return fmt.Errorf("handoff: activating task %s at destination: %w", taskID, err)
		}

		// 4. Supersede the source generation, bound to the destination
		// activation evidence (reservation, task, homes, generation, activation
		// operation/digest).
		evidence := tauth.TransferActivationInfo{
			ReservationID:         reservationID,
			TaskID:                taskID,
			SourceHome:            sourceAuth.HomeID().Value(),
			SourceGeneration:      agg.Generation,
			DestinationHome:       destinationAuth.HomeID().Value(),
			DestinationGeneration: destOut.Generation,
			ActivationOperationID: activateOp.ID.Value(),
			ActivationDigest:      activateOp.Digest,
		}
		commitReq := tauth.CanonicalCommitTransferRequest{
			HomeID:        sourceAuth.HomeID(),
			TaskID:        tid,
			Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)+1),
			ReservationID: reservationID,
			FenceToken:    fenceToken,
			Evidence:      evidence,
			Reason:        "handoff commit",
		}
		if _, err := sourceAuth.CommitTransfer(mustHandoffOperation("handoff-commit-"+taskID+"-"+generation, commitReq), commitReq); err != nil {
			return fmt.Errorf("handoff: superseding source task %s: %w", taskID, err)
		}
	}

	// Move the backlog entries to the destination.
	args := append([]string{"mv"}, keys...)
	args = append(args, "--to", destinationBacklog, "--file", sourceBacklog)
	if err := execCommand(path, args...).Run(); err != nil {
		return fmt.Errorf("tasks-axi mv failed during handoff: %w", err)
	}

	// Post-transfer projection copies: any failure here returns a typed
	// partial result; the authoritative transfer above is never rolled back or
	// re-transferred.
	var projectionErr error
	for _, taskID := range keys {
		for _, rel := range taskHandoffProjectionRelPaths(taskID) {
			src := filepath.Join(source, filepath.FromSlash(rel))
			dst := filepath.Join(destination, filepath.FromSlash(rel))
			data, err := os.ReadFile(src)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				projectionErr = err
				break
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
				projectionErr = err
				break
			}
			if err := os.WriteFile(dst, data, 0600); err != nil {
				projectionErr = err
				break
			}
			if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
				projectionErr = err
				break
			}
		}
		if projectionErr != nil {
			break
		}
	}
	if projectionErr != nil {
		return &HandoffPartialError{Transferred: keys, ProjectionErr: projectionErr}
	}
	for _, task := range keys {
		fmt.Printf("handed-off %s\n", task)
	}
	return nil
}

// handoffCanonical constructs the canonical Task Authority over one home. The
// home must be an initialized canonical v1 home; construction fails closed on
// a non-canonical home.
func handoffCanonical(homeDir string) (*tauth.Canonical, error) {
	h, err := mhome.Open(homeDir)
	if err != nil {
		return nil, err
	}
	return tauth.NewCanonical(h)
}

// mustHandoffOperation builds a validated Operation from a typed Operation ID
// and intent, deriving the digest from the typed intent. The identical
// operation is reused across retries so the canonical primitives replay
// idempotently.
func mustHandoffOperation(id string, intent domain.Intent) domain.Operation {
	opID, err := domain.NewOperationID(id)
	if err != nil {
		panic(fmt.Sprintf("handoff: invalid operation id %q: %v", id, err))
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		panic(fmt.Sprintf("handoff: invalid operation for %q: %v", id, err))
	}
	return op
}

// checkHandoffHolds is the fleet-owned dispatch-hold gate for the handoff
// action: it lists the home's durable holds through the canonical Authority
// and matches them against the task/project/generation/parent. The matching
// rule lives in tauth.DispatchHold.Matches; only the orchestration
// stays in fleet.
func checkHandoffHolds(auth *tauth.Canonical, taskID, project, generation, parentID string) error {
	holds, err := auth.ListHolds()
	if err != nil {
		return err
	}
	for _, hold := range holds {
		if hold.Matches(tauth.DispatchActionHandoff, taskID, project, generation, parentID) {
			return domain.NewError(domain.ErrorConflict,
				fmt.Sprintf("dispatch is held: %s (%s)", hold.ID, hold.Reason),
				domain.RetryNever, tauth.ErrDispatchHeld)
		}
	}
	return nil
}

// handoffCandidateOwners returns every canonical v1 home that currently owns
// the task: the source home plus every captain under its captains/ tree.
// Ownership is read from the canonical Get, so a home owns the task when its
// current aggregate exists. A home that cannot serve a canonical view fails
// closed: its ownership is unknowable. Cross-home resolution and candidate-
// owner collection remain fleet-owned (Task 6.2 criterion 4).
func handoffCandidateOwners(source, taskID string) ([]string, error) {
	var owners []string
	collect := func(home string) error {
		auth, err := handoffCanonical(home)
		if err != nil {
			return err
		}
		tid, err := domain.NewTaskID(taskID)
		if err != nil {
			return err
		}
		if _, err := auth.Get(tid); err == nil {
			owners = append(owners, home)
		} else if !errors.Is(err, tauth.ErrNotFound) {
			return fmt.Errorf("resolving handoff task %s: home %s cannot serve canonical authority state: %w", taskID, home, err)
		}
		return nil
	}
	if err := collect(source); err != nil {
		return nil, err
	}
	captainsRoot := filepath.Join(source, "captains")
	entries, err := os.ReadDir(captainsRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := collect(filepath.Join(captainsRoot, entry.Name())); err != nil {
			return nil, err
		}
	}
	return owners, nil
}

// resolveHandoffTaskID resolves one requested key against canonical v2
// ownership across the source home and its captains, collecting all candidate
// owners (criterion 4). Multiple canonical owners make the key ambiguous and
// the CLI surfaces correction commands preserving the requested destination.
func resolveHandoffTaskID(source, key string) (string, error) {
	if err := validateHandoffTaskID(key); err != nil {
		return "", err
	}
	owners, err := handoffCandidateOwners(source, key)
	if err != nil {
		return "", err
	}
	if len(owners) > 1 {
		return "", &mhome.AmbiguousTaskIDError{Requested: key, Matches: owners}
	}
	return key, nil
}

func validateHandoffTaskID(taskID string) error {
	if taskID == "" || taskID == "." || taskID == ".." || filepath.Base(taskID) != taskID || strings.ContainsAny(taskID, `/\\`) {
		return fmt.Errorf("invalid handoff task id %q", taskID)
	}
	return nil
}

func canonicalHandoffHome(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving handoff home %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("canonicalizing handoff home %s: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

// resolveHandoffKeys resolves the requested keys against the source backlog,
// refusing any key that is not in queued state. Each key must also resolve to
// a single canonical owner (no ambiguity).
func resolveHandoffKeys(source, backlog, path string, requested []string) ([]string, error) {
	seen := make(map[string]bool, len(requested))
	keys := make([]string, 0, len(requested))
	for _, key := range requested {
		resolved, err := resolveHandoffTaskID(source, key)
		if err != nil {
			return nil, fmt.Errorf("handoff: resolving task %s: %w", key, err)
		}
		if seen[resolved] {
			return nil, fmt.Errorf("handoff: duplicate task %s", resolved)
		}
		seen[resolved] = true
		out, err := execCommand(path, "show", resolved, "--file", backlog).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("handoff: key %s not found in source backlog: %w: %s", resolved, err, strings.TrimSpace(string(out)))
		}
		state := extractTaskStateFromShow(string(out))
		if state == "" {
			return nil, fmt.Errorf("handoff: key %s has no parseable state — only queued items may be handed off", resolved)
		}
		if state != "queued" {
			return nil, fmt.Errorf("handoff: key %s has state %q, only queued items may be handed off", resolved, state)
		}
		keys = append(keys, resolved)
	}
	return keys, nil
}

// handoffDestinationConflict is the typed conflict returned when the
// destination already owns the task: the transfer quarantines and never
// overwrites destination truth (ADR-0007 §10).
func handoffDestinationConflict(taskID string, generation tauth.Generation) error {
	return domain.NewError(domain.ErrorConflict,
		fmt.Sprintf("handoff: destination already has current authority for %s generation %s", taskID, generation),
		domain.RetryNever, nil)
}
