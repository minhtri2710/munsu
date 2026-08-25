//go:build integration

package fleet

import (
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// canonicalHomeAuthority opens the canonical home and constructs the canonical
// Task Authority over it, so canonical-read tests seed records the read path
// finds. The home must be initialized first.
func canonicalHomeAuthority(t *testing.T, homeDir string) *taskauthority.Canonical {
	t.Helper()
	h, err := home.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// taPrecondition is domain.Of.
func taPrecondition(gen, rev uint64) domain.Precondition { return domain.Of(gen, rev) }

// seedCanonicalShipTask creates one ship task through the canonical surface
// and advances it to the requested phase.
func seedCanonicalShipTask(t *testing.T, homeDir, id, phase string) *taskauthority.Canonical {
	t.Helper()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	auth := canonicalHomeAuthority(t, homeDir)
	create := taskauthority.CanonicalCreateRequest{
		HomeID:      auth.HomeID(),
		TaskID:      mustTaskID(t, id),
		Owner:       "owner",
		Description: "work on " + id,
		Kind:        "ship",
		Project:     mustProjectID(t, "proj-x"),
		Reason:      "test",
	}
	if _, err := auth.Create(mustOp(t, "op-create-"+id, create), create); err != nil {
		t.Fatal(err)
	}
	if phase == "working" || phase == "done" {
		start := taskauthority.CanonicalStartRequest{
			HomeID:       auth.HomeID(),
			TaskID:       mustTaskID(t, id),
			Precondition: taPrecondition(1, 1),
			Reason:       "spawned",
		}
		if _, err := auth.Start(mustOp(t, "op-start-"+id, start), start); err != nil {
			t.Fatal(err)
		}
	}
	if phase == "done" {
		complete := taskauthority.CanonicalCompleteRequest{
			HomeID:       auth.HomeID(),
			TaskID:       mustTaskID(t, id),
			Precondition: taPrecondition(1, 2),
			To:           taskauthority.PhaseDone,
			Reason:       "done",
		}
		if _, err := auth.Complete(mustOp(t, "op-complete-"+id, complete), complete); err != nil {
			t.Fatal(err)
		}
	}
	return auth
}

// TestSnapshotCanonicalPhaseOverridesStaleProjection proves the snapshot
// prefers canonical Authority state (Task 7.8 criterion 2): a stale .status
// showing working cannot override an authoritative done phase, and tampered
// .meta kind/project cannot override the canonical definition.
func TestSnapshotCanonicalPhaseOverridesStaleProjection(t *testing.T) {
	homeDir := t.TempDir()
	seedCanonicalShipTask(t, homeDir, "t1", "done")
	// Stale/tampered projection: meta claims a live scout on another project
	// and the status log still says working.
	if err := home.WriteMeta(homeDir, "t1", map[string]string{"window": "@win", "worktree": "/tmp/wt", "kind": "scout", "project": "hacked"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "t1", "working: still working"); err != nil {
		t.Fatal(err)
	}
	snap, err := Snapshot(homeDir, testSnapshotDeps(t))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]
	if ts.CurrentState != "done" {
		t.Fatalf("CurrentState = %q, want authoritative done (stale status cannot override)", ts.CurrentState)
	}
	if !ts.StatusLogSuperseded {
		t.Fatalf("StatusLogSuperseded = false, want true (canonical record supersedes the log)")
	}
	if ts.Kind != "ship" || ts.Project != "proj-x" {
		t.Fatalf("kind=%q project=%q, want canonical ship/proj-x (tampered meta cannot override)", ts.Kind, ts.Project)
	}
}

// TestSnapshotIncludesCanonicalTasksWithoutMeta proves canonical tasks with
// no .meta projection are still part of the fleet snapshot.
func TestSnapshotIncludesCanonicalTasksWithoutMeta(t *testing.T) {
	homeDir := t.TempDir()
	seedCanonicalShipTask(t, homeDir, "no-meta", "queued")
	snap, err := Snapshot(homeDir, testSnapshotDeps(t))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(snap.Tasks))
	}
	ts := snap.Tasks[0]
	if ts.ID != "no-meta" || ts.Kind != "ship" || ts.CurrentState != "queued" {
		t.Fatalf("task = %+v", ts)
	}
}

// TestReadWithProbeCanonicalPhaseOverridesStaleStatus proves observation
// prefers the authoritative phase: a stale .status showing working cannot
// override an authoritative done (criterion 3).
func TestReadWithProbeCanonicalPhaseOverridesStaleStatus(t *testing.T) {
	homeDir := t.TempDir()
	seedCanonicalShipTask(t, homeDir, "t1", "done")
	if err := home.WriteMeta(homeDir, "t1", map[string]string{"window": "@win"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "t1", "working: still working"); err != nil {
		t.Fatal(err)
	}
	state, err := ReadWithProbe(homeDir, "t1", nil)
	if err != nil {
		t.Fatalf("ReadSoldierState: %v", err)
	}
	if state.Status != "done" {
		t.Fatalf("status = %q, want authoritative done", state.Status)
	}
	if !state.StatusLogSuperseded {
		t.Fatalf("StatusLogSuperseded = false, want true")
	}
}

// TestSnapshotFailsClosedOnLegacyMetaOnlyMerged proves the snapshot fails
// closed on a meta-only delivery_state=merged claim without an authoritative
// record (Task 7.8 legacy decision (a)): the legacy shape is never silently
// projected, and the typed error names the heal path.
func TestSnapshotFailsClosedOnLegacyMetaOnlyMerged(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, "t1", map[string]string{"delivery_state": "merged", "kind": "ship", "window": "@1"}); err != nil {
		t.Fatal(err)
	}
	_, err := Snapshot(homeDir, testSnapshotDeps(t))
	if err == nil {
		t.Fatal("Snapshot over a legacy meta-only task = nil error, want fail-closed rejection")
	}
	if !strings.Contains(err.Error(), "t1") || !strings.Contains(err.Error(), homeDir) {
		t.Fatalf("clean-break error must carry task+home context, got: %v", err)
	}
}

// TestReadWithProbeFailsClosedOnLegacyMetaOnlyMerged proves observation fails
// closed on the same legacy shape (clean break: no canonical record).
func TestReadWithProbeFailsClosedOnLegacyMetaOnlyMerged(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, "t1", map[string]string{"delivery_state": "merged", "kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	_, err := ReadWithProbe(homeDir, "t1", nil)
	if err == nil {
		t.Fatal("ReadSoldierState over a legacy meta-only task = nil, want fail-closed")
	}
	if !strings.Contains(err.Error(), homeDir) {
		t.Errorf("clean-break error must carry home context, got: %v", err)
	}
}

// TestReadWithProbeFailsClosedOnLegacyMergeAuthorization proves the legacy
// merge_authorization JSON (old shape no longer consumed by the canonical
// read path) fails closed without an authoritative record.
func TestReadWithProbeFailsClosedOnLegacyMergeAuthorization(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, "t1", map[string]string{
		"merge_authorization": `{"task_generation": 1, "authorized_at": "2024-01-01T00:00:00Z", "head_sha": "abc"}`,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := ReadWithProbe(homeDir, "t1", nil)
	if err == nil {
		t.Fatal("ReadSoldierState over a legacy meta-only task = nil, want fail-closed")
	}
	if !strings.Contains(err.Error(), homeDir) {
		t.Errorf("clean-break error must carry home context, got: %v", err)
	}
}

// TestRetireTaskForceFailsClosedWithoutAuthoritativeEvidence proves the
// fleet-level --force derivation path fails closed: an identity-bearing task
// under --force with no committed canonical completed delivery outcome is
// refused and never retired (#414 B hard cut: the .meta delivery_state
// projection never authorizes merged truth).
func TestRetireTaskForceFailsClosedWithoutAuthoritativeEvidence(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "force-no-evidence"
	head := "aaa111aaa111aaa111aaa111aaa111aaa111aaa1"
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
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
	auth := mergeTestAuth(t, homeDir, taskID) // created + bound; no canonical delivery outcome

	result, err := RetireTask(Options{HomeDir: homeDir, ID: taskID, Force: true}, fakeTeardown{alive: true}, fakeRetirementJournals{}, auth)
	if err == nil {
		t.Fatalf("RetireTask --force without canonical outcome succeeded: %+v", result)
	}
	if !strings.Contains(err.Error(), "canonical delivery outcome") {
		t.Fatalf("error = %v, want canonical delivery outcome refusal (no .meta merged truth)", err)
	}
	agg, aggErr := auth.Get(mustTaskID(t, taskID))
	if aggErr != nil {
		t.Fatal(aggErr)
	}
	if agg.Phase == taskauthority.PhaseRetired {
		t.Fatal("task was retired without canonical merged truth")
	}
	// The meta is never removed on a refused retirement.
	metaPath, err := home.MetaFilePath(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(metaPath); statErr != nil {
		t.Fatalf("meta removed on refused retirement: %v", statErr)
	}
}

// TestSummarizeCaptainHomePrefersCanonicalPhase proves the captain-home
// summary prefers canonical Authority state: a stale status line showing
// working cannot classify a canonically done child as active.
func TestSummarizeCaptainHomePrefersCanonicalPhase(t *testing.T) {
	homeDir := t.TempDir()
	// Initialize the canonical home, then a canonically done child with a
	// stale working status line: the authoritative phase wins.
	seedCanonicalShipTask(t, homeDir, "t1", "done")
	if err := home.WriteMeta(homeDir, "t1", map[string]string{"kind": "ship", "window": "w1"}); err != nil {
		t.Fatal(err)
	}
	if err := home.AppendStatus(homeDir, "t1", "working: implementing"); err != nil {
		t.Fatal(err)
	}
	sum := SummarizeCaptainHome(homeDir)
	if sum.Counts.ActiveChildren != 0 {
		t.Fatalf("ActiveChildren = %d, want 0 (canonical done supersedes stale working status)", sum.Counts.ActiveChildren)
	}
	if sum.State == "active_child_work" {
		t.Fatalf("state = %q, want non-active (canonical phase wins)", sum.State)
	}
}
