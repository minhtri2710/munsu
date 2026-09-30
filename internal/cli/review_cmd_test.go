package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestTaskAddReviewReadsTheReviewedHeadFromTheWorktree: the reviewed head on a
// review task is what git reads at the reviewed task's bound worktree, never
// something the caller names.
func TestTaskAddReviewReadsTheReviewedHeadFromTheWorktree(t *testing.T) {
	homeDir := deliveryGuardShip(t, "t-ship")
	if out, err := runTaskCommand(t, []string{"task", "add", "r1", "review it", "--kind", "review", "--reviews", "t-ship", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	agg, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "r1"))
	if err != nil {
		t.Fatal(err)
	}
	def := agg.Definition
	if def.Kind != taskauthority.KindReview || def.ReviewTaskID != "t-ship" || def.ReviewHead != deliveryGuardHead {
		t.Fatalf("definition = %+v, want a review of t-ship at %s", def, deliveryGuardHead)
	}
}

// TestTaskAddReviewRefusesWithoutAReviewableTask: a review of a task with no
// bound worktree creates nothing.
func TestTaskAddReviewRefusesWithoutAReviewableTask(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	cliSeedCanonicalTask(t, homeDir, "queued-ship", "ship")
	for _, tc := range []struct{ reviews, want string }{
		{"queued-ship", "review verdict requires the bound worktree and endpoint of task queued-ship"},
		{"no-such-task", "task no-such-task not found"},
	} {
		reviews := tc.reviews
		out, err := runTaskCommand(t, []string{"task", "add", "r1", "review it", "--kind", "review", "--reviews", reviews, "--home", homeDir})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("--reviews %s: err = %v, want the worktree observation refusal %q\n%s", reviews, err, tc.want, out)
		}
		if _, err := testAuthorityFor(t, homeDir).Get(mustTaskIDFor(t, "r1")); err == nil {
			t.Fatalf("--reviews %s: a review task was created", reviews)
		}
	}
}

func TestDeliveryRecordVerdictRequiresReviewerTask(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	out, err := runTaskCommand(t, []string{"delivery", "record-verdict", "--home", homeDir})
	if err == nil || !strings.Contains(err.Error()+out, "reviewer-task") {
		t.Fatalf("err = %v, want the required --reviewer-task refusal\n%s", err, out)
	}
}

// stdoutOf returns what f wrote to os.Stdout.
func stdoutOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// writeGuardVerdictFile writes the verdict file the review task revID wrote
// for its current generation.
func writeGuardVerdictFile(t *testing.T, homeDir, revID, shipID, outcome string) {
	t.Helper()
	dir := filepath.Join(homeDir, "review", revID, "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(map[string]any{
		"schema_version": 1, "task": revID, "generation": 1, "reviews": shipID,
		"outcome": outcome, "head_sha": deliveryGuardHead, "base_sha": deliveryGuardBase, "evidence": "read the diff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verdict.json"), doc, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDeliveryRecordVerdictRecordsTheReviewTasksVerdictFile: the command reads
// the named review task's verdict file and records it on the reviewed task; a
// review task with no file records nothing.
func TestDeliveryRecordVerdictRecordsTheReviewTasksVerdictFile(t *testing.T) {
	homeDir := deliveryGuardShip(t, "t-ship")
	auth := cliCanonicalForHome(t, homeDir)
	workingGuardReview(t, auth, "t-ship", "rev-1")
	// record-verdict resolves the task home through the .meta projection.
	if err := home.WriteMeta(homeDir, "rev-1", map[string]string{"id": "rev-1", "kind": "review"}); err != nil {
		t.Fatal(err)
	}

	out, err := runTaskCommand(t, []string{"delivery", "record-verdict", "--reviewer-task", "rev-1", "--home", homeDir})
	if err == nil || !strings.Contains(err.Error(), "record-verdict rev-1") || !strings.Contains(err.Error(), "wrote no verdict file") {
		t.Fatalf("err = %v, want the no-verdict-file refusal\n%s", err, out)
	}

	writeGuardVerdictFile(t, homeDir, "rev-1", "t-ship", "pass")
	var runErr error
	out = stdoutOf(t, func() {
		_, runErr = runTaskCommand(t, []string{"delivery", "record-verdict", "--reviewer-task", "rev-1", "--home", homeDir})
	})
	if runErr != nil || !strings.Contains(out, "review verdict pass for task") || !strings.Contains(out, "recorded from rev-1 generation 1") {
		t.Fatalf("record-verdict: %v\nstdout=%q", runErr, out)
	}
	agg, err := auth.Get(mustTaskIDFor(t, "t-ship"))
	if err != nil {
		t.Fatal(err)
	}
	if agg.ReviewVerdict == nil || agg.ReviewVerdict.Verdict.Outcome != domain.VerdictPass || agg.ReviewVerdict.Verdict.ReviewerTask != "rev-1" {
		t.Fatalf("recorded verdict = %+v, want a PASS from rev-1", agg.ReviewVerdict)
	}
}

// TestReportRefusesAReviewTask: a review soldier's outcome is its verdict file,
// so `report` from a review task fails and writes no status.
func TestReportRefusesAReviewTask(t *testing.T) {
	homeDir := deliveryGuardShip(t, "t-ship")
	workingGuardReview(t, cliCanonicalForHome(t, homeDir), "t-ship", "rev-1")
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "rev-1")
	t.Setenv("MUNSU_ROLE", "soldier")
	t.Setenv("MUNSU_PARENT_STATUS", homeDir)

	out, err := runTaskCommand(t, []string{"report", "done", "looks fine", "--key", "rev-1", "--ring", "no-ring"})
	if err == nil || !strings.Contains(err.Error(), "task rev-1 is a review task") {
		t.Fatalf("err = %v, want the review-task refusal\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(homeDir, "state", "rev-1.status")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused report wrote a status file: %v", statErr)
	}
}

// TestReviewVerdictPortAdaptsTheFleetRecordStep: the port lists the working
// review tasks, sees a verdict file appear with its digest, and records it.
func TestReviewVerdictPortAdaptsTheFleetRecordStep(t *testing.T) {
	homeDir := deliveryGuardShip(t, "t-ship")
	workingGuardReview(t, cliCanonicalForHome(t, homeDir), "t-ship", "rev-1")
	port := fleetReviewVerdictPort{}

	targets, err := port.WorkingReviews(homeDir)
	if err != nil || len(targets) != 1 || targets[0] != (orchestrator.ReviewVerdictTarget{TaskID: "rev-1", Generation: 1}) {
		t.Fatalf("WorkingReviews = %+v, %v, want rev-1 generation 1", targets, err)
	}
	if present, _, err := port.ObserveReviewVerdict(homeDir, "rev-1", 1); err != nil || present {
		t.Fatalf("ObserveReviewVerdict before the file = %v, %v, want absent", present, err)
	}
	writeGuardVerdictFile(t, homeDir, "rev-1", "t-ship", "pass")
	present, digest, err := port.ObserveReviewVerdict(homeDir, "rev-1", 1)
	if err != nil || !present || len(digest) != 64 {
		t.Fatalf("ObserveReviewVerdict after the file = %v, %q, %v, want present with a sha256 digest", present, digest, err)
	}
	if summary, err := port.RecordReviewVerdict(homeDir, "rev-1", 1, string(digest)); err != nil || !strings.Contains(summary, "recorded from rev-1") {
		t.Fatalf("RecordReviewVerdict = %q, %v", summary, err)
	}
	if _, err := port.RecordReviewVerdict(homeDir, "rev-1", 1, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "changed after it was observed") {
		t.Fatalf("RecordReviewVerdict with a stale digest = %v, want the changed-file refusal", err)
	}
}

// TestReviewTaskCountsAsInFlight: a review task is a soldier, so the guard and
// the root summary count it next to the ship task it reviews.
func TestReviewTaskCountsAsInFlight(t *testing.T) {
	homeDir := deliveryGuardShip(t, "t-ship")
	auth := cliCanonicalForHome(t, homeDir)
	req := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskIDFor(t, "r1"), Owner: "owner", Description: "review",
		Kind: taskauthority.KindReview, ReviewTaskID: "t-ship", ReviewHead: deliveryGuardHead, Reason: "test",
	}
	if _, err := auth.Create(mustCanonicalOp(t, "op-create-r1", req), req); err != nil {
		t.Fatal(err)
	}
	if n, err := guardInFlight(homeDir); err != nil || n != 2 {
		t.Fatalf("guardInFlight = %d, %v, want 2", n, err)
	}
	view, err := loadRootSummary(homeDir)
	if err != nil || view.inFlight != 2 {
		t.Fatalf("root summary in-flight = %d, %v, want 2", view.inFlight, err)
	}
}

// TestBriefScaffoldsAReviewTaskFromItsDefinition: brief reads the reviewed
// task and head from the review task's canonical definition and scaffolds the
// review brief, reporting the review kind.
func TestBriefScaffoldsAReviewTaskFromItsDefinition(t *testing.T) {
	homeDir := deliveryGuardShip(t, "t-ship")
	if err := config.StoreFleetBase(homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux", DefaultMode: "local-only"},
	}); err != nil {
		t.Fatal(err)
	}
	if out, err := runRoot(t, "project", "add", "demo-repo", t.TempDir(), "--home", homeDir); err != nil {
		t.Fatalf("project add: %v\n%s", err, out)
	}
	if out, err := runTaskCommand(t, []string{"task", "add", "r1", "review it", "--kind", "review", "--reviews", "t-ship", "--home", homeDir}); err != nil {
		t.Fatalf("task add: %v\n%s", err, out)
	}
	out, err := runRoot(t, "brief", "r1", "demo-repo", "--home", homeDir)
	if err != nil {
		t.Fatalf("brief: %v\n%s", err, out)
	}
	if !strings.Contains(out, "kind:  review") {
		t.Fatalf("brief output = %s, want kind review", out)
	}
	brief, err := os.ReadFile(fleet.Path(homeDir, "r1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), deliveryGuardHead) || !strings.Contains(string(brief), "t-ship") {
		t.Fatalf("review brief does not name the reviewed task and head:\n%s", brief)
	}
}
