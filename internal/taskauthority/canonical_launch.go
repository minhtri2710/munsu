package taskauthority

import (
	"encoding/json"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// LaunchBoundary is the canonical Task Authority surface that journals one
// generation-bound Soldier launch (#412). It is the durable launch-intent
// foundation: BeginSpawn commits the immutable pre-acquisition launch intent
// (snapshot digest, explicit Backend/Harness identities, launch identity,
// one-time worktree/endpoint reservation fences) BEFORE any worktree,
// endpoint, or process acquisition; AttachEndpoint records the exact acquired
// endpoint identity while the phase remains queued; RecordLaunch records the
// successful launch submission evidence (script/command digest) before the
// final BindEndpoint transitions the task to working.
//
// Every operation is a typed, exact-generation/revision-fenced, Operation
// ID+digest-idempotent mutation with the same durable mechanics as the rest
// of the canonical surface (ADR-0008 §2): one atomic journaled home.Commit
// under the task's fenced scope, receipts for replay, and fail-closed typed
// conflicts for stale or reused intents. The existing BindWorktree and
// BindEndpoint operations are fenced to the committed intent: bindings must
// carry the exact reservation/fence identities the intent reserved, and
// BindEndpoint additionally requires the recorded acquired endpoint and
// launch evidence before queued → working. No operation transitions the
// phase to working except the existing BindEndpoint, and no identity is
// selected, detected, defaulted, probed, or fallen back: Fleet supplies
// every value explicitly.

// CanonicalBeginSpawnRequest commits the immutable pre-acquisition launch
// intent for one generation-bound Soldier launch. The request is the typed
// intent for the operation digest.
type CanonicalBeginSpawnRequest struct {
	HomeID                domain.HomeID
	TaskID                domain.TaskID
	Precondition          domain.Precondition
	SnapshotDigest        string
	Backend               string
	Harness               string
	Model                 string
	Effort                string
	Mode                  string
	Kind                  string
	Project               string
	ParentTaskID          string
	LaunchID              string
	WindowLabel           string
	WorktreeReservationID string
	WorktreeFenceToken    string
	EndpointReservationID string
	EndpointFenceToken    string
	// EndpointIncarnation is the opaque launch-operation provenance token
	// minted by Fleet BEFORE BeginSpawn and persisted here so a crash after
	// create but before attach reuses the same token (BEO-16/P1a). It is not
	// a backend attestation.
	EndpointIncarnation string
	Reason              string
}

func (r CanonicalBeginSpawnRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID                string `json:"home_id"`
		TaskID                string `json:"task_id"`
		Generation            uint64 `json:"generation"`
		Revision              uint64 `json:"revision"`
		SnapshotDigest        string `json:"snapshot_digest"`
		Backend               string `json:"backend"`
		Harness               string `json:"harness"`
		Model                 string `json:"model,omitempty"`
		Effort                string `json:"effort,omitempty"`
		Mode                  string `json:"mode,omitempty"`
		Kind                  string `json:"kind,omitempty"`
		Project               string `json:"project,omitempty"`
		ParentTaskID          string `json:"parent_task_id,omitempty"`
		LaunchID              string `json:"launch_id"`
		WindowLabel           string `json:"window_label,omitempty"`
		WorktreeReservationID string `json:"worktree_reservation_id"`
		WorktreeFenceToken    string `json:"worktree_fence_token"`
		EndpointReservationID string `json:"endpoint_reservation_id"`
		EndpointFenceToken    string `json:"endpoint_fence_token"`
		EndpointIncarnation   string `json:"endpoint_incarnation,omitempty"`
		Reason                string `json:"reason,omitempty"`
	}{
		r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision,
		r.SnapshotDigest, r.Backend, r.Harness, r.Model, r.Effort, r.Mode, r.Kind, r.Project,
		r.ParentTaskID, r.LaunchID, r.WindowLabel, r.WorktreeReservationID, r.WorktreeFenceToken,
		r.EndpointReservationID, r.EndpointFenceToken, r.EndpointIncarnation, r.Reason,
	})
}

// validateBeginSpawnRequest checks the typed request shape before any
// mutation: a valid task identity and generation/revision precondition and
// the shared launch identity fields (validated by validateLaunchIdentity).
func validateBeginSpawnRequest(req CanonicalBeginSpawnRequest) error {
	if err := req.TaskID.Validate(); err != nil {
		return err
	}
	if err := req.Precondition.Validate(); err != nil {
		return err
	}
	return validateLaunchIdentity(req.SnapshotDigest, req.Backend, req.Harness, req.Model, req.Effort, req.Mode, req.Kind, req.Project, req.ParentTaskID, req.LaunchID, req.WindowLabel, req.WorktreeReservationID, req.WorktreeFenceToken, req.EndpointReservationID, req.EndpointFenceToken, req.EndpointIncarnation)
}

// BeginSpawn commits the immutable launch intent for the task's current
// generation. It is exact-generation and idempotent: the request carries the
// expected Generation/Revision precondition, and reusing the same Operation
// ID with the same digest replays the durable prior outcome. The mutation
// checks the queued phase, owner presence, and the committed DispatchAction
// Spawn holds transactionally, and fails closed on a generation that already
// holds acquired worktree/endpoint bindings (durable intent must precede
// resource acquisition), on a second distinct intent for the same generation,
// on a stale precondition, or on a reserved-for-transfer/superseded
// generation.
func (c *Canonical) BeginSpawn(op domain.Operation, req CanonicalBeginSpawnRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateBeginSpawnRequest(req); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Worktree != nil || cur.Endpoint != nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s already holds acquired bindings; launch intent must precede resource acquisition", cur.TaskID, cur.Generation)
		}
		if cur.Launch != nil {
			if launchIntentSame(*cur.Launch, req) {
				return cur.clone(), nil
			}
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s already has a different launch intent", cur.TaskID, cur.Generation)
		}
		if cur.Phase != PhaseQueued {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s is %s, begin spawn requires queued", cur.TaskID, cur.Generation, cur.Phase)
		}
		if strings.TrimSpace(cur.Definition.Owner) == "" {
			return Aggregate{}, conflictError(ErrPrecondition, "task %s generation %s is not ready to spawn: %s", cur.TaskID, cur.Generation, ReadinessMissingOwner)
		}
		holds, err := c.listHolds()
		if err != nil {
			return Aggregate{}, err
		}
		if holdsBlockAction(holds, DispatchActionSpawn, cur) {
			return Aggregate{}, conflictError(ErrDispatchHeld, "%s: dispatch is held for %s", ErrDispatchHeld, cur.TaskID)
		}
		next := cur.clone()
		next.Launch = &LaunchIntent{
			OperationID:           op.ID.Value(),
			SnapshotDigest:        req.SnapshotDigest,
			Backend:               req.Backend,
			Harness:               req.Harness,
			Model:                 req.Model,
			Effort:                req.Effort,
			Mode:                  req.Mode,
			Kind:                  req.Kind,
			Project:               req.Project,
			ParentTaskID:          req.ParentTaskID,
			LaunchID:              req.LaunchID,
			WindowLabel:           req.WindowLabel,
			WorktreeReservationID: req.WorktreeReservationID,
			WorktreeFenceToken:    req.WorktreeFenceToken,
			EndpointReservationID: req.EndpointReservationID,
			EndpointFenceToken:    req.EndpointFenceToken,
			EndpointIncarnation:   req.EndpointIncarnation,
			PlannedAt:             c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}

// launchIntentSame reports whether a committed launch intent carries the
// identical immutable launch identity as a begin-spawn request. The committing
// Operation ID and the planned timestamp are record metadata, not request
// identity, and are excluded from the comparison.
func launchIntentSame(l LaunchIntent, req CanonicalBeginSpawnRequest) bool {
	return l.SnapshotDigest == req.SnapshotDigest &&
		l.Backend == req.Backend && l.Harness == req.Harness &&
		l.Model == req.Model && l.Effort == req.Effort && l.Mode == req.Mode &&
		l.Kind == req.Kind && l.Project == req.Project && l.ParentTaskID == req.ParentTaskID &&
		l.LaunchID == req.LaunchID && l.WindowLabel == req.WindowLabel &&
		l.WorktreeReservationID == req.WorktreeReservationID && l.WorktreeFenceToken == req.WorktreeFenceToken &&
		l.EndpointReservationID == req.EndpointReservationID && l.EndpointFenceToken == req.EndpointFenceToken &&
		l.EndpointIncarnation == req.EndpointIncarnation
}

// CanonicalAttachEndpointRequest records the exact acquired endpoint identity
// for a launch while the task phase remains queued. The request is the typed
// intent for the operation digest.
type CanonicalAttachEndpointRequest struct {
	HomeID       domain.HomeID
	TaskID       domain.TaskID
	Precondition domain.Precondition
	Backend      string
	Handle       string
	LeaseID      string
	FenceToken   string
	SessionOwner string
	WorkspaceID  string
	TabID        string
	Incarnation  string
	Reason       string
}

func (r CanonicalAttachEndpointRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID       string `json:"home_id"`
		TaskID       string `json:"task_id"`
		Generation   uint64 `json:"generation"`
		Revision     uint64 `json:"revision"`
		Backend      string `json:"backend"`
		Handle       string `json:"handle"`
		LeaseID      string `json:"lease_id"`
		FenceToken   string `json:"fence_token"`
		SessionOwner string `json:"session_owner,omitempty"`
		WorkspaceID  string `json:"workspace_id,omitempty"`
		TabID        string `json:"tab_id,omitempty"`
		Incarnation  string `json:"incarnation,omitempty"`
		Reason       string `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision,
		r.Backend, r.Handle, r.LeaseID, r.FenceToken, r.SessionOwner, r.WorkspaceID, r.TabID, r.Incarnation, r.Reason})
}

// validateAttachEndpointRequest checks the typed request shape: a valid task
// identity and precondition and the endpoint identity fields required for an
// endpoint binding (backend, handle, lease, fence).
func validateAttachEndpointRequest(req CanonicalAttachEndpointRequest) error {
	if err := req.TaskID.Validate(); err != nil {
		return err
	}
	if err := req.Precondition.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(req.Backend) == "" {
		return validationError("acquired endpoint requires backend")
	}
	if strings.TrimSpace(req.Handle) == "" {
		return validationError("acquired endpoint requires handle")
	}
	if strings.TrimSpace(req.LeaseID) == "" {
		return validationError("acquired endpoint requires lease id")
	}
	if strings.TrimSpace(req.FenceToken) == "" {
		return validationError("acquired endpoint requires fence token")
	}
	return nil
}

// AttachEndpoint records the exact acquired endpoint identity for the
// committed launch intent while the phase remains queued. It is
// exact-generation and idempotent: the request carries the expected
// Generation/Revision precondition, and reusing the same Operation ID with
// the same digest replays the durable prior outcome. The acquired endpoint
// must carry the launch intent's explicit backend and the exact endpoint
// reservation fence; a different acquired endpoint identity can never
// overwrite the committed record, and a missing intent, a non-queued phase,
// or a stale precondition fails closed with a typed conflict.
func (c *Canonical) AttachEndpoint(op domain.Operation, req CanonicalAttachEndpointRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateAttachEndpointRequest(req); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Launch == nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no launch intent; attach endpoint requires a committed launch intent", cur.TaskID, cur.Generation)
		}
		if cur.Phase != PhaseQueued {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s is %s, attach endpoint requires queued", cur.TaskID, cur.Generation, cur.Phase)
		}
		if req.Backend != cur.Launch.Backend {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s acquired endpoint backend %q does not match launch intent backend %q", cur.TaskID, cur.Generation, req.Backend, cur.Launch.Backend)
		}
		if req.LeaseID != cur.Launch.EndpointReservationID || req.FenceToken != cur.Launch.EndpointFenceToken {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s acquired endpoint does not match the launch endpoint reservation fence", cur.TaskID, cur.Generation)
		}
		if req.Incarnation == "" {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s acquired endpoint requires the launch incarnation token", cur.TaskID, cur.Generation)
		}
		if req.Incarnation != cur.Launch.EndpointIncarnation {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s acquired endpoint incarnation does not match the launch intent incarnation", cur.TaskID, cur.Generation)
		}
		if cur.AcquiredEndpoint != nil {
			if acquiredEndpointSame(*cur.AcquiredEndpoint, req) {
				return cur.clone(), nil
			}
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s already records a different acquired endpoint", cur.TaskID, cur.Generation)
		}
		next := cur.clone()
		next.AcquiredEndpoint = &AcquiredEndpoint{
			OperationID:  op.ID.Value(),
			Backend:      req.Backend,
			Handle:       req.Handle,
			LeaseID:      req.LeaseID,
			FenceToken:   req.FenceToken,
			SessionOwner: req.SessionOwner,
			WorkspaceID:  req.WorkspaceID,
			TabID:        req.TabID,
			Incarnation:  req.Incarnation,
			AcquiredAt:   c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}

// acquiredEndpointSame reports whether a committed acquired-endpoint record
// carries the identical endpoint identity as an attach-endpoint request. The
// acquiring Operation ID and the acquisition timestamp are record metadata,
// not request identity, and are excluded from the comparison.
func acquiredEndpointSame(e AcquiredEndpoint, req CanonicalAttachEndpointRequest) bool {
	return e.Backend == req.Backend && e.Handle == req.Handle &&
		e.LeaseID == req.LeaseID && e.FenceToken == req.FenceToken &&
		e.SessionOwner == req.SessionOwner && e.WorkspaceID == req.WorkspaceID &&
		e.TabID == req.TabID && e.Incarnation == req.Incarnation
}

// CanonicalRecordLaunchRequest records the successful launch submission
// evidence for a launch before the final BindEndpoint. The request is the
// typed intent for the operation digest.
type CanonicalRecordLaunchRequest struct {
	HomeID        domain.HomeID
	TaskID        domain.TaskID
	Precondition  domain.Precondition
	LaunchID      string
	CommandDigest string
	Reason        string
}

func (r CanonicalRecordLaunchRequest) DigestBytes() ([]byte, error) {
	return json.Marshal(struct {
		HomeID        string `json:"home_id"`
		TaskID        string `json:"task_id"`
		Generation    uint64 `json:"generation"`
		Revision      uint64 `json:"revision"`
		LaunchID      string `json:"launch_id"`
		CommandDigest string `json:"command_digest"`
		Reason        string `json:"reason,omitempty"`
	}{r.HomeID.Value(), r.TaskID.Value(), r.Precondition.Generation, r.Precondition.Revision, r.LaunchID, r.CommandDigest, r.Reason})
}

// validateRecordLaunchRequest checks the typed request shape: a valid task
// identity and precondition, a deterministic launch identity, and a full
// sha256 digest of the submitted launch script/command.
func validateRecordLaunchRequest(req CanonicalRecordLaunchRequest) error {
	if err := req.TaskID.Validate(); err != nil {
		return err
	}
	if err := req.Precondition.Validate(); err != nil {
		return err
	}
	if req.LaunchID == "" || strings.ContainsAny(req.LaunchID, `/\\`) {
		return validationError("launch evidence requires a deterministic launch identity")
	}
	if !domain.IsSHA256(req.CommandDigest) {
		return validationError("launch evidence command digest must be a 64-hex sha256 digest")
	}
	return nil
}

// RecordLaunch records the successful launch submission evidence for the
// committed launch intent, after the endpoint is acquired and before the
// final BindEndpoint. It is exact-generation and idempotent: the request
// carries the expected Generation/Revision precondition, and reusing the same
// Operation ID with the same digest replays the durable prior outcome. The
// evidence must carry the intent's exact launch identity and require the
// recorded acquired endpoint; different evidence can never overwrite the
// committed record, and a missing intent or acquired endpoint, a non-queued
// phase, or a stale precondition fails closed with a typed conflict.
func (c *Canonical) RecordLaunch(op domain.Operation, req CanonicalRecordLaunchRequest) (Outcome, error) {
	if err := c.prepare(op, req, req.HomeID); err != nil {
		return Outcome{}, err
	}
	if err := validateRecordLaunchRequest(req); err != nil {
		return Outcome{}, err
	}
	return c.mutateTask(op, req.TaskID, req.Precondition, func(cur Aggregate) (Aggregate, error) {
		if cur.Launch == nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no launch intent; record launch requires a committed launch intent", cur.TaskID, cur.Generation)
		}
		if cur.Phase != PhaseQueued {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s is %s, record launch requires queued", cur.TaskID, cur.Generation, cur.Phase)
		}
		if cur.AcquiredEndpoint == nil {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s has no recorded acquired endpoint; launch evidence requires the acquired endpoint", cur.TaskID, cur.Generation)
		}
		if req.LaunchID != cur.Launch.LaunchID {
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s launch evidence identity %q does not match launch intent identity %q", cur.TaskID, cur.Generation, req.LaunchID, cur.Launch.LaunchID)
		}
		if cur.LaunchEvidence != nil {
			if cur.LaunchEvidence.LaunchID == req.LaunchID && cur.LaunchEvidence.CommandDigest == req.CommandDigest {
				return cur.clone(), nil
			}
			return Aggregate{}, conflictError(ErrConflict, "task %s generation %s already records different launch evidence", cur.TaskID, cur.Generation)
		}
		next := cur.clone()
		next.LaunchEvidence = &LaunchEvidence{
			OperationID:   op.ID.Value(),
			LaunchID:      req.LaunchID,
			CommandDigest: req.CommandDigest,
			SubmittedAt:   c.now().UnixNano(),
		}
		next.Revision++
		return next, nil
	})
}
