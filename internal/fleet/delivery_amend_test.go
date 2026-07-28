package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// --- Test helpers ---

func testIdentity() *DeliveryIdentity {
	return &DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:    "main",
		HeadRef:    "feature/test",
		HeadSHA:    "aaa111aaa111aaa111aaa111aaa111aaa111aaa1",
		CapturedAt: "2026-07-18T12:00:00Z",
	}
}

func testNewHeadIdentity() *DeliveryIdentity {
	return &DeliveryIdentity{
		Provider:   "github",
		Owner:      "minhtri2710",
		Repo:       "munsu",
		Number:     42,
		URL:        "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef:    "main",
		HeadRef:    "feature/test",
		HeadSHA:    "bbb222bbb222bbb222bbb222bbb222bbb222bbb2",
		CapturedAt: "2026-07-19T12:00:00Z",
	}
}

// buildAmendGitRepo creates a git repo with two commits: old (ancestor) and new (descendant).
// Returns (repoPath, oldSHA, newSHA).
func buildAmendGitRepo(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()

	gitEnv := gitEnvForDir(repo)

	// Init
	cmd := exec.Command("git", "init")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	for _, cfg := range []string{"user.email test@test.com", "user.name Test"} {
		parts := strings.Split(cfg, " ")
		c := exec.Command("git", append([]string{"config"}, parts...)...)
		c.Dir = repo
		c.Env = gitEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %s", cfg, out)
		}
	}

	// First commit (old head)
	oldFile := filepath.Join(repo, "old.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add old: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "old")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit old: %s", out)
	}

	oldSHAOut, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse old: %v", err)
	}
	oldSHA := strings.TrimSpace(string(oldSHAOut))

	// Second commit (new head, descendant of old)
	newFile := filepath.Join(repo, "new.txt")
	if err := os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatalf("write new: %v", err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add new: %s", out)
	}
	cmd = exec.Command("git", "commit", "-m", "new")
	cmd.Dir = repo
	cmd.Env = gitEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit new: %s", out)
	}

	newSHAOut, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse new: %v", err)
	}
	newSHA := strings.TrimSpace(string(newSHAOut))

	// Verify ancestry
	ancCmd := exec.Command("git", "merge-base", "--is-ancestor", oldSHA, newSHA)
	ancCmd.Dir = repo
	ancCmd.Env = gitEnv
	if err := ancCmd.Run(); err != nil {
		t.Fatalf("old is not ancestor of new: %v", err)
	}

	return repo, oldSHA, newSHA
}

// --- BeginAmendment tests ---

func TestBeginAmendment_Success(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-begin-amend"

	// Write meta with identity and delivery_state=review-ready
	meta := testIdentity().ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateReviewReady)
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	result, err := BeginAmendment(homeDir, id)
	if err != nil {
		t.Fatalf("BeginAmendment: %v", err)
	}

	if result[MetaDeliveryState] != string(DeliveryStateAmending) {
		t.Errorf("expected state %q, got %q", DeliveryStateAmending, result[MetaDeliveryState])
	}
	if result[MetaAmendExpectedHead] != testIdentity().HeadSHA {
		t.Errorf("expected amend_expected_head %q, got %q", testIdentity().HeadSHA, result[MetaAmendExpectedHead])
	}
}

func TestBeginAmendment_DefaultsToReviewReady(t *testing.T) {
	// When delivery_state is not set, BeginAmendment should treat it as
	// "not yet set" and allow the transition to amending.
	homeDir := t.TempDir()
	id := "test-begin-default"

	// Write meta with identity but no delivery_state
	meta := testIdentity().ToMeta()
	// No MetaDeliveryState
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	result, err := BeginAmendment(homeDir, id)
	if err != nil {
		t.Fatalf("BeginAmendment with unset state: %v", err)
	}

	if result[MetaDeliveryState] != string(DeliveryStateAmending) {
		t.Errorf("expected state %q, got %q", DeliveryStateAmending, result[MetaDeliveryState])
	}
}

func TestBeginAmendment_RejectsWrongState(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-begin-wrong"

	meta := testIdentity().ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateAmending)
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := BeginAmendment(homeDir, id)
	if err == nil {
		t.Fatal("expected error for amending->amending transition")
	}
	if !strings.Contains(err.Error(), "cannot amend from state") {
		t.Errorf("expected 'cannot amend from state' error, got: %v", err)
	}
}

func TestBeginAmendment_RejectsMergedState(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-begin-merged"

	meta := testIdentity().ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateMerged)
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := BeginAmendment(homeDir, id)
	if err == nil {
		t.Fatal("expected error for merged->amending transition")
	}
}

func TestBeginAmendment_NoIdentity(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-begin-no-ident"

	meta := map[string]string{"project": "test", "delivery_state": string(DeliveryStateReviewReady)}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, err := BeginAmendment(homeDir, id)
	if err == nil {
		t.Fatal("expected error for no identity")
	}
}

func TestBeginAmendment_CASConflict(t *testing.T) {
	// Test that CompareAndSwapMeta fails when expected values don't match.
	// This is the CAS primitive test; concurrent begin-amendment scenarios
	// are covered by the per-field CAS checks within BeginAmendment itself.
	homeDir := t.TempDir()
	id := "test-cas-primitive"

	ident := testIdentity()
	meta := ident.ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateReviewReady)
	meta["project"] = "test-project"
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// CAS with wrong expected head should fail
	_, err := home.CompareAndSwapMeta(homeDir, id,
		map[string]string{"pr_head_sha": "WRONGWRONGWRONGWRONGWRONGWRONGWRONGWRONGWR"},
		map[string]string{MetaDeliveryState: string(DeliveryStateAmending)},
	)
	if err == nil {
		t.Fatal("expected CAS error for wrong expected head")
	}
	var casErr *home.CASError
	if !strings.Contains(err.Error(), "cas conflict") {
		t.Errorf("expected 'cas conflict' error, got: %v", err)
	}
	_ = casErr

	// CAS with correct expected head should succeed
	result, err := home.CompareAndSwapMeta(homeDir, id,
		map[string]string{"pr_head_sha": ident.HeadSHA, MetaDeliveryState: string(DeliveryStateReviewReady)},
		map[string]string{MetaDeliveryState: string(DeliveryStateAmending)},
	)
	if err != nil {
		t.Fatalf("expected CAS success: %v", err)
	}
	if result[MetaDeliveryState] != string(DeliveryStateAmending) {
		t.Errorf("expected state %q, got %q", DeliveryStateAmending, result[MetaDeliveryState])
	}

	// Second CAS with stale state should fail (already amending)
	_, err = home.CompareAndSwapMeta(homeDir, id,
		map[string]string{MetaDeliveryState: string(DeliveryStateReviewReady)},
		map[string]string{MetaDeliveryState: string(DeliveryStateAmending)},
	)
	if err == nil {
		t.Fatal("expected CAS error for stale state")
	}
	if !strings.Contains(err.Error(), "cas conflict") {
		t.Errorf("expected 'cas conflict' error, got: %v", err)
	}
}

// --- AcceptAmendment tests ---

func TestAcceptAmendment_Success(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-accept-success"
	repo, oldSHA, newSHA := buildAmendGitRepo(t)

	// Write identity with old head
	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateAmending)
	meta[MetaAmendExpectedHead] = oldSHA
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Inject provider snapshot that returns the new head
	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider:   "github",
			Owner:      "minhtri2710",
			Repo:       "munsu",
			Number:     42,
			URL:        "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef:    "main",
			HeadRef:    "feature/test",
			HeadSHA:    newSHA,
			State:      "OPEN",
			Merged:     false,
			MergedSHA:  "",
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	newIdent, record, err := AcceptAmendment(homeDir, id, repo)
	if err != nil {
		t.Fatalf("AcceptAmendment: %v", err)
	}

	if newIdent.HeadSHA != newSHA {
		t.Errorf("expected new head %q, got %q", newSHA, newIdent.HeadSHA)
	}
	if record.OldHeadSHA != oldSHA {
		t.Errorf("expected old head %q in record, got %q", oldSHA, record.OldHeadSHA)
	}
	if record.NewHeadSHA != newSHA {
		t.Errorf("expected new head %q in record, got %q", newSHA, record.NewHeadSHA)
	}

	// Verify meta was updated
	readMeta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaDeliveryState] != string(DeliveryStateReviewReady) {
		t.Errorf("expected state %q, got %q", DeliveryStateReviewReady, readMeta[MetaDeliveryState])
	}
	if readMeta["pr_head_sha"] != newSHA {
		t.Errorf("expected pr_head_sha %q, got %q", newSHA, readMeta["pr_head_sha"])
	}
	// Verify amend_expected_head is cleared
	if v, ok := readMeta[MetaAmendExpectedHead]; ok && v != "" {
		t.Errorf("amend_expected_head should be cleared, got %q", v)
	}
	// Verify amend_started_at is cleared
	if v, ok := readMeta[MetaAmendStartedAt]; ok && v != "" {
		t.Errorf("amend_started_at should be cleared, got %q", v)
	}
}

func TestAcceptAmendment_ForcePushRejected(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-accept-force-push"
	repo, oldSHA, _ := buildAmendGitRepo(t)

	// Create a divergent commit via an orphan branch (not descendant of old)
	gitEnv := gitEnvForDir(repo)
	// Create a temporary branch at the initial state
	orphanCmd := exec.Command("git", "checkout", "--orphan", "divergent")
	orphanCmd.Dir = repo
	orphanCmd.Env = gitEnv
	if out, err := orphanCmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout --orphan: %s", out)
	}
	// Remove existing files
	resetCmd := exec.Command("git", "rm", "-rf", ".")
	resetCmd.Dir = repo
	resetCmd.Env = gitEnv
	if out, err := resetCmd.CombinedOutput(); err != nil {
		t.Fatalf("git rm: %s", out)
	}
	divFile := filepath.Join(repo, "div.txt")
	if err := os.WriteFile(divFile, []byte("diverged"), 0644); err != nil {
		t.Fatalf("write div: %v", err)
	}
	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = repo
	addCmd.Env = gitEnv
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	commitCmd := exec.Command("git", "commit", "-m", "diverged")
	commitCmd.Dir = repo
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}

	divSHAOut, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse div: %v", err)
	}
	divSHA := strings.TrimSpace(string(divSHAOut))

	// Verify old is NOT ancestor of div
	ancCmd := exec.Command("git", "merge-base", "--is-ancestor", oldSHA, divSHA)
	ancCmd.Dir = repo
	ancCmd.Env = gitEnv
	if ancCmd.Run() == nil {
		t.Fatal("expected old not to be ancestor of div")
	}

	// Switch back to main branch for the test
	checkoutCmd := exec.Command("git", "checkout", "-")
	checkoutCmd.Dir = repo
	checkoutCmd.Env = gitEnv
	checkoutCmd.Run()

	// Write identity with old head
	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateAmending)
	meta[MetaAmendExpectedHead] = oldSHA
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: divSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	_, _, err = AcceptAmendment(homeDir, id, repo)
	if err == nil {
		t.Fatal("expected error for force-push (non-ancestor)")
	}
	if !strings.Contains(err.Error(), "not an ancestor") {
		t.Errorf("expected 'not an ancestor' error, got: %v", err)
	}
}

func TestAcceptAmendment_WrongState(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-accept-wrong-state"
	repo, _, _ := buildAmendGitRepo(t)

	stored := testIdentity()
	meta := stored.ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateReviewReady) // not amending
	meta[MetaAmendExpectedHead] = stored.HeadSHA
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, _, err := AcceptAmendment(homeDir, id, repo)
	if err == nil {
		t.Fatal("expected error for wrong state")
	}
	if !strings.Contains(err.Error(), "expected state") {
		t.Errorf("expected 'expected state' error, got: %v", err)
	}
}

func TestAcceptAmendment_CASConflict(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-accept-cas"
	repo, oldSHA, newSHA := buildAmendGitRepo(t)

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateAmending)
	meta[MetaAmendExpectedHead] = oldSHA
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: newSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	// Change head SHA behind our back (simulate concurrent modification)
	meta["pr_head_sha"] = "otherhashotherhashotherhashotherhashotherhash"
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, _, err := AcceptAmendment(homeDir, id, repo)
	if err == nil {
		t.Fatal("expected error for CAS conflict")
	}
}

func TestAcceptAmendment_HeadRefChanged(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-accept-headref"
	repo, oldSHA, newSHA := buildAmendGitRepo(t)

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	meta[MetaDeliveryState] = string(DeliveryStateAmending)
	meta[MetaAmendExpectedHead] = oldSHA
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		// Different head ref — branch replacement
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/replaced", // different head ref!
			HeadSHA: newSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	_, _, err := AcceptAmendment(homeDir, id, repo)
	if err == nil {
		t.Fatal("expected error for head ref change")
	}
	if !strings.Contains(err.Error(), "head ref mismatch") {
		t.Errorf("expected 'head ref mismatch' error, got: %v", err)
	}
}

// --- ReconcileIdentity tests ---

func TestReconcileIdentity_AlreadyUpToDate(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-reconcile-uptodate"
	repo, oldSHA, _ := buildAmendGitRepo(t)

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		// Same head as stored — up to date
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: oldSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	newIdent, record, err := ReconcileIdentity(homeDir, id, repo)
	if err != nil {
		t.Fatalf("ReconcileIdentity: %v", err)
	}
	if record != nil {
		t.Fatal("expected nil record for up-to-date identity")
	}
	if newIdent.HeadSHA != oldSHA {
		t.Errorf("expected unchanged head %q, got %q", oldSHA, newIdent.HeadSHA)
	}
}

func TestReconcileIdentity_AdvancedHead(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-reconcile-adv"
	repo, oldSHA, newSHA := buildAmendGitRepo(t)

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: newSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	newIdent, record, err := ReconcileIdentity(homeDir, id, repo)
	if err != nil {
		t.Fatalf("ReconcileIdentity: %v", err)
	}
	if record == nil {
		t.Fatal("expected non-nil record for reconciled identity")
	}
	if record.Reason != "reconciliation" {
		t.Errorf("expected reason 'reconciliation', got %q", record.Reason)
	}
	if newIdent.HeadSHA != newSHA {
		t.Errorf("expected new head %q, got %q", newSHA, newIdent.HeadSHA)
	}

	// Verify meta was updated
	readMeta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta["pr_head_sha"] != newSHA {
		t.Errorf("expected pr_head_sha %q, got %q", newSHA, readMeta["pr_head_sha"])
	}
	if readMeta[MetaDeliveryState] != string(DeliveryStateReviewReady) {
		t.Errorf("expected delivery_state %q, got %q", DeliveryStateReviewReady, readMeta[MetaDeliveryState])
	}
}

func TestReconcileIdentity_MergedPR(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-reconcile-merged"
	repo, oldSHA, newSHA := buildAmendGitRepo(t)

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: newSHA, State: "MERGED", Merged: true,
			MergedSHA:  "mergemergemergemergemergemergemergemergemerge",
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	_, record, err := ReconcileIdentity(homeDir, id, repo)
	if err != nil {
		t.Fatalf("ReconcileIdentity: %v", err)
	}
	if record == nil {
		t.Fatal("expected non-nil record")
	}

	// Verify meta shows merged state
	readMeta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if readMeta[MetaDeliveryState] != string(DeliveryStateMerged) {
		t.Errorf("expected delivery_state %q, got %q", DeliveryStateMerged, readMeta[MetaDeliveryState])
	}
}

func TestReconcileIdentity_ForcePushRejected(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-reconcile-fp"
	repo, oldSHA, _ := buildAmendGitRepo(t)

	// Create divergent commit via orphan branch
	gitEnv := gitEnvForDir(repo)
	orphanCmd := exec.Command("git", "checkout", "--orphan", "divergent2")
	orphanCmd.Dir = repo
	orphanCmd.Env = gitEnv
	if out, err := orphanCmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout --orphan: %s", out)
	}
	resetCmd := exec.Command("git", "rm", "-rf", ".")
	resetCmd.Dir = repo
	resetCmd.Env = gitEnv
	if out, err := resetCmd.CombinedOutput(); err != nil {
		t.Fatalf("git rm: %s", out)
	}
	divFile := filepath.Join(repo, "div.txt")
	if err := os.WriteFile(divFile, []byte("diverged"), 0644); err != nil {
		t.Fatalf("write div: %v", err)
	}
	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = repo
	addCmd.Env = gitEnv
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	commitCmd := exec.Command("git", "commit", "-m", "diverged")
	commitCmd.Dir = repo
	commitCmd.Env = gitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}
	divSHAOut, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	divSHA := strings.TrimSpace(string(divSHAOut))

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: divSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	_, _, err := ReconcileIdentity(homeDir, id, repo)
	if err == nil {
		t.Fatal("expected error for force-push (non-ancestor)")
	}
}

func TestReconcileIdentity_WrongRef(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-reconcile-wrongref"
	repo, oldSHA, newSHA := buildAmendGitRepo(t)

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		// Different head ref — branch replacement
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/replaced",
			HeadSHA: newSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	_, _, err := ReconcileIdentity(homeDir, id, repo)
	if err == nil {
		t.Fatal("expected error for head ref mismatch")
	}
}

func TestReconcileIdentity_DuplicateIdempotent(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-reconcile-dedup"
	repo, oldSHA, newSHA := buildAmendGitRepo(t)

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	snap := &ProviderSnapshot{
		Provider: "github", Owner: "minhtri2710", Repo: "munsu",
		Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
		BaseRef: "main", HeadRef: "feature/test",
		HeadSHA: newSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
	}
	snapCount := 0
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		snapCount++
		return snap, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	// First reconciliation
	_, record1, err := ReconcileIdentity(homeDir, id, repo)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if record1 == nil {
		t.Fatal("expected record for first reconcile")
	}

	// Second reconciliation — should be idempotent (already up to date)
	_, record2, err := ReconcileIdentity(homeDir, id, repo)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if record2 != nil {
		t.Fatal("expected nil record for duplicate reconcile (already up to date)")
	}
}

func TestReconcileIdentity_CASConflict(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-reconcile-cas"
	repo, oldSHA, newSHA := buildAmendGitRepo(t)

	stored := testIdentity()
	stored.HeadSHA = oldSHA
	meta := stored.ToMeta()
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(prURL string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: "https://github.com/minhtri2710/munsu/pull/42",
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: newSHA, State: "OPEN", ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	// Change stored head behind our back
	meta["pr_head_sha"] = "stalehashstalehashstalehashstalehashstalehash"
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	_, _, err := ReconcileIdentity(homeDir, id, repo)
	if err == nil {
		t.Fatal("expected error for CAS conflict")
	}
}

// --- VerifyAncestry tests ---

func TestVerifyAncestry_NonExistentRepo(t *testing.T) {
	err := verifyAncestry("/nonexistent/path", "abc", "def")
	if err == nil {
		t.Fatal("expected error for nonexistent repo")
	}
}

func TestVerifyAncestry_EmptySHA(t *testing.T) {
	repo, _, _ := buildAmendGitRepo(t)
	if err := verifyAncestry(repo, "", "abc"); err == nil {
		t.Fatal("expected error for empty SHA")
	}
	if err := verifyAncestry(repo, "abc", ""); err == nil {
		t.Fatal("expected error for empty SHA")
	}
}

func TestVerifyAncestry_IdenticalSHA(t *testing.T) {
	repo, oldSHA, _ := buildAmendGitRepo(t)
	if err := verifyAncestry(repo, oldSHA, oldSHA); err == nil {
		t.Fatal("expected error for identical SHAs")
	}
}

// --- VerifyDoneIdentity tests ---

func TestVerifyDoneIdentity_NonPR(t *testing.T) {
	homeDir := t.TempDir()
	id := "test"

	// Write minimal meta so ReadMeta succeeds
	meta := map[string]string{"kind": "ship", "project": "test"}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	err := VerifyDoneIdentity(homeDir, id, "task complete, no PR")
	if err != nil {
		t.Fatalf("expected nil for non-PR message: %v", err)
	}
}

func TestVerifyDoneIdentity_OpenPR(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-done-open"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	// Write minimal meta so ReadMeta succeeds
	meta := map[string]string{"kind": "ship"}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: prURL,
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: "abc123", State: "OPEN",
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	err := VerifyDoneIdentity(homeDir, id, "PR "+prURL)
	if err == nil {
		t.Fatal("expected error for open PR")
	}
	if !strings.Contains(err.Error(), "still open") && !strings.Contains(err.Error(), "is open") {
		t.Errorf("expected open PR error, got: %v", err)
	}
}

func TestVerifyDoneIdentity_ClosedUnmerged(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-done-closed"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	meta := map[string]string{"kind": "ship"}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: "abc123", State: "CLOSED", Merged: false,
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	err := VerifyDoneIdentity(homeDir, id, "PR "+prURL)
	if err == nil {
		t.Fatal("expected error for closed-unmerged PR")
	}
	if !strings.Contains(err.Error(), "closed but not merged") {
		t.Errorf("expected 'closed but not merged' error, got: %v", err)
	}
}

func TestVerifyDoneIdentity_MergedHeadMatches(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-done-merged-ok"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	// Write matching identity
	ident := testIdentity()
	if err := home.WriteMeta(homeDir, id, ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: ident.HeadSHA, State: "MERGED", Merged: true,
			MergedSHA:  "mergemergemergemergemergemergemergemergemerge",
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	err := VerifyDoneIdentity(homeDir, id, "PR "+prURL)
	if err != nil {
		t.Fatalf("expected nil for merged PR with matching head: %v", err)
	}
}

func TestVerifyDoneIdentity_MergedHeadMismatch(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-done-merged-mismatch"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	// Write identity with different head
	ident := testIdentity()
	ident.HeadSHA = "storedshastoredshastoredshastoredshastoreds"
	if err := home.WriteMeta(homeDir, id, ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: "differentdifferentdifferentdifferentdifferent",
			State:   "MERGED", Merged: true,
			MergedSHA:  "mergemergemergemergemergemergemergemergemerge",
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	err := VerifyDoneIdentity(homeDir, id, "PR "+prURL)
	if err == nil {
		t.Fatal("expected error for head mismatch on merged PR")
	}
	if !strings.Contains(err.Error(), "differs from stored") && !strings.Contains(err.Error(), "reconciliation") {
		t.Errorf("expected reconciliation directive, got: %v", err)
	}
}

func TestVerifyDoneIdentity_NoIdentity(t *testing.T) {
	homeDir := t.TempDir()
	id := "test-done-no-identity"
	prURL := "https://github.com/minhtri2710/munsu/pull/42"

	// Write meta without PR identity (ship task, no pr_url)
	meta := map[string]string{"kind": "ship", "project": "test"}
	if err := home.WriteMeta(homeDir, id, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: "abc123", State: "MERGED", Merged: true,
			MergedSHA:  "mergemergemergemergemergemergemergemergemerge",
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	// Should work — no stored identity to check against
	err := VerifyDoneIdentity(homeDir, id, "PR "+prURL)
	if err != nil {
		t.Fatalf("expected nil for merged PR with no stored identity: %v", err)
	}
}

// --- AppendAmendHistory tests ---

func TestAppendAmendHistory_Empty(t *testing.T) {
	record := &AmendRecord{
		OldHeadSHA: "aaa", NewHeadSHA: "bbb",
		PRIdentity: "github/owner/repo#1",
		Timestamp:  "2026-07-19T00:00:00Z", Reason: "amendment",
	}
	result := appendAmendHistory("", record)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "aaa") {
		t.Errorf("expected history to contain old head, got: %s", result)
	}
}

func TestAppendAmendHistory_Append(t *testing.T) {
	record1 := &AmendRecord{
		OldHeadSHA: "aaa", NewHeadSHA: "bbb",
		PRIdentity: "github/owner/repo#1",
		Timestamp:  "2026-07-19T00:00:00Z", Reason: "amendment",
	}
	record2 := &AmendRecord{
		OldHeadSHA: "bbb", NewHeadSHA: "ccc",
		PRIdentity: "github/owner/repo#1",
		Timestamp:  "2026-07-20T00:00:00Z", Reason: "reconciliation",
	}

	result := appendAmendHistory("", record1)
	result = appendAmendHistory(result, record2)

	if !strings.Contains(result, "aaa") || !strings.Contains(result, "ccc") {
		t.Errorf("expected history to contain both records, got: %s", result)
	}
	if !strings.Contains(result, "amendment") || !strings.Contains(result, "reconciliation") {
		t.Errorf("expected both reasons, got: %s", result)
	}
}

// --- IncrementRevision tests ---

func TestIncrementRevision_Empty(t *testing.T) {
	if r := incrementRevision(""); r != "1" {
		t.Errorf("expected '1', got %q", r)
	}
}

func TestIncrementRevision_Zero(t *testing.T) {
	if r := incrementRevision("0"); r != "1" {
		t.Errorf("expected '1', got %q", r)
	}
}

func TestIncrementRevision_One(t *testing.T) {
	if r := incrementRevision("1"); r != "2" {
		t.Errorf("expected '2', got %q", r)
	}
}

func TestIncrementRevision_Five(t *testing.T) {
	if r := incrementRevision("5"); r != "6" {
		t.Errorf("expected '6', got %q", r)
	}
}

// --- Required by delivery_test.go, already defined there. ---
