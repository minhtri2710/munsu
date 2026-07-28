package delivery

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/task"
)

func identityFor(owner, repo string, number int, headSHA string) *DeliveryIdentity {
	return &DeliveryIdentity{
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
	if err := task.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	ident := identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1")

	if err := MarkMerged(home, taskID, ident); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}

	// Verify delivery_state is merged.
	result, err := task.ReadMeta(home, taskID)
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
	if err := task.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	ident := identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1")

	// First call: idempotent.
	if err := MarkMerged(home, taskID, ident); err != nil {
		t.Fatalf("first MarkMerged: %v", err)
	}

	// Revision should still be "5" (not incremented).
	result, err := task.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if result[MetaIdentityRevision] != "5" {
		t.Fatalf("expected revision=5 (unchanged), got %q", result[MetaIdentityRevision])
	}

	// Second call: still idempotent.
	if err := MarkMerged(home, taskID, ident); err != nil {
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
	if err := task.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	ident := identityFor("testowner", "testrepo", 42, "aaa111aaa111aaa111aaa111aaa111aaa111aaa1")

	if err := MarkMerged(home, taskID, ident); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}

	result, err := task.ReadMeta(home, taskID)
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
	if err := task.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Use a wrong identity (different head SHA).
	wrongIdent := &DeliveryIdentity{
		Provider: "github",
		Owner:    "testowner",
		Repo:     "testrepo",
		Number:   42,
		URL:      "https://github.com/testowner/testrepo/pull/42",
		BaseRef:  "main",
		HeadRef:  "feature",
		HeadSHA:  "fff999fff999fff999fff999fff999fff999fff9",
	}

	if err := MarkMerged(home, taskID, wrongIdent); err == nil {
		t.Fatal("expected CAS error for identity mismatch")
	}

	// delivery_state should remain unchanged.
	result, err := task.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if result[MetaDeliveryState] != string(DeliveryStateReviewReady) {
		t.Fatalf("expected delivery_state to remain %q, got %q", DeliveryStateReviewReady, result[MetaDeliveryState])
	}
}

func TestMarkMergedFromRecord_Valid(t *testing.T) {
	home := t.TempDir()
	taskID := "test-ship"

	stateDir := filepath.Join(home, "state")
	if err := task.WriteMeta(home, taskID, map[string]string{
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
	); err != nil {
		t.Fatalf("MarkMergedFromRecord: %v", err)
	}

	result, err := task.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if result[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Fatalf("expected delivery_state=%q, got %q", DeliveryStateMerged, result[MetaDeliveryState])
	}
}
