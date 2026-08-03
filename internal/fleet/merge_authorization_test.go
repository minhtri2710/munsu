//go:build integration

package fleet

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// --- MergeAuthorization record round-trip ---

func TestMergeAuthorization_RoundTrip(t *testing.T) {
	auth := &taskauthority.MergeAuthorization{
		HeadSHA: "abc123def456abc123def456abc123def456abc1",
		ProviderSnapshot: taskauthority.ProviderIdentitySnapshot{
			Provider: "github",
			Owner:    "minhtri2710",
			Repo:     "munsu",
			Number:   42,
			URL:      "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef:  "main",
			HeadRef:  "feature/test",
			HeadSHA:  "abc123def456abc123def456abc123def456abc1",
		},
		AuthorizedAt: 1,
		Authorizer:   "general",
	}

	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored taskauthority.MergeAuthorization
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.HeadSHA != auth.HeadSHA {
		t.Errorf("HeadSHA: got %q, want %q", restored.HeadSHA, auth.HeadSHA)
	}
	if restored.ProviderSnapshot.Provider != auth.ProviderSnapshot.Provider {
		t.Errorf("Provider: got %q, want %q", restored.ProviderSnapshot.Provider, auth.ProviderSnapshot.Provider)
	}
	if restored.ProviderSnapshot.HeadSHA != auth.ProviderSnapshot.HeadSHA {
		t.Errorf("Provider headSHA: got %q, want %q", restored.ProviderSnapshot.HeadSHA, auth.ProviderSnapshot.HeadSHA)
	}
	if restored.Authorizer != auth.Authorizer {
		t.Errorf("Authorizer: got %q, want %q", restored.Authorizer, auth.Authorizer)
	}
}

// --- AuthorizeMerge tests (fleet cutover through the composed Authority) ---

// newMergeAuthAuthority seeds one task in an Authority (no worktree binding
// needed for merge authorization) and returns it with the task meta seed.
func newMergeAuthAuthority(t *testing.T, homeDir, taskID string) *taskauthority.Authority {
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

func TestAuthorizeMerge_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-ship"

	// Write meta with delivery identity.
	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	res, err := AuthorizeMerge(homeDir, auth, taskID, ident)
	if err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}
	if res.TaskID != taskID || res.Generation != 1 || res.Replayed {
		t.Fatalf("AuthorizeMerge result = %+v", res)
	}

	// The authoritative Aggregate holds the generation-bound record.
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if agg.MergeAuthorization == nil {
		t.Fatal("merge authorization record missing after authorize")
	}
	if agg.MergeAuthorization.HeadSHA != ident.HeadSHA {
		t.Errorf("HeadSHA: got %q, want %q", agg.MergeAuthorization.HeadSHA, ident.HeadSHA)
	}
	if agg.MergeAuthorization.ProviderSnapshot.Number != ident.Number {
		t.Errorf("PR number: got %d, want %d", agg.MergeAuthorization.ProviderSnapshot.Number, ident.Number)
	}
	if agg.MergeAuthorization.Authorizer == "" || agg.MergeAuthorization.AuthorizedAt <= 0 {
		t.Errorf("authorizer/authorized-at not set: %+v", agg.MergeAuthorization)
	}

	// The .meta merge_authorization projection is reconciled.
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaMergeAuthorization] == "" {
		t.Fatal("merge_authorization should be set in meta")
	}
	var stored taskauthority.MergeAuthorization
	if err := json.Unmarshal([]byte(readMeta[MetaMergeAuthorization]), &stored); err != nil {
		t.Fatalf("unmarshal stored: %v", err)
	}
	if stored.HeadSHA != ident.HeadSHA {
		t.Errorf("stored HeadSHA: got %q, want %q", stored.HeadSHA, ident.HeadSHA)
	}
}

func TestAuthorizeMerge_RejectsNoIdentity(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-ident"

	meta := map[string]string{"generation": "1", "kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	_, err := AuthorizeMerge(homeDir, auth, taskID, validIdentity())
	if err == nil {
		t.Fatal("expected error for no identity")
	}
	if !strings.Contains(err.Error(), "no delivery identity") {
		t.Errorf("expected 'no delivery identity' error, got: %v", err)
	}
}

func TestAuthorizeMerge_RejectsIdentityMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ident-mismatch"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Provide a different identity (different head SHA).
	different := validIdentity()
	different.HeadSHA = "differentdifferentdifferentdifferentdifferent"
	different.CapturedAt = "2026-07-21T00:00:00Z"

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	_, err := AuthorizeMerge(homeDir, auth, taskID, different)
	if err == nil {
		t.Fatal("expected error for identity mismatch")
	}
	if !strings.Contains(err.Error(), "identity mismatch") {
		t.Errorf("expected 'identity mismatch' error, got: %v", err)
	}
}

func TestAuthorizeMerge_RejectsNoMeta(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-meta"

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	_, err := AuthorizeMerge(homeDir, auth, taskID, validIdentity())
	if err == nil {
		t.Fatal("expected error for missing meta")
	}
}

func TestAuthorizeMerge_FailsClosedWithoutAuthority(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-auth"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	if _, err := AuthorizeMerge(homeDir, nil, taskID, ident); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

func TestAuthorizeMerge_CASConflict(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-cas"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	// Change head SHA behind our back before authorizing.
	meta["pr_head_sha"] = "shadowchangeshadowchangeshadowchangeshadowcha"
	meta["pr_head"] = "shadowchangeshadowchangeshadowchangeshadowcha"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize with the original identity — should fail because the stored
	// head changed (the wrapper validates the request against stored meta).
	auth := newMergeAuthAuthority(t, homeDir, taskID)
	_, err := AuthorizeMerge(homeDir, auth, taskID, ident)
	if err == nil {
		t.Fatal("expected error for CAS conflict (head changed)")
	}
	if !strings.Contains(err.Error(), "identity mismatch") && !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("expected identity mismatch error, got: %v", err)
	}
}

func TestAuthorizeMerge_AlreadyAuthorizedIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-idempotent"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	if _, err := AuthorizeMerge(homeDir, auth, taskID, ident); err != nil {
		t.Fatalf("first AuthorizeMerge: %v", err)
	}

	// Second authorization (same identity, same head) — should succeed; the
	// authoritative record keeps the same head.
	if _, err := AuthorizeMerge(homeDir, auth, taskID, ident); err != nil {
		t.Fatalf("second AuthorizeMerge: %v", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.MergeAuthorization.HeadSHA != ident.HeadSHA {
		t.Errorf("HeadSHA changed: got %q, want %q", agg.MergeAuthorization.HeadSHA, ident.HeadSHA)
	}
}

// --- CheckMergeAuthorization tests (canonical read through the Authority) ---

func TestCheckMergeAuthorization_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-ok"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	if _, err := AuthorizeMerge(homeDir, auth, taskID, ident); err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	checked, err := CheckMergeAuthorization(auth, taskID, ident)
	if err != nil {
		t.Fatalf("CheckMergeAuthorization: %v", err)
	}
	if checked.HeadSHA != ident.HeadSHA {
		t.Errorf("HeadSHA: got %q, want %q", checked.HeadSHA, ident.HeadSHA)
	}
}

func TestCheckMergeAuthorization_NoAuthorization(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-no-auth"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	_, err := CheckMergeAuthorization(auth, taskID, ident)
	if err == nil {
		t.Fatal("expected error for no authorization")
	}
	if !strings.Contains(err.Error(), "no merge authorization") {
		t.Errorf("expected 'no merge authorization' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "run munsu delivery authorize") {
		t.Errorf("expected remediation hint, got: %v", err)
	}
}

func TestCheckMergeAuthorization_StaleHead(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-stale-head"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	if _, err := AuthorizeMerge(homeDir, auth, taskID, ident); err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	// Check with a new head — the changed head invalidates the stale
	// authorization (force-with-lease semantics).
	newIdent := validIdentity()
	newIdent.HeadSHA = "newheadnewheadnewheadnewheadnewheadnewheadnew"
	newIdent.CapturedAt = "2026-07-22T00:00:00Z"

	_, err := CheckMergeAuthorization(auth, taskID, newIdent)
	if err == nil {
		t.Fatal("expected error for stale head")
	}
	if !strings.Contains(err.Error(), "stale") && !strings.Contains(err.Error(), "head") {
		t.Errorf("expected 'stale' or 'head' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "re-authorize") && !strings.Contains(err.Error(), "pr-amend") {
		t.Errorf("expected remediation hint, got: %v", err)
	}
}

func TestCheckMergeAuthorization_MismatchedIdentity(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-mismatch"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	if _, err := AuthorizeMerge(homeDir, auth, taskID, ident); err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	// Check with a different identity (different repo).
	different := validIdentity()
	different.Repo = "different-repo"
	different.URL = "https://github.com/minhtri2710/different-repo/pull/42"

	_, err := CheckMergeAuthorization(auth, taskID, different)
	if err == nil {
		t.Fatal("expected error for mismatched identity")
	}
	if !strings.Contains(err.Error(), "mismatch") && !strings.Contains(err.Error(), "provider") {
		t.Errorf("expected 'mismatch' or 'provider' error, got: %v", err)
	}
}

func TestCheckMergeAuthorization_MissingTask(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-missing"

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	if _, err := CheckMergeAuthorization(auth, "missing-task", validIdentity()); err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestCheckMergeAuthorization_FailsClosedWithoutAuthority(t *testing.T) {
	if _, err := CheckMergeAuthorization(nil, "task", validIdentity()); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

func TestPRMerge_RequiresAuthorization(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-merge-no-auth"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Attempt merge without authorizing first — the authorization check fails.
	auth := newMergeAuthAuthority(t, homeDir, taskID)
	_, err := CheckMergeAuthorization(auth, taskID, ident)
	if err == nil {
		t.Fatal("expected error: no authorization before merge")
	}
	if !strings.Contains(err.Error(), "no merge authorization") {
		t.Errorf("expected 'no merge authorization' error, got: %v", err)
	}
}

func TestAuthorizeThenCheck_ChangedHeadInvalidates(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-then-change"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	if _, err := AuthorizeMerge(homeDir, auth, taskID, ident); err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	// Check with a changed head — should fail closed.
	newIdent := validIdentity()
	newIdent.HeadSHA = "newsha_newsha_newsha_newsha_newsha_newsha_ne"
	newIdent.CapturedAt = "2026-07-23T00:00:00Z"

	_, err := CheckMergeAuthorization(auth, taskID, newIdent)
	if err == nil {
		t.Fatal("expected error: changed head should invalidate authorization")
	}
	if !strings.Contains(err.Error(), "stale") && !strings.Contains(err.Error(), "head") {
		t.Errorf("expected 'stale' or 'head' error for changed head, got: %v", err)
	}
}

// --- RecordExternalMerge tests (evidence record through the Authority) ---

func TestRecordExternalMerge_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ext-merge"
	mergedSHA := "mergedmergedmergedmergedmergedmergedmergedmerg"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	if _, err := RecordExternalMerge(homeDir, auth, taskID, mergedSHA, ident); err != nil {
		t.Fatalf("RecordExternalMerge: %v", err)
	}

	// The authoritative Aggregate holds the generation-bound evidence record.
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if agg.ExternalMerge == nil {
		t.Fatal("external merge record missing")
	}
	if agg.ExternalMerge.MergedSHA != mergedSHA {
		t.Errorf("MergedSHA: got %q, want %q", agg.ExternalMerge.MergedSHA, mergedSHA)
	}
	if agg.ExternalMerge.MergedAt <= 0 {
		t.Error("MergedAt must be set")
	}
	if agg.ExternalMerge.MergeSource != "external" {
		t.Errorf("MergeSource: got %q, want %q", agg.ExternalMerge.MergeSource, "external")
	}

	// The .meta external_merge projection is reconciled.
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaExternalMerge] == "" {
		t.Fatal("external_merge should be set in meta")
	}
	var ext taskauthority.ExternalMergeRecord
	if err := json.Unmarshal([]byte(readMeta[MetaExternalMerge]), &ext); err != nil {
		t.Fatalf("unmarshal external merge: %v", err)
	}
	if ext.MergedSHA != mergedSHA {
		t.Errorf("MergedSHA: got %q, want %q", ext.MergedSHA, mergedSHA)
	}

	// Task 7.6: the merged-state transition deferred from Task 7.4 now commits
	// via the Authority — verified external merge evidence drives the
	// generation-bound merged merge outcome and the delivery_state=merged
	// projection.
	agg2, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if agg2.MergeAttempt == nil || agg2.MergeAttempt.Outcome != taskauthority.MergeOutcomeMerged {
		t.Fatalf("merged merge outcome missing after external merge: %+v", agg2.MergeAttempt)
	}
	if agg2.MergeAttempt.HeadSHA != ident.HeadSHA {
		t.Errorf("merged outcome head = %q, want %q", agg2.MergeAttempt.HeadSHA, ident.HeadSHA)
	}
	if agg2.MergeAttempt.MergedSHA != mergedSHA {
		t.Errorf("merged outcome merged SHA = %q, want %q", agg2.MergeAttempt.MergedSHA, mergedSHA)
	}
	if readMeta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Errorf("delivery_state projection = %q, want merged", readMeta[MetaDeliveryState])
	}
}

func TestRecordExternalMerge_RejectsIdentityMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ext-mismatch"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "1"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	different := validIdentity()
	different.Repo = "different-repo"

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	err := func() error {
		_, err := RecordExternalMerge(homeDir, auth, taskID, "mergedsha", different)
		return err
	}()
	if err == nil {
		t.Fatal("expected error for identity mismatch")
	}
	if !strings.Contains(err.Error(), "identity mismatch") {
		t.Errorf("expected 'identity mismatch' error, got: %v", err)
	}
}

func TestRecordExternalMerge_NoIdentity(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ext-no-ident"

	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newMergeAuthAuthority(t, homeDir, taskID)
	if _, err := RecordExternalMerge(homeDir, auth, taskID, "mergedsha", validIdentity()); err == nil {
		t.Fatal("expected error for no identity")
	}
}

// --- Typed error types ---

func TestErrNoMergeAuthorization_Type(t *testing.T) {
	err := &ErrNoMergeAuthorization{TaskID: "test-task"}
	if err.Error() == "" {
		t.Fatal("ErrNoMergeAuthorization.Error() should not be empty")
	}
	if !strings.Contains(err.Error(), "test-task") {
		t.Errorf("expected task ID in error message, got: %v", err)
	}
}

func TestErrStaleAuthorization_Type(t *testing.T) {
	err := &ErrStaleAuthorization{
		TaskID:         "test-task",
		AuthorizedHead: "aaa",
		CurrentHead:    "bbb",
	}
	if err.Error() == "" {
		t.Fatal("ErrStaleAuthorization.Error() should not be empty")
	}
	if !strings.Contains(err.Error(), "aaa") || !strings.Contains(err.Error(), "bbb") {
		t.Errorf("expected both SHAs in error message, got: %v", err)
	}
}

func TestExternalMergeRecord_RoundTrip(t *testing.T) {
	rec := &taskauthority.ExternalMergeRecord{
		MergedSHA:   "mergedmergedmergedmergedmergedmergedmergedmerg",
		MergedAt:    time.Now().UnixNano(),
		MergeSource: "external",
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored taskauthority.ExternalMergeRecord
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.MergedSHA != rec.MergedSHA {
		t.Errorf("MergedSHA: got %q, want %q", restored.MergedSHA, rec.MergedSHA)
	}
	if restored.MergeSource != rec.MergeSource {
		t.Errorf("MergeSource: got %q, want %q", restored.MergeSource, rec.MergeSource)
	}
}
