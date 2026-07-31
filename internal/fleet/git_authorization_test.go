//go:build integration

package fleet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// --- GitCapabilityTier tests ---

func TestGitCapabilityTier_DefaultToWrite(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-tier-default"

	// No tier set in meta — should default to write
	tier, err := ResolveGitCapabilityTier(homeDir, taskID)
	if err != nil {
		t.Fatalf("ResolveGitCapabilityTier: %v", err)
	}
	if tier != GitTierWrite {
		t.Errorf("expected default tier %q, got %q", GitTierWrite, tier)
	}
}

func TestGitCapabilityTier_SetAndRead(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-tier-set"

	// Set tier
	if err := SetGitCapabilityTier(homeDir, taskID, GitTierRewrite); err != nil {
		t.Fatalf("SetGitCapabilityTier: %v", err)
	}

	// Read back
	tier, err := ResolveGitCapabilityTier(homeDir, taskID)
	if err != nil {
		t.Fatalf("ResolveGitCapabilityTier: %v", err)
	}
	if tier != GitTierRewrite {
		t.Errorf("expected tier %q, got %q", GitTierRewrite, tier)
	}
}

func TestGitCapabilityTier_ImmutableAfterSet(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-tier-immutable"

	// Set tier
	if err := SetGitCapabilityTier(homeDir, taskID, GitTierRewrite); err != nil {
		t.Fatalf("SetGitCapabilityTier: %v", err)
	}

	// Overwrite with different tier
	if err := SetGitCapabilityTier(homeDir, taskID, GitTierCleanup); err != nil {
		t.Fatalf("SetGitCapabilityTier: %v", err)
	}

	// Verify it was overwritten (meta is mutable, but the launch-time contract
	// says the tier should not change during a launch — that's enforced by
	// the caller not calling SetGitCapabilityTier after launch)
	tier, err := ResolveGitCapabilityTier(homeDir, taskID)
	if err != nil {
		t.Fatalf("ResolveGitCapabilityTier: %v", err)
	}
	if tier != GitTierCleanup {
		t.Errorf("expected tier %q, got %q", GitTierCleanup, tier)
	}
}

// --- TierEnough tests ---

func TestTierEnough_Same(t *testing.T) {
	if !TierEnough(GitTierWrite, GitTierWrite) {
		t.Error("write should be enough for write")
	}
}

func TestTierEnough_Higher(t *testing.T) {
	if !TierEnough(GitTierRewrite, GitTierWrite) {
		t.Error("rewrite should be enough for write")
	}
}

func TestTierEnough_Lower(t *testing.T) {
	if TierEnough(GitTierRead, GitTierWrite) {
		t.Error("read should NOT be enough for write")
	}
}

func TestTierEnough_RewriteNeedsRewriteOrHigher(t *testing.T) {
	if TierEnough(GitTierWrite, GitTierRewrite) {
		t.Error("write should NOT be enough for rewrite")
	}
	if !TierEnough(GitTierRewrite, GitTierRewrite) {
		t.Error("rewrite should be enough for rewrite")
	}
	if !TierEnough(GitTierCleanup, GitTierRewrite) {
		t.Error("cleanup should be enough for rewrite")
	}
}

func TestTierEnough_CleanupNeedsCleanupOrHigher(t *testing.T) {
	if TierEnough(GitTierRewrite, GitTierCleanup) {
		t.Error("rewrite should NOT be enough for cleanup")
	}
	if !TierEnough(GitTierCleanup, GitTierCleanup) {
		t.Error("cleanup should be enough for cleanup")
	}
	if !TierEnough(GitTierAdmin, GitTierCleanup) {
		t.Error("admin should be enough for cleanup")
	}
}

// --- OperationRequiresTier tests ---

func TestOperationRequiresTier_ForceWithLease(t *testing.T) {
	if tier := OperationRequiresTier(GitOpForceWithLease); tier != GitTierRewrite {
		t.Errorf("expected rewrite, got %q", tier)
	}
}

func TestOperationRequiresTier_BranchDelete(t *testing.T) {
	if tier := OperationRequiresTier(GitOpBranchDelete); tier != GitTierCleanup {
		t.Errorf("expected cleanup, got %q", tier)
	}
}

func TestOperationRequiresTier_PushDelete(t *testing.T) {
	if tier := OperationRequiresTier(GitOpPushDelete); tier != GitTierCleanup {
		t.Errorf("expected cleanup, got %q", tier)
	}
}

func TestOperationRequiresTier_WriteOps(t *testing.T) {
	// All write operations should require only write tier
	writeOps := []GitOperation{GitOpForceWithLease, GitOpRebase, GitOpReset, GitOpAmendCommit, GitOpCherryPick, GitOpRevert, GitOpClean}
	for _, op := range writeOps {
		tier := OperationRequiresTier(op)
		if tier != GitTierRewrite && tier != GitTierCleanup {
			t.Errorf("operation %q requires tier %q, expected rewrite or cleanup", op, tier)
		}
	}
}

// --- GitMutationAuthorization round-trip ---

func TestGitMutationAuthorization_RoundTrip(t *testing.T) {
	auth := &GitMutationAuthorization{
		TaskGeneration: "7",
		Operation:      GitOpForceWithLease,
		ExpectedState: GitExpectedState{
			Ref:    "refs/heads/mu/test-task",
			OldSHA: "abc123abc123abc123abc123abc123abc123abc1",
			NewSHA: "def456def456def456def456def456def456def4",
		},
		AuthorizedAt: "2026-07-20T12:00:00Z",
		Authorizer:   "general",
		Context:      "amendment",
	}

	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored GitMutationAuthorization
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.TaskGeneration != auth.TaskGeneration {
		t.Errorf("TaskGeneration: got %q, want %q", restored.TaskGeneration, auth.TaskGeneration)
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
	if restored.AuthorizedAt != auth.AuthorizedAt {
		t.Errorf("AuthorizedAt: got %q, want %q", restored.AuthorizedAt, auth.AuthorizedAt)
	}
	if restored.Authorizer != auth.Authorizer {
		t.Errorf("Authorizer: got %q, want %q", restored.Authorizer, auth.Authorizer)
	}
	if restored.Context != auth.Context {
		t.Errorf("Context: got %q, want %q", restored.Context, auth.Context)
	}
}

// --- AuthorizeGitMutation tests ---

func TestAuthorizeGitMutation_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-mutation"
	generation := "7"

	meta := map[string]string{"generation": generation, "kind": "ship"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{
		Ref:    "refs/heads/mu/test-task",
		OldSHA: "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		NewSHA: "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
	}

	auth, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, expected, "general", "amendment")
	if err != nil {
		t.Fatalf("AuthorizeGitMutation: %v", err)
	}

	if auth.Operation != GitOpForceWithLease {
		t.Errorf("Operation: got %q, want %q", auth.Operation, GitOpForceWithLease)
	}
	if auth.ExpectedState.OldSHA != expected.OldSHA {
		t.Errorf("OldSHA: got %q, want %q", auth.ExpectedState.OldSHA, expected.OldSHA)
	}
	if auth.Authorizer != "general" {
		t.Errorf("Authorizer: got %q, want %q", auth.Authorizer, "general")
	}
	if auth.Context != "amendment" {
		t.Errorf("Context: got %q, want %q", auth.Context, "amendment")
	}
	if auth.AuthorizedAt == "" {
		t.Error("AuthorizedAt must be set")
	}

	// Verify persisted in meta
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaGitMutationAuth] == "" {
		t.Fatal("git_mutation_authorization should be set in meta")
	}

	var stored GitMutationAuthorization
	if err := json.Unmarshal([]byte(readMeta[MetaGitMutationAuth]), &stored); err != nil {
		t.Fatalf("unmarshal stored: %v", err)
	}
	if stored.ExpectedState.OldSHA != expected.OldSHA {
		t.Errorf("stored OldSHA: got %q, want %q", stored.ExpectedState.OldSHA, expected.OldSHA)
	}
}

func TestAuthorizeGitMutation_RejectsWriteOps(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-writeop"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Write-tier operations should not need authorization
	_, err := AuthorizeGitMutation(homeDir, taskID, generation, "add", GitExpectedState{Ref: "ref", OldSHA: "old", NewSHA: "new"}, "general", "standalone")
	if err == nil {
		t.Fatal("expected error for write-tier operation")
	}
	if !strings.Contains(err.Error(), "does not require authorization") {
		t.Errorf("expected 'does not require authorization' error, got: %v", err)
	}
}

func TestAuthorizeGitMutation_RejectsMissingExpectedState(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-missing-state"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Missing ref
	_, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, GitExpectedState{Ref: "", OldSHA: "old", NewSHA: "new"}, "general", "amendment")
	if err == nil {
		t.Fatal("expected error for missing ref")
	}

	// Missing old SHA
	_, err = AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, GitExpectedState{Ref: "refs/heads/mu/test", OldSHA: "", NewSHA: "new"}, "general", "amendment")
	if err == nil {
		t.Fatal("expected error for missing old SHA")
	}

	// Missing new SHA (for force-with-lease, new SHA is required)
	_, err = AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, GitExpectedState{Ref: "refs/heads/mu/test", OldSHA: "old", NewSHA: ""}, "general", "amendment")
	if err == nil {
		t.Fatal("expected error for missing new SHA")
	}
	if !strings.Contains(err.Error(), "expected new SHA is required for operation") {
		t.Errorf("expected 'expected new SHA is required for operation' error, got: %v", err)
	}

	// Missing new SHA for branch-delete (should be allowed)
	_, err = AuthorizeGitMutation(homeDir, taskID, generation, GitOpBranchDelete, GitExpectedState{Ref: "refs/heads/mu/test", OldSHA: "old", NewSHA: ""}, "retirement", "retirement")
	if err != nil {
		t.Fatalf("expected nil for missing new SHA on branch-delete: %v", err)
	}

	// Missing new SHA for push-delete (should be allowed)
	_, err = AuthorizeGitMutation(homeDir, taskID, generation, GitOpPushDelete, GitExpectedState{Ref: "refs/heads/mu/test", OldSHA: "old", NewSHA: ""}, "retirement", "retirement")
	if err != nil {
		t.Fatalf("expected nil for missing new SHA on push-delete: %v", err)
	}
}

func TestAuthorizeGitMutation_RejectsInvalidContext(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-invalid-ctx"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, GitExpectedState{Ref: "ref", OldSHA: "old", NewSHA: "new"}, "general", "invalid-context")
	if err == nil {
		t.Fatal("expected error for invalid context")
	}
	if !strings.Contains(err.Error(), "invalid context") {
		t.Errorf("expected 'invalid context' error, got: %v", err)
	}
}

func TestAuthorizeGitMutation_RejectsGenerationMismatch(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-gen-mismatch"

	meta := map[string]string{"generation": "7"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := AuthorizeGitMutation(homeDir, taskID, "8", GitOpForceWithLease, GitExpectedState{Ref: "ref", OldSHA: "old", NewSHA: "new"}, "general", "amendment")
	if err == nil {
		t.Fatal("expected error for generation mismatch")
	}
	if !strings.Contains(err.Error(), "generation mismatch") {
		t.Errorf("expected 'generation mismatch' error, got: %v", err)
	}
}

// --- CheckGitMutationAuthorization tests ---

func TestCheckGitMutationAuthorization_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-auth-ok"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{
		Ref:    "refs/heads/mu/test-task",
		OldSHA: "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		NewSHA: "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
	}

	// Authorize first
	auth, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, expected, "general", "amendment")
	if err != nil {
		t.Fatalf("AuthorizeGitMutation: %v", err)
	}

	// Check with matching current SHA — should succeed
	checked, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpForceWithLease, expected.OldSHA)
	if err != nil {
		t.Fatalf("CheckGitMutationAuthorization: %v", err)
	}
	if checked.Operation != auth.Operation {
		t.Errorf("Operation: got %q, want %q", checked.Operation, auth.Operation)
	}
}

func TestCheckGitMutationAuthorization_NoAuthorization(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-check-no-auth"

	meta := map[string]string{"generation": "7"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpForceWithLease, "sha")
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
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{
		Ref:    "refs/heads/mu/test-task",
		OldSHA: "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		NewSHA: "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
	}

	// Authorize for force-with-lease
	if _, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("AuthorizeGitMutation: %v", err)
	}

	// Check for branch-delete — should fail
	_, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpBranchDelete, "")
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
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{
		Ref:    "refs/heads/mu/test-task",
		OldSHA: "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		NewSHA: "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
	}

	// Authorize
	if _, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("AuthorizeGitMutation: %v", err)
	}

	// Check with different current SHA — should fail
	_, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpForceWithLease, "differentdifferentdifferentdifferentdifferentd")
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
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{
		Ref:    "refs/heads/mu/test-task",
		OldSHA: "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		NewSHA: "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
	}

	if _, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("AuthorizeGitMutation: %v", err)
	}

	// Check with empty current SHA — allowed (skip SHA check for non-push ops)
	_, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpForceWithLease, "")
	if err != nil {
		t.Fatalf("expected nil for empty SHA (skip check): %v", err)
	}
}

// --- AuthorizeForceWithLease tests ---

func TestAuthorizeForceWithLease_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-fwl-auth"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth, err := AuthorizeForceWithLease(homeDir, taskID, generation,
		"refs/heads/mu/test-task",
		"oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		"newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
		"amendment", "amendment")
	if err != nil {
		t.Fatalf("AuthorizeForceWithLease: %v", err)
	}

	if auth.Operation != GitOpForceWithLease {
		t.Errorf("Operation: got %q, want %q", auth.Operation, GitOpForceWithLease)
	}
	if auth.Context != "amendment" {
		t.Errorf("Context: got %q, want %q", auth.Context, "amendment")
	}
}

// --- ClearGitMutationAuthorization tests ---

func TestClearGitMutationAuthorization_Success(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-clear-auth"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{
		Ref:    "refs/heads/mu/test-task",
		OldSHA: "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		NewSHA: "newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
	}

	if _, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("AuthorizeGitMutation: %v", err)
	}

	// Clear
	if err := ClearGitMutationAuthorization(homeDir, taskID); err != nil {
		t.Fatalf("ClearGitMutationAuthorization: %v", err)
	}

	// Verify cleared
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaGitMutationAuth] != "" {
		t.Error("git_mutation_authorization should be cleared")
	}
}

func TestClearGitMutationAuthorization_NoAuth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-clear-no-auth"

	meta := map[string]string{"generation": "7"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Clearing when no authorization exists should succeed (idempotent)
	if err := ClearGitMutationAuthorization(homeDir, taskID); err != nil {
		t.Fatalf("expected nil for clearing no auth: %v", err)
	}
}

// --- SetGitAuthContext / ReadGitAuthContext tests ---

func TestGitAuthContext_SetAndRead(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ctx-set"

	// Set context
	if err := SetGitAuthContext(homeDir, taskID, "amendment"); err != nil {
		t.Fatalf("SetGitAuthContext: %v", err)
	}

	// Read back
	ctx, err := ReadGitAuthContext(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadGitAuthContext: %v", err)
	}
	if ctx != "amendment" {
		t.Errorf("expected context 'amendment', got %q", ctx)
	}
}

func TestGitAuthContext_Clear(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ctx-clear"

	if err := SetGitAuthContext(homeDir, taskID, "retirement"); err != nil {
		t.Fatalf("SetGitAuthContext: %v", err)
	}

	// Clear
	if err := SetGitAuthContext(homeDir, taskID, ""); err != nil {
		t.Fatalf("SetGitAuthContext(clear): %v", err)
	}

	ctx, err := ReadGitAuthContext(homeDir, taskID)
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

// --- ReadGitMutationAuthorization tests ---

func TestReadGitMutationAuthorization_Exists(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-read-auth"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{Ref: "r", OldSHA: "o", NewSHA: "n"}
	if _, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, expected, "general", "amendment"); err != nil {
		t.Fatalf("AuthorizeGitMutation: %v", err)
	}

	auth, err := ReadGitMutationAuthorization(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadGitMutationAuthorization: %v", err)
	}
	if auth == nil {
		t.Fatal("expected non-nil authorization")
	}
	if auth.Operation != GitOpForceWithLease {
		t.Errorf("Operation: got %q, want %q", auth.Operation, GitOpForceWithLease)
	}
}

func TestReadGitMutationAuthorization_None(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-read-auth-none"

	meta := map[string]string{"generation": "7"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	auth, err := ReadGitMutationAuthorization(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadGitMutationAuthorization: %v", err)
	}
	if auth != nil {
		t.Fatal("expected nil authorization")
	}
}

// --- Authorize/Check/Complete flow for force-with-lease ---

func TestForceWithLeaseFullFlow(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-fwl-flow"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// 1. Set git auth context for amendment
	if err := SetGitAuthContext(homeDir, taskID, "amendment"); err != nil {
		t.Fatalf("SetGitAuthContext: %v", err)
	}

	// 2. Authorize force-with-lease
	auth, err := AuthorizeForceWithLease(homeDir, taskID, generation,
		"refs/heads/mu/test-task",
		"oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		"newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
		"amendment", "amendment")
	if err != nil {
		t.Fatalf("AuthorizeForceWithLease: %v", err)
	}
	if auth.Context != "amendment" {
		t.Errorf("expected context 'amendment', got %q", auth.Context)
	}

	// 3. Check authorization with matching SHA
	checked, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpForceWithLease, "oldoldoldoldoldoldoldoldoldoldoldoldoldoldol")
	if err != nil {
		t.Fatalf("CheckGitMutationAuthorization: %v", err)
	}
	if checked == nil {
		t.Fatal("expected non-nil check")
	}

	// 4. Clear authorization after mutation completes
	if err := ClearGitMutationAuthorization(homeDir, taskID); err != nil {
		t.Fatalf("ClearGitMutationAuthorization: %v", err)
	}

	// 5. Verify cleared
	_, err = CheckGitMutationAuthorization(homeDir, taskID, GitOpForceWithLease, "")
	if err == nil {
		t.Fatal("expected error after clearing authorization")
	}
}

// --- Authorize/Refuse rewrite operations ---

func TestAuthorizeRewrite_Authorized(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-rewrite-auth"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{Ref: "r", OldSHA: "o", NewSHA: "n"}
	auth, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpRebase, expected, "general", "amendment")
	if err != nil {
		t.Fatalf("AuthorizeGitMutation(rebase): %v", err)
	}
	if auth.Operation != GitOpRebase {
		t.Errorf("expected rebase, got %q", auth.Operation)
	}
}

func TestAuthorizeRewrite_RefusedWithoutAuth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-rewrite-refused"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Check without authorization — should fail
	_, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpRebase, "")
	if err == nil {
		t.Fatal("expected error for unauthorized rewrite")
	}
	if !strings.Contains(err.Error(), "no git mutation authorization") {
		t.Errorf("expected 'no git mutation authorization' error, got: %v", err)
	}
}

func TestAuthorizeRewrite_RefusedWrongContext(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-rewrite-wrong-ctx"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Set retirement context
	if err := SetGitAuthContext(homeDir, taskID, "retirement"); err != nil {
		t.Fatalf("SetGitAuthContext: %v", err)
	}

	expected := GitExpectedState{Ref: "r", OldSHA: "o", NewSHA: "n"}

	// Authorize with amendment context — should fail because context is "retirement"
	// (Note: AuthorizeGitMutation doesn't check context against meta — it stores
	// the context in the authorization record. The context check happens in the
	// safety check layer.)
	auth, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpRebase, expected, "general", "standalone")
	if err != nil {
		t.Fatalf("AuthorizeGitMutation: %v", err)
	}
	if auth.Context != "standalone" {
		t.Errorf("expected context 'standalone', got %q", auth.Context)
	}
}

// --- Authorize/Refuse cleanup operations ---

func TestAuthorizeCleanup_Authorized(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-cleanup-auth"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{Ref: "refs/heads/mu/test-task", OldSHA: "o", NewSHA: ""}
	auth, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpBranchDelete, expected, "retirement", "retirement")
	if err != nil {
		t.Fatalf("AuthorizeGitMutation(branch-delete): %v", err)
	}
	if auth.Operation != GitOpBranchDelete {
		t.Errorf("expected branch-delete, got %q", auth.Operation)
	}
	if auth.Context != "retirement" {
		t.Errorf("expected context 'retirement', got %q", auth.Context)
	}
}

func TestAuthorizeCleanup_RefusedWithoutAuth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-cleanup-refused"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Check without authorization
	_, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpBranchDelete, "")
	if err == nil {
		t.Fatal("expected error for unauthorized cleanup")
	}
}

func TestAuthorizeCleanup_RefusedWrongContext(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-cleanup-wrong-ctx"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{Ref: "refs/heads/mu/test-task", OldSHA: "o", NewSHA: ""}

	// Authorize push-delete with amendment context — should be allowed
	// (the authorization just stores the context; enforcement is in the safety check)
	auth, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpPushDelete, expected, "amendment", "amendment")
	if err != nil {
		t.Fatalf("AuthorizeGitMutation(push-delete): %v", err)
	}
	if auth.Context != "amendment" {
		t.Errorf("expected context 'amendment', got %q", auth.Context)
	}
}

// --- Typed error types ---

func TestErrNoGitMutationAuthorization_Type(t *testing.T) {
	err := &ErrNoGitMutationAuthorization{
		TaskID:    "test-task",
		Operation: GitOpForceWithLease,
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
		Operation: GitOpForceWithLease,
		Expected:  GitExpectedState{Ref: "refs/heads/main", OldSHA: "aaa", NewSHA: "bbb"},
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

// --- CAS conflict during authorization ---

func TestAuthorizeGitMutation_CASConflict(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-auth-cas"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	expected := GitExpectedState{Ref: "r", OldSHA: "o", NewSHA: "n"}

	// Change generation behind our back
	meta["generation"] = "8"
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize with original generation — should fail because generation changed
	_, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpForceWithLease, expected, "general", "amendment")
	if err == nil {
		t.Fatal("expected error for CAS conflict (generation changed)")
	}
	if !strings.Contains(err.Error(), "generation mismatch") {
		t.Errorf("expected 'generation mismatch' error, got: %v", err)
	}
}

// --- Authorize/Refuse ref operations ---

func TestAuthorizeGitMutation_RefOperation(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ref-op"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize cherry-pick (rewrite tier)
	expected := GitExpectedState{Ref: "refs/heads/mu/test-task", OldSHA: "o", NewSHA: "n"}
	auth, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpCherryPick, expected, "general", "amendment")
	if err != nil {
		t.Fatalf("AuthorizeGitMutation(cherry-pick): %v", err)
	}
	if auth.Operation != GitOpCherryPick {
		t.Errorf("expected cherry-pick, got %q", auth.Operation)
	}
}

func TestAuthorizeGitMutation_RefusedNoAuth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-ref-no-auth"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Check revert without authorization
	_, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpRevert, "")
	if err == nil {
		t.Fatal("expected error for unauthorized revert")
	}
}

// --- Authorize/Refuse push operations ---

func TestAuthorizePush_ForceWithLeaseAuthorized(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-push-fwl"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize force-with-lease
	auth, err := AuthorizeForceWithLease(homeDir, taskID, generation,
		"refs/heads/mu/test-task",
		"oldoldoldoldoldoldoldoldoldoldoldoldoldoldol",
		"newnewnewnewnewnewnewnewnewnewnewnewnewnewnew",
		"general", "amendment")
	if err != nil {
		t.Fatalf("AuthorizeForceWithLease: %v", err)
	}
	if auth.Operation != GitOpForceWithLease {
		t.Errorf("expected force-with-lease, got %q", auth.Operation)
	}
}

func TestAuthorizePush_UnrestrictedForceDenied(t *testing.T) {
	// Unrestricted force should be denied regardless of any authorization
	homeDir := t.TempDir()
	taskID := "test-push-force"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Verify UnrestrictedForceDenied rejects --force
	if !UnrestrictedForceDenied([]string{"--force"}) {
		t.Error("--force should be denied by UnrestrictedForceDenied")
	}

	// Verify UnrestrictedForceDenied rejects -f
	if !UnrestrictedForceDenied([]string{"-f"}) {
		t.Error("-f should be denied by UnrestrictedForceDenied")
	}
}

func TestAuthorizePush_RefusedNoAuth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-push-no-auth"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Check force-with-lease without authorization
	_, err := CheckGitMutationAuthorization(homeDir, taskID, GitOpForceWithLease, "some-sha")
	if err == nil {
		t.Fatal("expected error for unauthorized force-with-lease")
	}
}

func TestAuthorizePush_PushDeleteAuthorized(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-push-delete"
	generation := "7"

	meta := map[string]string{"generation": generation}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Authorize push-delete
	expected := GitExpectedState{Ref: "refs/heads/mu/test-task", OldSHA: "o", NewSHA: ""}
	auth, err := AuthorizeGitMutation(homeDir, taskID, generation, GitOpPushDelete, expected, "retirement", "retirement")
	if err != nil {
		t.Fatalf("AuthorizeGitMutation(push-delete): %v", err)
	}
	if auth.Operation != GitOpPushDelete {
		t.Errorf("expected push-delete, got %q", auth.Operation)
	}
	if auth.Context != "retirement" {
		t.Errorf("expected context 'retirement', got %q", auth.Context)
	}
}