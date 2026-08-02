//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// mergedShipFixture seeds one ship task in the retired-eligible authoritative
// state the fleet retirement path requires (Task 7.7): a provider-verified
// merged merge attempt inside an in-memory Authority plus matching .meta
// (delivery identity + delivery_state=merged). Returns the Authority that
// owns the task.
func mergedShipFixture(t *testing.T, homeDir, taskID string) *taskauthority.Authority {
	t.Helper()
	head := "aaa111aaa111aaa111aaa111aaa111aaa111aaa1"
	meta := map[string]string{
		"kind":                 "ship",
		"backend":              "tmux",
		"window":               "@1",
		"delivery_state":       string(DeliveryStateMerged),
		"pr_provider":          "github",
		"pr_owner":             "testowner",
		"pr_repo":              "testrepo",
		"pr_number":            "42",
		"pr_url":               "https://github.com/testowner/testrepo/pull/42",
		"pr_base_ref":          "main",
		"pr_head_ref":          "feature",
		"pr_head_sha":          head,
		"pr_timestamp":         "2024-01-01T00:00:00Z",
		"pr_identity_revision": "1",
	}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatal(err)
	}
	auth := mergeTestAuth(t, taskID)
	if _, err := auth.RecordMergeAttempt(taskauthority.RecordMergeAttemptRequest{
		OperationID:        "op-merge-attempt-" + taskID,
		Actor:              taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: 1,
		Outcome:            taskauthority.MergeOutcomeMerged,
		HeadSHA:            head,
		MergedSHA:          "bbb222bbb222bbb222bbb222bbb222bbb222bbb2",
		Identity: taskauthority.ProviderIdentitySnapshot{
			Provider: "github",
			Owner:    "testowner",
			Repo:     "testrepo",
			Number:   42,
			URL:      "https://github.com/testowner/testrepo/pull/42",
			BaseRef:  "main",
			HeadRef:  "feature",
			HeadSHA:  head,
		},
		Reason: "merge delivery",
	}); err != nil {
		t.Fatal(err)
	}
	return auth
}

// mergedTruth reads the committed merged outcome of one task.
func mergedTruth(t *testing.T, a *taskauthority.Authority, taskID string) *taskauthority.MergeAttempt {
	t.Helper()
	agg, err := a.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	return agg.MergeAttempt
}

func TestMergeAndRetireNilAuthorityFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-nil-auth"
	stateDir := filepath.Join(homeDir, "state")
	os.MkdirAll(stateDir, 0755)
	os.WriteFile(filepath.Join(stateDir, taskID+".meta"), []byte("kind=scout\nbackend=tmux\nwindow=@1\ndelivery_state=merged\n"), 0644)

	// A missing composed Authority fails closed: the retirement transition
	// never commits without one.
	result := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, nil)
	if result == nil || result.TeardownError == nil {
		t.Fatal("expected teardown error when the authority is nil")
	}
	if !strings.Contains(result.TeardownError.Error(), "composed task authority") {
		t.Fatalf("error = %v, want composed-authority failure", result.TeardownError)
	}
	metaPath := filepath.Join(stateDir, taskID+".meta")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta removed on nil-authority failure: %v", err)
	}
}

func TestMergeAndRetireRetiresThroughAuthority(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-retire-through"
	auth := mergedShipFixture(t, homeDir, taskID)

	result := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MergeOutcome != MergeOutcomeAlreadyMerged {
		t.Fatalf("merge outcome = %q, want already-merged (skip)", result.MergeOutcome)
	}
	if result.TeardownError != nil {
		t.Fatalf("unexpected teardown error: %v", result.TeardownError)
	}

	// The authoritative retirement transition committed: phase retired at
	// revision 3 (create + merge attempt + retire), merged truth retained.
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != 3 {
		t.Fatalf("aggregate = phase %q revision %d, want retired revision 3", agg.Phase, agg.Revision)
	}
	if att := mergedTruth(t, auth, taskID); att == nil || att.Outcome != taskauthority.MergeOutcomeMerged || att.MergedSHA == "" {
		t.Fatalf("retirement erased the verified merged truth: %+v", att)
	}

	// Saga-side cleanup removed the task meta.
	metaPath := filepath.Join(homeDir, "state", taskID+".meta")
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatal("meta should be removed after successful retirement")
	}
	if result.IsError() {
		t.Fatal("expected IsError=false for fully successful retirement")
	}
}

func TestMergeAndRetireCleanupFailurePreservesMergedTruth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-cleanup-failure"
	auth := mergedShipFixture(t, homeDir, taskID)

	metaPath := filepath.Join(homeDir, "state", taskID+".meta")
	before, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}

	// First attempt: the Retire op commits (durable receipt) but the saga-side
	// cleanup fails at the session dispose step.
	first := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true, disposeErr: errors.New("window busy")}, fakeRetirementJournals{}, auth)
	if first == nil || first.TeardownError == nil {
		t.Fatal("expected teardown error on cleanup failure")
	}
	var pending *RetirementCleanupPendingError
	if !errors.As(first.TeardownError, &pending) {
		t.Fatalf("teardown error = %T %v, want typed RetirementCleanupPendingError", first.TeardownError, first.TeardownError)
	}
	if pending.TaskID != taskID {
		t.Fatalf("pending task = %q, want %q", pending.TaskID, taskID)
	}
	if !first.IsError() {
		t.Fatal("expected IsError=true for retired-but-cleanup-pending")
	}

	// The committed merged truth is never rolled back or mutated: the
	// authoritative aggregate is retired with the merged evidence intact, and
	// the .meta projection is untouched (cleanup only removes it later).
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != 3 {
		t.Fatalf("aggregate = phase %q revision %d, want retired revision 3", agg.Phase, agg.Revision)
	}
	if att := mergedTruth(t, auth, taskID); att == nil || att.Outcome != taskauthority.MergeOutcomeMerged || att.MergedSHA != "bbb222bbb222bbb222bbb222bbb222bbb222bbb2" {
		t.Fatalf("merged truth lost: %+v", att)
	}
	after, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("meta removed by the retirement transition: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("retirement transition mutated .meta: before=%q after=%q", before, after)
	}

	// Retry: merge is never rerun (already-merged skip), the durable receipt
	// replays idempotently (revision stays 3 — no double transition), and the
	// cleanup resumes to completion.
	second := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if second == nil {
		t.Fatal("expected non-nil retry result")
	}
	if second.MergeOutcome != MergeOutcomeAlreadyMerged {
		t.Fatalf("retry reran merge: outcome = %q, want already-merged", second.MergeOutcome)
	}
	if second.TeardownError != nil {
		t.Fatalf("retry teardown error: %v", second.TeardownError)
	}
	agg, err = auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 3 {
		t.Fatalf("retry re-committed the retirement: revision = %d, want 3", agg.Revision)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatal("retry should complete the cleanup and remove meta")
	}
}

func TestMergeAndRetireCrossHomeRetirement(t *testing.T) {
	// A handed-off task lives in a captain home: the fleet retirement path
	// targets the resolved task home and its composed Authority, so the
	// authoritative retirement transition lands in the captain home.
	parent := t.TempDir()
	capHome := filepath.Join(parent, "captains", "cap1")
	if err := os.MkdirAll(capHome, 0700); err != nil {
		t.Fatal(err)
	}
	taskID := "test-cross-home"
	auth := mergedShipFixture(t, capHome, taskID)

	result := MergeAndRetire(capHome, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if result == nil || result.TeardownError != nil {
		t.Fatalf("cross-home retirement failed: %+v", result)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired {
		t.Fatalf("captain-home aggregate phase = %q, want retired", agg.Phase)
	}
	if _, err := os.Stat(filepath.Join(capHome, "state", taskID+".meta")); !os.IsNotExist(err) {
		t.Fatal("captain-home meta should be removed after retirement")
	}
}

func TestRetireTaskCleanupFailureReturnsResumableReceipt(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-receipt-resume"
	auth := mergedShipFixture(t, homeDir, taskID)

	// Direct teardown path: the Retire op commits first, then the cleanup
	// fails — the typed partial result carries the committed state and the
	// partial steps, and the merged truth stays intact.
	opts := Options{HomeDir: homeDir, ID: taskID, Force: true}
	result, err := RetireTask(opts, fakeTeardown{alive: true, disposeErr: errors.New("window busy")}, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatal("expected cleanup failure error")
	}
	var pending *RetirementCleanupPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("error = %T %v, want typed RetirementCleanupPendingError", err, err)
	}
	if result == nil {
		t.Fatal("expected a partial teardown result alongside the typed error")
	}
	if att := mergedTruth(t, auth, taskID); att == nil || att.Outcome != taskauthority.MergeOutcomeMerged {
		t.Fatalf("merged truth lost on cleanup failure: %+v", att)
	}

	// Resume: the same stable Operation identity replays the durable receipt
	// (revision unchanged) and the cleanup completes.
	_, err = RetireTask(opts, fakeTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	agg, _ := auth.Get(taskID)
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != 3 {
		t.Fatalf("aggregate after resume = %+v, want retired revision 3", agg)
	}
}
