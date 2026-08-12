package taskauthority

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/minhtri2710/munsu/internal/domain"
)

// Generation is the positive monotonic identity of one Task lifecycle
// incarnation. Reopen creates the next Generation; superseded generations are
// preserved as historical records.
type Generation uint64

// String renders the Generation as its on-disk decimal identity.
func (g Generation) String() string { return strconv.FormatUint(uint64(g), 10) }

// Validate rejects zero, which is not a positive monotonic identity.
func (g Generation) Validate() error {
	if g == 0 {
		return ErrInvalidGeneration
	}
	return nil
}

// Next returns the successor Generation, rejecting overflow.
func (g Generation) Next() (Generation, error) {
	if g == 0 || g == ^Generation(0) {
		return 0, fmt.Errorf("%w: %d", ErrInvalidGeneration, g)
	}
	return g + 1, nil
}

// ParseGeneration parses a decimal string Generation preserving the existing
// positive-monotonic identity semantics used on disk.
func ParseGeneration(s string) (Generation, error) {
	if s == "" || s == "." || s == ".." || path.Base(s) != s || strings.ContainsAny(s, `/\\`) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidGeneration, s)
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidGeneration, s)
	}
	return Generation(n), nil
}

// Revision is the monotonic ordering of authoritative mutations within one
// Task Generation. It starts at FirstRevision, advances on every committed
// mutation, and resets for a new Generation. It is an internal ordering and
// audit identity, not a universal caller compare-and-swap token.
type Revision uint64

// FirstRevision is the Revision of a freshly created Task Generation.
const FirstRevision Revision = 1

// Phase is the lifecycle state of one Task Generation.
type Phase string

const (
	PhaseQueued   Phase = "queued"
	PhaseBlocked  Phase = "blocked"
	PhaseWorking  Phase = "working"
	PhaseDone     Phase = "done"
	PhaseResolved Phase = "resolved"
	PhaseRetired  Phase = "retired"
)

// terminal reports whether the phase is an end state of the lifecycle.
func (p Phase) terminal() bool {
	return p == PhaseDone || p == PhaseResolved || p == PhaseRetired
}

// ValidFrom reports whether the phase is a valid authoritative lifecycle
// value. "in-flight" is a projection display of working and is not an
// authoritative phase.
func (p Phase) Valid() bool {
	switch p {
	case PhaseQueued, PhaseBlocked, PhaseWorking, PhaseDone, PhaseResolved, PhaseRetired:
		return true
	}
	return false
}

// TaskDefinition carries the durable definition of one Task Generation.
type TaskDefinition struct {
	Owner                  string `json:"owner"`
	Description            string `json:"description,omitempty"`
	Kind                   string `json:"kind,omitempty"`
	Project                string `json:"project,omitempty"`
	ParentTaskID           string `json:"parent_task_id,omitempty"`
	ScoutScope             string `json:"scout_scope,omitempty"`
	ScoutRuntimeBudgetSecs int64  `json:"scout_runtime_budget_secs,omitempty"`
}

// EndpointBinding is a generation-bound runtime endpoint lease. Incarnation
// is the opaque generation-bound identity minted by Fleet for this exact
// endpoint binding and used to reject stale/foreign observations (freshness).
// taskauthority persists the opaque value without importing backend.
type EndpointBinding struct {
	Backend      string `json:"backend"`
	Handle       string `json:"handle"`
	LeaseID      string `json:"lease_id"`
	FenceToken   string `json:"fence_token"`
	SessionOwner string `json:"session_owner,omitempty"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	TabID        string `json:"tab_id,omitempty"`
	Incarnation  string `json:"incarnation,omitempty"`
	BoundAtUnix  int64  `json:"bound_at_unix"`
}

// WorktreeBinding is a generation-bound repository worktree lease.
type WorktreeBinding struct {
	RepositoryIdentity string `json:"repository_identity"`
	Path               string `json:"path"`
	GitDir             string `json:"git_dir"`
	CommonDir          string `json:"common_dir"`
	Head               string `json:"head"`
	LeaseID            string `json:"lease_id"`
	FenceToken         string `json:"fence_token"`
	BoundAtUnix        int64  `json:"bound_at_unix"`
}

// TransferState is the generation-bound transfer reservation or reception
// state of one Task Generation. It is set only by the transfer boundary
// operations (ReserveTransfer / ReceiveTransfer) and is never the vehicle for
// raw document copy: the destination re-creates the generation from a typed
// TaskDefinition, never from the source Aggregate document. On the source it
// records the reservation/fencing (destination target, fence token); on the
// destination it records the received generation's provenance (source home and
// generation). Transferred marks a source generation superseded by a committed
// transfer; Activation records the committed destination-activation evidence
// bound to the reservation.
type TransferState struct {
	ReservationID    string                  `json:"reservation_id,omitempty"`
	DestinationHome  string                  `json:"destination_home,omitempty"`
	FenceToken       string                  `json:"fence_token,omitempty"`
	SourceHome       string                  `json:"source_home,omitempty"`
	SourceGeneration Generation              `json:"source_generation,omitempty"`
	ReservedAt       int64                   `json:"reserved_at,omitempty"`
	Transferred      bool                    `json:"transferred,omitempty"`
	Activation       *TransferActivationInfo `json:"activation,omitempty"`
}

// TransferActivationInfo is the durable destination-activation evidence bound
// to one source reservation, recorded by CommitTransfer. #413 obtains and
// persists this evidence through its own orchestration; CommitTransfer
// verifies only the evidence shape/binding against the exact source
// reservation and records it durably so the source supersession is never made
// without destination-activation proof. No raw document and no cross-home
// trust/authentication policy beyond typed receipt binding is invented here.
type TransferActivationInfo struct {
	ReservationID         string     `json:"reservation_id"`
	TaskID                string     `json:"task_id"`
	SourceHome            string     `json:"source_home"`
	SourceGeneration      Generation `json:"source_generation"`
	DestinationHome       string     `json:"destination_home"`
	DestinationGeneration Generation `json:"destination_generation"`
	ActivationOperationID string     `json:"activation_operation_id"`
	ActivationDigest      string     `json:"activation_digest"`
}

// LaunchIntent is the immutable, pre-acquisition record of one generation-bound
// Soldier launch. It is committed by BeginSpawn before any worktree, endpoint,
// or process acquisition, and fences the later binding mutations to the exact
// identities reserved here. It stores only facts Fleet knows before acquisition
// — the frozen snapshot digest, the explicit Backend and Harness/adapter
// identity, model/effort/mode/kind/project/parent identities, the deterministic
// launch identity/window label, and the one-time worktree/endpoint reservation
// identities (reservation ID + fence token) — sufficient to bind or recover the
// same operation. No identity is selected, detected, defaulted, probed, or
// fallen back here: Fleet supplies every value explicitly.
//
// LaunchIntent is not a process record and carries no executable content: it is
// the durable launch-intent foundation on which #412's Fleet runner later
// proves re-entrancy under the exact launch fence.
type LaunchIntent struct {
	OperationID           string `json:"operation_id"`
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
	PlannedAt             int64  `json:"planned_at"`
}

// AcquiredEndpoint is the durable record of the exact endpoint identity Fleet
// acquired for the launch, committed by AttachEndpoint while the task phase
// remains queued. It is exact-generation/revision fenced and idempotent: a
// different acquired endpoint identity can never overwrite the committed
// record. The record is not an active binding (BindEndpoint makes the
// acquired endpoint the active Endpoint) and does not transition the phase.
type AcquiredEndpoint struct {
	OperationID  string `json:"operation_id"`
	Backend      string `json:"backend"`
	Handle       string `json:"handle"`
	LeaseID      string `json:"lease_id"`
	FenceToken   string `json:"fence_token"`
	SessionOwner string `json:"session_owner,omitempty"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	TabID        string `json:"tab_id,omitempty"`
	Incarnation  string `json:"incarnation,omitempty"`
	AcquiredAt   int64  `json:"acquired_at"`
}

// LaunchEvidence is the durable record of the successful launch submission
// for one launch, committed by RecordLaunch after the endpoint is acquired and
// before the final BindEndpoint transitions the task to working. It pins the
// deterministic launch identity and the sha256 digest of the submitted launch
// script/command so the downstream Fleet launch artifact is re-entrant under
// the exact launch fence: the same launch identity and command digest are
// provably the same submission, and a duplicate process launch after a crash
// fails closed instead of re-submitting.
type LaunchEvidence struct {
	OperationID   string `json:"operation_id"`
	LaunchID      string `json:"launch_id"`
	CommandDigest string `json:"command_digest"`
	SubmittedAt   int64  `json:"submitted_at"`
}

// RetirementEvidence is the immutable, generation-bound record of the resource
// ownership a retired generation released. It preserves the exact endpoint and
// worktree lease identities (lease IDs, fence tokens, handles/paths, repository
// identity), the task generation, and the retirement Operation ID, so #412 can
// release only resources still owned by that generation and for diagnostics.
// The retired generation's active Endpoint/Worktree are nil: it no longer acts
// as an active binding owner, while the evidence remains durably rereadable.
type RetirementEvidence struct {
	OperationID string           `json:"operation_id,omitempty"`
	Generation  Generation       `json:"generation"`
	RetiredAt   int64            `json:"retired_at,omitempty"`
	Endpoint    *EndpointBinding `json:"endpoint,omitempty"`
	Worktree    *WorktreeBinding `json:"worktree,omitempty"`
}

// Aggregate is the authoritative record of one Task Generation.
type Aggregate struct {
	SchemaVersion    string              `json:"schema_version"`
	TaskID           string              `json:"task_id"`
	Generation       Generation          `json:"generation"`
	Revision         Revision            `json:"revision"`
	Current          bool                `json:"current"`
	Definition       TaskDefinition      `json:"definition"`
	Phase            Phase               `json:"phase"`
	PhaseDetail      string              `json:"phase_detail,omitempty"`
	Endpoint         *EndpointBinding    `json:"endpoint,omitempty"`
	Worktree         *WorktreeBinding    `json:"worktree,omitempty"`
	Launch           *LaunchIntent       `json:"launch,omitempty"`
	AcquiredEndpoint *AcquiredEndpoint   `json:"acquired_endpoint,omitempty"`
	LaunchEvidence   *LaunchEvidence     `json:"launch_evidence,omitempty"`
	Transfer         *TransferState      `json:"transfer,omitempty"`
	Retirement       *RetirementEvidence `json:"retirement,omitempty"`
}

// TaskAuthoritySchema is the deterministic schema identity for the canonical
// JSON representation of authoritative records. It is the single current
// document identity (ADR-0008 §11): internal-history v2 identities are
// replaced in place by the first supported current v1 definition.
const TaskAuthoritySchema = "munsu.task-authority/v1"

func validateScoutContract(def TaskDefinition) error {
	if def.Kind != "scout" {
		if strings.TrimSpace(def.ScoutScope) != "" || def.ScoutRuntimeBudgetSecs != 0 {
			return validationError("scout-only fields are not valid for ship tasks")
		}
		return nil
	}
	if strings.TrimSpace(def.ScoutScope) == "" {
		return validationError("scout task requires non-empty scope")
	}
	if def.ScoutRuntimeBudgetSecs <= 0 {
		return validationError("scout task requires positive runtime budget seconds")
	}
	return nil
}

// NewAggregate builds the first Generation of a task with Revision one and
// phase queued, validating the request fields.
func NewAggregate(taskID, owner, description, kind, project, parentTaskID string) (Aggregate, error) {
	agg := Aggregate{
		SchemaVersion: TaskAuthoritySchema,
		TaskID:        taskID,
		Generation:    1,
		Revision:      FirstRevision,
		Current:       true,
		Definition: TaskDefinition{
			Owner:        owner,
			Description:  description,
			Kind:         kind,
			Project:      project,
			ParentTaskID: parentTaskID,
		},
		Phase: PhaseQueued,
	}
	if err := validateAggregate(agg); err != nil {
		return Aggregate{}, err
	}
	return agg, nil
}

// validateAggregate checks record-level invariants: identity, phase, owner,
// and generation-bound bindings. It does not evaluate lifecycle transitions.
func validateAggregate(agg Aggregate) error {
	if agg.SchemaVersion != TaskAuthoritySchema {
		return validationError("invalid task authority schema %q", agg.SchemaVersion)
	}
	if err := validateTaskID(agg.TaskID); err != nil {
		return err
	}
	if err := agg.Generation.Validate(); err != nil {
		return err
	}
	if agg.Revision == 0 {
		return validationError("task %s/%s missing revision", agg.TaskID, agg.Generation)
	}
	if !agg.Phase.Valid() {
		return validationError("task %s/%s has invalid phase %q", agg.TaskID, agg.Generation, agg.Phase)
	}
	if strings.TrimSpace(agg.Definition.Owner) == "" {
		return validationError("task %s/%s missing owner", agg.TaskID, agg.Generation)
	}
	if agg.Endpoint != nil {
		if err := validateEndpointBinding(*agg.Endpoint); err != nil {
			return err
		}
	}
	if agg.Worktree != nil {
		if err := validateWorktreeBinding(*agg.Worktree); err != nil {
			return err
		}
	}
	if agg.Transfer != nil {
		if err := validateTransferState(*agg.Transfer); err != nil {
			return err
		}
	}
	if agg.Retirement != nil {
		if err := validateRetirementEvidence(*agg.Retirement); err != nil {
			return err
		}
	}
	if agg.Launch != nil {
		if err := validateLaunchIntent(*agg.Launch); err != nil {
			return err
		}
	}
	if agg.AcquiredEndpoint != nil {
		if err := validateAcquiredEndpoint(*agg.AcquiredEndpoint); err != nil {
			return err
		}
	}
	if agg.LaunchEvidence != nil {
		if err := validateLaunchEvidence(*agg.LaunchEvidence); err != nil {
			return err
		}
	}
	return nil
}

// validateRetirementEvidence checks the preserved retirement evidence shape:
// a non-empty retirement Operation ID, a valid generation, and valid preserved
// bindings when present.
func validateRetirementEvidence(ev RetirementEvidence) error {
	if ev.OperationID == "" || strings.ContainsAny(ev.OperationID, `/\\`) {
		return validationError("retirement evidence missing operation id")
	}
	if err := ev.Generation.Validate(); err != nil {
		return err
	}
	if ev.RetiredAt <= 0 {
		return validationError("retirement evidence missing retired timestamp")
	}
	if ev.Endpoint != nil {
		if err := validateEndpointBinding(*ev.Endpoint); err != nil {
			return err
		}
	}
	if ev.Worktree != nil {
		if err := validateWorktreeBinding(*ev.Worktree); err != nil {
			return err
		}
	}
	return nil
}

// validateLaunchIdentity checks the launch identity fields shared by a
// BeginSpawn request and a committed LaunchIntent record: the snapshot digest
// must be a full sha256 digest, the explicit Backend and Harness identities
// and the deterministic launch identity must be present and safe, every
// optional identity must be safe when present, and the one-time worktree and
// endpoint reservation fences (reservation ID + fence token) must be present
// and safe. Validation is shape-only: no value is selected, detected,
// defaulted, probed, or fallen back.
func validateLaunchIdentity(snapshotDigest, backend, harness, model, effort, mode, kind, project, parentTaskID, launchID, windowLabel, worktreeReservationID, worktreeFenceToken, endpointReservationID, endpointFenceToken string) error {
	if !domain.IsSHA256(snapshotDigest) {
		return validationError("launch snapshot digest must be a 64-hex sha256 digest")
	}
	if strings.TrimSpace(backend) == "" || strings.ContainsAny(backend, `/\\`) {
		return validationError("launch requires an explicit backend identity")
	}
	if strings.TrimSpace(harness) == "" || strings.ContainsAny(harness, `/\\`) {
		return validationError("launch requires an explicit harness identity")
	}
	for _, v := range []string{model, effort, mode, kind, project, parentTaskID, windowLabel} {
		if strings.ContainsAny(v, `/\\`) {
			return validationError("launch carries an unsafe identity value")
		}
	}
	if launchID == "" || strings.ContainsAny(launchID, `/\\`) {
		return validationError("launch requires a deterministic launch identity")
	}
	if worktreeReservationID == "" || strings.ContainsAny(worktreeReservationID, `/\\`) {
		return validationError("launch requires a worktree reservation id")
	}
	if worktreeFenceToken == "" || strings.ContainsAny(worktreeFenceToken, `/\\`) {
		return validationError("launch requires a worktree fence token")
	}
	if endpointReservationID == "" || strings.ContainsAny(endpointReservationID, `/\\`) {
		return validationError("launch requires an endpoint reservation id")
	}
	if endpointFenceToken == "" || strings.ContainsAny(endpointFenceToken, `/\\`) {
		return validationError("launch requires an endpoint fence token")
	}
	return nil
}

// validateLaunchIntent checks the committed launch intent record shape: the
// shared launch identity plus the committing Operation ID and the planned
// timestamp.
func validateLaunchIntent(l LaunchIntent) error {
	if l.OperationID == "" || strings.ContainsAny(l.OperationID, `/\\`) {
		return validationError("launch intent missing operation id")
	}
	if err := validateLaunchIdentity(l.SnapshotDigest, l.Backend, l.Harness, l.Model, l.Effort, l.Mode, l.Kind, l.Project, l.ParentTaskID, l.LaunchID, l.WindowLabel, l.WorktreeReservationID, l.WorktreeFenceToken, l.EndpointReservationID, l.EndpointFenceToken); err != nil {
		return err
	}
	if l.PlannedAt <= 0 {
		return validationError("launch intent missing planned timestamp")
	}
	return nil
}

// validateAcquiredEndpoint checks the committed acquired-endpoint record
// shape: the acquiring Operation ID, the endpoint identity fields required by
// an endpoint binding (backend, handle, lease, fence), safe optional identity
// fields, and the acquisition timestamp.
func validateAcquiredEndpoint(e AcquiredEndpoint) error {
	if e.OperationID == "" || strings.ContainsAny(e.OperationID, `/\\`) {
		return validationError("acquired endpoint missing operation id")
	}
	if strings.TrimSpace(e.Backend) == "" {
		return validationError("acquired endpoint missing backend")
	}
	if strings.TrimSpace(e.Handle) == "" {
		return validationError("acquired endpoint missing handle")
	}
	if strings.TrimSpace(e.LeaseID) == "" {
		return validationError("acquired endpoint missing lease id")
	}
	if strings.TrimSpace(e.FenceToken) == "" {
		return validationError("acquired endpoint missing fence token")
	}
	for _, v := range []string{e.SessionOwner, e.WorkspaceID, e.TabID} {
		if strings.ContainsAny(v, `/\\`) {
			return validationError("acquired endpoint carries an unsafe identity value")
		}
	}
	if e.AcquiredAt <= 0 {
		return validationError("acquired endpoint missing acquisition timestamp")
	}
	return nil
}

// validateLaunchEvidence checks the committed launch-evidence record shape:
// the recording Operation ID, the deterministic launch identity, a full sha256
// digest of the submitted launch script/command, and the submission timestamp.
func validateLaunchEvidence(e LaunchEvidence) error {
	if e.OperationID == "" || strings.ContainsAny(e.OperationID, `/\\`) {
		return validationError("launch evidence missing operation id")
	}
	if e.LaunchID == "" || strings.ContainsAny(e.LaunchID, `/\\`) {
		return validationError("launch evidence missing launch identity")
	}
	if !domain.IsSHA256(e.CommandDigest) {
		return validationError("launch evidence command digest must be a 64-hex sha256 digest")
	}
	if e.SubmittedAt <= 0 {
		return validationError("launch evidence missing submission timestamp")
	}
	return nil
}

// validateTransferState checks the generation-bound transfer state shape.
func validateTransferState(ts TransferState) error {
	if ts.ReservationID == "" || strings.ContainsAny(ts.ReservationID, `/\\`) {
		return validationError("transfer reservation ID must be a safe non-empty value")
	}
	if ts.DestinationHome != "" && strings.ContainsAny(ts.DestinationHome, `/\\`) {
		return validationError("transfer destination home must be a safe value")
	}
	if ts.SourceHome != "" && strings.ContainsAny(ts.SourceHome, `/\\`) {
		return validationError("transfer source home must be a safe value")
	}
	if ts.SourceGeneration != 0 {
		if err := ts.SourceGeneration.Validate(); err != nil {
			return err
		}
	}
	if ts.Activation != nil {
		if err := validateTransferActivation(*ts.Activation); err != nil {
			return err
		}
	}
	return nil
}

// validateTransferActivation checks the recorded destination-activation
// evidence shape: every binding field must be present and safe, the source
// generation must be valid, and the activation digest must be a full sha256
// hex digest.
func validateTransferActivation(a TransferActivationInfo) error {
	if a.ReservationID == "" || a.TaskID == "" || a.SourceHome == "" || a.DestinationHome == "" || a.ActivationOperationID == "" {
		return validationError("transfer activation evidence is incomplete")
	}
	if err := a.SourceGeneration.Validate(); err != nil {
		return err
	}
	if err := a.DestinationGeneration.Validate(); err != nil {
		return err
	}
	for _, v := range []string{a.ReservationID, a.TaskID, a.SourceHome, a.DestinationHome, a.ActivationOperationID} {
		if strings.ContainsAny(v, `/\\`) {
			return validationError("transfer activation evidence carries an unsafe identity value")
		}
	}
	if !domain.IsSHA256(a.ActivationDigest) {
		return validationError("transfer activation digest must be a 64-hex sha256 digest")
	}
	return nil
}

func validateEndpointBinding(binding EndpointBinding) error {
	if strings.TrimSpace(binding.Backend) == "" {
		return validationError("endpoint binding missing backend")
	}
	if strings.TrimSpace(binding.Handle) == "" {
		return validationError("endpoint binding missing handle")
	}
	if strings.TrimSpace(binding.LeaseID) == "" {
		return validationError("endpoint binding missing lease id")
	}
	if strings.TrimSpace(binding.FenceToken) == "" {
		return validationError("endpoint binding missing fence token")
	}
	if binding.BoundAtUnix <= 0 {
		return validationError("endpoint binding missing bound timestamp")
	}
	return nil
}

func validateWorktreeBinding(binding WorktreeBinding) error {
	if strings.TrimSpace(binding.RepositoryIdentity) == "" {
		return validationError("worktree binding missing repository identity")
	}
	if strings.TrimSpace(binding.Path) == "" {
		return validationError("worktree binding missing path")
	}
	if strings.TrimSpace(binding.GitDir) == "" {
		return validationError("worktree binding missing git dir")
	}
	if strings.TrimSpace(binding.CommonDir) == "" {
		return validationError("worktree binding missing common dir")
	}
	if strings.TrimSpace(binding.Head) == "" {
		return validationError("worktree binding missing head")
	}
	if strings.TrimSpace(binding.LeaseID) == "" {
		return validationError("worktree binding missing lease id")
	}
	if strings.TrimSpace(binding.FenceToken) == "" {
		return validationError("worktree binding missing fence token")
	}
	if binding.BoundAtUnix <= 0 {
		return validationError("worktree binding missing bound timestamp")
	}
	return nil
}

// validateTaskID accepts safe non-empty slug identities (no path separators).
func validateTaskID(id string) error {
	if id == "" || id == "." || id == ".." || path.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return validationError("invalid task ID %q", id)
	}
	return nil
}

// clone returns a deep copy of the aggregate so committed records can never
// be aliased by callers or staged transactions.
func (a Aggregate) clone() Aggregate {
	out := a
	if a.Endpoint != nil {
		e := *a.Endpoint
		out.Endpoint = &e
	}
	if a.Worktree != nil {
		w := *a.Worktree
		out.Worktree = &w
	}
	if a.Transfer != nil {
		t := *a.Transfer
		if t.Activation != nil {
			act := *t.Activation
			t.Activation = &act
		}
		out.Transfer = &t
	}
	if a.Retirement != nil {
		e := *a.Retirement
		if e.Endpoint != nil {
			cp := *e.Endpoint
			e.Endpoint = &cp
		}
		if e.Worktree != nil {
			cp := *e.Worktree
			e.Worktree = &cp
		}
		out.Retirement = &e
	}
	if a.Launch != nil {
		l := *a.Launch
		out.Launch = &l
	}
	if a.AcquiredEndpoint != nil {
		e := *a.AcquiredEndpoint
		out.AcquiredEndpoint = &e
	}
	if a.LaunchEvidence != nil {
		e := *a.LaunchEvidence
		out.LaunchEvidence = &e
	}
	return out
}

// MarshalJSON keeps the canonical JSON representation deterministic.
func (a Aggregate) MarshalJSON() ([]byte, error) {
	type alias Aggregate
	return json.Marshal(alias(a))
}
