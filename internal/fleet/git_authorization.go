package fleet

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Git authorization writes route through the composed Task Authority (Task
// 7.4): the authoritative capability tier, authorization context, and
// elevated git mutation authorization records live inside the Authority
// Aggregate. This file retains only the read helpers and pure policy over
// the .meta projections; no authorization record is written here.
//
// Meta field keys for git authorization (post-commit .meta projections of
// the authoritative records).
const (
	MetaGitCapabilityTier = "git_capability_tier"
	MetaGitMutationAuth   = "git_mutation_authorization"
	MetaGitAuthContext    = "git_auth_context"
)

// ErrNoGitMutationAuthorization is returned when no git mutation authorization
// exists for the requested operation.
type ErrNoGitMutationAuthorization struct {
	TaskID    string
	Operation taskauthority.GitOperation
	Reason    string
}

func (e *ErrNoGitMutationAuthorization) Error() string {
	return fmt.Sprintf("no git mutation authorization for task %s operation %s: %s", e.TaskID, e.Operation, e.Reason)
}

// ErrStaleGitMutationAuthorization is returned when the expected state does
// not match the current state, indicating the ref has moved since authorization.
type ErrStaleGitMutationAuthorization struct {
	TaskID    string
	Operation taskauthority.GitOperation
	Expected  taskauthority.GitExpectedState
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
// task. The tier is read from the .meta projection (set at launch time through
// the Authority). If not set, defaults to GitTierWrite, which is backward
// compatible with the existing allowlist (add, commit, normal push, task-local
// branch creation).
func ResolveGitCapabilityTier(homeDir, taskID string) (taskauthority.GitCapabilityTier, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		// No meta file is equivalent to default tier (write).
		return taskauthority.GitTierWrite, nil
	}
	raw := meta[MetaGitCapabilityTier]
	if raw == "" {
		return taskauthority.GitTierWrite, nil // default, backward compatible
	}
	tier := taskauthority.GitCapabilityTier(raw)
	switch tier {
	case taskauthority.GitTierRead, taskauthority.GitTierWrite, taskauthority.GitTierRewrite, taskauthority.GitTierCleanup, taskauthority.GitTierAdmin:
		return tier, nil
	default:
		return taskauthority.GitTierWrite, fmt.Errorf("resolve git tier: unknown tier %q, defaulting to write", raw)
	}
}

// TierEnough returns true when the task's tier is at least the required tier.
func TierEnough(taskTier, requiredTier taskauthority.GitCapabilityTier) bool {
	order := map[taskauthority.GitCapabilityTier]int{
		taskauthority.GitTierRead:    0,
		taskauthority.GitTierWrite:   1,
		taskauthority.GitTierRewrite: 2,
		taskauthority.GitTierCleanup: 3,
		taskauthority.GitTierAdmin:   4,
	}
	return order[taskTier] >= order[requiredTier]
}

// CheckGitMutationAuthorization validates a git mutation authorization against
// the current state. It checks:
//   - An authorization record exists for the operation
//   - The expected old SHA matches the current SHA (force-with-lease semantics)
//   - The context is appropriate
//
// The record is read from the .meta projection reconciled after the
// authoritative commit (Task 7.4).
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
func CheckGitMutationAuthorization(homeDir, taskID string, op taskauthority.GitOperation, currentSHA string) (*taskauthority.GitMutationAuthorization, error) {
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

	var auth taskauthority.GitMutationAuthorization
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

// ReadGitAuthContext reads the current git authorization context from the
// .meta projection. Returns empty string if no context is set.
func ReadGitAuthContext(homeDir, taskID string) (string, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		// No meta file is equivalent to no context.
		return "", nil
	}
	return meta[MetaGitAuthContext], nil
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
// from the .meta projection without performing any checks. Returns nil if no
// authorization is stored.
func ReadGitMutationAuthorization(homeDir, taskID string) (*taskauthority.GitMutationAuthorization, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("read git mutation auth: reading meta: %w", err)
	}

	raw := meta[MetaGitMutationAuth]
	if raw == "" {
		return nil, nil
	}

	var auth taskauthority.GitMutationAuthorization
	if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return nil, fmt.Errorf("read git mutation auth: deserializing: %w", err)
	}

	return &auth, nil
}
