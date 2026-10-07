package taskauthority

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

// testWords is a complete words record: a grantor, a channel and a quote.
func testWords() domain.Words {
	return domain.Words{Grantor: "beo", Channel: "supervisor-relay:typed", Quote: "munsu: dong y"}
}

// testSeat is a valid seat record of a launch that ran without a fence.
func testSeat() LaunchSeat {
	return LaunchSeat{
		Argv:         []string{"pi", "--no-session"},
		PromptDigest: digestOf("prompt"),
		Fence:        FenceRecord{Reason: "no fence on this host"},
	}
}

const (
	reviewBaseSHA = "0000111122223333444455556666777788889999"
	// shipIncarnation is the endpoint incarnation endpointBinding gives a
	// ship task: the authoring soldier instance a verdict must name.
	shipIncarnation = "inc-bind"
)

func reviewTree(head string) domain.TreeState {
	return domain.TreeState{Head: head, Porcelain: digestOf("porcelain:" + head)}
}

// passVerdictFixture is a valid PASS verdict standing on its own, for
// validators that take no task context.
func passVerdictFixture() domain.ReviewVerdict {
	return reviewVerdict("rev1", deliveryHead)
}

// reviewVerdict is the PASS verdict the review task reviewID produces for head
// of a ship task run by shipIncarnation: what workingReview's launch records.
func reviewVerdict(reviewID, head string) domain.ReviewVerdict {
	tree := reviewTree(head)
	return domain.ReviewVerdict{
		Outcome: domain.VerdictPass, HeadSHA: head, BaseSHA: reviewBaseSHA,
		ReviewerTask: reviewID, ReviewerGeneration: 1, ReviewerIncarnation: "inc-" + reviewID,
		Author: shipIncarnation, Before: tree, After: tree,
	}
}

func createReview(t *testing.T, c *Canonical, reviewID, target, head string) error {
	t.Helper()
	req := createRequest(c, reviewID)
	req.Kind, req.ReviewTaskID, req.ReviewHead = KindReview, target, head
	_, err := c.Create(mustOperation(t, "op-create-"+reviewID, req), req)
	return err
}

// reviewLaunchRequest is the launch intent of a review task: kind review and no
// worktree reservation.
func reviewLaunchRequest(c *Canonical, reviewID string, prec domain.Precondition) CanonicalBeginSpawnRequest {
	req := launchRequest(c, reviewID, prec)
	req.Kind, req.WorktreeReservationID, req.WorktreeFenceToken = KindReview, "", ""
	return req
}

// workingReview creates the review task reviewID reading target at head and
// drives it through its launch to working, recording the tree observed before
// submit. It returns the aggregate revision.
func workingReview(t *testing.T, c *Canonical, reviewID, target, head string) uint64 {
	t.Helper()
	if err := createReview(t, c, reviewID, target, head); err != nil {
		t.Fatalf("Create review %s: %v", reviewID, err)
	}
	intent := reviewLaunchRequest(c, reviewID, preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-"+reviewID, intent), intent); err != nil {
		t.Fatalf("BeginSpawn(%s): %v", reviewID, err)
	}
	attach := attachRequest(c, reviewID, preconditionOf(1, 2), intent, "handle-"+reviewID)
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-"+reviewID, attach), attach); err != nil {
		t.Fatalf("AttachEndpoint(%s): %v", reviewID, err)
	}
	record := recordLaunchRequest(c, reviewID, preconditionOf(1, 3), intent)
	tree := reviewTree(head)
	record.ReviewTree = &tree
	if _, err := c.RecordLaunch(mustOperation(t, "op-record-"+reviewID, record), record); err != nil {
		t.Fatalf("RecordLaunch(%s): %v", reviewID, err)
	}
	bind := CanonicalBindEndpointRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, reviewID), Precondition: preconditionOf(1, 4),
		Binding: launchEndpointBinding(intent, "handle-"+reviewID), Reason: "spawn",
	}
	if _, err := c.BindEndpoint(mustOperation(t, "op-bindep-"+reviewID, bind), bind); err != nil {
		t.Fatalf("BindEndpoint(%s): %v", reviewID, err)
	}
	return 5
}

// mustRecordVerdict records the PASS verdict of review task reviewID for head
// on the ship task and returns the ship task's next revision.
func mustRecordVerdict(t *testing.T, c *Canonical, taskID, reviewID, head string, rev uint64) uint64 {
	t.Helper()
	req := CanonicalRecordReviewVerdictRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(1, rev), Verdict: reviewVerdict(reviewID, head)}
	if _, err := c.RecordReviewVerdict(mustOperation(t, "op-verdict-"+taskID+"-"+reviewID, req), req); err != nil {
		t.Fatalf("RecordReviewVerdict(%s): %v", taskID, err)
	}
	return rev + 1
}

// workingShip creates a ship task and binds its worktree and endpoint, leaving
// it working at revision 3.
func workingShip(t *testing.T, c *Canonical, taskID string) {
	t.Helper()
	mustCreate(t, c, taskID)
	for _, step := range []struct {
		name string
		do   func() error
	}{
		{"BindWorktree", func() error {
			req := bindWorktreeRequest(c, taskID, preconditionOf(1, 1))
			_, err := c.BindWorktree(mustOperation(t, "op-ship-wt-"+taskID, req), req)
			return err
		}},
		{"BindEndpoint", func() error {
			req := bindEndpointRequest(c, taskID, preconditionOf(1, 2))
			_, err := c.BindEndpoint(mustOperation(t, "op-ship-ep-"+taskID, req), req)
			return err
		}},
	} {
		if err := step.do(); err != nil {
			t.Fatalf("%s(%s): %v", step.name, taskID, err)
		}
	}
}

func TestCanonicalCreateReviewRequiresAWorkingShipTarget(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	workingShip(t, c, "ship1")
	mustCreate(t, c, "queued-ship")
	scout := scoutCreateRequest(c, "scout1")
	if _, err := c.Create(mustOperation(t, "op-create-scout1", scout), scout); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		id, tgt, hd  string
		wantIs       error
		wantContains string
	}{
		{"no reviewed head", "rev-a", "ship1", "", ErrInvalidInput, "review task requires the reviewed head"},
		{"no reviewed task id", "rev-b", "", deliveryHead, ErrInvalidInput, "review task requires the reviewed task id"},
		{"reviewed task does not exist", "rev-c", "nope", deliveryHead, ErrNotFound, ""},
		{"reviewed task is queued", "rev-d", "queued-ship", deliveryHead, ErrPrecondition, "must be working with its worktree and endpoint bound"},
		{"reviewed task is a scout", "rev-e", "scout1", deliveryHead, ErrPrecondition, "only a ship task is reviewed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := createReview(t, c, tc.id, tc.tgt, tc.hd)
			if !errors.Is(err, tc.wantIs) || !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("Create review = %v, want %v containing %q", err, tc.wantIs, tc.wantContains)
			}
			if _, err := c.Get(mustTaskID(t, tc.id)); !errors.Is(err, ErrNotFound) {
				t.Fatalf("refused review %s exists: Get = %v", tc.id, err)
			}
		})
	}

	ship := createRequest(c, "ship-with-review-fields")
	ship.ReviewTaskID, ship.ReviewHead = "ship1", deliveryHead
	_, err := c.Create(mustOperation(t, "op-create-ship-fields", ship), ship)
	wantErrSubstring(t, err, "review-only fields are not valid for ship tasks", "Create of a ship task carrying review fields")

	if err := createReview(t, c, "rev1", "ship1", deliveryHead); err != nil {
		t.Fatalf("Create review of a working ship: %v", err)
	}
	got, err := c.Get(mustTaskID(t, "rev1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Definition.Kind != KindReview || got.Definition.ReviewTaskID != "ship1" || got.Definition.ReviewHead != deliveryHead {
		t.Fatalf("review definition = %+v, want kind review reading ship1 at %s", got.Definition, deliveryHead)
	}
}

func TestCanonicalReviewOwnsNoWorktreeAndLaunchesOnlyAsReview(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	workingShip(t, c, "ship1")
	if err := createReview(t, c, "rev1", "ship1", deliveryHead); err != nil {
		t.Fatal(err)
	}

	bind := bindWorktreeRequest(c, "rev1", preconditionOf(1, 1))
	_, err := c.BindWorktree(mustOperation(t, "op-bind-wt-rev", bind), bind)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("BindWorktree on a review = %v, want ErrConflict", err)
	}
	wantErrSubstring(t, err, "is a review task; it reads the reviewed worktree and owns none", "BindWorktree on a review")

	ep := bindEndpointRequest(c, "rev1", preconditionOf(1, 1))
	_, err = c.BindEndpoint(mustOperation(t, "op-bind-ep-rev", ep), ep)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("BindEndpoint before a launch intent = %v, want ErrConflict", err)
	}
	wantErrSubstring(t, err, "bind endpoint requires its committed launch intent", "BindEndpoint on a review with no launch intent")

	asShip := launchRequest(c, "rev1", preconditionOf(1, 1))
	_, err = c.BeginSpawn(mustOperation(t, "op-begin-rev-ship", asShip), asShip)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginSpawn kind ship on a review = %v, want ErrConflict", err)
	}
	wantErrSubstring(t, err, "its launch intent kind", "BeginSpawn kind ship on a review task")

	withWorktree := reviewLaunchRequest(c, "rev1", preconditionOf(1, 1))
	withWorktree.WorktreeReservationID, withWorktree.WorktreeFenceToken = "wt-res-rev1", "wt-fence-rev1"
	_, err = c.BeginSpawn(mustOperation(t, "op-begin-rev-wt", withWorktree), withWorktree)
	wantErrSubstring(t, err, "a review launch reserves no worktree", "BeginSpawn of a review reserving a worktree")

	mustCreate(t, c, "ship3")
	asReview := reviewLaunchRequest(c, "ship3", preconditionOf(1, 1))
	_, err = c.BeginSpawn(mustOperation(t, "op-begin-ship-as-rev", asReview), asReview)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("BeginSpawn kind review on a ship = %v, want ErrConflict", err)
	}
	wantErrSubstring(t, err, "its launch intent kind", "BeginSpawn kind review on a ship task")

	review := reviewLaunchRequest(c, "rev1", preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-rev", review), review); err != nil {
		t.Fatalf("BeginSpawn of a review with no worktree: %v", err)
	}
}

// A review generation carries the tree observed before submit, and no other
// kind does; the seat record is part of the evidence identity.
func TestCanonicalRecordLaunchCarriesTheReviewTreeExactlyForReviews(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	workingShip(t, c, "ship1")
	if err := createReview(t, c, "rev1", "ship1", deliveryHead); err != nil {
		t.Fatal(err)
	}
	intent := reviewLaunchRequest(c, "rev1", preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-rev1", intent), intent); err != nil {
		t.Fatal(err)
	}
	attach := attachRequest(c, "rev1", preconditionOf(1, 2), intent, "handle-rev1")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-rev1", attach), attach); err != nil {
		t.Fatal(err)
	}

	noTree := recordLaunchRequest(c, "rev1", preconditionOf(1, 3), intent)
	_, err := c.RecordLaunch(mustOperation(t, "op-record-no-tree", noTree), noTree)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("RecordLaunch of a review without its tree = %v, want ErrConflict", err)
	}
	wantErrSubstring(t, err, "launch evidence carries the review tree exactly for a review task", "RecordLaunch of a review without its tree")

	tree := reviewTree(deliveryHead)
	withTree := recordLaunchRequest(c, "rev1", preconditionOf(1, 3), intent)
	withTree.ReviewTree = &tree
	op := mustOperation(t, "op-record-tree", withTree)
	if _, err := c.RecordLaunch(op, withTree); err != nil {
		t.Fatalf("RecordLaunch of a review with its tree: %v", err)
	}
	agg, err := c.Get(mustTaskID(t, "rev1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.LaunchEvidence == nil || agg.LaunchEvidence.ReviewTree == nil || *agg.LaunchEvidence.ReviewTree != tree {
		t.Fatalf("launch evidence = %+v, want the review tree %+v", agg.LaunchEvidence, tree)
	}
	if got := agg.LaunchEvidence.Seat; got.PromptDigest != testSeat().PromptDigest || len(got.Argv) != len(testSeat().Argv) {
		t.Fatalf("launch evidence seat = %+v, want %+v", got, testSeat())
	}

	otherTree := reviewTree(reviewBaseSHA)
	differentTree := withTree
	differentTree.Precondition = preconditionOf(1, 4)
	differentTree.ReviewTree = &otherTree
	_, err = c.RecordLaunch(mustOperation(t, "op-record-other-tree", differentTree), differentTree)
	wantErrSubstring(t, err, "already records different launch evidence", "RecordLaunch replaying with another review tree")

	differentSeat := withTree
	differentSeat.Precondition = preconditionOf(1, 4)
	differentSeat.Seat = testSeat()
	differentSeat.Seat.Argv = []string{"pi", "--other"}
	_, err = c.RecordLaunch(mustOperation(t, "op-record-other-seat", differentSeat), differentSeat)
	wantErrSubstring(t, err, "already records different launch evidence", "RecordLaunch replaying with another seat")

	ship := createRequest(c, "ship2")
	if _, err := c.Create(mustOperation(t, "op-create-ship2", ship), ship); err != nil {
		t.Fatal(err)
	}
	shipIntent := launchRequest(c, "ship2", preconditionOf(1, 1))
	if _, err := c.BeginSpawn(mustOperation(t, "op-begin-ship2", shipIntent), shipIntent); err != nil {
		t.Fatal(err)
	}
	shipWorktree := launchWorktreeBinding(shipIntent)
	shipBind := CanonicalBindWorktreeRequest{
		HomeID: c.HomeID(), TaskID: mustTaskID(t, "ship2"), Precondition: preconditionOf(1, 2),
		Binding: shipWorktree, Reason: "bind worktree",
	}
	if _, err := c.BindWorktree(mustOperation(t, "op-bind-wt-ship2", shipBind), shipBind); err != nil {
		t.Fatal(err)
	}
	mustRecordLaunchManifest(t, c, "ship2", shipIntent, shipWorktree, 3)
	shipAttach := attachRequest(c, "ship2", preconditionOf(1, 4), shipIntent, "handle-ship2")
	if _, err := c.AttachEndpoint(mustOperation(t, "op-attach-ship2", shipAttach), shipAttach); err != nil {
		t.Fatal(err)
	}
	shipWithTree := recordLaunchRequest(c, "ship2", preconditionOf(1, 5), shipIntent)
	shipWithTree.ReviewTree = &tree
	_, err = c.RecordLaunch(mustOperation(t, "op-record-ship-tree", shipWithTree), shipWithTree)
	wantErrSubstring(t, err, "launch evidence carries the review tree exactly for a review task", "RecordLaunch of a ship carrying a review tree")

	badTree := recordLaunchRequest(c, "ship2", preconditionOf(1, 5), shipIntent)
	badTree.ReviewTree = &domain.TreeState{Head: deliveryHead}
	_, err = c.RecordLaunch(mustOperation(t, "op-record-bad-tree", badTree), badTree)
	wantErrSubstring(t, err, "launch evidence review tree requires a head and a porcelain digest", "RecordLaunch with a tree lacking its porcelain digest")

}

// verdictScene is a working ship task "ship1" (endpoint incarnation
// shipIncarnation, revision 3) and a working review task "rev1" reading it at
// deliveryHead.
func verdictScene(t *testing.T) *Canonical {
	t.Helper()
	c, _, _ := newTestCanonical(t)
	workingShip(t, c, "ship1")
	workingReview(t, c, "rev1", "ship1", deliveryHead)
	return c
}

func recordVerdict(t *testing.T, c *Canonical, opID string, rev uint64, v domain.ReviewVerdict) (Outcome, error) {
	t.Helper()
	req := CanonicalRecordReviewVerdictRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "ship1"), Precondition: preconditionOf(1, rev), Verdict: v}
	return c.RecordReviewVerdict(mustOperation(t, opID, req), req)
}

func TestCanonicalRecordReviewVerdictStoresOneReplaceableRecord(t *testing.T) {
	c := verdictScene(t)
	v := reviewVerdict("rev1", deliveryHead)

	out, err := recordVerdict(t, c, "op-verdict-1", 3, v)
	if err != nil {
		t.Fatalf("RecordReviewVerdict: %v", err)
	}
	if out.Revision != 4 {
		t.Fatalf("outcome revision = %d, want 4", out.Revision)
	}
	agg, err := c.Get(mustTaskID(t, "ship1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.ReviewVerdict == nil || agg.ReviewVerdict.Verdict != v || agg.ReviewVerdict.OperationID != "op-verdict-1" || agg.ReviewVerdict.RecordedAt <= 0 {
		t.Fatalf("stored verdict = %+v, want the recorded verdict under op-verdict-1", agg.ReviewVerdict)
	}

	replay, err := recordVerdict(t, c, "op-verdict-1", 3, v)
	if err != nil || !replay.Replayed || replay.Revision != 4 {
		t.Fatalf("replay = %+v, %v, want a replayed outcome at revision 4", replay, err)
	}

	failed := v
	failed.Outcome = domain.VerdictFail
	if _, err := recordVerdict(t, c, "op-verdict-2", 4, failed); err != nil {
		t.Fatalf("second verdict: %v", err)
	}
	agg, _ = c.Get(mustTaskID(t, "ship1"))
	if agg.ReviewVerdict.Verdict.Outcome != domain.VerdictFail || agg.ReviewVerdict.OperationID != "op-verdict-2" {
		t.Fatalf("stored verdict after a later record = %+v, want the later verdict to replace the earlier", agg.ReviewVerdict)
	}

	reopenGuardTask(t, c, "ship1")
	agg, _ = c.Get(mustTaskID(t, "ship1"))
	if agg.ReviewVerdict != nil {
		t.Fatalf("reopened generation carries verdict %+v, want none", agg.ReviewVerdict)
	}
}

// reopenGuardTask completes and reopens the task so a new generation starts.
func reopenGuardTask(t *testing.T, c *Canonical, taskID string) {
	t.Helper()
	agg, err := c.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	complete := CanonicalCompleteRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(uint64(agg.Generation), uint64(agg.Revision)), To: PhaseDone, Reason: "done"}
	if _, err := c.Complete(mustOperation(t, "op-complete-"+taskID, complete), complete); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	reopen := CanonicalReopenRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, taskID), Precondition: preconditionOf(uint64(agg.Generation), uint64(agg.Revision)+1), Reason: "reopen"}
	if _, err := c.Reopen(mustOperation(t, "op-reopen-"+taskID, reopen), reopen); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
}

func TestCanonicalRecordReviewVerdictRefusals(t *testing.T) {
	good := reviewVerdict("rev1", deliveryHead)
	cases := []struct {
		name   string
		mutate func(*domain.ReviewVerdict)
		want   string
	}{
		{"invalid verdict", func(v *domain.ReviewVerdict) { v.BaseSHA = v.HeadSHA }, "review range base equals head"},
		{"author is not the bound soldier instance", func(v *domain.ReviewVerdict) { v.Author = "inc-someone-else" }, "is not the authoring soldier instance"},
		{"reviewer task id is unsafe", func(v *domain.ReviewVerdict) { v.ReviewerTask = "../rev1" }, "review verdict reviewer task"},
		{"reviewer task is a ship task", func(v *domain.ReviewVerdict) { v.ReviewerTask = "ship1" }, "is a ship task, not a review task"},
		{"reviewer generation is not current", func(v *domain.ReviewVerdict) { v.ReviewerGeneration = 2 }, "is not the current generation"},
		{"verdict head is not the head the reviewer was asked to review", func(v *domain.ReviewVerdict) {
			*v = reviewVerdict("rev1", reviewBaseSHA)
			v.BaseSHA = deliveryHead
		}, "was asked to review"},
		{"reviewer instance is not the bound endpoint instance", func(v *domain.ReviewVerdict) { v.ReviewerIncarnation = "inc-impostor" }, "is not the bound endpoint instance"},
		{"before tree is not the tree recorded at launch", func(v *domain.ReviewVerdict) {
			v.Before.Porcelain = digestOf("another tree")
			v.After = v.Before
		}, "does not match the tree recorded at the launch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := verdictScene(t)
			v := good
			tc.mutate(&v)
			_, err := recordVerdict(t, c, "op-verdict-refused", 3, v)
			wantErrSubstring(t, err, tc.want, "RecordReviewVerdict")
			if agg, _ := c.Get(mustTaskID(t, "ship1")); agg.ReviewVerdict != nil || agg.Revision != 3 {
				t.Fatalf("refused verdict left revision %d, record %+v", agg.Revision, agg.ReviewVerdict)
			}
		})
	}

	t.Run("reviewer task does not exist", func(t *testing.T) {
		c := verdictScene(t)
		v := good
		v.ReviewerTask = "ghost"
		if _, err := recordVerdict(t, c, "op-verdict-ghost", 3, v); !errors.Is(err, ErrNotFound) {
			t.Fatalf("RecordReviewVerdict by an unknown reviewer = %v, want ErrNotFound", err)
		}
	})

	t.Run("reviewer reads another task", func(t *testing.T) {
		c := verdictScene(t)
		workingShip(t, c, "ship2")
		workingReview(t, c, "rev2", "ship2", deliveryHead)
		_, err := recordVerdict(t, c, "op-verdict-other", 3, reviewVerdict("rev2", deliveryHead))
		wantErrSubstring(t, err, `reviews "ship2", not task ship1`, "RecordReviewVerdict by the reviewer of another task")
	})

	t.Run("reviewer recorded no tree at launch", func(t *testing.T) {
		c := verdictScene(t)
		rewriteTaskDocForTest(t, c, "rev1", func(agg Aggregate) Aggregate {
			ev := *agg.LaunchEvidence
			ev.ReviewTree = nil
			agg.LaunchEvidence = &ev
			return agg
		})
		_, err := recordVerdict(t, c, "op-verdict-no-tree", 3, good)
		wantErrSubstring(t, err, "recorded no tree before its review", "RecordReviewVerdict by a reviewer with no launch tree")
	})

	t.Run("reviewer has no bound endpoint", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		workingShip(t, c, "ship1")
		if err := createReview(t, c, "rev1", "ship1", deliveryHead); err != nil {
			t.Fatal(err)
		}
		_, err := recordVerdict(t, c, "op-verdict-unbound", 3, good)
		wantErrSubstring(t, err, "is not the bound endpoint instance", "RecordReviewVerdict by a reviewer that never launched")
	})

	t.Run("task is not working", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		mustCreate(t, c, "ship1")
		_, err := recordVerdict(t, c, "op-verdict-queued", 1, good)
		wantErrSubstring(t, err, "review verdict requires a working task", "RecordReviewVerdict on a queued task")
	})

	t.Run("task has no bound worktree", func(t *testing.T) {
		c := verdictScene(t)
		req := CanonicalRecordReviewVerdictRequest{HomeID: c.HomeID(), TaskID: mustTaskID(t, "rev1"), Precondition: preconditionOf(1, 5), Verdict: good}
		_, err := c.RecordReviewVerdict(mustOperation(t, "op-verdict-on-review", req), req)
		wantErrSubstring(t, err, "requires the bound worktree and endpoint", "RecordReviewVerdict on a review task that owns no worktree")
	})
}

// AuthorizeDelivery relies on the recorded verdict, never on the request: it
// needs one, bound to exactly the identity head, passing, and naming the bound
// soldier instance as its author.
func TestCanonicalAuthorizeDeliveryReliesOnTheHeadBoundVerdictAndRecordsTheWords(t *testing.T) {
	authorize := func(c *Canonical, rev uint64, opID string, mutate func(*CanonicalDeliveryAuthorizationRequest)) (DeliveryAuthorizationResult, error) {
		req := authorizeRequest(c, "ship1", preconditionOf(1, rev))
		if mutate != nil {
			mutate(&req)
		}
		return c.AuthorizeDelivery(mustOperation(t, opID, req), req)
	}

	t.Run("no verdict recorded", func(t *testing.T) {
		c, _, _ := newTestCanonical(t)
		workingShip(t, c, "ship1")
		_, err := authorize(c, 3, "op-auth-no-verdict", nil)
		wantErrSubstring(t, err, "delivery authorization requires a review verdict for head", "AuthorizeDelivery with no verdict recorded")
	})

	t.Run("verdict does not pass", func(t *testing.T) {
		c := verdictScene(t)
		failed := reviewVerdict("rev1", deliveryHead)
		failed.Outcome = domain.VerdictFail
		if _, err := recordVerdict(t, c, "op-verdict-fail", 3, failed); err != nil {
			t.Fatal(err)
		}
		_, err := authorize(c, 4, "op-auth-failed-verdict", nil)
		wantErrSubstring(t, err, "delivery authorization refused: review verdict is", "AuthorizeDelivery on a failing verdict")
	})

	t.Run("records the verdict and the words it relied on", func(t *testing.T) {
		c := verdictScene(t)
		if _, err := recordVerdict(t, c, "op-verdict-ok", 3, reviewVerdict("rev1", deliveryHead)); err != nil {
			t.Fatal(err)
		}
		res, err := authorize(c, 4, "op-auth-ok", nil)
		if err != nil {
			t.Fatalf("AuthorizeDelivery: %v", err)
		}
		if res.Authorization.Verdict != reviewVerdict("rev1", deliveryHead) || res.Authorization.Words != testWords() {
			t.Fatalf("authorization verdict/words = %+v / %+v, want the recorded verdict and the request words", res.Authorization.Verdict, res.Authorization.Words)
		}
		if got := currentAuthorizationForTest(t, c, "ship1"); got.Words != testWords() || got.Verdict != reviewVerdict("rev1", deliveryHead) {
			t.Fatalf("stored authorization = %+v, want verdict and words persisted", got)
		}
	})
}

func TestCanonicalReleaseHoldRecordsTheReleaseWordsAndKeepsThemOnReplay(t *testing.T) {
	c, _, _ := newTestCanonical(t)
	add := addHoldRequest(c, "hold-1")
	if _, err := c.AddHold(mustOperation(t, "op-hold-add", add), add); err != nil {
		t.Fatal(err)
	}
	holdWords := func() *domain.Words {
		holds, err := c.listHolds()
		if err != nil || len(holds) != 1 {
			t.Fatalf("listHolds = %v, %v", holds, err)
		}
		return holds[0].ReleaseWords
	}
	if holdWords() != nil {
		t.Fatal("an active hold carries release words")
	}

	noQuote := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-1", Reason: "resume", Words: testWords()}
	noQuote.Words.Quote = ""
	_, err := c.ReleaseHold(mustOperation(t, "op-release-noquote", noQuote), noQuote)
	wantErrSubstring(t, err, "dispatch hold release: words: quote is required", "ReleaseHold with no quote")
	if holdWords() != nil {
		t.Fatal("a refused release stored words")
	}

	first := CanonicalReleaseHoldRequest{HomeID: c.HomeID(), HoldID: "hold-1", Reason: "resume", Words: testWords()}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-1", first), first); err != nil {
		t.Fatal(err)
	}
	if got := holdWords(); got == nil || *got != testWords() {
		t.Fatalf("release words = %+v, want %+v", got, testWords())
	}

	other := first
	other.Words = domain.Words{Grantor: "someone", Channel: "supervisor-relay:typed", Quote: "a later quote"}
	if _, err := c.ReleaseHold(mustOperation(t, "op-release-2", other), other); err != nil {
		t.Fatalf("second release of a released hold: %v", err)
	}
	if got := holdWords(); got == nil || *got != testWords() {
		t.Fatalf("release words after a second release = %+v, want the original %+v", got, testWords())
	}
}
