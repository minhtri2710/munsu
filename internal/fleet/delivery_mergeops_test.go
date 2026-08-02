//go:build integration

package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func identityFor(owner, repo string, number int, headSHA string) *domain.DeliveryIdentity {
	return &domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      owner,
		Repo:       repo,
		Number:     number,
		URL:        "https://github.com/" + owner + "/" + repo + "/pull/" + fmt.Sprintf("%d", number),
		BaseRef:    "main",
		HeadRef:    "feature",
		HeadSHA:    headSHA,
		CapturedAt: "2024-01-01T00:00:00Z",
	}
}

// mergeTestAuth seeds one ship task in an in-memory Authority for a merge
// outcome test (Task 7.6): the merge attempt commits fenced to the task
// generation, so the task must exist in the Authority.
func mergeTestAuth(t *testing.T, taskID string) *taskauthority.Authority {
	t.Helper()
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-" + taskID,
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      taskID,
		Owner:       "owner",
		Kind:        "ship",
		Reason:      "create",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return auth
}

func TestMarkMerged_TransitionsToMerged(t *testing.T) {
	home := t.TempDir()
	taskID := "test-ship"

	// Write initial meta with review-ready state.
	meta := map[string]string{
		"kind":                 "ship",
		"delivery_state":       string(DeliveryStateReviewReady),
		"pr_provider":          "github",
		"pr_owner":             "testowner",
		"pr_repo":              "testrepo",
		"pr_number":            "42",
		"pr_url":               "https://github.com/testowner/testrepo/pull/42",
		"pr_base_ref":          "main",
		"pr_head_ref":          "feature",
		"pr_head_sha":          "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		"pr_timestamp":         "2024-01-01T00:00:00Z",
		"pr_identity_revision": "1",
	}
	if err := mhome.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	ident := identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1")

	if err := MarkMerged(home, taskID, ident, mergeTestAuth(t, taskID)); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}

	// Verify delivery_state is merged.
	result, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if result[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Fatalf("expected delivery_state=%q, got %q", DeliveryStateMerged, result[MetaDeliveryState])
	}

	// Verify revision was incremented.
	// rev "1" -> "2"
	if result[MetaIdentityRevision] != "2" {
		t.Fatalf("expected revision=2, got %q", result[MetaIdentityRevision])
	}

	// Verify other meta is preserved.
	if result["kind"] != "ship" {
		t.Fatal("kind should be preserved")
	}
}

func TestMarkMerged_Idempotent(t *testing.T) {
	home := t.TempDir()
	taskID := "test-ship"

	// Start with already-merged state.
	meta := map[string]string{
		"delivery_state":       string(DeliveryStateMerged),
		"pr_provider":          "github",
		"pr_owner":             "testowner",
		"pr_repo":              "testrepo",
		"pr_number":            "42",
		"pr_url":               "https://github.com/testowner/testrepo/pull/42",
		"pr_base_ref":          "main",
		"pr_head_ref":          "feature",
		"pr_head_sha":          "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		"pr_timestamp":         "2024-01-01T00:00:00Z",
		"pr_identity_revision": "5",
	}
	if err := mhome.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	ident := identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1")

	// First call: idempotent.
	if err := MarkMerged(home, taskID, ident, mergeTestAuth(t, taskID)); err != nil {
		t.Fatalf("first MarkMerged: %v", err)
	}

	// Revision should still be "5" (not incremented).
	result, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if result[MetaIdentityRevision] != "5" {
		t.Fatalf("expected revision=5 (unchanged), got %q", result[MetaIdentityRevision])
	}

	// Second call: still idempotent.
	if err := MarkMerged(home, taskID, ident, mergeTestAuth(t, taskID)); err != nil {
		t.Fatalf("second MarkMerged: %v", err)
	}
}

func TestMarkMerged_EmptyDeliveryState(t *testing.T) {
	// Meta without delivery_state (legacy or pre-init).
	home := t.TempDir()
	taskID := "test-ship"

	meta := map[string]string{
		"kind":         "ship",
		"pr_provider":  "github",
		"pr_owner":     "testowner",
		"pr_repo":      "testrepo",
		"pr_number":    "42",
		"pr_url":       "https://github.com/testowner/testrepo/pull/42",
		"pr_base_ref":  "main",
		"pr_head_ref":  "feature",
		"pr_head_sha":  "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		"pr_timestamp": "2024-01-01T00:00:00Z",
	}
	if err := mhome.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	ident := identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1")

	if err := MarkMerged(home, taskID, ident, mergeTestAuth(t, taskID)); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}

	result, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if result[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Fatalf("expected delivery_state=%q, got %q", DeliveryStateMerged, result[MetaDeliveryState])
	}
}

func TestMarkMerged_CASFailOnIdentityMismatch(t *testing.T) {
	home := t.TempDir()
	taskID := "test-ship"

	meta := map[string]string{
		"delivery_state":       string(DeliveryStateReviewReady),
		"pr_provider":          "github",
		"pr_owner":             "testowner",
		"pr_repo":              "testrepo",
		"pr_number":            "42",
		"pr_url":               "https://github.com/testowner/testrepo/pull/42",
		"pr_base_ref":          "main",
		"pr_head_ref":          "feature",
		"pr_head_sha":          "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		"pr_timestamp":         "2024-01-01T00:00:00Z",
		"pr_identity_revision": "1",
	}
	if err := mhome.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Use a wrong identity (different head SHA).
	wrongIdent := &domain.DeliveryIdentity{
		Provider: "github",
		Owner:    "testowner",
		Repo:     "testrepo",
		Number:   42,
		URL:      "https://github.com/testowner/testrepo/pull/42",
		BaseRef:  "main",
		HeadRef:  "feature",
		HeadSHA:  "fff999fff999fff999fff999fff999fff999fff9",
	}

	if err := MarkMerged(home, taskID, wrongIdent, mergeTestAuth(t, taskID)); err == nil {
		t.Fatal("expected error for identity mismatch")
	}

	// delivery_state should remain unchanged.
	result, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if result[MetaDeliveryState] != string(DeliveryStateReviewReady) {
		t.Fatalf("expected delivery_state to remain %q, got %q", DeliveryStateReviewReady, result[MetaDeliveryState])
	}
}

func TestMarkMerged_NilAuthFailsClosed(t *testing.T) {
	home := t.TempDir()
	taskID := "test-ship"
	if err := mhome.WriteMeta(home, taskID, map[string]string{"kind": "ship"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if err := MarkMerged(home, taskID, identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1"), nil); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

// --- ReconcileMergeDelivery tests ---

// mergeDeliverySnapshot returns a ProviderSnapshot for a given state.
func mergeDeliverySnapshot(state string, merged bool, mergedSHA string) *ProviderSnapshot {
	return &ProviderSnapshot{
		Provider:   "github",
		Owner:      "testowner",
		Repo:       "testrepo",
		Number:     42,
		URL:        "https://github.com/testowner/testrepo/pull/42",
		BaseRef:    "main",
		HeadRef:    "feature",
		HeadSHA:    "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		State:      state,
		Merged:     merged,
		MergedSHA:  mergedSHA,
		ObservedAt: "2024-01-01T00:00:00Z",
	}
}

// writeShipMeta writes a minimal ship task meta with delivery identity.
func writeShipMeta(t *testing.T, homeDir, taskID, deliveryState, rev string) {
	t.Helper()
	meta := map[string]string{
		"kind":                 "ship",
		"delivery_state":       deliveryState,
		"pr_provider":          "github",
		"pr_owner":             "testowner",
		"pr_repo":              "testrepo",
		"pr_number":            "42",
		"pr_url":               "https://github.com/testowner/testrepo/pull/42",
		"pr_base_ref":          "main",
		"pr_head_ref":          "feature",
		"pr_head_sha":          "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		"pr_timestamp":         "2024-01-01T00:00:00Z",
		"pr_identity_revision": rev,
	}
	if err := mhome.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
}

func TestReconcileMergeDelivery_Merged(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-merged"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "1")

	// Inject mock provider: PR is merged
	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return mergeDeliverySnapshot("MERGED", true, "abc123def456abc123def456abc123def456abc1"), nil
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	result, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("ReconcileMergeDelivery: %v", err)
	}

	if result.Outcome != MergeOutcomeMerged {
		t.Errorf("expected outcome=%q, got %q", MergeOutcomeMerged, result.Outcome)
	}
	if !result.RemoteKnown {
		t.Error("expected RemoteKnown=true")
	}
	if result.Escalated {
		t.Error("expected Escalated=false")
	}

	// Verify persisted state
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Errorf("expected delivery_state=%q, got %q", DeliveryStateMerged, meta[MetaDeliveryState])
	}
	if meta[MetaIdentityRevision] != "2" {
		t.Errorf("expected revision=2, got %q", meta[MetaIdentityRevision])
	}
}

func TestReconcileMergeDelivery_AlreadyMerged(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-already-merged"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	// Start with already-merged state
	writeShipMeta(t, homeDir, taskID, string(DeliveryStateMerged), "5")

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return mergeDeliverySnapshot("MERGED", true, "abc123def456abc123def456abc123def456abc1"), nil
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	result, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("ReconcileMergeDelivery: %v", err)
	}

	if result.Outcome != MergeOutcomeAlreadyMerged {
		t.Errorf("expected outcome=%q, got %q", MergeOutcomeAlreadyMerged, result.Outcome)
	}
	if !result.RemoteKnown {
		t.Error("expected RemoteKnown=true")
	}

	// State should remain merged
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Errorf("expected delivery_state=%q, got %q", DeliveryStateMerged, meta[MetaDeliveryState])
	}
	// Revision unchanged (idempotent)
	if meta[MetaIdentityRevision] != "5" {
		t.Errorf("expected revision=5 (unchanged), got %q", meta[MetaIdentityRevision])
	}
}

func TestReconcileMergeDelivery_Open(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-open"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "1")

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return mergeDeliverySnapshot("OPEN", false, ""), nil
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	result, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("ReconcileMergeDelivery: %v", err)
	}

	if result.Outcome != MergeOutcomeOpen {
		t.Errorf("expected outcome=%q, got %q", MergeOutcomeOpen, result.Outcome)
	}
	if !result.RemoteKnown {
		t.Error("expected RemoteKnown=true")
	}

	// State should transition to review-ready (allows retry)
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateReviewReady) {
		t.Errorf("expected delivery_state=%q, got %q", DeliveryStateReviewReady, meta[MetaDeliveryState])
	}
	if meta[MetaIdentityRevision] != "2" {
		t.Errorf("expected revision=2, got %q", meta[MetaIdentityRevision])
	}
}

func TestReconcileMergeDelivery_RemoteUnknown_Timeout(t *testing.T) {
	// Provider timeout/error → remote-unknown, same mutation never repeated
	homeDir := t.TempDir()
	taskID := "test-timeout"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "1")

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return nil, fmt.Errorf("timeout: provider unreachable")
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	result, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("ReconcileMergeDelivery: %v", err)
	}

	if result.Outcome != MergeOutcomeRemoteUnknown {
		t.Errorf("expected outcome=%q, got %q", MergeOutcomeRemoteUnknown, result.Outcome)
	}
	if result.RemoteKnown {
		t.Error("expected RemoteKnown=false")
	}
	if result.Escalated {
		t.Error("expected Escalated=false on first remote-unknown")
	}

	// State should be remote-unknown
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateRemoteUnknown) {
		t.Errorf("expected delivery_state=%q, got %q", DeliveryStateRemoteUnknown, meta[MetaDeliveryState])
	}
}

func TestReconcileMergeDelivery_PersistentRemoteUnknown_Escalated(t *testing.T) {
	// Already in remote-unknown and provider still ambiguous → escalated
	homeDir := t.TempDir()
	taskID := "test-persistent"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	// Start with already remote-unknown state
	writeShipMeta(t, homeDir, taskID, string(DeliveryStateRemoteUnknown), "2")

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return nil, fmt.Errorf("still timeout: provider unreachable")
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	result, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("ReconcileMergeDelivery: %v", err)
	}

	if result.Outcome != MergeOutcomeRemoteUnknown {
		t.Errorf("expected outcome=%q, got %q", MergeOutcomeRemoteUnknown, result.Outcome)
	}
	if !result.Escalated {
		t.Error("expected Escalated=true for persistent uncertainty")
	}
	if result.RemoteKnown {
		t.Error("expected RemoteKnown=false")
	}

	// State should remain remote-unknown
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateRemoteUnknown) {
		t.Errorf("expected delivery_state=%q, got %q", DeliveryStateRemoteUnknown, meta[MetaDeliveryState])
	}
}

func TestReconcileMergeDelivery_FalseNegative(t *testing.T) {
	// False-negative scenario: provider says OPEN but PR is actually merged
	// (e.g., eventual consistency delay). Reconcile should report OPEN and
	// allow retry, not get stuck.
	homeDir := t.TempDir()
	taskID := "test-false-negative"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "1")

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl

	// Simulate false-negative: first call returns OPEN
	callCount := 0
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		callCount++
		if callCount == 1 {
			return mergeDeliverySnapshot("OPEN", false, ""), nil
		}
		// Second call: merged
		return mergeDeliverySnapshot("MERGED", true, "abc123def456abc123def456abc123def456abc1"), nil
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	// First call: false-negative, PR appears open
	result1, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("first ReconcileMergeDelivery: %v", err)
	}
	if result1.Outcome != MergeOutcomeOpen {
		t.Errorf("first call: expected outcome=%q, got %q", MergeOutcomeOpen, result1.Outcome)
	}

	// Update meta to show the PR is now open (simulating retry)
	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "2")

	// Second call: merged
	result2, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("second ReconcileMergeDelivery: %v", err)
	}
	if result2.Outcome != MergeOutcomeMerged {
		t.Errorf("second call: expected outcome=%q, got %q", MergeOutcomeMerged, result2.Outcome)
	}
}

func TestReconcileMergeDelivery_ExternalMerge(t *testing.T) {
	// External merge: someone else merged the PR while we were waiting.
	// The provider reports MERGED, and we should transition to merged.
	homeDir := t.TempDir()
	taskID := "test-external-merge"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "1")

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return mergeDeliverySnapshot("MERGED", true, "ext123def456abc123def456abc123def456abc1"), nil
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	result, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("ReconcileMergeDelivery: %v", err)
	}

	if result.Outcome != MergeOutcomeMerged {
		t.Errorf("expected outcome=%q, got %q", MergeOutcomeMerged, result.Outcome)
	}
	if result.MergedSHA != "ext123def456abc123def456abc123def456abc1" {
		t.Errorf("expected MergedSHA=%q, got %q", "ext123def456abc123def456abc123def456abc1", result.MergedSHA)
	}
	if !result.RemoteKnown {
		t.Error("expected RemoteKnown=true")
	}

	// State should be merged
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Errorf("expected delivery_state=%q, got %q", DeliveryStateMerged, meta[MetaDeliveryState])
	}
}

func TestReconcileMergeDelivery_SameMutationNeverRepeated(t *testing.T) {
	// Once remote-unknown is persisted, calling ReconcileMergeDelivery
	// again with the same state should not change the mutation attempt:
	// the Authority refuses further provider-mutating attempts and only
	// read reconciliation is allowed.
	homeDir := t.TempDir()
	taskID := "test-no-repeat"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "1")

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl

	callCount := 0
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		callCount++
		return nil, fmt.Errorf("timeout: provider unreachable")
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	auth := mergeTestAuth(t, taskID)

	// First call: remote-unknown commits.
	result1, err := ReconcileMergeDelivery(homeDir, taskID, prURL, auth)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if result1.Outcome != MergeOutcomeRemoteUnknown {
		t.Errorf("first call: expected outcome=%q, got %q", MergeOutcomeRemoteUnknown, result1.Outcome)
	}

	// Provider is still ambiguous — second call is read reconciliation only:
	// escalated, nothing mutates, the committed revision is unchanged.
	result2, err := ReconcileMergeDelivery(homeDir, taskID, prURL, auth)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if result2.Outcome != MergeOutcomeRemoteUnknown {
		t.Errorf("second call: expected outcome=%q, got %q", MergeOutcomeRemoteUnknown, result2.Outcome)
	}
	if !result2.Escalated {
		t.Error("second call: expected Escalated=true")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("read reconciliation advanced the revision to %d, want 2", agg.Revision)
	}
	if agg.MergeAttempt == nil || agg.MergeAttempt.Outcome != taskauthority.MergeOutcomeRemoteUnknown {
		t.Fatalf("committed merge attempt = %+v, want remote-unknown", agg.MergeAttempt)
	}

	// The same mutation (same head SHA) was never repeated
	// because the provider was never confirmed
}

func TestReconcileMergeDelivery_OpenDoesNotRegressFromMerged(t *testing.T) {
	// If stored state is merged but provider says OPEN (eventual consistency),
	// we should not regress to review-ready.
	homeDir := t.TempDir()
	taskID := "test-no-regress"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateMerged), "5")

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return mergeDeliverySnapshot("OPEN", false, ""), nil
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	result, err := ReconcileMergeDelivery(homeDir, taskID, prURL, mergeTestAuth(t, taskID))
	if err != nil {
		t.Fatalf("ReconcileMergeDelivery: %v", err)
	}

	if result.Outcome != MergeOutcomeOpen {
		t.Errorf("expected outcome=%q, got %q", MergeOutcomeOpen, result.Outcome)
	}

	// State should remain merged (not regressed)
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Errorf("expected delivery_state to remain %q, got %q", DeliveryStateMerged, meta[MetaDeliveryState])
	}
}

func TestMarkMergedFromRecord_Valid(t *testing.T) {
	home := t.TempDir()
	taskID := "test-ship"

	stateDir := filepath.Join(home, "state")
	if err := mhome.WriteMeta(home, taskID, map[string]string{
		"delivery_state": string(DeliveryStateReviewReady),
		"pr_provider":    "github",
		"pr_owner":       "testowner",
		"pr_repo":        "testrepo",
		"pr_number":      "42",
		"pr_url":         "https://github.com/testowner/testrepo/pull/42",
		"pr_base_ref":    "main",
		"pr_head_ref":    "feature",
		"pr_head_sha":    "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		"pr_timestamp":   "2024-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	_ = stateDir

	if err := MarkMergedFromRecord(home, taskID,
		"github", "testowner", "testrepo",
		42, "https://github.com/testowner/testrepo/pull/42",
		"main", "feature", "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		mergeTestAuth(t, taskID),
	); err != nil {
		t.Fatalf("MarkMergedFromRecord: %v", err)
	}

	result, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if result[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Fatalf("expected delivery_state=%q, got %q", DeliveryStateMerged, result[MetaDeliveryState])
	}
}

// --- StoreMergeAttempt (Task 7.6) ---

func TestStoreMergeAttemptNilAuthFailsClosed(t *testing.T) {
	if _, err := StoreMergeAttempt(t.TempDir(), nil, "task", MergeOutcomeMerged, identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1"), "", "MERGED", "test"); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

func TestStoreMergeAttemptMissingTaskFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-missing"
	if err := mhome.WriteMeta(homeDir, taskID, map[string]string{"kind": "ship"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeMerged, identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1"), "", "MERGED", "test"); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}
}

// TestReconcileMergeDelivery_CommittedRemoteUnknownRefusesMutation proves the
// remote-unknown fail-closed semantics at the fleet layer: once a
// remote-unknown outcome is committed, a later provider-verified read is
// classified read-only (Escalated) and never mutates the committed truth.
func TestReconcileMergeDelivery_CommittedRemoteUnknownRefusesMutation(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-refused"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "1")
	auth := mergeTestAuth(t, taskID)

	// Commit a remote-unknown outcome first (as the first reconcile did).
	if _, err := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeRemoteUnknown, identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1"), "", "", "provider unreachable"); err != nil {
		t.Fatalf("StoreMergeAttempt(remote-unknown): %v", err)
	}

	// The provider is now reachable and confirms merged: the mutation is
	// refused; only read reconciliation is permitted.
	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return mergeDeliverySnapshot("MERGED", true, "abc123def456abc123def456abc123def456abc1"), nil
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	result, err := ReconcileMergeDelivery(homeDir, taskID, prURL, auth)
	if err != nil {
		t.Fatalf("ReconcileMergeDelivery: %v", err)
	}
	if !result.Escalated {
		t.Error("expected Escalated=true for committed remote-unknown read reconciliation")
	}
	if !result.RemoteKnown {
		t.Error("expected RemoteKnown=true (the provider did respond)")
	}

	// Nothing mutated: the committed remote-unknown outcome and revision stand.
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAttempt == nil || agg.MergeAttempt.Outcome != taskauthority.MergeOutcomeRemoteUnknown {
		t.Fatalf("committed outcome was mutated: %+v", agg.MergeAttempt)
	}
	if agg.Revision != 2 {
		t.Fatalf("read reconciliation advanced the revision to %d, want 2", agg.Revision)
	}
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateRemoteUnknown) {
		t.Fatalf("delivery_state = %q, want remote-unknown", meta[MetaDeliveryState])
	}
}

// TestReconcileMergeDelivery_ProjectionFailureNeverErasesRemoteTruth proves
// that a partial .meta projection failure never rolls back or mutates the
// committed remote truth (ADR-0007 §7): the authoritative merged attempt
// commits, the projection returns a typed partial error, and a retry heals
// the projection idempotently.
func TestReconcileMergeDelivery_ProjectionFailureNeverErasesRemoteTruth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-projection"
	prURL := "https://github.com/testowner/testrepo/pull/42"

	writeShipMeta(t, homeDir, taskID, string(DeliveryStateReviewReady), "1")
	auth := mergeTestAuth(t, taskID)

	// Make the state path read-only so the projection write fails while the
	// meta read (and the authoritative commit) still works.
	if err := os.Chmod(filepath.Join(homeDir, "state"), 0555); err != nil {
		t.Fatal(err)
	}

	saved := ReconcileMergeDelivery
	savedSnapshot := FetchProviderSnapshot
	ReconcileMergeDelivery = reconcileMergeDeliveryImpl
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return mergeDeliverySnapshot("MERGED", true, "abc123def456abc123def456abc123def456abc1"), nil
	}
	defer func() {
		ReconcileMergeDelivery = saved
		FetchProviderSnapshot = savedSnapshot
	}()

	_, err := ReconcileMergeDelivery(homeDir, taskID, prURL, auth)
	var typed *MergeOutcomeProjectionError
	if !errors.As(err, &typed) {
		t.Fatalf("projection error = %v, want *MergeOutcomeProjectionError", err)
	}

	// The authoritative merged truth is committed and never erased.
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAttempt == nil || agg.MergeAttempt.Outcome != taskauthority.MergeOutcomeMerged {
		t.Fatalf("authoritative remote truth lost after projection failure: %+v", agg.MergeAttempt)
	}
	if agg.MergeAttempt.MergedSHA != "abc123def456abc123def456abc123def456abc1" {
		t.Fatalf("merged SHA lost: %+v", agg.MergeAttempt)
	}

	// The projection did not erase the old meta state (it could not write).
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateReviewReady) {
		t.Fatalf("failed projection mutated meta: %q", meta[MetaDeliveryState])
	}

	// Retry heals the projection idempotently: the already-merged re-attempt
	// is an in-value no-op that never erases the committed truth.
	if err := os.Chmod(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	res, err := StoreMergeAttempt(homeDir, auth, taskID, MergeOutcomeMerged, identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1"), "abc123def456abc123def456abc123def456abc1", "MERGED", "retry heal")
	if err != nil {
		t.Fatalf("StoreMergeAttempt retry: %v", err)
	}
	if res.Revision != 2 {
		t.Fatalf("idempotent re-attempt advanced revision to %d, want 2", res.Revision)
	}
	meta, err = mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Fatalf("healed delivery_state = %q, want merged", meta[MetaDeliveryState])
	}
}
