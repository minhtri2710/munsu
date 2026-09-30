package cli

import (
	"fmt"
	"os"
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

// The bound worktree head the fixture pins. The delivery identity, the
// canonical binding and the provider observation all have to agree on it or
// the run fails closed before reaching the refusals under test.
const deliveryGuardHead = "1111111111111111111111111111111111111111"

const deliveryGuardPRURL = "https://github.com/acme/widgets/pull/42"

const deliveryGuardMRURL = "https://gitlab.com/acme/widgets/-/merge_requests/42"

// deliveryGuardHome builds the exact state pr-merge requires before it can
// reach a committed non-completed outcome: a current working ship task that
// owns both bindings, with the bound worktree head the identity will carry.
// Anything less fails closed earlier, and the refusals under test sit after
// the outcome is committed.
func deliveryGuardHome(t *testing.T, taskID string) string {
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
	bw := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding: taskauthority.WorktreeBinding{
			RepositoryIdentity: "repo-" + taskID,
			Path:               filepath.Join("/worktrees", taskID),
			GitDir:             filepath.Join("/worktrees", taskID, ".git"),
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
	fleet.FetchProviderSnapshot = func(prURL string) (*fleet.ProviderSnapshot, error) {
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
	for _, tc := range []struct {
		name      string
		state     string
		mergeable bool
		wantErr   bool
	}{
		{name: "open incomplete", state: "OPEN", mergeable: false, wantErr: true},
		{name: "merged terminal", state: "MERGED", wantErr: false},
		{name: "closed terminal", state: "CLOSED", wantErr: false},
		{name: "unknown", state: "UNKNOWN", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskID := "t-state-" + strings.ReplaceAll(strings.ToLower(tc.name), " ", "-")
			homeDir := deliveryGuardHome(t, taskID)
			auth := cliCanonicalForHome(t, homeDir)
			old := fleet.FetchProviderSnapshot
			fleet.FetchProviderSnapshot = func(prURL string) (*fleet.ProviderSnapshot, error) {
				snapshot := &fleet.ProviderSnapshot{Provider: "github", Owner: "acme", Repo: "widgets", Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature", HeadSHA: deliveryGuardHead, State: tc.state, ObservedAt: time.Now().UTC().Format(time.RFC3339)}
				if tc.mergeable {
					snapshot.Checks = []domain.CheckRun{{Status: domain.CheckPassed}}
					snapshot.Reviews = []domain.Review{{State: domain.ReviewState("approved")}}
				}
				return snapshot, nil
			}
			t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
			_, err := buildDeliverRequest(auth, taskID, deliveryGuardPRURL, nil, domain.Words{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

// installTerminalGlab puts a fake glab on PATH that passes the capability
// probe and reports the MR in a terminal state. Any merge call touches the
// returned marker and fails, so a test can prove no merge was attempted.
func installTerminalGlab(t *testing.T, state string) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "merge-attempt")
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
	testutil.WriteFakeExecutable(t, filepath.Join(dir, "glab"), script)
	testutil.PrependPath(t, dir)
	return marker
}

func stubGitLabTerminalSnapshot(t *testing.T, state string) {
	t.Helper()
	old := fleet.FetchProviderSnapshot
	fleet.FetchProviderSnapshot = func(prURL string) (*fleet.ProviderSnapshot, error) {
		return &fleet.ProviderSnapshot{Provider: "gitlab", Owner: "acme", Repo: "widgets", Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature", HeadSHA: deliveryGuardHead, State: state, ObservedAt: time.Now().UTC().Format(time.RFC3339)}, nil
	}
	t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
}

func TestPRMergeAllowsMergedTerminalReconciliation(t *testing.T) {
	marker := installTerminalGlab(t, "merged")
	stubGitLabTerminalSnapshot(t, "MERGED")
	deliveryGuardHome(t, "t-prmerge-terminal-merged")
	if err := newPRMergeCmd().RunE(nil, []string{"t-prmerge-terminal-merged", deliveryGuardMRURL}); err != nil {
		t.Fatalf("pr-merge: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("merge mutation attempted for merged terminal state")
	}
}

func TestPRMergeReportsClosedTerminalReconciliation(t *testing.T) {
	marker := installTerminalGlab(t, "closed")
	stubGitLabTerminalSnapshot(t, "CLOSED")
	deliveryGuardHome(t, "t-prmerge-terminal-closed")
	err := newPRMergeCmd().RunE(nil, []string{"t-prmerge-terminal-closed", deliveryGuardMRURL})
	if err == nil || !strings.Contains(err.Error(), "delivery did not complete") {
		t.Fatalf("error = %v, want partial delivery refusal", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("merge mutation attempted for closed terminal state")
	}
}

// TestPRMergeRefusesGitHubDelivery proves pr-merge refuses a GitHub PR as
// unsupported before any delivery journal is written.
func TestPRMergeRefusesGitHubDelivery(t *testing.T) {
	stubDeliverySnapshot(t)
	homeDir := deliveryGuardHome(t, "t-prmerge-github")
	err := newPRMergeCmd().RunE(nil, []string{"t-prmerge-github", deliveryGuardPRURL})
	if err == nil || !strings.Contains(err.Error(), "GitHub delivery is unsupported") {
		t.Fatalf("error = %v, want GitHub delivery refused", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", ".delivery-journal")); !os.IsNotExist(err) {
		t.Fatalf("delivery journal state stat err = %v, want no journal written", err)
	}
}

// TestPRMergeTeardownReportsClosedWithoutRetiring proves pr-merge --teardown
// on a CLOSED GitLab MR reports the non-completed merge-and-retire outcome
// and retires nothing: teardown runs only after a completed outcome.
func TestPRMergeTeardownReportsClosedWithoutRetiring(t *testing.T) {
	marker := installTerminalGlab(t, "closed")
	stubGitLabTerminalSnapshot(t, "CLOSED")
	taskID := "t-prmerge-teardown-closed"
	homeDir := deliveryGuardHome(t, taskID)

	cmd := newPRMergeCmd()
	if err := cmd.Flags().Set("teardown", "true"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, []string{taskID, deliveryGuardMRURL})
	if err == nil || !strings.Contains(err.Error(), "merge-and-retire "+taskID+":") {
		t.Fatalf("error = %v, want the merge-and-retire refusal", err)
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
