package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// The bound worktree head the fixture pins. The delivery identity, the
// canonical binding and the provider observation all have to agree on it or
// the run fails closed before reaching the refusals under test.
const deliveryGuardHead = "1111111111111111111111111111111111111111"

const deliveryGuardPRURL = "https://github.com/acme/widgets/pull/42"

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
			Head:               deliveryGuardHead,
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
// and still shells out, which is what installStuckOpenGhAxi covers.
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
			Reviews:    []domain.Review{{State: domain.ReviewApproved}},
			ObservedAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	}
	t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
}

// installStuckOpenGhAxi puts a gh-axi on PATH that reports the pull request
// open both before and after an apparently successful merge. That is the
// retryable outcome: the mutation was attempted, the provider does not show
// it, and Deliver commits a non-completed outcome and returns no error --
// so the refusal the caller must make is on the result, not on an error.
func installStuckOpenGhAxi(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake provider is a shell script; the delivery refusals are covered on the unix lanes")
	}
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
api)
  if [ "$2" != "graphql" ]; then
    exit 1
  fi
  printf '{"data":{"repository":{"pullRequest":{"state":"OPEN","headRefOid":"%s","baseRefName":"main","merged":false,"reviewDecision":"APPROVED","mergeable":"MERGEABLE","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}}}}\n'
  ;;
pr)
  exit 0
  ;;
*)
  exit 1
  ;;
esac
`, deliveryGuardHead)
	path := filepath.Join(dir, "gh-axi")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestPRMergeRefusesADeliveryThatCommittedANonCompletedOutcome enters the
// `result.IsError()` refusal in newPRMergeCmd. Deliver returns a nil error
// here -- the journaled delivery ran to a committed outcome -- so a caller
// that only checked the error would report a merge that did not happen.
func TestPRMergeRefusesADeliveryThatCommittedANonCompletedOutcome(t *testing.T) {
	installStuckOpenGhAxi(t)
	stubDeliverySnapshot(t)
	deliveryGuardHome(t, "t-prmerge")

	cmd := newPRMergeCmd()
	err := cmd.RunE(cmd, []string{"t-prmerge", deliveryGuardPRURL})
	if err == nil {
		t.Fatal("pr-merge returned nil for a delivery that did not complete")
	}
	if !strings.Contains(err.Error(), "delivery did not complete") {
		t.Fatalf("error = %v, want the non-completed delivery refusal", err)
	}
	if !strings.Contains(err.Error(), string(taskauthority.DeliveryOutcomeRetryable)) {
		t.Fatalf("error = %v, want the committed outcome named in the refusal", err)
	}
}

// TestPRMergeTeardownRefusesAMergeAndRetireThatDidNotComplete enters the
// `mars.IsError()` refusal on the --teardown branch. Retirement must not be
// resumed off a delivery that never reached a completed outcome.
func TestPRMergeTeardownRefusesAMergeAndRetireThatDidNotComplete(t *testing.T) {
	installStuckOpenGhAxi(t)
	stubDeliverySnapshot(t)
	deliveryGuardHome(t, "t-prmergetd")

	cmd := newPRMergeCmd()
	if err := cmd.Flags().Set("teardown", "true"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, []string{"t-prmergetd", deliveryGuardPRURL})
	if err == nil {
		t.Fatal("pr-merge --teardown returned nil for a merge-and-retire that did not complete")
	}
	if !strings.Contains(err.Error(), "merge-and-retire") {
		t.Fatalf("error = %v, want the merge-and-retire refusal", err)
	}
}
