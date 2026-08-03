//go:build integration

package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// --- GitCapabilityTier tests (read path over the .meta projection) ---

func TestGitCapabilityTier_DefaultToWrite(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-tier-default"

	// No tier set in meta — should default to write
	tier, err := ResolveGitCapabilityTier(homeDir, taskID)
	if err != nil {
		t.Fatalf("ResolveGitCapabilityTier: %v", err)
	}
	if tier != taskauthority.GitTierWrite {
		t.Errorf("expected default tier %q, got %q", taskauthority.GitTierWrite, tier)
	}
}

func TestGitCapabilityTier_SetAndRead(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-tier-set"

	// Seed the .meta projection as the production wrapper reconciles it
	// after the authoritative commit (Task 7.4).
	meta := map[string]string{"generation": "1", "kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if err := projectGitCapabilityTier(homeDir, taskID, string(taskauthority.GitTierRewrite)); err != nil {
		t.Fatalf("projectGitCapabilityTier: %v", err)
	}

	tier, err := ResolveGitCapabilityTier(homeDir, taskID)
	if err != nil {
		t.Fatalf("ResolveGitCapabilityTier: %v", err)
	}
	if tier != taskauthority.GitTierRewrite {
		t.Errorf("expected tier %q, got %q", taskauthority.GitTierRewrite, tier)
	}
}

func TestGitCapabilityTier_UnknownTierDefaultsToWrite(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-tier-unknown"

	meta := map[string]string{"git_capability_tier": "super"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	tier, err := ResolveGitCapabilityTier(homeDir, taskID)
	if err == nil {
		t.Fatal("expected error for unknown tier")
	}
	if tier != taskauthority.GitTierWrite {
		t.Errorf("expected fallback tier %q, got %q", taskauthority.GitTierWrite, tier)
	}
}

// --- StoreGitCapabilityTier tests (fleet cutover through the Authority) ---

// newGitAuthAuthority seeds one worktree-bound task in an Authority.
func newGitAuthAuthority(t *testing.T, taskID string) *taskauthority.Authority {
	t.Helper()
	return amendAuth(t, taskID)
}

func TestStoreGitCapabilityTier_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-store-tier"
	meta := map[string]string{"generation": "1", "kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	res, err := StoreGitCapabilityTier(homeDir, auth, taskID, taskauthority.GitTierRewrite)
	if err != nil {
		t.Fatalf("StoreGitCapabilityTier: %v", err)
	}
	if res.TaskID != taskID || res.Generation != 1 || res.Replayed {
		t.Fatalf("tier result = %+v", res)
	}

	// The authoritative Aggregate holds the generation-bound tier.
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if agg.GitCapabilityTier != string(taskauthority.GitTierRewrite) {
		t.Fatalf("authoritative tier = %q, want %q", agg.GitCapabilityTier, taskauthority.GitTierRewrite)
	}

	// The .meta projection is reconciled.
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaGitCapabilityTier] != string(taskauthority.GitTierRewrite) {
		t.Errorf("projected tier = %q, want %q", readMeta[MetaGitCapabilityTier], taskauthority.GitTierRewrite)
	}
}

func TestStoreGitCapabilityTier_ImmutableAfterSet(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-tier-immutable"
	meta := map[string]string{"generation": "1", "kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	if _, err := StoreGitCapabilityTier(homeDir, auth, taskID, taskauthority.GitTierRewrite); err != nil {
		t.Fatalf("StoreGitCapabilityTier(rewrite): %v", err)
	}
	// The launch-time contract is enforced: a different tier on the same
	// generation conflicts (the old meta write silently overwrote; the
	// Authority binds the tier, Task 7.4).
	if _, err := StoreGitCapabilityTier(homeDir, auth, taskID, taskauthority.GitTierCleanup); err == nil {
		t.Fatal("expected conflict when changing the tier within the generation")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.GitCapabilityTier != string(taskauthority.GitTierRewrite) {
		t.Fatalf("tier changed after rejected set: %q", agg.GitCapabilityTier)
	}
}

func TestStoreGitCapabilityTier_FailsClosedWithoutAuthority(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-tier-no-auth"
	if _, err := StoreGitCapabilityTier(homeDir, nil, taskID, taskauthority.GitTierRewrite); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

// --- TierEnough tests ---

func TestTierEnough_Same(t *testing.T) {
	if !TierEnough(taskauthority.GitTierWrite, taskauthority.GitTierWrite) {
		t.Error("write should be enough for write")
	}
}

func TestTierEnough_Higher(t *testing.T) {
	if !TierEnough(taskauthority.GitTierRewrite, taskauthority.GitTierWrite) {
		t.Error("rewrite should be enough for write")
	}
}

func TestTierEnough_Lower(t *testing.T) {
	if TierEnough(taskauthority.GitTierRead, taskauthority.GitTierWrite) {
		t.Error("read should NOT be enough for write")
	}
}

func TestTierEnough_RewriteNeedsRewriteOrHigher(t *testing.T) {
	if TierEnough(taskauthority.GitTierWrite, taskauthority.GitTierRewrite) {
		t.Error("write should NOT be enough for rewrite")
	}
	if !TierEnough(taskauthority.GitTierRewrite, taskauthority.GitTierRewrite) {
		t.Error("rewrite should be enough for rewrite")
	}
	if !TierEnough(taskauthority.GitTierCleanup, taskauthority.GitTierRewrite) {
		t.Error("cleanup should be enough for rewrite")
	}
}

func TestTierEnough_CleanupNeedsCleanupOrHigher(t *testing.T) {
	if TierEnough(taskauthority.GitTierRewrite, taskauthority.GitTierCleanup) {
		t.Error("rewrite should NOT be enough for cleanup")
	}
	if !TierEnough(taskauthority.GitTierCleanup, taskauthority.GitTierCleanup) {
		t.Error("cleanup should be enough for cleanup")
	}
	if !TierEnough(taskauthority.GitTierAdmin, taskauthority.GitTierCleanup) {
		t.Error("admin should be enough for cleanup")
	}
}

// --- OperationRequiresTier tests (moved to the Authority module) ---

func TestOperationRequiresTier_ForceWithLease(t *testing.T) {
	if tier := taskauthority.OperationRequiresTier(taskauthority.GitOpForceWithLease); tier != taskauthority.GitTierRewrite {
		t.Errorf("expected rewrite, got %q", tier)
	}
}

func TestOperationRequiresTier_BranchDelete(t *testing.T) {
	if tier := taskauthority.OperationRequiresTier(taskauthority.GitOpBranchDelete); tier != taskauthority.GitTierCleanup {
		t.Errorf("expected cleanup, got %q", tier)
	}
}

func TestOperationRequiresTier_PushDelete(t *testing.T) {
	if tier := taskauthority.OperationRequiresTier(taskauthority.GitOpPushDelete); tier != taskauthority.GitTierCleanup {
		t.Errorf("expected cleanup, got %q", tier)
	}
}

func TestOperationRequiresTier_ElevatedOps(t *testing.T) {
	elevated := []taskauthority.GitOperation{
		taskauthority.GitOpForceWithLease, taskauthority.GitOpRebase, taskauthority.GitOpReset,
		taskauthority.GitOpAmendCommit, taskauthority.GitOpCherryPick, taskauthority.GitOpRevert,
		taskauthority.GitOpClean, taskauthority.GitOpBranchDelete, taskauthority.GitOpPushDelete,
	}
	for _, op := range elevated {
		tier := taskauthority.OperationRequiresTier(op)
		if tier != taskauthority.GitTierRewrite && tier != taskauthority.GitTierCleanup {
			t.Errorf("operation %q requires tier %q, expected rewrite or cleanup", op, tier)
		}
	}
}

func TestOperationRequiresTier_WriteOps(t *testing.T) {
	// Operations not listed require at most write tier.
	if tier := taskauthority.OperationRequiresTier(taskauthority.GitOperation("add")); tier != taskauthority.GitTierWrite {
		t.Errorf("expected write, got %q", tier)
	}
}

// --- GitMutationAuthorization round-trip ---

func TestGitMutationAuthorization_RoundTrip(t *testing.T) {
	auth := &taskauthority.GitMutationAuthorization{
		Operation: taskauthority.GitOpForceWithLease,
		ExpectedState: taskauthority.GitExpectedState{
			Ref:    "refs/heads/mu/test-task",
			OldSHA: "abc123abc123abc123abc123abc123abc123abc1",
			NewSHA: "def456def456def456def456def456def456def4",
		},
		AuthorizedAt: 1,
		Authorizer:   "general",
		Context:      "amendment",
	}

	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored taskauthority.GitMutationAuthorization
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.Operation != auth.Operation {
		t.Errorf("Operation: got %q, want %q", restored.Operation, auth.Operation)
	}
	if restored.ExpectedState.Ref != auth.ExpectedState.Ref {
		t.Errorf("Ref: got %q, want %q", restored.ExpectedState.Ref, auth.ExpectedState.Ref)
	}
	if restored.ExpectedState.OldSHA != auth.ExpectedState.OldSHA {
		t.Errorf("OldSHA: got %q, want %q", restored.ExpectedState.OldSHA, auth.ExpectedState.OldSHA)
	}
	if restored.ExpectedState.NewSHA != auth.ExpectedState.NewSHA {
		t.Errorf("NewSHA: got %q, want %q", restored.ExpectedState.NewSHA, auth.ExpectedState.NewSHA)
	}
	if restored.Authorizer != auth.Authorizer {
		t.Errorf("Authorizer: got %q, want %q", restored.Authorizer, auth.Authorizer)
	}
	if restored.Context != auth.Context {
		t.Errorf("Context: got %q, want %q", restored.Context, auth.Context)
	}
}

// --- StoreGitMutationAuthorization / ClearStoredGitMutationAuthorization tests ---

func mustGitState(ref, oldSHA, newSHA string) taskauthority.GitExpectedState {
	return taskauthority.GitExpectedState{Ref: ref, OldSHA: oldSHA, NewSHA: newSHA}
}

func TestStoreGitMutationAuthorization_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-store-git-auth"
	meta := map[string]string{"generation": "1", "kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	expected := mustGitState("refs/heads/mu/test-task", "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol", "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew")
	res, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, expected, "general", "amendment")
	if err != nil {
		t.Fatalf("StoreGitMutationAuthorization: %v", err)
	}
	if res.TaskID != taskID || res.Generation != 1 || res.Replayed {
		t.Fatalf("authorize result = %+v", res)
	}

	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.GitMutationAuthorization == nil || agg.GitMutationAuthorization.Operation != taskauthority.GitOpForceWithLease ||
		agg.GitMutationAuthorization.ExpectedState.OldSHA != expected.OldSHA ||
		agg.GitMutationAuthorization.Authorizer != "general" || agg.GitMutationAuthorization.Context != "amendment" {
		t.Fatalf("authoritative record = %+v", agg.GitMutationAuthorization)
	}

	// The .meta projection is reconciled so the safety read path sees it.
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaGitMutationAuth] == "" {
		t.Fatal("git_mutation_authorization projection should be set in meta")
	}
	var stored taskauthority.GitMutationAuthorization
	if err := json.Unmarshal([]byte(readMeta[MetaGitMutationAuth]), &stored); err != nil {
		t.Fatalf("unmarshal stored: %v", err)
	}
	if stored.ExpectedState.OldSHA != expected.OldSHA {
		t.Errorf("stored OldSHA: got %q, want %q", stored.ExpectedState.OldSHA, expected.OldSHA)
	}

	// Clear through the Authority.
	clearRes, err := ClearStoredGitMutationAuthorization(homeDir, auth, taskID)
	if err != nil {
		t.Fatalf("ClearStoredGitMutationAuthorization: %v", err)
	}
	if clearRes.TaskID != taskID {
		t.Fatalf("clear result = %+v", clearRes)
	}
	agg, err = auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.GitMutationAuthorization != nil {
		t.Fatalf("record not cleared: %+v", agg.GitMutationAuthorization)
	}
	readMeta, err = home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaGitMutationAuth] != "" {
		t.Error("git_mutation_authorization projection should be cleared")
	}
}

func TestStoreGitMutationAuthorization_RejectsWriteOps(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-store-writeop"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	// Write-tier operations should not need authorization.
	_, err := StoreGitMutationAuthorization(homeDir, auth, taskID, "add", mustGitState("ref", "old", "new"), "general", "standalone")
	if err == nil {
		t.Fatal("expected error for write-tier operation")
	}
	if !strings.Contains(err.Error(), "does not require authorization") {
		t.Errorf("expected 'does not require authorization' error, got: %v", err)
	}
}

func TestStoreGitMutationAuthorization_RejectsMissingExpectedState(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-store-missing-state"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	// Missing ref.
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, mustGitState("", "old", "new"), "general", "amendment"); err == nil {
		t.Fatal("expected error for missing ref")
	}
	// Missing old SHA.
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, mustGitState("refs/heads/mu/test", "", "new"), "general", "amendment"); err == nil {
		t.Fatal("expected error for missing old SHA")
	}
	// Missing new SHA (for force-with-lease, new SHA is required).
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, mustGitState("refs/heads/mu/test", "old", ""), "general", "amendment"); err == nil {
		t.Fatal("expected error for missing new SHA")
	}
	// Missing new SHA for branch-delete should be allowed.
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpBranchDelete, mustGitState("refs/heads/mu/test", "old", ""), "retirement", "retirement"); err != nil {
		t.Fatalf("expected nil for missing new SHA on branch-delete: %v", err)
	}
}

func TestStoreGitMutationAuthorization_FailsClosedWithoutAuthority(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-store-no-auth"
	if _, err := StoreGitMutationAuthorization(homeDir, nil, taskID, taskauthority.GitOpForceWithLease, mustGitState("r", "o", "n"), "general", "amendment"); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

// --- CheckGitMutationAuthorization tests (read path over the projection) ---

func TestCheckGitMutationAuthorization_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-auth-ok"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := mustGitState("refs/heads/mu/test-task", "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol", "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew")

	// Authorize through the Authority (commits + projects).
	auth := newGitAuthAuthority(t, taskID)
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("StoreGitMutationAuthorization: %v", err)
	}

	// Check with matching current SHA — should succeed.
	checked, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpForceWithLease, expected.OldSHA)
	if err != nil {
		t.Fatalf("CheckGitMutationAuthorization: %v", err)
	}
	if checked.Operation != taskauthority.GitOpForceWithLease {
		t.Errorf("Operation: got %q, want %q", checked.Operation, taskauthority.GitOpForceWithLease)
	}
}

func TestCheckGitMutationAuthorization_NoAuthorization(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-no-auth"

	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpForceWithLease, "sha")
	if err == nil {
		t.Fatal("expected error for no authorization")
	}
	var noAuthErr *ErrNoGitMutationAuthorization
	if !strings.Contains(err.Error(), "no git mutation authorization") {
		t.Errorf("expected 'no git mutation authorization' error, got: %v", err)
	}
	_ = noAuthErr
}

func TestCheckGitMutationAuthorization_WrongOperation(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-wrong-op"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := mustGitState("refs/heads/mu/test-task", "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol", "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew")
	auth := newGitAuthAuthority(t, taskID)
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("StoreGitMutationAuthorization: %v", err)
	}

	// Check for branch-delete — should fail.
	_, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpBranchDelete, "")
	if err == nil {
		t.Fatal("expected error for wrong operation")
	}
	if !strings.Contains(err.Error(), "authorization is for operation") {
		t.Errorf("expected 'authorization is for operation' error, got: %v", err)
	}
}

func TestCheckGitMutationAuthorization_StaleSHA(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-stale-sha"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := mustGitState("refs/heads/mu/test-task", "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol", "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew")
	auth := newGitAuthAuthority(t, taskID)
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("StoreGitMutationAuthorization: %v", err)
	}

	// Check with different current SHA — should fail.
	_, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpForceWithLease, "differentdifferentdifferentdifferentdifferentd")
	if err == nil {
		t.Fatal("expected error for stale SHA")
	}
	if !strings.Contains(err.Error(), "stale") && !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected 'stale' or 'does not match' error, got: %v", err)
	}
}

func TestCheckGitMutationAuthorization_SkipSHA(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-skip-sha"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := mustGitState("refs/heads/mu/test-task", "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol", "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew")
	auth := newGitAuthAuthority(t, taskID)
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("StoreGitMutationAuthorization: %v", err)
	}

	// Check with empty current SHA — allowed (skip SHA check).
	if _, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpForceWithLease, ""); err != nil {
		t.Fatalf("expected nil for empty SHA (skip check): %v", err)
	}
}

// --- Set/Read git auth context through the Authority ---

func TestGitAuthContext_SetAndRead(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ctx-set"
	meta := map[string]string{"generation": "1", "kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	if _, err := StoreGitAuthContext(homeDir, auth, taskID, "amendment"); err != nil {
		t.Fatalf("StoreGitAuthContext: %v", err)
	}

	ctx, err := ReadGitAuthContext(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadGitAuthContext: %v", err)
	}
	if ctx != "amendment" {
		t.Errorf("expected context 'amendment', got %q", ctx)
	}

	// Clear through the Authority.
	if _, err := StoreGitAuthContext(homeDir, auth, taskID, ""); err != nil {
		t.Fatalf("StoreGitAuthContext(clear): %v", err)
	}
	ctx, err = ReadGitAuthContext(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadGitAuthContext: %v", err)
	}
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}
}

func TestGitAuthContext_DefaultEmpty(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ctx-default"

	ctx, err := ReadGitAuthContext(homeDir, taskID)
	if err != nil {
		t.Fatalf("expected nil error for missing meta: %v", err)
	}
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}
}

func TestStoreGitAuthContext_FailsClosedWithoutAuthority(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ctx-no-auth"
	if _, err := StoreGitAuthContext(homeDir, nil, taskID, "amendment"); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

// --- UnrestrictedForceDenied tests ---

func TestUnrestrictedForceDenied_ForceFlag(t *testing.T) {
	if !UnrestrictedForceDenied([]string{"--force"}) {
		t.Error("--force should be denied")
	}
}

func TestUnrestrictedForceDenied_ShortForce(t *testing.T) {
	if !UnrestrictedForceDenied([]string{"-f"}) {
		t.Error("-f should be denied")
	}
}

func TestUnrestrictedForceDenied_ForceWithLease(t *testing.T) {
	if UnrestrictedForceDenied([]string{"--force-with-lease"}) {
		t.Error("--force-with-lease should NOT be denied by UnrestrictedForceDenied")
	}
}

func TestUnrestrictedForceDenied_NoForce(t *testing.T) {
	if UnrestrictedForceDenied([]string{"origin", "main"}) {
		t.Error("normal push should not be denied")
	}
}

func TestUnrestrictedForceDenied_Empty(t *testing.T) {
	if UnrestrictedForceDenied(nil) {
		t.Error("empty args should not be denied")
	}
}

// --- ForceWithLeaseRequested tests ---

func TestForceWithLeaseRequested_LeaseFlag(t *testing.T) {
	if !ForceWithLeaseRequested([]string{"--force-with-lease"}) {
		t.Error("--force-with-lease should be detected")
	}
}

func TestForceWithLeaseRequested_LeaseEquals(t *testing.T) {
	if !ForceWithLeaseRequested([]string{"--force-with-lease=origin/main"}) {
		t.Error("--force-with-lease=ref should be detected")
	}
}

func TestForceWithLeaseRequested_ForceNotLease(t *testing.T) {
	if ForceWithLeaseRequested([]string{"--force"}) {
		t.Error("--force should NOT be detected as force-with-lease")
	}
}

func TestForceWithLeaseRequested_NormalPush(t *testing.T) {
	if ForceWithLeaseRequested([]string{"origin", "main"}) {
		t.Error("normal push should not be detected")
	}
}

// --- PushDeleteRequested tests ---

func TestPushDeleteRequested_DeleteFlag(t *testing.T) {
	if !PushDeleteRequested([]string{"--delete", "origin", "branch"}, "branch") {
		t.Error("--delete should be detected")
	}
}

func TestPushDeleteRequested_DeleteRefspec(t *testing.T) {
	if !PushDeleteRequested([]string{"origin", ":refs/heads/branch"}, ":refs/heads/branch") {
		t.Error("delete refspec should be detected")
	}
}

func TestPushDeleteRequested_NormalPush(t *testing.T) {
	if PushDeleteRequested([]string{"origin", "main"}, "main") {
		t.Error("normal push should not be detected")
	}
}

// --- ReadGitMutationAuthorization tests (projection read) ---

func TestReadGitMutationAuthorization_Exists(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-read-auth"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth := newGitAuthAuthority(t, taskID)
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease, mustGitState("r", "o", "n"), "general", "amendment"); err != nil {
		t.Fatalf("StoreGitMutationAuthorization: %v", err)
	}

	rec, err := ReadGitMutationAuthorization(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadGitMutationAuthorization: %v", err)
	}
	if rec == nil {
		t.Fatal("expected non-nil authorization")
	}
	if rec.Operation != taskauthority.GitOpForceWithLease {
		t.Errorf("Operation: got %q, want %q", rec.Operation, taskauthority.GitOpForceWithLease)
	}
}

func TestReadGitMutationAuthorization_None(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-read-auth-none"

	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	rec, err := ReadGitMutationAuthorization(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadGitMutationAuthorization: %v", err)
	}
	if rec != nil {
		t.Fatal("expected nil authorization")
	}
}

// --- Full flow: context + authorize + check + clear through the Authority ---

func TestForceWithLeaseFullFlow(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-fwl-flow"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	// 1. Set git auth context for amendment.
	if _, err := StoreGitAuthContext(homeDir, auth, taskID, "amendment"); err != nil {
		t.Fatalf("StoreGitAuthContext: %v", err)
	}

	// 2. Authorize force-with-lease.
	oldSHA := "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol"
	newSHA := "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew"
	if _, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpForceWithLease,
		mustGitState("refs/heads/mu/test-task", oldSHA, newSHA), "amendment", "amendment"); err != nil {
		t.Fatalf("StoreGitMutationAuthorization: %v", err)
	}

	// 3. Check authorization with matching SHA.
	checked, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpForceWithLease, oldSHA)
	if err != nil {
		t.Fatalf("CheckGitMutationAuthorization: %v", err)
	}
	if checked == nil {
		t.Fatal("expected non-nil check")
	}

	// 4. Clear authorization after mutation completes.
	if _, err := ClearStoredGitMutationAuthorization(homeDir, auth, taskID); err != nil {
		t.Fatalf("ClearStoredGitMutationAuthorization: %v", err)
	}

	// 5. Verify cleared.
	if _, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpForceWithLease, ""); err == nil {
		t.Fatal("expected error after clearing authorization")
	}
}

// --- Authorize/Refuse rewrite operations ---

func TestAuthorizeRewrite_Authorized(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-rewrite-auth"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	res, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpRebase, mustGitState("r", "o", "n"), "general", "amendment")
	if err != nil {
		t.Fatalf("StoreGitMutationAuthorization(rebase): %v", err)
	}
	if res.TaskID != taskID {
		t.Fatalf("result = %+v", res)
	}
}

func TestAuthorizeRewrite_RefusedWithoutAuth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-rewrite-refused"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Check without authorization — should fail.
	_, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpRebase, "")
	if err == nil {
		t.Fatal("expected error for unauthorized rewrite")
	}
	if !strings.Contains(err.Error(), "no git mutation authorization") {
		t.Errorf("expected 'no git mutation authorization' error, got: %v", err)
	}
}

// --- Authorize/Refuse cleanup operations ---

func TestAuthorizeCleanup_Authorized(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-cleanup-auth"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := newGitAuthAuthority(t, taskID)

	res, err := StoreGitMutationAuthorization(homeDir, auth, taskID, taskauthority.GitOpBranchDelete,
		mustGitState("refs/heads/mu/test-task", "o", ""), "retirement", "retirement")
	if err != nil {
		t.Fatalf("StoreGitMutationAuthorization(branch-delete): %v", err)
	}
	if res.TaskID != taskID {
		t.Fatalf("result = %+v", res)
	}
}

func TestAuthorizeCleanup_RefusedWithoutAuth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-cleanup-refused"
	meta := map[string]string{"generation": "1"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Check without authorization.
	if _, err := CheckGitMutationAuthorization(homeDir, taskID, taskauthority.GitOpBranchDelete, ""); err == nil {
		t.Fatal("expected error for unauthorized cleanup")
	}
}

// --- Typed error types ---

func TestErrNoGitMutationAuthorization_Type(t *testing.T) {
	err := &ErrNoGitMutationAuthorization{
		TaskID:    "test-task",
		Operation: taskauthority.GitOpForceWithLease,
		Reason:    "no record",
	}
	if err.Error() == "" {
		t.Fatal("ErrNoGitMutationAuthorization.Error() should not be empty")
	}
	if !strings.Contains(err.Error(), "test-task") {
		t.Errorf("expected task ID in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "force-with-lease") {
		t.Errorf("expected operation in error, got: %v", err)
	}
}

func TestErrStaleGitMutationAuthorization_Type(t *testing.T) {
	err := &ErrStaleGitMutationAuthorization{
		TaskID:    "test-task",
		Operation: taskauthority.GitOpForceWithLease,
		Expected:  mustGitState("refs/heads/main", "aaa", "bbb"),
		ActualSHA: "ccc",
		Reason:    "SHA changed",
	}
	if err.Error() == "" {
		t.Fatal("ErrStaleGitMutationAuthorization.Error() should not be empty")
	}
	if !strings.Contains(err.Error(), "aaa") || !strings.Contains(err.Error(), "ccc") {
		t.Errorf("expected both SHAs in error, got: %v", err)
	}
}

func TestErrForcePushDenied_Type(t *testing.T) {
	err := &ErrForcePushDenied{
		TaskID: "test-task",
		Reason: "unrestricted force not allowed",
	}
	if err.Error() == "" {
		t.Fatal("ErrForcePushDenied.Error() should not be empty")
	}
	if !strings.Contains(err.Error(), "unrestricted force push denied") {
		t.Errorf("expected 'unrestricted force push denied' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--force-with-lease") {
		t.Errorf("expected remediation hint, got: %v", err)
	}
}
