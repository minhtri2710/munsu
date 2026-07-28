package fleet

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/home"
)

// MarkMerged atomically transitions delivery_state to merged for a task,
// using CAS to guard against concurrent modifications.
//
// It checks the canonical identity fields (from expected), the current
// delivery_state value, and increments pr_identity_revision when the
// state was not already merged.
//
// Returns nil when delivery_state is already merged (idempotent).
// Returns a CASError when another writer modified meta concurrently.
// Returns an error when meta cannot be read or written.
func MarkMerged(homeDir, taskID string, expected *DeliveryIdentity) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("mark merged: reading meta: %w", err)
	}

	// Idempotent: if already merged, nothing to do.
	if meta[MetaDeliveryState] == string(DeliveryStateMerged) {
		return nil
	}

	// Build CAS checks from the expected identity.
	checks := map[string]string{
		"pr_provider": expected.Provider,
		"pr_owner":    expected.Owner,
		"pr_repo":     expected.Repo,
		"pr_number":   fmt.Sprintf("%d", expected.Number),
		"pr_url":      expected.URL,
		"pr_base_ref": expected.BaseRef,
		"pr_head_ref": expected.HeadRef,
		"pr_head_sha": expected.HeadSHA,
	}

	// Include delivery_state in checks only when present in meta.
	// When absent (pre-init or legacy), skip the check — the identity
	// checks are sufficient to guard against concurrent mutation.
	if currentState := meta[MetaDeliveryState]; currentState != "" {
		checks[MetaDeliveryState] = currentState
	}

	// Build updates.
	updates := map[string]string{
		MetaDeliveryState:    string(DeliveryStateMerged),
		MetaIdentityRevision: incrementRevision(meta[MetaIdentityRevision]),
	}

	if _, err := home.CompareAndSwapMeta(homeDir, taskID, checks, updates); err != nil {
		return fmt.Errorf("mark merged: %w", err)
	}

	return nil
}

// MarkMergedFromRecord is a convenience wrapper that builds a DeliveryIdentity
// from a PollRetirementRecord's fields and calls MarkMerged. It is used by
// the supervision recovery path.
//
// The record's identity fields (Provider, Owner, Repo, Number, URL, BaseRef,
// HeadRef, HeadSHA) must be non-empty and consistent.
func MarkMergedFromRecord(homeDir, taskID string, provider, owner, repo string,
	number int, url, baseRef, headRef, headSHA string) error {

	ident := &DeliveryIdentity{
		Provider: provider,
		Owner:    owner,
		Repo:     repo,
		Number:   number,
		URL:      url,
		BaseRef:  baseRef,
		HeadRef:  headRef,
		HeadSHA:  headSHA,
	}
	return MarkMerged(homeDir, taskID, ident)
}
