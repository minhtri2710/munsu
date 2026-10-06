//go:build integration

package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// createReviewTask registers a review task of shipID at head that has not
// launched, and returns a Runner resolved to it.
func createReviewTask(t *testing.T, c *taskauthority.Canonical, homeDir, shipID, reviewID, head string) *Runner {
	t.Helper()
	project, err := domain.NewProjectID("project")
	if err != nil {
		t.Fatal(err)
	}
	create := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, reviewID), Owner: "owner", Description: "review",
		Kind: taskauthority.KindReview, Project: project, ReviewTaskID: shipID, ReviewHead: head, Reason: "create",
	}
	if _, err := c.Create(mustFleetOperation(t, "op-create-"+reviewID, create), create); err != nil {
		t.Fatalf("Create(%s): %v", reviewID, err)
	}
	r := NewRunner(Args{ID: reviewID, HomeDir: homeDir, Authority: c})
	r.homeDir = homeDir
	if err := r.resolveKind(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolveKindReadsTheDefinitionKind(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	mustWorkingShipTaskAt(t, c, "ship-1", newLinkedDeliveryWorktree(t))
	r := createReviewTask(t, c, homeDir, "ship-1", "review-1", deliveryTestHead)
	if r.kind != taskauthority.KindReview || r.reviewTask != "ship-1" || r.reviewHead != deliveryTestHead {
		t.Fatalf("review kind=%q task=%q head=%q", r.kind, r.reviewTask, r.reviewHead)
	}
	ship := NewRunner(Args{ID: "ship-1", HomeDir: homeDir, Authority: c})
	if err := ship.resolveKind(); err != nil {
		t.Fatal(err)
	}
	if ship.kind != taskauthority.KindShip || ship.reviewTask != "" || ship.reviewHead != "" {
		t.Fatalf("ship kind=%q task=%q head=%q", ship.kind, ship.reviewTask, ship.reviewHead)
	}
	if err := NewRunner(Args{ID: "absent", HomeDir: homeDir, Authority: c}).resolveKind(); err == nil || !strings.Contains(err.Error(), "no canonical Task Authority record") {
		t.Fatalf("unknown task error = %v", err)
	}
	if err := NewRunner(Args{ID: "ship-1", HomeDir: homeDir}).resolveKind(); err == nil || !strings.Contains(err.Error(), "task authority is not composed") {
		t.Fatalf("no authority error = %v", err)
	}
}

func TestAdoptReviewedWorktreeReadsTheReviewedCheckoutInPlace(t *testing.T) {
	c, homeDir := newFleetCanonical(t)
	worktree := mustWorkingShipTaskAt(t, c, "ship-1", newLinkedDeliveryWorktree(t))
	r := createReviewTask(t, c, homeDir, "ship-1", "review-1", deliveryTestHead)

	bound, err := r.adoptReviewedWorktree()
	if err != nil {
		t.Fatal(err)
	}
	wantDir := reviewLaunchDir(homeDir, "review-1", "1")
	wantWT, err := canonicalExistingPath(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Path() != wantWT || r.cwd != wantWT {
		t.Fatalf("bound=%q cwd=%q, want the reviewed worktree %q", bound.Path(), r.cwd, wantWT)
	}
	if r.launchDir != wantDir {
		t.Fatalf("launchDir=%q, want %q", r.launchDir, wantDir)
	}
	if st, err := os.Stat(wantDir); err != nil || !st.IsDir() {
		t.Fatalf("launch dir not created: %v", err)
	}
	rv := r.review
	if rv == nil || rv.TaskID != "review-1" || rv.Generation != 1 || rv.ReviewedTaskID != "ship-1" ||
		rv.ReviewHead != deliveryTestHead || rv.LaunchDir != wantDir || rv.VerdictFile != filepath.Join(wantDir, reviewVerdictFileName) {
		t.Fatalf("review launch = %+v", rv)
	}
	if rv.Before.Head != deliveryTestHead || rv.Before.Porcelain == "" {
		t.Fatalf("review Before = %+v, want the observed head and tree digest", rv.Before)
	}
	if entries, _ := os.ReadDir(worktree); len(entries) != 2 { // f and .git link: nothing written into the checkout
		t.Fatalf("reviewed checkout entries = %d, want 2", len(entries))
	}
}

func TestAdoptReviewedWorktreeRefusals(t *testing.T) {
	t.Run("authority not composed", func(t *testing.T) {
		r := NewRunner(Args{ID: "review-1", HomeDir: t.TempDir()})
		if _, err := r.adoptReviewedWorktree(); err == nil || !strings.Contains(err.Error(), "task authority is not composed") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown review task", func(t *testing.T) {
		c, homeDir := newFleetCanonical(t)
		r := NewRunner(Args{ID: "review-1", HomeDir: homeDir, Authority: c})
		if _, err := r.adoptReviewedWorktree(); err == nil || !strings.Contains(err.Error(), "no canonical Task Authority record") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid reviewed task id", func(t *testing.T) {
		c, homeDir := newFleetCanonical(t)
		mustWorkingShipTaskAt(t, c, "ship-1", newLinkedDeliveryWorktree(t))
		r := createReviewTask(t, c, homeDir, "ship-1", "review-1", deliveryTestHead)
		r.reviewTask = "Not A Task"
		if _, err := r.adoptReviewedWorktree(); err == nil || !strings.Contains(err.Error(), "adopting reviewed worktree: ") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown reviewed task", func(t *testing.T) {
		c, homeDir := newFleetCanonical(t)
		mustWorkingShipTaskAt(t, c, "ship-1", newLinkedDeliveryWorktree(t))
		r := createReviewTask(t, c, homeDir, "ship-1", "review-1", deliveryTestHead)
		r.reviewTask = "ship-2"
		if _, err := r.adoptReviewedWorktree(); err == nil || !strings.Contains(err.Error(), "resolving reviewed task ship-2") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("reviewed task no longer working", func(t *testing.T) {
		c, homeDir := newFleetCanonical(t)
		mustWorkingShipTaskAt(t, c, "ship-1", newLinkedDeliveryWorktree(t))
		r := createReviewTask(t, c, homeDir, "ship-1", "review-1", deliveryTestHead)
		ship, err := c.Get(mustFleetTaskID(t, "ship-1"))
		if err != nil {
			t.Fatal(err)
		}
		block := taskauthority.CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, "ship-1"), Precondition: domain.Of(uint64(ship.Generation), uint64(ship.Revision)), Detail: "dep", Reason: "block"}
		if _, err := c.Block(mustFleetOperation(t, "op-block-ship", block), block); err != nil {
			t.Fatal(err)
		}
		if _, err := r.adoptReviewedWorktree(); err == nil || !strings.Contains(err.Error(), "no longer a working ship task") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("reviewed worktree gone", func(t *testing.T) {
		c, homeDir := newFleetCanonical(t)
		worktree := mustWorkingShipTaskAt(t, c, "ship-1", newLinkedDeliveryWorktree(t))
		r := createReviewTask(t, c, homeDir, "ship-1", "review-1", deliveryTestHead)
		if err := os.RemoveAll(worktree); err != nil {
			t.Fatal(err)
		}
		if _, err := r.adoptReviewedWorktree(); err == nil || !strings.Contains(err.Error(), "adopting reviewed worktree: ") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("head moved", func(t *testing.T) {
		c, homeDir := newFleetCanonical(t)
		mustWorkingShipTaskAt(t, c, "ship-1", newLinkedDeliveryWorktree(t))
		other := strings.Repeat("9", 40)
		r := createReviewTask(t, c, homeDir, "ship-1", "review-1", other)
		_, err := r.adoptReviewedWorktree()
		if err == nil || !strings.Contains(err.Error(), "not the reviewed head "+other) || !strings.Contains(err.Error(), "add a new review for the new head") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("launch dir unwritable", func(t *testing.T) {
		c, homeDir := newFleetCanonical(t)
		mustWorkingShipTaskAt(t, c, "ship-1", newLinkedDeliveryWorktree(t))
		r := createReviewTask(t, c, homeDir, "ship-1", "review-1", deliveryTestHead)
		if err := os.WriteFile(filepath.Join(homeDir, "review"), []byte("file"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := r.adoptReviewedWorktree(); err == nil || !strings.Contains(err.Error(), "adopting reviewed worktree: ") {
			t.Fatalf("err = %v", err)
		}
		if r.review != nil {
			t.Fatal("a refused adoption must leave no review launch")
		}
	})
}

func TestBuildBeginSpawnRequestReservesNoWorktreeForAReview(t *testing.T) {
	c, _ := newFleetCanonical(t)
	for _, tc := range []struct {
		kind        string
		wantReserve bool
	}{
		{taskauthority.KindShip, true},
		{taskauthority.KindReview, false},
	} {
		r := NewRunner(Args{ID: "task-1", Authority: c})
		r.kind, r.taskID = tc.kind, mustFleetTaskID(t, "task-1")
		req := r.buildBeginSpawnRequest(domain.Of(1, 1), 1, "incarnation")
		if got := req.WorktreeReservationID != "" && req.WorktreeFenceToken != ""; got != tc.wantReserve {
			t.Errorf("%s: worktree reservation %q fence %q, want reserved=%v", tc.kind, req.WorktreeReservationID, req.WorktreeFenceToken, tc.wantReserve)
		}
		if req.EndpointReservationID == "" || req.EndpointFenceToken == "" || req.Kind != tc.kind {
			t.Errorf("%s: endpoint reservation %q fence %q kind %q, want an endpoint reservation and the kind", tc.kind, req.EndpointReservationID, req.EndpointFenceToken, req.Kind)
		}
	}
}

// TestReviewLaunchRunsEveryPhaseAgainstTheReviewedCheckout drives a review
// task through the launch phases a review runs: it adopts the ship task's
// worktree, launches a reviewer under its own fence with the launch files in
// the home, records the review tree in its launch evidence, and leaves the
// reviewed checkout untouched and unbound to the review.
func TestReviewLaunchRunsEveryPhaseAgainstTheReviewedCheckout(t *testing.T) {
	isolateHuman(t)
	f := newLaunchFixture(t, "unused-ship")
	worktree := mustWorkingShipTaskAt(t, f.auth, "ship-1", newLinkedDeliveryWorktree(t))
	settings := harness.PiProjectSettings() // the ship launch wrote it; a reviewer only reads it
	writePiSettingsInWorktree(t, worktree, settings)
	r := createReviewTask(t, f.auth, f.homeDir, "ship-1", "review-1", deliveryTestHead)
	cp := *f.runner
	cp.args.ID, cp.kind, cp.reviewTask, cp.reviewHead = "review-1", r.kind, r.reviewTask, r.reviewHead
	rr := &cp
	briefDir := filepath.Join(f.homeDir, "data", "review-1")
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(briefDir, "brief.md"), []byte("# review brief"), 0o644); err != nil {
		t.Fatal(err)
	}

	var bound BoundWorktree
	phases := []struct {
		name string
		fn   func() error
	}{
		{"begin", rr.beginLaunchIntent},
		{"adopt", func() (err error) { bound, err = rr.adoptReviewedWorktree(); return }},
		{"prompt", func() error { return rr.buildSoldierPrompt(bound) }},
		{"probe-fence", func() error { return rr.probeFence(bound) }},
		{"create-session", rr.createSession},
		{"attach-endpoint", rr.attachEndpoint},
		{"submit", rr.submitLaunch},
		{"ready", rr.waitAndInjectBrief},
		{"verify", rr.verifyEndpointReadyBeforePersist},
		{"meta", rr.writeTaskMeta},
		{"confirm", func() error { _, err := rr.confirmSpawn(); return err }},
	}
	for _, p := range phases {
		if err := p.fn(); err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
	}

	agg, err := f.auth.Get(mustFleetTaskID(t, "review-1"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseWorking || agg.Worktree != nil || agg.Endpoint == nil {
		t.Fatalf("review phase=%q worktree=%+v endpoint=%+v, want working with an endpoint and no worktree of its own", agg.Phase, agg.Worktree, agg.Endpoint)
	}
	ev := agg.LaunchEvidence
	if ev == nil || ev.ReviewTree == nil || *ev.ReviewTree != rr.review.Before || ev.ReviewTree.Head != deliveryTestHead {
		t.Fatalf("launch evidence = %+v, want the review tree observed before launch", ev)
	}
	wantRole := ""
	if runtime.GOOS == "darwin" {
		wantRole = "reviewer"
	}
	if ev.Seat.Fence.Role != wantRole {
		t.Fatalf("seat fence = %+v, want role %q", ev.Seat.Fence, wantRole)
	}
	script := readFile(t, filepath.Join(rr.launchDir, ".soldier-launch.sh"))
	if !strings.Contains(script, "MUNSU_VERDICT_FILE") || !strings.HasPrefix(rr.launchDir, filepath.Join(f.homeDir, "review", "review-1")) {
		t.Fatalf("launch dir %q script lacks the verdict file export", rr.launchDir)
	}
	ship, err := f.auth.Get(mustFleetTaskID(t, "ship-1"))
	if err != nil || ship.Worktree == nil || ship.Phase != taskauthority.PhaseWorking {
		t.Fatalf("reviewed task = %+v, %v; want it still working with its worktree", ship, err)
	}
	if got := readFile(t, filepath.Join(worktree, filepath.FromSlash(PiSettingsName))); got != string(settings) {
		t.Fatalf("the review rewrote the reviewed checkout's pi settings: %q", got)
	}
	if entries, _ := os.ReadDir(worktree); len(entries) != 3 { // f, .git link and .pi
		t.Fatalf("reviewed checkout entries = %d, want 3: the review wrote into it", len(entries))
	}
}

// TestReviewPromptWritesNothingIntoTheReviewedWorktreeGitDir proves the review
// guard in buildSoldierPrompt: a reviewer's prompt phase persists its launch
// files under the home and writes nothing into the reviewed worktree — no
// excludes file in its git directory and no extensions.worktreeConfig on the
// shared repository config. Without the r.review guard the excludes write
// would run git inside the home (not a repository) and fail the phase.
func TestReviewPromptWritesNothingIntoTheReviewedWorktreeGitDir(t *testing.T) {
	isolateHuman(t)
	f := newLaunchFixture(t, "unused-ship")
	worktree := mustWorkingShipTaskAt(t, f.auth, "ship-1", newLinkedDeliveryWorktree(t))
	r := createReviewTask(t, f.auth, f.homeDir, "ship-1", "review-1", deliveryTestHead)
	cp := *f.runner
	cp.args.ID, cp.kind, cp.reviewTask, cp.reviewHead = "review-1", r.kind, r.reviewTask, r.reviewHead
	rr := &cp
	briefDir := filepath.Join(f.homeDir, "data", "review-1")
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(briefDir, "brief.md"), []byte("# review brief"), 0o644); err != nil {
		t.Fatal(err)
	}

	bound, err := rr.adoptReviewedWorktree()
	if err != nil {
		t.Fatalf("adoptReviewedWorktree: %v", err)
	}
	if err := rr.buildSoldierPrompt(bound); err != nil {
		t.Fatalf("buildSoldierPrompt: %v", err)
	}

	// The launch files live under the home, never in the reviewed checkout.
	if !strings.HasPrefix(rr.launchDir, filepath.Join(f.homeDir, "review", "review-1")) {
		t.Fatalf("launchDir = %q, want it under the home's review directory", rr.launchDir)
	}
	entries, err := os.ReadDir(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 { // the pre-existing file and the .git link
		t.Fatalf("reviewed checkout entries = %d, want 2: the review wrote into it", len(entries))
	}
	gitLink, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(string(gitLink), "gitdir: "))
	if _, err := os.Stat(filepath.Join(gitDir, worktreeExcludeFileName)); !os.IsNotExist(err) {
		t.Fatalf("the review wrote %s into the reviewed worktree's git dir", worktreeExcludeFileName)
	}
	if out, err := exec.Command("git", "-C", worktree, "config", "--local", "--get", "extensions.worktreeConfig").CombinedOutput(); err == nil && strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the review enabled extensions.worktreeConfig on the shared repository config: %s", out)
	}
}
