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

// mergedShipFixture seeds one ship task in the retired-eligible state the
// fleet retirement path requires (#414 B hard cut): a canonical Task
// Authority record with a committed completed delivery outcome (authorize +
// outcome under the task's own identity) plus matching identity .meta. The
// .meta delivery_state projection never authorizes merged truth. Returns the
// canonical Authority that owns the task.
func mergedShipFixture(t *testing.T, homeDir, taskID string) *taskauthority.Canonical {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	auth, err := taskauthority.NewCanonical(mustHome(t, homeDir))
	if err != nil {
		t.Fatal(err)
	}
	canonicalCreateTask(t, auth, taskID, "ship", "")
	seedMergedDelivery(t, auth, homeDir, taskID)

	head := strings.Repeat("a", 40)
	meta := map[string]string{
		"kind":                 "ship",
		"backend":              "tmux",
		"window":               "@1",
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
	return auth
}

func TestMergeAndRetireNilAuthorityFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-nil-auth"

	// A missing composed canonical Authority fails closed: no retirement
	// transition ever commits without one.
	result := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, nil)
	if result == nil || !result.IsError() {
		t.Fatal("expected an error result when the authority is nil")
	}
	if !strings.Contains(result.MergeDetail, "composed task authority") {
		t.Fatalf("detail = %v, want composed-authority failure", result.MergeDetail)
	}
	if result.TeardownResult != nil {
		t.Fatal("teardown must not run without an authority")
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
	if result.MergeOutcome != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("merge outcome = %q, want completed (canonical outcome skip)", result.MergeOutcome)
	}
	if result.TeardownError != nil {
		t.Fatalf("unexpected teardown error: %v", result.TeardownError)
	}

	// The authoritative retirement transition committed through the canonical
	// Authority: phase retired (create + bind wt + bind ep + authorize +
	// outcome + retire + claim complete).
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != 8 {
		t.Fatalf("aggregate = phase %q revision %d, want retired revision 8", agg.Phase, agg.Revision)
	}

	// Saga-side cleanup removed the task meta.
	metaPath, err := home.MetaFilePath(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatal("meta should be removed after successful retirement")
	}
	if result.IsError() {
		t.Fatal("expected IsError=false for fully successful retirement")
	}
}

func TestMergeAndRetireCleanupFailurePreservesCanonicalTruth(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-cleanup-failure"
	auth := mergedShipFixture(t, homeDir, taskID)

	metaPath, err := home.MetaFilePath(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}

	// First attempt: the canonical Retire op commits (durable receipt) but the
	// saga-side cleanup fails at the session dispose step.
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

	// The committed retirement stands and the .meta projection is untouched
	// (cleanup only removes it later); the canonical completed delivery
	// outcome is preserved. Revision 6 = retire committed the durable claim;
	// the failed first attempt's BeginCleanup was a no-op (claim already
	// active), so the aggregate carries exactly the retire bump.
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != 6 {
		t.Fatalf("aggregate = phase %q revision %d, want retired revision 6", agg.Phase, agg.Revision)
	}
	// The exact ownership evidence is preserved durably.
	if agg.Retirement == nil || agg.Retirement.Endpoint == nil || agg.Retirement.Worktree == nil {
		t.Fatalf("retirement evidence missing: %+v", agg.Retirement)
	}
	outcome, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil {
		t.Fatalf("canonical delivery outcome lost: %v", err)
	}
	if outcome.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical delivery outcome = %q, want completed", outcome.Status)
	}
	after, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("meta removed by the retirement transition: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("retirement transition mutated .meta: before=%q after=%q", before, after)
	}

	// Retry: delivery is never rerun (canonical completed outcome skips),
	// the retired phase is observed (no double transition, only the claim
	// completion advances revision 6 -> 7), and the cleanup resumes to
	// completion.
	second := MergeAndRetire(homeDir, taskID, "https://github.com/owner/repo/pull/1", nil, fakeTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if second == nil {
		t.Fatal("expected non-nil retry result")
	}
	if second.MergeOutcome != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("retry reran delivery: outcome = %q, want completed", second.MergeOutcome)
	}
	if second.TeardownError != nil {
		t.Fatalf("retry teardown error: %v", second.TeardownError)
	}
	agg, err = auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 8 {
		t.Fatalf("retry re-committed the retirement: revision = %d, want 8 after archival marker and claim completion", agg.Revision)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatal("retry should complete the cleanup and remove meta")
	}
}

func TestMergeAndRetireCrossHomeRetirement(t *testing.T) {
	// A handed-off task lives in a captain home: the fleet retirement path
	// targets the resolved task home and its composed canonical Authority, so
	// the authoritative retirement transition lands in the captain home.
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
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseRetired {
		t.Fatalf("captain-home aggregate phase = %q, want retired", agg.Phase)
	}
	metaPath, err := home.MetaFilePath(capHome, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatal("captain-home meta should be removed after retirement")
	}
}

func TestRetireTaskCleanupFailureReturnsResumableReceipt(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-receipt-resume"
	auth := mergedShipFixture(t, homeDir, taskID)

	// Direct teardown path: the canonical Retire op commits first, then the
	// cleanup fails — the typed partial result carries the committed state and
	// the partial steps, and the canonical completed delivery outcome stays
	// intact.
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
	outcome, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil || outcome.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical completed delivery outcome lost on cleanup failure: %v %+v", err, outcome)
	}

	// Resume: the retired phase is observed (same stable Operation identity
	// replays the durable receipt; only the claim completion advances the
	// revision) and the cleanup completes.
	_, err = RetireTask(opts, fakeTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	agg, _ := auth.Get(mustTaskID(t, taskID))
	if agg.Phase != taskauthority.PhaseRetired || agg.Revision != 8 {
		t.Fatalf("aggregate after resume = %+v, want retired revision 8", agg)
	}
}
