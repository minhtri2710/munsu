package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// The head of the real git worktree the fixture binds: the one empty commit
// deliveryGuardWorktree makes with pinned author, committer and dates. The
// delivery identity, the PASS verdict and the provider observation all have
// to agree with what git reads there or the run fails closed before reaching
// the refusals under test.
const deliveryGuardHead = "cb98b60d3e83165203ce66ab856d97a4c275c030"

const deliveryGuardBase = "0000111122223333444455556666777788889999"

const deliveryGuardPRURL = "https://github.com/acme/widgets/pull/42"

const deliveryGuardMRURL = "https://gitlab.com/acme/widgets/-/merge_requests/42"

// deliveryGuardGitHub is the captured github forge step of a guard task. Its
// probe runs through PATH, so the absolute path only has to validate.
var deliveryGuardGitHub = taskauthority.DeliveryStep{Adapter: "github", Path: "/opt/gh-axi/bin/gh-axi", ProbeState: "ready"}

// deliveryGuardHome builds the exact state pr-merge requires before it can
// reach a committed non-completed outcome: a current working ship task that
// owns both bindings, with the bound worktree head the identity will carry,
// and a captured direct-PR contract whose forge is the given step.
// Anything less fails closed earlier, and the refusals under test sit after
// the outcome is committed.
func deliveryGuardWords() domain.Words {
	return domain.Words{Grantor: "human", Channel: "herdr", Quote: "merge it"}
}

// deliveryGuardWorktree makes the git worktree whose HEAD is deliveryGuardHead
// and whose status is clean.
func deliveryGuardWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(out)) != deliveryGuardHead {
		t.Fatalf("fixture worktree head = %q (%v), want %s", out, err, deliveryGuardHead)
	}
	return dir
}

func guardDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// workingGuardReview drives the review task revID of the working ship taskID
// to working over the clean deliveryGuardHead tree, and returns the review's
// endpoint incarnation.
func workingGuardReview(t *testing.T, auth *taskauthority.Canonical, taskID, revID string) string {
	t.Helper()
	rev, err := domain.NewTaskID(revID)
	if err != nil {
		t.Fatal(err)
	}
	commit := func(id string, intent domain.Intent, run func(domain.Operation) error) {
		t.Helper()
		if err := run(mustCanonicalOp(t, id+"-"+revID, intent)); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	create := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: rev, Owner: "owner", Description: "review", Kind: taskauthority.KindReview, ReviewTaskID: taskID, ReviewHead: deliveryGuardHead, Reason: "create"}
	commit("op-create", create, func(op domain.Operation) error { _, err := auth.Create(op, create); return err })
	begin := taskauthority.CanonicalBeginSpawnRequest{
		HomeID: auth.HomeID(), TaskID: rev, Precondition: domain.Of(1, 1), SnapshotDigest: guardDigest("snapshot:" + revID),
		Backend: "claude", Harness: "pi", Model: "opus", Effort: "high", Mode: "direct-PR", Kind: taskauthority.KindReview,
		Project: "proj", ParentTaskID: "parent", LaunchID: "launch-" + revID, WindowLabel: "window-" + revID,
		EndpointReservationID: "ep-res-" + revID, EndpointFenceToken: "ep-fence-" + revID, EndpointIncarnation: "inc-" + revID, Reason: "spawn",
	}
	commit("op-begin", begin, func(op domain.Operation) error { _, err := auth.BeginSpawn(op, begin); return err })
	attach := taskauthority.CanonicalAttachEndpointRequest{
		HomeID: auth.HomeID(), TaskID: rev, Precondition: domain.Of(1, 2), Backend: begin.Backend, Handle: "handle-" + revID,
		LeaseID: begin.EndpointReservationID, FenceToken: begin.EndpointFenceToken, SessionOwner: "owner",
		WorkspaceID: "ws", TabID: "tab", Incarnation: begin.EndpointIncarnation, Reason: "attach",
	}
	commit("op-attach", attach, func(op domain.Operation) error { _, err := auth.AttachEndpoint(op, attach); return err })
	tree := domain.TreeState{Head: deliveryGuardHead, Porcelain: guardDigest("")}
	record := taskauthority.CanonicalRecordLaunchRequest{
		HomeID: auth.HomeID(), TaskID: rev, Precondition: domain.Of(1, 3), LaunchID: begin.LaunchID,
		CommandDigest: guardDigest("launch:" + revID), ReviewTree: &tree, Reason: "record",
		Seat: taskauthority.LaunchSeat{Argv: []string{"pi", "--no-session"}, PromptDigest: guardDigest("prompt"), Fence: taskauthority.FenceRecord{Reason: "no fence on this host"}},
	}
	commit("op-record", record, func(op domain.Operation) error { _, err := auth.RecordLaunch(op, record); return err })
	bind := taskauthority.CanonicalBindEndpointRequest{
		HomeID: auth.HomeID(), TaskID: rev, Precondition: domain.Of(1, 4), Reason: "spawn",
		Binding: taskauthority.EndpointBinding{
			Backend: begin.Backend, Handle: "handle-" + revID, LeaseID: begin.EndpointReservationID, FenceToken: begin.EndpointFenceToken,
			SessionOwner: "owner", WorkspaceID: "ws", TabID: "tab", Incarnation: begin.EndpointIncarnation, BoundAtUnix: time.Now().Unix(),
		},
	}
	commit("op-bindep", bind, func(op domain.Operation) error { _, err := auth.BindEndpoint(op, bind); return err })
	return begin.EndpointIncarnation
}

// recordGuardPassVerdict drives a review task of taskID to working and records
// its PASS verdict for deliveryGuardHead on the reviewed task, which must be
// the working task with both bindings.
func recordGuardPassVerdict(t *testing.T, auth *taskauthority.Canonical, taskID string) {
	t.Helper()
	revID := "rev-" + taskID
	incarnation := workingGuardReview(t, auth, taskID, revID)
	ship, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	tree := domain.TreeState{Head: deliveryGuardHead, Porcelain: guardDigest("")}
	agg, err := auth.Get(ship)
	if err != nil {
		t.Fatal(err)
	}
	rv := taskauthority.CanonicalRecordReviewVerdictRequest{
		HomeID: auth.HomeID(), TaskID: ship, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Verdict: domain.ReviewVerdict{
			Outcome: domain.VerdictPass, HeadSHA: deliveryGuardHead, BaseSHA: deliveryGuardBase,
			ReviewerTask: revID, ReviewerGeneration: 1, ReviewerIncarnation: incarnation,
			Author: agg.Endpoint.Incarnation, Before: tree, After: tree,
		},
	}
	if _, err := auth.RecordReviewVerdict(mustCanonicalOp(t, "op-verdict-"+revID, rv), rv); err != nil {
		t.Fatalf("RecordReviewVerdict: %v", err)
	}
}

// deliveryGuardHome is deliveryGuardShip plus a recorded PASS verdict.
func deliveryGuardHome(t *testing.T, taskID string, forge taskauthority.DeliveryStep) string {
	t.Helper()
	homeDir := deliveryGuardShip(t, taskID)
	auth := cliCanonicalForHome(t, homeDir)
	recordGuardPassVerdict(t, auth, taskID)
	seedGuardDeliveryContract(t, auth, taskID, "direct-PR", forge)
	return homeDir
}

// deliveryGuardShip is a working ship task that owns a real git worktree and
// an endpoint, with no review yet.
func deliveryGuardShip(t *testing.T, taskID string) string {
	t.Helper()
	homeDir := t.TempDir()
	writeTaskMeta(t, homeDir, taskID, "ship")
	// pr-merge resolves the task home through the .meta projection, which the
	// canonical record does not write.
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"id": taskID, "kind": "ship"}); err != nil {
		t.Fatal(err)
	}

	auth := cliCanonicalForHome(t, homeDir)
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	worktree := deliveryGuardWorktree(t)
	bw := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding: taskauthority.WorktreeBinding{
			RepositoryIdentity: "repo-" + taskID,
			Path:               worktree,
			GitDir:             filepath.Join(worktree, ".git"),
			CommonDir:          "/repo/.git",
			BaseHead:           deliveryGuardHead,
			LeaseID:            "lease-wt-" + taskID,
			FenceToken:         "fence-wt-" + taskID,
			BoundAtUnix:        time.Now().Unix(),
		},
		Reason: "delivery guard fixture",
	}
	if _, err := auth.BindWorktree(mustCanonicalOp(t, "op-guard-bindwt-"+taskID, bw), bw); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}

	agg2, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	be := taskauthority.CanonicalBindEndpointRequest{
		HomeID:       auth.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg2.Generation), uint64(agg2.Revision)),
		Binding: taskauthority.EndpointBinding{
			Backend:      "tmux",
			Handle:       "@1",
			LeaseID:      "lease-ep-" + taskID,
			FenceToken:   "fence-ep-" + taskID,
			SessionOwner: "session-" + taskID,
			Incarnation:  "inc-" + taskID,
			BoundAtUnix:  time.Now().Unix(),
		},
		Reason: "delivery guard fixture",
	}
	if _, err := auth.BindEndpoint(mustCanonicalOp(t, "op-guard-bindep-"+taskID, be), be); err != nil {
		t.Fatalf("BindEndpoint(%s): %v", taskID, err)
	}

	// BindEndpoint is the transition into working: delivery requires a
	// working task, and there is no separate start on this path.
	if agg3, err := auth.Get(tid); err != nil {
		t.Fatal(err)
	} else if agg3.Phase != taskauthority.PhaseWorking {
		t.Fatalf("phase after binding = %s, want working", agg3.Phase)
	}
	// Another test in this package can leave homeOverride set, and it wins
	// over MUNSU_HOME; pin it explicitly rather than depending on run order.
	previous := homeOverride
	homeOverride = homeDir
	t.Cleanup(func() { homeOverride = previous })
	return homeDir
}

// stubDeliverySnapshot replaces the read-only identity capture. It is an
// exported package variable precisely so a caller outside internal/fleet can
// substitute it; the provider capability itself is still resolved for real
// and still shells out, which is what installTerminalGlab covers.
func stubDeliverySnapshot(t *testing.T) {
	t.Helper()
	old := fleet.FetchProviderSnapshot
	fleet.FetchProviderSnapshot = func(_ taskauthority.DeliveryStep, prURL string) (*fleet.ProviderSnapshot, error) {
		return &fleet.ProviderSnapshot{
			Provider:   "github",
			Owner:      "acme",
			Repo:       "widgets",
			Number:     42,
			URL:        prURL,
			BaseRef:    "main",
			HeadRef:    "feature",
			HeadSHA:    deliveryGuardHead,
			State:      "OPEN",
			Checks:     []domain.CheckRun{{Status: domain.CheckPassed}},
			Reviews:    []domain.Review{{State: domain.ReviewState("approved")}},
			ObservedAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	}
	t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
}

func TestBuildDeliverRequestStateGuard(t *testing.T) {
	failed := []domain.CheckRun{{Status: domain.CheckFailed}}
	passed := []domain.CheckRun{{Status: domain.CheckPassed}}
	changes := []domain.Review{{State: domain.ReviewChangesRequested}}
	for _, tc := range []struct {
		name    string
		state   string
		checks  []domain.CheckRun
		reviews []domain.Review
		wantErr string
	}{
		{name: "open failed check", state: "OPEN", checks: failed, wantErr: "delivery provider state is not mergeable"},
		{name: "open changes requested", state: "OPEN", checks: passed, reviews: changes, wantErr: "delivery provider state is not mergeable"},
		{name: "open passing without provider review", state: "OPEN", checks: passed},
		{name: "merged terminal", state: "MERGED"},
		{name: "closed terminal", state: "CLOSED"},
		{name: "unknown", state: "UNKNOWN", wantErr: "delivery provider state is unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskID := "t-state-" + strings.ReplaceAll(strings.ToLower(tc.name), " ", "-")
			homeDir := deliveryGuardHome(t, taskID, deliveryGuardGitHub)
			auth := cliCanonicalForHome(t, homeDir)
			old := fleet.FetchProviderSnapshot
			fleet.FetchProviderSnapshot = func(_ taskauthority.DeliveryStep, prURL string) (*fleet.ProviderSnapshot, error) {
				return &fleet.ProviderSnapshot{Provider: "github", Owner: "acme", Repo: "widgets", Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature", HeadSHA: deliveryGuardHead, State: tc.state, Checks: tc.checks, Reviews: tc.reviews, ObservedAt: time.Now().UTC().Format(time.RFC3339)}, nil
			}
			t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
			_, err := buildDeliverRequest(auth, taskID, deliveryGuardPRURL, nil, deliveryGuardWords())
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestBuildDeliverRequestRefusesIncompleteWords: the words are checked before
// any provider read or task lookup.
func TestBuildDeliverRequestRefusesIncompleteWords(t *testing.T) {
	old := fleet.FetchProviderSnapshot
	fleet.FetchProviderSnapshot = func(_ taskauthority.DeliveryStep, _ string) (*fleet.ProviderSnapshot, error) {
		t.Fatal("the provider was read before the words were checked")
		return nil, nil
	}
	t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
	for _, words := range []domain.Words{
		{Channel: "herdr", Quote: "merge it"},
		{Grantor: "human", Quote: "merge it"},
		{Grantor: "human", Channel: "herdr"},
	} {
		_, err := buildDeliverRequest(nil, "t1", deliveryGuardPRURL, nil, words)
		if err == nil || !strings.Contains(err.Error(), "words:") {
			t.Fatalf("words %+v: error = %v, want a words refusal", words, err)
		}
	}
}

// installTerminalGlab puts a fake glab on PATH that passes the capability
// probe and reports the MR in a terminal state. Any merge call touches the
// returned marker and fails, so a test can prove no merge was attempted.
func installTerminalGlab(t *testing.T, state string) (glab, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "merge-attempt")
	markerTarget := "'" + strings.ReplaceAll(filepath.ToSlash(marker), "'", "'\\''") + "'"
	mergeCommit := "null"
	if state == "merged" {
		mergeCommit = `"0123456789abcdef0123456789abcdef01234567"`
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
"--version "*) echo "glab 1.0.0" ;;
"auth status") echo "Logged in: authenticated" ;;
"api --help") echo "glab api" ;;
"api /projects/acme%%2Fwidgets/merge_requests/42")
  if [ "$3" = "--method" ]; then touch %s; exit 1; fi
  printf '{"state":"%s","sha":"%s","merge_commit_sha":%s,"target_branch":"main"}'
  ;;
*) exit 1 ;;
esac
`, markerTarget, state, deliveryGuardHead, mergeCommit)
	glab = testutil.WriteFakeExecutable(t, filepath.Join(dir, "glab"), script)
	testutil.PrependPath(t, dir)
	return glab, marker
}

// deliveryGuardGitLab is the captured gitlab forge step pointing at the fake glab.
func deliveryGuardGitLab(glab string) taskauthority.DeliveryStep {
	return taskauthority.DeliveryStep{Adapter: "gitlab", Path: glab, ProbeState: "ready"}
}

func stubGitLabTerminalSnapshot(t *testing.T, state string) {
	t.Helper()
	old := fleet.FetchProviderSnapshot
	fleet.FetchProviderSnapshot = func(_ taskauthority.DeliveryStep, prURL string) (*fleet.ProviderSnapshot, error) {
		return &fleet.ProviderSnapshot{Provider: "gitlab", Owner: "acme", Repo: "widgets", Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature", HeadSHA: deliveryGuardHead, State: state, ObservedAt: time.Now().UTC().Format(time.RFC3339)}, nil
	}
	t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
}

// runPRMerge runs pr-merge with the delivery words flags the command requires.
func runPRMerge(t *testing.T, flags []string, args ...string) error {
	t.Helper()
	words := deliveryGuardWords()
	cmd := newPRMergeCmd()
	cmd.SetArgs(append(append([]string{"--grantor", words.Grantor, "--channel", words.Channel, "--quote", words.Quote}, flags...), args...))
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return cmd.Execute()
}

func TestPRMergeAllowsMergedTerminalReconciliation(t *testing.T) {
	glab, marker := installTerminalGlab(t, "merged")
	stubGitLabTerminalSnapshot(t, "MERGED")
	deliveryGuardHome(t, "t-prmerge-terminal-merged", deliveryGuardGitLab(glab))
	if err := runPRMerge(t, nil, "t-prmerge-terminal-merged", deliveryGuardMRURL); err != nil {
		t.Fatalf("pr-merge: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("merge mutation attempted for merged terminal state")
	}
}

func TestPRMergeReportsClosedTerminalReconciliation(t *testing.T) {
	glab, marker := installTerminalGlab(t, "closed")
	stubGitLabTerminalSnapshot(t, "CLOSED")
	deliveryGuardHome(t, "t-prmerge-terminal-closed", deliveryGuardGitLab(glab))
	err := runPRMerge(t, nil, "t-prmerge-terminal-closed", deliveryGuardMRURL)
	if err == nil || !strings.Contains(err.Error(), "delivery did not complete") {
		t.Fatalf("error = %v, want partial delivery refusal", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("merge mutation attempted for closed terminal state")
	}
}

// TestPRMergeRefusesGitHubWithoutCapability proves pr-merge on a GitHub PR
// fails closed on the missing gh-axi/gh capability before any delivery journal
// is written: there is no fallback execution route.
func TestPRMergeRefusesGitHubWithoutCapability(t *testing.T) {
	stubDeliverySnapshot(t)
	homeDir := deliveryGuardHome(t, "t-prmerge-github", deliveryGuardGitHub)
	t.Setenv("PATH", t.TempDir())
	err := runPRMerge(t, nil, "t-prmerge-github", deliveryGuardPRURL)
	if err == nil || !strings.Contains(err.Error(), "configured forge is not Ready") {
		t.Fatalf("error = %v, want the GitHub capability refusal", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", ".delivery-journal")); !os.IsNotExist(err) {
		t.Fatalf("delivery journal state stat err = %v, want no journal written", err)
	}
}

// TestPRMergeTeardownReportsClosedWithoutRetiring proves pr-merge --teardown
// on a CLOSED GitLab MR reports the non-completed merge-and-retire outcome
// and retires nothing: teardown runs only after a completed outcome.
func TestPRMergeTeardownReportsClosedWithoutRetiring(t *testing.T) {
	glab, marker := installTerminalGlab(t, "closed")
	stubGitLabTerminalSnapshot(t, "CLOSED")
	taskID := "t-prmerge-teardown-closed"
	homeDir := deliveryGuardHome(t, taskID, deliveryGuardGitLab(glab))

	err := runPRMerge(t, []string{"--teardown"}, taskID, deliveryGuardMRURL)
	if err == nil || !strings.Contains(err.Error(), "merge-and-retire "+taskID+": partial provider reports closed but not merged") {
		t.Fatalf("error = %v, want the provider closed-but-not-merged outcome (reached only when the words were carried into the delivery)", err)
	}
	if strings.Contains(err.Error(), "post-merge teardown") {
		t.Fatalf("error = %v, want no teardown attempted", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("merge mutation attempted for closed terminal state")
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		t.Fatal(err)
	}
	agg, err := cliCanonicalForHome(t, homeDir).Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase == taskauthority.PhaseRetired || agg.Worktree == nil || agg.Endpoint == nil {
		t.Fatalf("task phase=%s worktree=%v endpoint=%v, want not retired with bindings intact", agg.Phase, agg.Worktree, agg.Endpoint)
	}
	if _, err := home.ReadMeta(homeDir, taskID); err != nil {
		t.Fatalf("task meta after refused merge-and-retire: %v, want kept", err)
	}
}

// TestBuildDeliverRequestRefusesUncontractedTask pins the refusal for a task
// that was never spawned: it has no captured forge step to deliver through.
func TestBuildDeliverRequestRefusesUncontractedTask(t *testing.T) {
	taskID := "t-uncontracted"
	homeDir := deliveryGuardShip(t, taskID)
	auth := cliCanonicalForHome(t, homeDir)
	_, err := buildDeliverRequest(auth, taskID, deliveryGuardPRURL, nil, deliveryGuardWords())
	if err == nil || !strings.Contains(err.Error(), "has no captured delivery contract") {
		t.Fatalf("buildDeliverRequest = %v, want a missing-contract refusal", err)
	}
}
