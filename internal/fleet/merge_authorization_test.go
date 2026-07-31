//go:build integration

package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// --- MergeAuthorization struct tests ---

func TestMergeAuthorization_RoundTrip(t *testing.T) {
	auth := &MergeAuthorization{
		TaskGeneration: "7",
		HeadSHA:        "abc123def456abc123def456abc123def456abc1",
		ProviderSnapshot: ProviderIdentitySnapshot{
			Provider: "github",
			Owner:    "minhtri2710",
			Repo:     "munsu",
			Number:   42,
			URL:      "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef:  "main",
			HeadRef:  "feature/test",
		},
		AuthorizedAt: "2026-07-20T12:00:00Z",
		Authorizer:   "general",
	}

	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored MergeAuthorization
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.TaskGeneration != auth.TaskGeneration {
		t.Errorf("TaskGeneration: got %q, want %q", restored.TaskGeneration, auth.TaskGeneration)
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
	if restored.AuthorizedAt != auth.AuthorizedAt {
		t.Errorf("AuthorizedAt: got %q, want %q", restored.AuthorizedAt, auth.AuthorizedAt)
	}
	if restored.Authorizer != auth.Authorizer {
		t.Errorf("Authorizer: got %q, want %q", restored.Authorizer, auth.Authorizer)
	}
}

// --- AuthorizeMerge tests ---

func TestAuthorizeMerge_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-ship"
	generation := "7"

	// Write meta with delivery identity
	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = generation
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth, err := AuthorizeMerge(homeDir, taskID, generation, ident)
	if err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	if auth.TaskGeneration != generation {
		t.Errorf("TaskGeneration: got %q, want %q", auth.TaskGeneration, generation)
	}
	if auth.HeadSHA != ident.HeadSHA {
		t.Errorf("HeadSHA: got %q, want %q", auth.HeadSHA, ident.HeadSHA)
	}
	if auth.ProviderSnapshot.Number != ident.Number {
		t.Errorf("PR number: got %d, want %d", auth.ProviderSnapshot.Number, ident.Number)
	}
	if auth.Authorizer != "general" {
		t.Errorf("Authorizer: got %q, want %q", auth.Authorizer, "general")
	}
	if auth.AuthorizedAt == "" {
		t.Error("AuthorizedAt must be set")
	}

	// Verify authorization is persisted in meta
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaMergeAuthorization] == "" {
		t.Fatal("merge_authorization should be set in meta")
	}

	var stored MergeAuthorization
	if err := json.Unmarshal([]byte(readMeta[MetaMergeAuthorization]), &stored); err != nil {
		t.Fatalf("unmarshal stored: %v", err)
	}
	if stored.HeadSHA != ident.HeadSHA {
		t.Errorf("stored HeadSHA: got %q, want %q", stored.HeadSHA, ident.HeadSHA)
	}
}

func TestAuthorizeMerge_RejectsGenerationMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-gen-mismatch"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "7"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize with different generation
	_, err := AuthorizeMerge(homeDir, taskID, "8", ident)
	if err == nil {
		t.Fatal("expected error for generation mismatch")
	}
	if !strings.Contains(err.Error(), "task generation") {
		t.Errorf("expected 'task generation' error, got: %v", err)
	}
}

func TestAuthorizeMerge_RejectsNoIdentity(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-ident"

	meta := map[string]string{"generation": "7", "kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := AuthorizeMerge(homeDir, taskID, "7", nil)
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
	generation := "7"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = generation
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Provide a different identity (different head SHA)
	different := validIdentity()
	different.HeadSHA = "differentdifferentdifferentdifferentdifferent"
	different.CapturedAt = "2026-07-21T00:00:00Z"

	_, err := AuthorizeMerge(homeDir, taskID, generation, different)
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

	_, err := AuthorizeMerge(homeDir, taskID, "7", validIdentity())
	if err == nil {
		t.Fatal("expected error for missing meta")
	}
}

// --- CheckMergeAuthorization tests ---

func TestCheckMergeAuthorization_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-ok"
	generation := "7"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = generation
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// First authorize
	auth, err := AuthorizeMerge(homeDir, taskID, generation, ident)
	if err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	// Then check — should succeed
	checked, err := CheckMergeAuthorization(homeDir, taskID, ident)
	if err != nil {
		t.Fatalf("CheckMergeAuthorization: %v", err)
	}
	if checked.HeadSHA != auth.HeadSHA {
		t.Errorf("HeadSHA: got %q, want %q", checked.HeadSHA, auth.HeadSHA)
	}
}

func TestCheckMergeAuthorization_NoAuthorization(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-no-auth"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "7"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := CheckMergeAuthorization(homeDir, taskID, ident)
	if err == nil {
		t.Fatal("expected error for no authorization")
	}
	var authErr *ErrNoMergeAuthorization
	if !strings.Contains(err.Error(), "no merge authorization") {
		t.Errorf("expected 'no merge authorization' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "run munsu delivery authorize") {
		t.Errorf("expected remediation hint, got: %v", err)
	}
	_ = authErr
}

func TestCheckMergeAuthorization_StaleHead(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-stale-head"
	generation := "7"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = generation
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize with original head
	_, err := AuthorizeMerge(homeDir, taskID, generation, ident)
	if err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	// Read meta after authorization (preserves merge_authorization record)
	currentMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta after auth: %v", err)
	}

	// Change the head SHA in meta (simulating a new push) while preserving
	// the authorization record that was written by AuthorizeMerge.
	currentMeta["pr_head_sha"] = "newheadnewheadnewheadnewheadnewheadnewheadnew"
	currentMeta["pr_head"] = "newheadnewheadnewheadnewheadnewheadnewheadnew"
	if err := home.WriteMeta(homeDir, taskID, currentMeta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Check with the new identity
	newIdent := validIdentity()
	newIdent.HeadSHA = "newheadnewheadnewheadnewheadnewheadnewheadnew"
	newIdent.CapturedAt = "2026-07-22T00:00:00Z"

	_, err = CheckMergeAuthorization(homeDir, taskID, newIdent)
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
	generation := "7"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = generation
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize with original identity
	_, err := AuthorizeMerge(homeDir, taskID, generation, ident)
	if err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	// Check with a different identity (different repo)
	different := validIdentity()
	different.Repo = "different-repo"
	different.URL = "https://github.com/minhtri2710/different-repo/pull/42"

	_, err = CheckMergeAuthorization(homeDir, taskID, different)
	if err == nil {
		t.Fatal("expected error for mismatched identity")
	}
	if !strings.Contains(err.Error(), "mismatch") && !strings.Contains(err.Error(), "provider") {
		t.Errorf("expected 'mismatch' or 'provider' error, got: %v", err)
	}
}

func TestCheckMergeAuthorization_NoMeta(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-no-meta"

	_, err := CheckMergeAuthorization(homeDir, taskID, validIdentity())
	if err == nil {
		t.Fatal("expected error for missing meta")
	}
}

// --- RecordExternalMerge tests ---

func TestRecordExternalMerge_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ext-merge"
	mergedSHA := "mergedmergedmergedmergedmergedmergedmergedmerg"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "7"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	err := RecordExternalMerge(homeDir, taskID, mergedSHA, ident)
	if err != nil {
		t.Fatalf("RecordExternalMerge: %v", err)
	}

	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	// Verify delivery_state is merged
	if readMeta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Errorf("delivery_state: got %q, want %q", readMeta[MetaDeliveryState], DeliveryStateMerged)
	}

	// Verify external merge is recorded
	if readMeta[MetaExternalMerge] == "" {
		t.Fatal("external_merge should be set in meta")
	}

	var ext ExternalMergeRecord
	if err := json.Unmarshal([]byte(readMeta[MetaExternalMerge]), &ext); err != nil {
		t.Fatalf("unmarshal external merge: %v", err)
	}
	if ext.MergedSHA != mergedSHA {
		t.Errorf("MergedSHA: got %q, want %q", ext.MergedSHA, mergedSHA)
	}
	if ext.MergedAt == "" {
		t.Error("MergedAt must be set")
	}
	if ext.MergeSource != "external" {
		t.Errorf("MergeSource: got %q, want %q", ext.MergeSource, "external")
	}
}

func TestRecordExternalMerge_RejectsIdentityMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ext-mismatch"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "7"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Record with different identity
	different := validIdentity()
	different.Repo = "different-repo"

	err := RecordExternalMerge(homeDir, taskID, "mergedsha", different)
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

	err := RecordExternalMerge(homeDir, taskID, "mergedsha", validIdentity())
	if err == nil {
		t.Fatal("expected error for no identity")
	}
}

func TestRecordExternalMerge_AlreadyMerged(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ext-already"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateMerged)
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	err := RecordExternalMerge(homeDir, taskID, "mergedsha", ident)
	if err != nil {
		t.Fatalf("expected nil for already merged: %v", err)
	}
}

// --- PRMerge integration with authorization ---

func TestPRMerge_RequiresAuthorization(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-merge-no-auth"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = "7"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Attempt merge without authorizing first — should fail at authorization check
	// This is a unit-level check; the actual gh-axi merge would fail later.
	// We can test the authorization check by calling CheckMergeAuthorization first.
	_, err := CheckMergeAuthorization(homeDir, taskID, ident)
	if err == nil {
		t.Fatal("expected error: no authorization before merge")
	}
	var noAuthErr *ErrNoMergeAuthorization
	if !strings.Contains(err.Error(), "no merge authorization") {
		t.Errorf("expected 'no merge authorization' error, got: %v", err)
	}
	_ = noAuthErr
}

func TestAuthorizeThenCheck_ChangedHeadInvalidates(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-then-change"
	generation := "7"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = generation
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize
	_, err := AuthorizeMerge(homeDir, taskID, generation, ident)
	if err != nil {
		t.Fatalf("AuthorizeMerge: %v", err)
	}

	// Read meta after authorization (preserves merge_authorization)
	currentMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta after auth: %v", err)
	}

	// Change head in meta (simulating a force push or new push) while preserving
	// the authorization record.
	currentMeta["pr_head_sha"] = "newsha_newsha_newsha_newsha_newsha_newsha_ne"
	currentMeta["pr_head"] = "newsha_newsha_newsha_newsha_newsha_newsha_ne"
	if err := home.WriteMeta(homeDir, taskID, currentMeta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Check with new head — should fail
	newIdent := validIdentity()
	newIdent.HeadSHA = "newsha_newsha_newsha_newsha_newsha_newsha_ne"
	newIdent.CapturedAt = "2026-07-23T00:00:00Z"

	_, err = CheckMergeAuthorization(homeDir, taskID, newIdent)
	if err == nil {
		t.Fatal("expected error: changed head should invalidate authorization")
	}
	if !strings.Contains(err.Error(), "stale") && !strings.Contains(err.Error(), "head") {
		t.Errorf("expected 'stale' or 'head' error for changed head, got: %v", err)
	}
}

func TestAuthorizeMerge_RejectsNoGeneration(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-gen"

	ident := validIdentity()
	meta := ident.ToMeta()
	// No generation in meta
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := AuthorizeMerge(homeDir, taskID, "7", ident)
	if err == nil {
		t.Fatal("expected error: no generation in meta")
	}
	if !strings.Contains(err.Error(), "generation") {
		t.Errorf("expected 'generation' error, got: %v", err)
	}
}

func TestAuthorizeMerge_AlreadyAuthorizedIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-idempotent"
	generation := "7"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = generation
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// First authorization
	auth1, err := AuthorizeMerge(homeDir, taskID, generation, ident)
	if err != nil {
		t.Fatalf("first AuthorizeMerge: %v", err)
	}

	// Second authorization (same identity, same head) — should succeed (idempotent)
	auth2, err := AuthorizeMerge(homeDir, taskID, generation, ident)
	if err != nil {
		t.Fatalf("second AuthorizeMerge: %v", err)
	}

	if auth2.HeadSHA != auth1.HeadSHA {
		t.Errorf("HeadSHA changed: got %q, want %q", auth2.HeadSHA, auth1.HeadSHA)
	}
}

// --- Typed error types ---

func TestErrNoMergeAuthorization_Type(t *testing.T) {
	// Verify the error type is constructable and has the expected interface
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
	rec := &ExternalMergeRecord{
		MergedSHA:   "mergedmergedmergedmergedmergedmergedmergedmerg",
		MergedAt:    "2026-07-20T12:00:00Z",
		MergeSource: "external",
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored ExternalMergeRecord
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

// --- CAS conflict during authorization ---

func TestAuthorizeMerge_CASConflict(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-cas"
	generation := "7"

	ident := validIdentity()
	meta := ident.ToMeta()
	meta["generation"] = generation
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Change head SHA behind our back before authorizing
	meta["pr_head_sha"] = "shadowchangeshadowchangeshadowchangeshadowcha"
	meta["pr_head"] = "shadowchangeshadowchangeshadowchangeshadowcha"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize with the original identity — should fail because stored head changed
	_, err := AuthorizeMerge(homeDir, taskID, generation, ident)
	if err == nil {
		t.Fatal("expected error for CAS conflict (head changed)")
	}
	if !strings.Contains(err.Error(), "identity mismatch") && !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("expected identity mismatch error, got: %v", err)
	}
}