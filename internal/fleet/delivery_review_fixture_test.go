//go:build integration

package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// deliveryWords is a complete Human words record for a delivery authorization.
func deliveryWords() domain.Words {
	return domain.Words{Grantor: "beo", Channel: "supervisor-relay:typed", Quote: "munsu: dong y"}
}

// unfencedSeat is a valid seat record of a launch that ran without a fence.
func unfencedSeat() taskauthority.LaunchSeat {
	sum := sha256.Sum256([]byte("prompt"))
	return taskauthority.LaunchSeat{
		Argv:         []string{"pi", "--no-session"},
		PromptDigest: hex.EncodeToString(sum[:]),
		Fence:        taskauthority.FenceRecord{Reason: "no fence on this host"},
	}
}

// reviewFixture is a review task driven to working with its endpoint bound.
type reviewFixture struct {
	TaskID      string
	Incarnation string
	Tree        domain.TreeState
}

// mustRecordApprovingVerdict records a PASS verdict for head on the working
// ship task: it drives a review task that reads the ship task at head through
// its launch to working, then records the verdict that review produced, the
// only approval source delivery authorization accepts.
func mustRecordApprovingVerdict(t *testing.T, c *taskauthority.Canonical, shipID, head string) {
	t.Helper()
	review := mustBindReviewTask(t, c, shipID, head)
	ship, err := c.Get(mustFleetTaskID(t, shipID))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := c.Get(mustFleetTaskID(t, review.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	verdict := domain.ReviewVerdict{
		Outcome: domain.VerdictPass, HeadSHA: head, BaseSHA: strings.Repeat("1", 40),
		ReviewerTask: review.TaskID, ReviewerGeneration: uint64(rev.Generation), ReviewerIncarnation: review.Incarnation,
		Author: ship.Endpoint.Incarnation, Before: review.Tree, After: review.Tree,
	}
	rec := taskauthority.CanonicalRecordReviewVerdictRequest{
		HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, shipID),
		Precondition: domain.Of(uint64(ship.Generation), uint64(ship.Revision)), Verdict: verdict,
	}
	if _, err := c.RecordReviewVerdict(mustFleetOperation(t, "op-verdict-"+shipID, rec), rec); err != nil {
		t.Fatalf("RecordReviewVerdict(%s): %v", shipID, err)
	}
}

// mustBindReviewTask creates the review task of shipID at head and drives it
// through its launch to working, with its endpoint bound and its review tree
// recorded.
func mustBindReviewTask(t *testing.T, c *taskauthority.Canonical, shipID, head string) reviewFixture {
	t.Helper()
	reviewID := "review-" + shipID
	rid := mustFleetTaskID(t, reviewID)
	project, err := domain.NewProjectID("project")
	if err != nil {
		t.Fatal(err)
	}
	create := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: rid, Owner: "owner", Description: "review",
		Kind: taskauthority.KindReview, Project: project, ReviewTaskID: shipID, ReviewHead: head, Reason: "create",
	}
	if _, err := c.Create(mustFleetOperation(t, "op-create-"+reviewID, create), create); err != nil {
		t.Fatalf("Create(%s): %v", reviewID, err)
	}
	begin := taskauthority.CanonicalBeginSpawnRequest{
		HomeID: c.HomeID(), TaskID: rid, Precondition: domain.Of(1, 1),
		SnapshotDigest: strings.Repeat("a", 64), Backend: "tmux", Harness: "pi", Model: "opus", Effort: "high",
		Mode: "direct-PR", Kind: taskauthority.KindReview, Project: "proj", ParentTaskID: "parent",
		LaunchID: "launch-" + reviewID, WindowLabel: "window-" + reviewID,
		EndpointReservationID: "ep-res-" + reviewID, EndpointFenceToken: "ep-fence-" + reviewID,
		EndpointIncarnation: "inc-" + reviewID, Reason: "spawn",
	}
	if _, err := c.BeginSpawn(mustFleetOperation(t, "op-begin-"+reviewID, begin), begin); err != nil {
		t.Fatalf("BeginSpawn(%s): %v", reviewID, err)
	}
	attach := taskauthority.CanonicalAttachEndpointRequest{
		HomeID: c.HomeID(), TaskID: rid, Precondition: domain.Of(1, 2), Backend: begin.Backend, Handle: "handle-" + reviewID,
		LeaseID: begin.EndpointReservationID, FenceToken: begin.EndpointFenceToken, SessionOwner: "owner",
		WorkspaceID: "ws", TabID: "tab", Incarnation: begin.EndpointIncarnation, Reason: "attach",
	}
	if _, err := c.AttachEndpoint(mustFleetOperation(t, "op-attach-"+reviewID, attach), attach); err != nil {
		t.Fatalf("AttachEndpoint(%s): %v", reviewID, err)
	}
	sum := sha256.Sum256(nil) // the porcelain digest of a clean worktree
	tree := domain.TreeState{Head: head, Porcelain: hex.EncodeToString(sum[:])}
	record := taskauthority.CanonicalRecordLaunchRequest{
		HomeID: c.HomeID(), TaskID: rid, Precondition: domain.Of(1, 3), LaunchID: begin.LaunchID,
		CommandDigest: strings.Repeat("c", 64), ReviewTree: &tree, Seat: unfencedSeat(), Reason: "record",
	}
	if _, err := c.RecordLaunch(mustFleetOperation(t, "op-record-"+reviewID, record), record); err != nil {
		t.Fatalf("RecordLaunch(%s): %v", reviewID, err)
	}
	bind := taskauthority.CanonicalBindEndpointRequest{
		HomeID: c.HomeID(), TaskID: rid, Precondition: domain.Of(1, 4), Reason: "spawn",
		Binding: taskauthority.EndpointBinding{
			Backend: begin.Backend, Handle: "handle-" + reviewID, LeaseID: begin.EndpointReservationID,
			FenceToken: begin.EndpointFenceToken, SessionOwner: "owner", WorkspaceID: "ws", TabID: "tab", Incarnation: begin.EndpointIncarnation, BoundAtUnix: 1,
		},
	}
	if _, err := c.BindEndpoint(mustFleetOperation(t, "op-bindep-"+reviewID, bind), bind); err != nil {
		t.Fatalf("BindEndpoint(%s): %v", reviewID, err)
	}

	return reviewFixture{TaskID: reviewID, Incarnation: begin.EndpointIncarnation, Tree: tree}
}

// newDeliveryWorktree creates a git worktree whose HEAD is deliveryTestHead: a
// one-file commit with fixed author, committer and dates, so its object id is
// the same on every host. Delivery compares the identity head with the head git
// reads at the bound worktree, so the fixture's head must be a real one.
func newDeliveryWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=munsu", "GIT_AUTHOR_EMAIL=munsu@example.invalid",
		"GIT_COMMITTER_NAME=munsu", "GIT_COMMITTER_EMAIL=munsu@example.invalid",
		"GIT_AUTHOR_DATE=2026-08-05T00:00:00Z", "GIT_COMMITTER_DATE=2026-08-05T00:00:00Z",
	)
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"add", "f"},
		{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "delivery fixture"},
	} {
		if args[0] == "add" {
			if err := os.WriteFile(dir+"/f", []byte("hello\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(head)); got != deliveryTestHead {
		t.Fatalf("delivery fixture HEAD = %s, want %s", got, deliveryTestHead)
	}
	return dir
}

// newLinkedDeliveryWorktree adds a linked worktree at deliveryTestHead to a
// fresh delivery repository: the shape spawn binds, with its git directory
// under <common>/worktrees.
func newLinkedDeliveryWorktree(t *testing.T) string {
	t.Helper()
	primary := newDeliveryWorktree(t)
	linked := filepath.Join(t.TempDir(), "wt")
	cmd := exec.Command("git", "-C", primary, "worktree", "add", "--detach", "-q", linked, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return linked
}
