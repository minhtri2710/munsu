package fleet

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// Meta field keys for merge authorization.
const (
	MetaMergeAuthorization = "merge_authorization"
	MetaExternalMerge      = "external_merge"
)

// MergeAuthorization is a durable authorization record that authorizes a merge
// against an exact Task Generation and head SHA. It is distinct from the
// delivery lifecycle phase (delivery_state) and provides an auditable trail.
type MergeAuthorization struct {
	TaskGeneration   string                   `json:"task_generation"`
	HeadSHA          string                   `json:"head_sha"`
	ProviderSnapshot ProviderIdentitySnapshot `json:"provider_snapshot"`
	AuthorizedAt     string                   `json:"authorized_at"`
	Authorizer       string                   `json:"authorizer"`
}

// ProviderIdentitySnapshot captures a point-in-time view of the provider
// identity fields that were valid when the authorization was created.
type ProviderIdentitySnapshot struct {
	Provider string `json:"provider"`
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	URL      string `json:"url"`
	BaseRef  string `json:"baseRef"`
	HeadRef  string `json:"headRef"`
	HeadSHA  string `json:"headSHA"`
}

// ExternalMergeRecord records that a PR/MR was merged externally (e.g., via
// GitHub UI, CLI, or automation) without munsu performing the merge. This
// allows recording external merge truth without fabricating munsu approval.
type ExternalMergeRecord struct {
	MergedSHA   string `json:"merged_sha"`
	MergedAt    string `json:"merged_at"`
	MergeSource string `json:"merge_source"` // e.g. "external"
}

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

// snapshotFromIdentity builds a ProviderIdentitySnapshot from a domain.DeliveryIdentity.
func snapshotFromIdentity(ident *domain.DeliveryIdentity) ProviderIdentitySnapshot {
	return ProviderIdentitySnapshot{
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
func identityMatchesSnapshot(snap *ProviderIdentitySnapshot, ident *domain.DeliveryIdentity) error {
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

// readMergeAuthorization reads and deserializes the merge authorization from
// meta. Returns nil if no authorization exists.
func readMergeAuthorization(meta map[string]string) (*MergeAuthorization, error) {
	raw := meta[MetaMergeAuthorization]
	if raw == "" {
		return nil, nil
	}
	var auth MergeAuthorization
	if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return nil, fmt.Errorf("reading merge authorization: %w", err)
	}
	return &auth, nil
}

// AuthorizeMerge creates a durable merge authorization for a task against an
// exact Task Generation and head SHA. The authorization is stored in meta and
// is distinct from the delivery lifecycle phase.
//
// It validates:
//   - The task has a delivery identity
//   - The provided identity matches the stored identity
//   - The provided generation matches the stored generation
//
// Returns the created MergeAuthorization on success.
// Returns typed errors for identity mismatch, generation mismatch, or missing meta.
func AuthorizeMerge(homeDir, taskID, generation string, expected *domain.DeliveryIdentity) (*MergeAuthorization, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("authorize merge: reading meta: %w", err)
	}

	// Validate generation
	metaGeneration := meta["generation"]
	if metaGeneration == "" {
		return nil, fmt.Errorf("authorize merge: task %s has no generation in meta; cannot authorize without generation", taskID)
	}
	if metaGeneration != generation {
		return nil, fmt.Errorf("authorize merge: task generation mismatch: meta=%q provided=%q", metaGeneration, generation)
	}

	// Read stored identity
	stored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("authorize merge: reading stored identity: %w", err)
	}
	if stored == nil {
		return nil, fmt.Errorf("authorize merge: no delivery identity for task %s; run pr-check first", taskID)
	}

	// Validate the provided identity matches the stored identity
	if expected == nil {
		return nil, fmt.Errorf("authorize merge: expected identity is nil")
	}
	if stored.HeadSHA != expected.HeadSHA {
		return nil, fmt.Errorf("authorize merge: identity mismatch: stored head SHA %q differs from provided %q", stored.HeadSHA, expected.HeadSHA)
	}

	// Build authorization
	auth := &MergeAuthorization{
		TaskGeneration:   generation,
		HeadSHA:          stored.HeadSHA,
		ProviderSnapshot: snapshotFromIdentity(stored),
		AuthorizedAt:     time.Now().UTC().Format(time.RFC3339),
		Authorizer:       "general",
	}

	authJSON, err := json.Marshal(auth)
	if err != nil {
		return nil, fmt.Errorf("authorize merge: serializing: %w", err)
	}

	// Persist atomically via CAS: check that the identity and head haven't changed
	checks := map[string]string{
		"pr_head_sha": stored.HeadSHA,
		"generation":  metaGeneration,
	}
	// Include delivery_state in checks when present
	if ds := meta[MetaDeliveryState]; ds != "" {
		checks[MetaDeliveryState] = ds
	}

	updates := map[string]string{
		MetaMergeAuthorization: string(authJSON),
	}

	if _, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates); err != nil {
		return nil, fmt.Errorf("authorize merge: cas: %w", err)
	}

	return auth, nil
}

// CheckMergeAuthorization checks whether a task has a valid merge authorization
// for the given identity. Returns the authorization on success, or a typed error
// on failure:
//   - ErrNoMergeAuthorization: no authorization exists
//   - ErrStaleAuthorization: head SHA has changed since authorization
//   - error: mismatched provider identity or other error
//
// The provider identity check validates that the stored snapshot matches the
// provided identity across all provider-identity fields (provider, owner, repo,
// PR number, URL, base ref, head ref). This catches branch replacement and
// repo retargeting after authorization was granted.
func CheckMergeAuthorization(homeDir, taskID string, expected *domain.DeliveryIdentity) (*MergeAuthorization, error) {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("check merge authorization: reading meta: %w", err)
	}

	// Read stored authorization
	auth, err := readMergeAuthorization(meta)
	if err != nil {
		return nil, fmt.Errorf("check merge authorization: %w", err)
	}
	if auth == nil {
		return nil, &ErrNoMergeAuthorization{
			TaskID: taskID,
			Reason: "no authorization record found in meta",
		}
	}

	// Check that the provided identity matches the snapshot (catches branch
	// replacement, repo retargeting, etc.)
	if err := identityMatchesSnapshot(&auth.ProviderSnapshot, expected); err != nil {
		return nil, fmt.Errorf("check merge authorization: provider identity mismatch since authorization: %w", err)
	}

	// Check that the current stored head SHA matches the authorized head SHA
	currentHead := meta["pr_head_sha"]
	if currentHead == "" {
		currentHead = meta["pr_head"]
	}
	if currentHead != auth.HeadSHA {
		return nil, &ErrStaleAuthorization{
			TaskID:         taskID,
			AuthorizedHead: auth.HeadSHA,
			CurrentHead:    currentHead,
			Reason:         "head SHA changed since authorization",
		}
	}

	return auth, nil
}

// RecordExternalMerge records that a PR/MR was merged by an external actor
// (e.g., GitHub UI, CI/CD, or another tool) without munsu performing the merge.
// This allows recording external merge truth without fabricating munsu approval.
//
// It validates:
//   - The task has a delivery identity matching the provided identity
//   - The task is not already merged (idempotent if already merged)
//
// On success, transitions delivery_state to merged and records the external
// merge evidence.
func RecordExternalMerge(homeDir, taskID, mergedSHA string, expected *domain.DeliveryIdentity) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("record external merge: reading meta: %w", err)
	}

	// Idempotent: if already merged, nothing to do
	if meta[MetaDeliveryState] == string(DeliveryStateMerged) {
		return nil
	}

	// Read stored identity
	stored, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return fmt.Errorf("record external merge: reading stored identity: %w", err)
	}
	if stored == nil {
		return fmt.Errorf("record external merge: no delivery identity for task %s; run pr-check first", taskID)
	}

	// Validate the provided identity matches the stored identity
	if expected == nil {
		return fmt.Errorf("record external merge: expected identity is nil")
	}
	storedSnap := snapshotFromIdentity(stored)
	if err := identityMatchesSnapshot(&storedSnap, expected); err != nil {
		return fmt.Errorf("record external merge: identity mismatch: %w", err)
	}

	// Build external merge record
	ext := &ExternalMergeRecord{
		MergedSHA:   mergedSHA,
		MergedAt:    time.Now().UTC().Format(time.RFC3339),
		MergeSource: "external",
	}

	extJSON, err := json.Marshal(ext)
	if err != nil {
		return fmt.Errorf("record external merge: serializing: %w", err)
	}

	// CAS: verify identity hasn't changed, then update state
	checks := identityChecks(stored)
	// Include delivery_state in checks when present
	if ds := meta[MetaDeliveryState]; ds != "" {
		checks[MetaDeliveryState] = ds
	}
	checks[MetaIdentityRevision] = meta[MetaIdentityRevision]

	updates := map[string]string{
		MetaDeliveryState:    string(DeliveryStateMerged),
		MetaExternalMerge:    string(extJSON),
		MetaIdentityRevision: incrementRevision(meta[MetaIdentityRevision]),
	}

	if _, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates); err != nil {
		return fmt.Errorf("record external merge: cas: %w", err)
	}

	return nil
}