//go:build integration

package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// verdictFixture is a working ship task at deliveryTestHead and the working
// review task that reads it, with no verdict recorded yet.
type verdictFixture struct {
	c        *taskauthority.Canonical
	homeDir  string
	shipID   string
	review   reviewFixture
	worktree string
}

func newVerdictFixture(t *testing.T) verdictFixture {
	t.Helper()
	c, homeDir := newFleetCanonical(t)
	worktree := mustWorkingShipTask(t, c, "ship-1")
	return verdictFixture{
		c: c, homeDir: homeDir, shipID: "ship-1", worktree: worktree,
		review: mustBindReviewTask(t, c, "ship-1", deliveryTestHead),
	}
}

func (f verdictFixture) goodDoc() map[string]any {
	return map[string]any{
		"schema_version": 1, "task": f.review.TaskID, "generation": 1, "reviews": f.shipID,
		"outcome": "pass", "head_sha": deliveryTestHead, "base_sha": strings.Repeat("1", 40),
		"evidence": "read every changed file",
	}
}

func (f verdictFixture) verdictPath() string {
	return filepath.Join(f.homeDir, "review", f.review.TaskID, "1", "verdict.json")
}

func (f verdictFixture) writeRaw(t *testing.T, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.verdictPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.verdictPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f verdictFixture) writeDoc(t *testing.T, doc map[string]any) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	f.writeRaw(t, data)
}

func (f verdictFixture) ship(t *testing.T) taskauthority.Aggregate {
	t.Helper()
	agg, err := f.c.Get(mustFleetTaskID(t, f.shipID))
	if err != nil {
		t.Fatal(err)
	}
	return agg
}

func TestRecordReviewVerdictRecordsTheVerdictFromTheReviewTask(t *testing.T) {
	f := newVerdictFixture(t)
	f.writeDoc(t, f.goodDoc())
	summary, err := RecordReviewVerdict(f.homeDir, f.review.TaskID, 1, "")
	if err != nil {
		t.Fatalf("RecordReviewVerdict: %v", err)
	}
	if !strings.Contains(summary, "pass") || !strings.Contains(summary, deliveryTestHead) {
		t.Fatalf("summary = %q, want the outcome and head", summary)
	}
	if !strings.Contains(summary, "for task "+f.shipID+" at") {
		t.Fatalf("summary = %q, want the reviewed task's id", summary)
	}
	ship := f.ship(t)
	if ship.ReviewVerdict == nil {
		t.Fatal("no verdict recorded on the reviewed task")
	}
	v := ship.ReviewVerdict.Verdict
	if v.Outcome != domain.VerdictPass || v.HeadSHA != deliveryTestHead || v.BaseSHA != strings.Repeat("1", 40) {
		t.Fatalf("verdict = %+v", v)
	}
	if v.ReviewerTask != f.review.TaskID || v.ReviewerGeneration != 1 || v.ReviewerIncarnation != f.review.Incarnation {
		t.Fatalf("verdict reviewer = %s gen %d inc %s, want the review task's own identity", v.ReviewerTask, v.ReviewerGeneration, v.ReviewerIncarnation)
	}
	if v.Author != ship.Endpoint.Incarnation {
		t.Fatalf("verdict author = %q, want the reviewed task's endpoint incarnation %q", v.Author, ship.Endpoint.Incarnation)
	}
	if v.Before != f.review.Tree || v.After != f.review.Tree {
		t.Fatalf("verdict trees before %+v after %+v, want the recorded review tree %+v", v.Before, v.After, f.review.Tree)
	}
	after, err := ObserveReviewTree(f.homeDir, f.shipID)
	if err != nil || after != v.After {
		t.Fatalf("ObserveReviewTree = %+v, %v; want the tree the verdict recorded %+v", after, err, v.After)
	}
}

func TestRecordReviewVerdictObservesTheTreeAfterTheReview(t *testing.T) {
	f := newVerdictFixture(t)
	f.writeDoc(t, f.goodDoc())
	if err := os.WriteFile(filepath.Join(f.worktree, "dirty"), []byte("edit after the review started\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := RecordReviewVerdict(f.homeDir, f.review.TaskID, 1, "")
	if err == nil || !strings.Contains(err.Error(), "the reviewed tree moved during the review") {
		t.Fatalf("RecordReviewVerdict error = %v, want the custody refusal for a tree that changed during the review", err)
	}
	if f.ship(t).ReviewVerdict != nil {
		t.Fatal("a verdict was recorded for a tree that changed during the review")
	}
}

func TestRecordReviewVerdictRefusesWhatTheFileOrTaskCannotSupport(t *testing.T) {
	doc := func(mutate func(map[string]any)) func(*testing.T, verdictFixture) {
		return func(t *testing.T, f verdictFixture) {
			d := f.goodDoc()
			mutate(d)
			f.writeDoc(t, d)
		}
	}
	for _, tc := range []struct {
		name       string
		prepare    func(*testing.T, verdictFixture)
		review     string
		generation uint64
		digest     string
		want       string
	}{
		{name: "no verdict file", prepare: func(*testing.T, verdictFixture) {}, want: "wrote no verdict file"},
		{name: "schema version", prepare: doc(func(d map[string]any) { d["schema_version"] = 2 }), want: "schema_version 2 is not 1"},
		{name: "task", prepare: doc(func(d map[string]any) { d["task"] = "other" }), want: `is for task "other"`},
		{name: "generation", prepare: doc(func(d map[string]any) { d["generation"] = 2 }), want: "is for generation 2"},
		{name: "reviews", prepare: doc(func(d map[string]any) { d["reviews"] = "other" }), want: `reviews "other"`},
		{name: "head", prepare: doc(func(d map[string]any) { d["head_sha"] = strings.Repeat("9", 40) }), want: "is for head"},
		{name: "evidence", prepare: doc(func(d map[string]any) { d["evidence"] = "  \n" }), want: "carries no evidence"},
		{name: "unknown outcome", prepare: doc(func(d map[string]any) { d["outcome"] = "maybe" }), want: "recording review verdict"},
		{name: "unknown field", prepare: doc(func(d map[string]any) { d["approved_by"] = "me" }), want: "verdict file is malformed"},
		{name: "not json", prepare: func(t *testing.T, f verdictFixture) { f.writeRaw(t, []byte("PASS")) }, want: "verdict file is malformed"},
		{name: "data after the object", prepare: func(t *testing.T, f verdictFixture) {
			data, _ := json.Marshal(f.goodDoc())
			f.writeRaw(t, append(data, []byte(` {"second":true}`)...))
		}, want: "data after the verdict object"},
		{name: "oversize file", prepare: func(t *testing.T, f verdictFixture) { f.writeRaw(t, make([]byte, reviewVerdictMaxBytes+1)) }, want: "not a regular file"},
		{name: "symlinked file", prepare: func(t *testing.T, f verdictFixture) {
			target := filepath.Join(t.TempDir(), "target.json")
			data, _ := json.Marshal(f.goodDoc())
			if err := os.WriteFile(target, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(f.verdictPath()), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, f.verdictPath()); err != nil {
				t.Fatal(err)
			}
		}, want: "not a regular file"},
		{name: "digest of another file", prepare: doc(func(map[string]any) {}), digest: strings.Repeat("0", 64), want: "changed after it was observed"},
		{name: "stale generation", prepare: doc(func(map[string]any) {}), generation: 2, want: "not the observed generation 2"},
		{name: "ship task is not a review task", prepare: doc(func(map[string]any) {}), review: "ship-1", want: "not a review task"},
		{name: "reviewed task reopened without its bindings", prepare: func(t *testing.T, f verdictFixture) {
			f.writeDoc(t, f.goodDoc())
			ship := f.ship(t)
			complete := taskauthority.CanonicalCompleteRequest{HomeID: f.c.HomeID(), TaskID: mustFleetTaskID(t, ship.TaskID), Precondition: domain.Of(uint64(ship.Generation), uint64(ship.Revision)), To: taskauthority.PhaseDone, Reason: "done"}
			if _, err := f.c.Complete(mustFleetOperation(t, "op-complete-ship", complete), complete); err != nil {
				t.Fatal(err)
			}
			reopen := taskauthority.CanonicalReopenRequest{HomeID: f.c.HomeID(), TaskID: mustFleetTaskID(t, ship.TaskID), Precondition: domain.Of(uint64(ship.Generation), uint64(ship.Revision)+1), Reason: "reopen"}
			if _, err := f.c.Reopen(mustFleetOperation(t, "op-reopen-ship", reopen), reopen); err != nil {
				t.Fatal(err)
			}
		}, want: "review verdict requires the bound worktree and endpoint"},
		{name: "unknown task", prepare: doc(func(map[string]any) {}), review: "missing-task", want: "resolving review task"},
		{name: "invalid task id", prepare: doc(func(map[string]any) {}), review: "Not A Task!", want: "invalid typed identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerdictFixture(t)
			tc.prepare(t, f)
			review := f.review.TaskID
			if tc.review != "" {
				review = tc.review
			}
			_, err := RecordReviewVerdict(f.homeDir, review, tc.generation, tc.digest)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RecordReviewVerdict error = %v, want %q", err, tc.want)
			}
			if f.ship(t).ReviewVerdict != nil {
				t.Fatal("a refused verdict was recorded")
			}
		})
	}
}

func TestRecordReviewVerdictRefusesAReviewTaskThatNeverLaunched(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingShipTask(t, c, "ship-1")
	project, err := domain.NewProjectID("project")
	if err != nil {
		t.Fatal(err)
	}
	create := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, "review-ship-1"), Owner: "owner", Description: "review",
		Kind: taskauthority.KindReview, Project: project, ReviewTaskID: "ship-1", ReviewHead: deliveryTestHead, Reason: "create",
	}
	if _, err := c.Create(mustFleetOperation(t, "op-create-review", create), create); err != nil {
		t.Fatal(err)
	}
	_, err = RecordReviewVerdict(homeDir, "review-ship-1", 0, "")
	if err == nil || !strings.Contains(err.Error(), "no bound endpoint or recorded tree") {
		t.Fatalf("RecordReviewVerdict error = %v, want the not-launched refusal", err)
	}
}

func TestRecordReviewVerdictRecordsAFailVerdictThatDoesNotApprove(t *testing.T) {
	f := newVerdictFixture(t)
	d := f.goodDoc()
	d["outcome"] = "fail"
	f.writeDoc(t, d)
	if _, err := RecordReviewVerdict(f.homeDir, f.review.TaskID, 1, ""); err != nil {
		t.Fatalf("RecordReviewVerdict: %v", err)
	}
	v := f.ship(t).ReviewVerdict.Verdict
	if v.Outcome != domain.VerdictFail || v.Approves(deliveryTestHead, f.ship(t).Endpoint.Incarnation, f.shipID) == nil {
		t.Fatalf("verdict = %+v, want a recorded fail that approves nothing", v)
	}
}

func TestObserveReviewVerdictFile(t *testing.T) {
	f := newVerdictFixture(t)
	present, digest, err := ObserveReviewVerdictFile(f.homeDir, f.review.TaskID, 1)
	if err != nil || present || digest != nil {
		t.Fatalf("absent file = %v %q %v, want absent", present, digest, err)
	}
	f.writeDoc(t, f.goodDoc())
	data, err := os.ReadFile(f.verdictPath())
	if err != nil {
		t.Fatal(err)
	}
	present, digest, err = ObserveReviewVerdictFile(f.homeDir, f.review.TaskID, 1)
	if err != nil || !present || string(digest) != verdictFileDigest(data) {
		t.Fatalf("present file = %v %q %v, want present with the content digest %s", present, digest, err, verdictFileDigest(data))
	}
	if err := os.Remove(f.verdictPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.homeDir, f.verdictPath()); err != nil {
		t.Fatal(err)
	}
	present, digest, err = ObserveReviewVerdictFile(f.homeDir, f.review.TaskID, 1)
	if err != nil || !present || digest != nil {
		t.Fatalf("unreadable file = %v %q %v, want present without a digest so the record step reports why", present, digest, err)
	}
}

func TestWorkingReviewTasksListsOnlyWorkingReviewTasksWithAnEndpoint(t *testing.T) {
	f := newVerdictFixture(t)
	refs, err := WorkingReviewTasks(f.homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != (ReviewTaskRef{TaskID: f.review.TaskID, Generation: 1}) {
		t.Fatalf("WorkingReviewTasks = %+v, want only the working review task", refs)
	}
	rid := mustFleetTaskID(t, f.review.TaskID)
	rev, err := f.c.Get(rid)
	if err != nil {
		t.Fatal(err)
	}
	block := taskauthority.CanonicalBlockRequest{HomeID: f.c.HomeID(), TaskID: rid, Precondition: domain.Of(uint64(rev.Generation), uint64(rev.Revision)), Detail: "dep", Reason: "block"}
	if _, err := f.c.Block(mustFleetOperation(t, "op-block-review", block), block); err != nil {
		t.Fatal(err)
	}
	if refs, err = WorkingReviewTasks(f.homeDir); err != nil || len(refs) != 0 {
		t.Fatalf("WorkingReviewTasks after the review task blocked = %+v, %v; want none", refs, err)
	}
}

func TestObserveReviewTreeRefusesAnUnboundTask(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustFleetCreate(t, c, "ship-1")
	if _, err := ObserveReviewTree(homeDir, "ship-1"); err == nil || !strings.Contains(err.Error(), "requires the bound worktree and endpoint") {
		t.Fatalf("ObserveReviewTree error = %v, want the unbound refusal", err)
	}
	if _, err := ObserveReviewTree(homeDir, "missing"); err == nil || !strings.Contains(err.Error(), "resolving task") {
		t.Fatalf("ObserveReviewTree error = %v, want the unknown-task refusal", err)
	}
	if _, err := observeTree(filepath.Join(homeDir, "not-a-repo")); err == nil || !strings.Contains(err.Error(), "observing worktree HEAD") {
		t.Fatalf("observeTree error = %v, want the HEAD refusal", err)
	}
}

func TestReviewVerdictEntryPointsRefuseAnUnopenableHome(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "home-file")
	if err := os.WriteFile(bad, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveReviewTree(bad, "ship-1"); err == nil || !strings.Contains(err.Error(), "review verdict: opening home") {
		t.Fatalf("ObserveReviewTree error = %v, want the opening-home refusal", err)
	}
	if _, err := WorkingReviewTasks(bad); err == nil || !strings.Contains(err.Error(), "review verdict: opening home") {
		t.Fatalf("WorkingReviewTasks error = %v, want the opening-home refusal", err)
	}
	if _, err := RecordReviewVerdict(bad, "review-ship-1", 1, ""); err == nil || !strings.Contains(err.Error(), "review verdict: opening home") {
		t.Fatalf("RecordReviewVerdict error = %v, want the opening-home refusal", err)
	}
}

func TestObserveReviewTreeRefusesAnInvalidTaskID(t *testing.T) {
	_, homeDir := newFleetCanonical(t)
	if _, err := ObserveReviewTree(homeDir, "Not A Task!"); err == nil || !strings.Contains(err.Error(), "invalid typed identity") {
		t.Fatalf("ObserveReviewTree error = %v, want the invalid-id refusal", err)
	}
}

func TestRecordReviewVerdictRefusesAVerdictPathItCannotInspect(t *testing.T) {
	f := newVerdictFixture(t)
	// The generation directory is a regular file, so Lstat of the verdict file
	// fails with something other than "not found".
	gen := filepath.Dir(f.verdictPath())
	if err := os.MkdirAll(filepath.Dir(gen), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gen, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordReviewVerdict(f.homeDir, f.review.TaskID, 1, ""); err == nil || !strings.Contains(err.Error(), "reading verdict file") {
		t.Fatalf("RecordReviewVerdict error = %v, want the unreadable-file refusal", err)
	}
	if f.ship(t).ReviewVerdict != nil {
		t.Fatal("a verdict was recorded from an uninspectable path")
	}
}
