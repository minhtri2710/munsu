package fleet

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Merge authorization writes route through the composed Task Authority (Task
// 7.4): the authoritative merge authorization and external merge evidence
// records live inside the Authority Aggregate. This file retains the read
// helper over the canonical record and the pure identity-matching helpers;
// no authorization record is written here.
//
// Meta field keys for merge authorization (post-commit .meta projections of
// the authoritative records).
const (
	MetaMergeAuthorization = "merge_authorization"
	MetaExternalMerge      = "external_merge"
)

// ErrNoMergeAuthorization is returned when no merge authorization exists for
// a task. The caller should run `munsu delivery authorize <id>` to create one.
type ErrNoMergeAuthorization struct {
	TaskID string
	Reason string
}

func (e *ErrNoMergeAuthorization) Error() string {
	return fmt.Sprintf("no merge authorization for task %s: %s; run munsu delivery authorize %s", e.TaskID, e.Reason, e.TaskID)
}

// ErrStaleAuthorization is returned when the stored authorization references
// a different head SHA than the current stored identity. This indicates the
// PR head has changed since authorization was granted.
type ErrStaleAuthorization struct {
	TaskID         string
	AuthorizedHead string
	CurrentHead    string
	Reason         string
}

func (e *ErrStaleAuthorization) Error() string {
	return fmt.Sprintf("stale merge authorization for task %s: authorized head %q differs from current head %q; re-authorize after pr-amend or reconcile",
		e.TaskID, e.AuthorizedHead, e.CurrentHead)
}

// snapshotFromIdentity builds a taskauthority.ProviderIdentitySnapshot from a
// domain.DeliveryIdentity.
func snapshotFromIdentity(ident *domain.DeliveryIdentity) taskauthority.ProviderIdentitySnapshot {
	return taskauthority.ProviderIdentitySnapshot{
		Provider: ident.Provider,
		Owner:    ident.Owner,
		Repo:     ident.Repo,
		Number:   ident.Number,
		URL:      ident.URL,
		BaseRef:  ident.BaseRef,
		HeadRef:  ident.HeadRef,
		HeadSHA:  ident.HeadSHA,
	}
}

// identityMatchesSnapshot checks whether the identity fields in a snapshot
// match those in a delivery identity. Returns nil on match, or an error
// describing the first mismatch.
func identityMatchesSnapshot(snap *taskauthority.ProviderIdentitySnapshot, ident *domain.DeliveryIdentity) error {
	if ident == nil {
		return fmt.Errorf("identity is nil")
	}
	if snap.Provider != ident.Provider {
		return fmt.Errorf("provider mismatch: snapshot=%q identity=%q", snap.Provider, ident.Provider)
	}
	if snap.Owner != ident.Owner {
		return fmt.Errorf("owner mismatch: snapshot=%q identity=%q", snap.Owner, ident.Owner)
	}
	if snap.Repo != ident.Repo {
		return fmt.Errorf("repo mismatch: snapshot=%q identity=%q", snap.Repo, ident.Repo)
	}
	if snap.Number != ident.Number {
		return fmt.Errorf("PR number mismatch: snapshot=%d identity=%d", snap.Number, ident.Number)
	}
	if snap.URL != ident.URL {
		return fmt.Errorf("URL mismatch: snapshot=%q identity=%q", snap.URL, ident.URL)
	}
	if snap.BaseRef != ident.BaseRef {
		return fmt.Errorf("base ref mismatch: snapshot=%q identity=%q", snap.BaseRef, ident.BaseRef)
	}
	if snap.HeadRef != ident.HeadRef {
		return fmt.Errorf("head ref mismatch: snapshot=%q identity=%q", snap.HeadRef, ident.HeadRef)
	}
	return nil
}

// CheckMergeAuthorization checks whether a task has a valid merge authorization
// against the canonical record in the composed Task Authority (Task 7.4).
// Returns the authorization on success, or a typed error:
//   - ErrNoMergeAuthorization: no authorization exists
//   - ErrStaleAuthorization: head SHA has changed since authorization
//   - error: mismatched provider identity or other error
//
// The provider identity check validates that the committed snapshot matches
// the provided identity across all provider-identity fields (provider, owner,
// repo, PR number, URL, base ref, head ref). This catches branch replacement
// and repo retargeting after authorization was granted. The provided
// identity's HeadSHA is the current head: a changed head invalidates the
// stale authorization (never silent reuse).
func CheckMergeAuthorization(auth *taskauthority.Authority, taskID string, expected *domain.DeliveryIdentity) (*taskauthority.MergeAuthorization, error) {
	if auth == nil {
		return nil, fmt.Errorf("check merge authorization: requires a composed task authority")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		return nil, fmt.Errorf("check merge authorization: %w", err)
	}
	rec := agg.MergeAuthorization
	if rec == nil {
		return nil, &ErrNoMergeAuthorization{
			TaskID: taskID,
			Reason: "no authorization record in the task authority",
		}
	}

	// Check that the provided identity matches the snapshot (catches branch
	// replacement, repo retargeting, etc.)
	if err := identityMatchesSnapshot(&rec.ProviderSnapshot, expected); err != nil {
		return nil, fmt.Errorf("check merge authorization: provider identity mismatch since authorization: %w", err)
	}

	// Check that the current head SHA matches the authorized head SHA.
	if expected.HeadSHA != rec.HeadSHA {
		return nil, &ErrStaleAuthorization{
			TaskID:         taskID,
			AuthorizedHead: rec.HeadSHA,
			CurrentHead:    expected.HeadSHA,
			Reason:         "head SHA changed since authorization",
		}
	}

	return rec, nil
}
