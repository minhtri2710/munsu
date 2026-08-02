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

// Aggregate is the authoritative record of one Task Generation.
type Aggregate struct {
	SchemaVersion                string           `json:"schema_version"`
	TaskID                       string           `json:"task_id"`
	Generation                   Generation       `json:"generation"`
	Revision                     Revision         `json:"revision"`
	Current                      bool             `json:"current"`
	Definition                   TaskDefinition   `json:"definition"`
	Phase                        Phase            `json:"phase"`
	PhaseDetail                  string           `json:"phase_detail,omitempty"`
	Endpoint                     *EndpointBinding `json:"endpoint,omitempty"`
	Worktree                     *WorktreeBinding `json:"worktree,omitempty"`
	DispatchInterpretationID     string           `json:"dispatch_interpretation_id,omitempty"`
	DispatchInterpretationDigest string           `json:"dispatch_interpretation_digest,omitempty"`
	// IssueLinks is the generation-bound definition record of the task's
	// issue links; IssueLinkReconciliation is the provider evidence of one
	// post-merge reconciliation committed with the same operation (Task 7.2).
	IssueLinks              []domain.IssueLink                     `json:"issue_links,omitempty"`
	IssueLinkReconciliation []domain.IssueLinkReconciliationResult `json:"issue_link_reconciliation,omitempty"`
	// DeliveryPlan and CapabilityAttestation are the generation-bound
	// delivery-plan and capability-attestation definition records committed
	// together by the AttachAttestation operation (Task 7.3, ADR-0004 §6).
	// The plan records the bounded requested → effective mode transition with
	// its fallback reason; the attestation reference binds project, home, and
	// config snapshot digest. Runtime capability observation data stays
	// outside the Aggregate.
	DeliveryPlan          *DeliveryPlan          `json:"delivery_plan,omitempty"`
	CapabilityAttestation *CapabilityAttestation `json:"capability_attestation,omitempty"`
	// MergeAuthorization is the generation-bound merge authorization record
	// committed by the AuthorizeMerge operation: it binds the provider
	// identity snapshot and the immutable head SHA the merge was authorized
	// against (Task 7.4). A changed head makes the prior authorization stale
	// and is never silently reused. ExternalMerge is the generation-bound
	// evidence record of an external merge committed by RecordExternalMerge.
	MergeAuthorization *MergeAuthorization  `json:"merge_authorization,omitempty"`
	ExternalMerge      *ExternalMergeRecord `json:"external_merge,omitempty"`
	// GitCapabilityTier, GitAuthContext, and GitMutationAuthorization are the
	// generation-bound git authorization records committed by the
	// SetGitCapabilityTier, SetGitAuthContext, and
	// AuthorizeGitMutation/ClearGitMutationAuthorization operations (Task
	// 7.4): the launch capability tier, the amendment/retirement context, and
	// the elevated git mutation authorization with its exact expected state.
	GitCapabilityTier        string                    `json:"git_capability_tier,omitempty"`
	GitAuthContext           string                    `json:"git_auth_context,omitempty"`
	GitMutationAuthorization *GitMutationAuthorization `json:"git_mutation_authorization,omitempty"`
	// DeliveryPrepare is the generation-bound delivery preparation record
	// committed by the PrepareDelivery operation (Task 7.5): the provider
	// identity snapshot, the immutable head SHA the delivery is prepared
	// against, and the review-ready delivery state. DeliveryTerminal is the
	// generation-bound terminal evidence record committed by the
	// CompleteDelivery operation: the delivered/done terminal transition,
	// the exact head, and the terminal provider evidence. resolved is never
	// a delivery terminal state.
	DeliveryPrepare  *DeliveryPrepare  `json:"delivery_prepare,omitempty"`
	DeliveryTerminal *DeliveryTerminal `json:"delivery_terminal,omitempty"`
	// MergeAttempt is the generation-bound merge attempt and outcome record
	// committed by the RecordMergeAttempt operation (Task 7.6): the stable
	// attempt identity binds the provider identity, PR identity, and exact
	// head SHA with the provider-verified remote outcome. A remote-unknown
	// outcome is terminal: once committed, the Authority refuses further
	// provider-mutating attempts and only read reconciliation is allowed.
	// Verified merged truth is never erased by a later ambiguous or
	// false-negative read.
	MergeAttempt *MergeAttempt `json:"merge_attempt,omitempty"`
}

// TaskAuthoritySchema is the deterministic schema identity for the canonical
// JSON representation of authoritative records.
const TaskAuthoritySchema = "munsu.task-authority/v2"

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
	if err := validateIssueLinkDefinition(agg); err != nil {
		return err
	}
	if err := validateDeliveryDefinition(agg); err != nil {
		return err
	}
	if err := validateAuthorizationDefinition(agg); err != nil {
		return err
	}
	if err := validateDeliveryRecord(agg); err != nil {
		return err
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
	if a.IssueLinks != nil {
		out.IssueLinks = append([]domain.IssueLink(nil), a.IssueLinks...)
	}
	if a.IssueLinkReconciliation != nil {
		out.IssueLinkReconciliation = append([]domain.IssueLinkReconciliationResult(nil), a.IssueLinkReconciliation...)
	}
	if a.DeliveryPlan != nil {
		p := *a.DeliveryPlan
		out.DeliveryPlan = &p
	}
	if a.CapabilityAttestation != nil {
		c := *a.CapabilityAttestation
		out.CapabilityAttestation = &c
	}
	if a.MergeAuthorization != nil {
		m := *a.MergeAuthorization
		out.MergeAuthorization = &m
	}
	if a.ExternalMerge != nil {
		e := *a.ExternalMerge
		out.ExternalMerge = &e
	}
	if a.GitMutationAuthorization != nil {
		g := *a.GitMutationAuthorization
		out.GitMutationAuthorization = &g
	}
	if a.DeliveryPrepare != nil {
		p := *a.DeliveryPrepare
		out.DeliveryPrepare = &p
	}
	if a.DeliveryTerminal != nil {
		tr := *a.DeliveryTerminal
		out.DeliveryTerminal = &tr
	}
	if a.MergeAttempt != nil {
		m := *a.MergeAttempt
		out.MergeAttempt = &m
	}
	return out
}

// MarshalJSON keeps the canonical JSON representation deterministic.
func (a Aggregate) MarshalJSON() ([]byte, error) {
	type alias Aggregate
	return json.Marshal(alias(a))
}
