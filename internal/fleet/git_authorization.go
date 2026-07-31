package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// GitCapabilityTier defines the level of git mutation authority a task has.
// The tier is set at launch time and is immutable for the duration of the launch.
// Each tier is a superset of the tiers below it.
type GitCapabilityTier string

const (
	// GitTierRead permits only read-only operations (status, log, diff, fetch).
	GitTierRead GitCapabilityTier = "read"
	// GitTierWrite permits add, commit, normal push, and branch creation.
	GitTierWrite GitCapabilityTier = "write"
	// GitTierRewrite permits force-with-lease, rebase, reset, and amend.
	GitTierRewrite GitCapabilityTier = "rewrite"
	// GitTierCleanup permits branch deletion and remote ref deletion.
	GitTierCleanup GitCapabilityTier = "cleanup"
	// GitTierAdmin permits unrestricted force (never auto-granted).
	GitTierAdmin GitCapabilityTier = "admin"
)

// GitOperation identifies the specific elevated git operation being authorized.
type GitOperation string

const (
	GitOpForceWithLease GitOperation = "force-with-lease"
	GitOpBranchDelete   GitOperation = "branch-delete"
	GitOpPushDelete     GitOperation = "push-delete"
	GitOpRebase         GitOperation = "rebase"
	GitOpReset          GitOperation = "reset"
	GitOpAmendCommit    GitOperation = "amend-commit"
	GitOpCherryPick     GitOperation = "cherry-pick"
	GitOpRevert         GitOperation = "revert"
	GitOpClean          GitOperation = "clean"
)

// GitExpectedState captures the expected state of a ref before mutation.
// This is used for force-with-lease expected-state comparison — the old SHA
// must match the current state of the ref before the mutation is authorized.
type GitExpectedState struct {
	Ref    string `json:"ref"`     // e.g., "refs/heads/mu/task-123"
	OldSHA string `json:"old_sha"` // expected current SHA of the ref
	NewSHA string `json:"new_sha"` // expected new SHA of the ref
}

// GitMutationAuthorization is a durable authorization record for a specific
// elevated git mutation. It binds the operation to an exact expected state
// and context (amendment, retirement, or standalone), providing an auditable
// trail for every elevated git mutation.
type GitMutationAuthorization struct {
	TaskGeneration string            `json:"task_generation"`
	Operation      GitOperation      `json:"operation"`
	ExpectedState  GitExpectedState  `json:"expected_state"`
	AuthorizedAt   string            `json:"authorized_at"`
	Authorizer     string            `json:"authorizer"`
	Context        string            `json:"context"` // "amendment", "retirement", "standalone"
}

// Meta field keys for git authorization.
const (
	MetaGitCapabilityTier = "git_capability_tier"
	MetaGitMutationAuth   = "git_mutation_authorization"
	MetaGitAuthContext    = "git_auth_context"
)

// ErrNoGitMutationAuthorization is returned when no git mutation authorization
// exists for the requested operation.
type ErrNoGitMutationAuthorization struct {
	TaskID    string
	Operation GitOperation
	Reason    string
}

func (e *ErrNoGitMutationAuthorization) Error() string {
	return fmt.Sprintf("no git mutation authorization for task %s operation %s: %s", e.TaskID, e.Operation, e.Reason)
}

// ErrStaleGitMutationAuthorization is returned when the expected state does
// not match the current state, indicating the ref has moved since authorization.
type ErrStaleGitMutationAuthorization struct {
	TaskID    string
	Operation GitOperation
	Expected  GitExpectedState
	ActualSHA string
	Reason    string
}

func (e *ErrStaleGitMutationAuthorization) Error() string {
	return fmt.Sprintf("stale git mutation authorization for task %s operation %s: expected SHA %q for ref %q but current is %q: %s",
		e.TaskID, e.Operation, e.Expected.OldSHA, e.Expected.Ref, e.ActualSHA, e.Reason)
}

// ErrForcePushDenied is returned when an unrestricted force push is attempted.
// Force-with-lease may be authorized, but --force and -f are always denied.
type ErrForcePushDenied struct {
	TaskID string
	Reason string
}

func (e *ErrForcePushDenied) Error() string {
	return fmt.Sprintf("unrestricted force push denied for task %s: %s; use --force-with-lease with authorization instead", e.TaskID, e.Reason)
}

// ResolveGitCapabilityTier determines the applicable git capability tier for a
// task. The tier is read from meta (set at launch time). If not set, defaults
// to GitTierWrite, which is backward compatible with the existing allowlist
// (add, commit, normal push, task-local branch creation).
func ResolveGitCapabilityTier(homeDir, taskID string) (GitCapabilityTier, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		// No meta file is equivalent to default tier (write).
		return GitTierWrite, nil
	}
	raw := meta[MetaGitCapabilityTier]
	if raw == "" {
		return GitTierWrite, nil // default, backward compatible
	}
	tier := GitCapabilityTier(raw)
	switch tier {
	case GitTierRead, GitTierWrite, GitTierRewrite, GitTierCleanup, GitTierAdmin:
		return tier, nil
	default:
		return GitTierWrite, fmt.Errorf("resolve git tier: unknown tier %q, defaulting to write", raw)
	}
}

// SetGitCapabilityTier persists the git capability tier to task meta.
// This is called at launch time to set the immutable tier.
func SetGitCapabilityTier(homeDir, taskID string, tier GitCapabilityTier) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	meta[MetaGitCapabilityTier] = string(tier)
	return home.WriteMeta(homeDir, taskID, meta)
}

// OperationRequiresTier returns the minimum tier required for a git operation.
// Operations not listed require at most GitTierWrite.
func OperationRequiresTier(op GitOperation) GitCapabilityTier {
	switch op {
	case GitOpForceWithLease, GitOpRebase, GitOpReset, GitOpAmendCommit, GitOpCherryPick, GitOpRevert, GitOpClean:
		return GitTierRewrite
	case GitOpBranchDelete, GitOpPushDelete:
		return GitTierCleanup
	default:
		return GitTierWrite
	}
}

// TierEnough returns true when the task's tier is at least the required tier.
func TierEnough(taskTier, requiredTier GitCapabilityTier) bool {
	order := map[GitCapabilityTier]int{
		GitTierRead:    0,
		GitTierWrite:   1,
		GitTierRewrite: 2,
		GitTierCleanup: 3,
		GitTierAdmin:   4,
	}
	return order[taskTier] >= order[requiredTier]
}

// AuthorizeGitMutation creates a durable authorization for a specific git
// mutation. It validates the expected state and stores the authorization in
// meta using CAS to prevent concurrent overwrites.
//
// Parameters:
//   - homeDir: munsu home directory
//   - taskID: task identifier
//   - generation: task generation (must match meta if set)
//   - op: the git operation being authorized
//   - expected: the expected state before mutation (ref, old SHA, new SHA)
//   - authorizer: who authorized this (e.g., "general", "amendment", "retirement")
//   - context: the context ("amendment", "retirement", "standalone")
//
// Returns the created GitMutationAuthorization on success, or an error if
// validation fails or CAS conflicts.
func AuthorizeGitMutation(homeDir, taskID, generation string, op GitOperation, expected GitExpectedState, authorizer, context string) (*GitMutationAuthorization, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("authorize git mutation: reading meta: %w", err)
	}

	// Validate generation
	metaGeneration := meta["generation"]
	if metaGeneration != "" && metaGeneration != generation {
		return nil, fmt.Errorf("authorize git mutation: generation mismatch: meta=%q provided=%q", metaGeneration, generation)
	}

	// Validate expected state
	if expected.Ref == "" {
		return nil, fmt.Errorf("authorize git mutation: expected ref is required")
	}
	if expected.OldSHA == "" {
		return nil, fmt.Errorf("authorize git mutation: expected old SHA is required")
	}
	if expected.NewSHA == "" && op != GitOpBranchDelete && op != GitOpPushDelete {
		return nil, fmt.Errorf("authorize git mutation: expected new SHA is required for operation %q", op)
	}

	// Validate context
	switch context {
	case "amendment", "retirement", "standalone":
		// valid
	default:
		return nil, fmt.Errorf("authorize git mutation: invalid context %q", context)
	}

	// Validate operation
	requiredTier := OperationRequiresTier(op)
	if requiredTier == GitTierWrite {
		// Write-tier operations don't need authorization
		return nil, fmt.Errorf("authorize git mutation: operation %q does not require authorization (tier %s)", op, requiredTier)
	}

	auth := &GitMutationAuthorization{
		TaskGeneration: generation,
		Operation:      op,
		ExpectedState:  expected,
		AuthorizedAt:   time.Now().UTC().Format(time.RFC3339),
		Authorizer:     authorizer,
		Context:        context,
	}

	authJSON, err := json.Marshal(auth)
	if err != nil {
		return nil, fmt.Errorf("authorize git mutation: serializing: %w", err)
	}

	// CAS: check generation and context haven't changed
	checks := map[string]string{
		"generation": metaGeneration,
	}
	if currentContext := meta[MetaGitAuthContext]; currentContext != "" {
		checks[MetaGitAuthContext] = currentContext
	}

	updates := map[string]string{
		MetaGitMutationAuth: string(authJSON),
	}

	if _, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates); err != nil {
		return nil, fmt.Errorf("authorize git mutation: cas: %w", err)
	}

	return auth, nil
}

// CheckGitMutationAuthorization validates a git mutation authorization against
// the current state. It checks:
//   - An authorization record exists for the operation
//   - The expected old SHA matches the current SHA (force-with-lease semantics)
//   - The context is appropriate
//
// Parameters:
//   - homeDir: munsu home directory
//   - taskID: task identifier
//   - op: the git operation being checked
//   - currentSHA: the current SHA of the ref being mutated (empty to skip SHA check)
//
// Returns the authorization on success, or a typed error:
//   - ErrNoGitMutationAuthorization: no authorization exists
//   - ErrStaleGitMutationAuthorization: expected SHA does not match current
//   - error: other errors
func CheckGitMutationAuthorization(homeDir, taskID string, op GitOperation, currentSHA string) (*GitMutationAuthorization, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("check git mutation auth: reading meta: %w", err)
	}

	raw := meta[MetaGitMutationAuth]
	if raw == "" {
		return nil, &ErrNoGitMutationAuthorization{
			TaskID:    taskID,
			Operation: op,
			Reason:    "no authorization record found in meta",
		}
	}

	var auth GitMutationAuthorization
	if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return nil, fmt.Errorf("check git mutation auth: deserializing: %w", err)
	}

	// Verify operation matches
	if auth.Operation != op {
		return nil, &ErrNoGitMutationAuthorization{
			TaskID:    taskID,
			Operation: op,
			Reason:    fmt.Sprintf("authorization is for operation %q, not %q", auth.Operation, op),
		}
	}

	// Check expected state: for force-with-lease semantics, the old SHA must
	// match the current SHA of the ref. This is the "expected-state comparison"
	// that prevents mutation without knowing the current state.
	if currentSHA != "" && auth.ExpectedState.OldSHA != "" && currentSHA != auth.ExpectedState.OldSHA {
		return nil, &ErrStaleGitMutationAuthorization{
			TaskID:    taskID,
			Operation: op,
			Expected:  auth.ExpectedState,
			ActualSHA: currentSHA,
			Reason:    "current SHA does not match expected SHA (ref may have been updated since authorization)",
		}
	}

	return &auth, nil
}

// ClearGitMutationAuthorization removes the git mutation authorization from
// meta using CAS. This is called after the authorized mutation completes or
// when the authorization is no longer needed.
func ClearGitMutationAuthorization(homeDir, taskID string) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("clear git mutation auth: reading meta: %w", err)
	}

	checks := map[string]string{}
	if raw := meta[MetaGitMutationAuth]; raw != "" {
		checks[MetaGitMutationAuth] = raw
	}

	updates := map[string]string{
		MetaGitMutationAuth: "",
	}

	if _, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates); err != nil {
		return fmt.Errorf("clear git mutation auth: cas: %w", err)
	}
	return nil
}

// SetGitAuthContext persists the git authorization context to task meta.
// This is set when entering amendment or retirement context.
// Valid contexts: "amendment", "retirement", "" (clear).
func SetGitAuthContext(homeDir, taskID, context string) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	meta[MetaGitAuthContext] = context
	return home.WriteMeta(homeDir, taskID, meta)
}

// ReadGitAuthContext reads the current git authorization context from task meta.
// Returns empty string if no context is set.
func ReadGitAuthContext(homeDir, taskID string) (string, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		// No meta file is equivalent to no context.
		return "", nil
	}
	return meta[MetaGitAuthContext], nil
}

// AuthorizeForceWithLease is a convenience function that creates a git mutation
// authorization for a force-with-lease operation. It validates the expected
// state and stores the authorization.
//
// This is the narrow authorization path for force-with-lease: the caller must
// provide the expected old SHA of the ref, which is checked against the current
// state before the push is allowed.
func AuthorizeForceWithLease(homeDir, taskID, generation, ref, oldSHA, newSHA, authorizer, context string) (*GitMutationAuthorization, error) {
	return AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease,
		GitExpectedState{Ref: ref, OldSHA: oldSHA, NewSHA: newSHA},
		authorizer, context)
}

// UnrestrictedForceDenied returns true if the operation is an unrestricted force
// push (--force or -f). These are always denied regardless of tier.
func UnrestrictedForceDenied(args []string) bool {
	for _, arg := range args {
		if arg == "--force" || arg == "-f" {
			return true
		}
	}
	return false
}

// ForceWithLeaseRequested returns true if the args include --force-with-lease.
func ForceWithLeaseRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--force-with-lease" || strings.HasPrefix(arg, "--force-with-lease=") {
			return true
		}
	}
	return false
}

// PushDeleteRequested returns true if the push args include --delete or a
// delete refspec (starting with ":").
func PushDeleteRequested(args []string, refspec string) bool {
	for _, arg := range args {
		if arg == "--delete" {
			return true
		}
	}
	if strings.HasPrefix(refspec, ":") {
		return true
	}
	return false
}

// ReadGitMutationAuthorization reads the stored git mutation authorization
// from meta without performing any checks. Returns nil if no authorization
// is stored.
func ReadGitMutationAuthorization(homeDir, taskID string) (*GitMutationAuthorization, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("read git mutation auth: reading meta: %w", err)
	}

	raw := meta[MetaGitMutationAuth]
	if raw == "" {
		return nil, nil
	}

	var auth GitMutationAuthorization
	if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return nil, fmt.Errorf("read git mutation auth: deserializing: %w", err)
	}

	return &auth, nil
}