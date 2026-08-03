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
	Owner        string `json:"owner"`
	Description  string `json:"description,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Project      string `json:"project,omitempty"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
}

// EndpointBinding is a generation-bound runtime endpoint lease.
type EndpointBinding struct {
	Backend      string `json:"backend"`
	Handle       string `json:"handle"`
	LeaseID      string `json:"lease_id"`
	FenceToken   string `json:"fence_token"`
	SessionOwner string `json:"session_owner,omitempty"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	TabID        string `json:"tab_id,omitempty"`
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
	SchemaVersion string              `json:"schema_version"`
	TaskID        string              `json:"task_id"`
	Generation    Generation          `json:"generation"`
	Revision      Revision            `json:"revision"`
	Current       bool                `json:"current"`
	Definition    TaskDefinition      `json:"definition"`
	Phase         Phase               `json:"phase"`
	PhaseDetail   string              `json:"phase_detail,omitempty"`
	Endpoint      *EndpointBinding    `json:"endpoint,omitempty"`
	Worktree      *WorktreeBinding    `json:"worktree,omitempty"`
	Transfer      *TransferState      `json:"transfer,omitempty"`
	Retirement    *RetirementEvidence `json:"retirement,omitempty"`
}

// TaskAuthoritySchema is the deterministic schema identity for the canonical
// JSON representation of authoritative records. It is the single current
// document identity (ADR-0008 §11): internal-history v2 identities are
// replaced in place by the first supported current v1 definition.
const TaskAuthoritySchema = "munsu.task-authority/v1"

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
	return out
}

// MarshalJSON keeps the canonical JSON representation deterministic.
func (a Aggregate) MarshalJSON() ([]byte, error) {
	type alias Aggregate
	return json.Marshal(alias(a))
}
