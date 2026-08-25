package cli

import (
	"fmt"
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

func installTerminalGhAxi(t *testing.T, state string, merged bool) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "merge-attempt")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
api)
  if [ "$2" != "graphql" ]; then exit 1; fi
  printf '{"data":{"repository":{"pullRequest":{"state":"%s","headRefOid":"%s","baseRefName":"main","merged":%t,"mergeCommit":{"oid":"merge123"}}}}}\n'
  ;;
pr)
  touch %q
  exit 1
  ;;
*) exit 1 ;;
esac
`, state, deliveryGuardHead, merged, marker)
	path := filepath.Join(dir, "gh-axi")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

// installStuckOpenGhAxi is retained for legacy command fixtures.
func installStuckOpenGhAxi(t *testing.T) {
	t.Helper()
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
	testutil.WriteFakeExecutable(t, path, script)
	testutil.PrependPath(t, dir)
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
					snapshot.Reviews = []domain.Review{{State: domain.ReviewApproved}}
				}
				return snapshot, nil
			}
			t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
			_, err := buildDeliverRequest(auth, taskID, deliveryGuardPRURL, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

func stubTerminalDeliverySnapshot(t *testing.T, state string) {
	t.Helper()
	old := fleet.FetchProviderSnapshot
	fleet.FetchProviderSnapshot = func(prURL string) (*fleet.ProviderSnapshot, error) {
		return &fleet.ProviderSnapshot{Provider: "github", Owner: "acme", Repo: "widgets", Number: 42, URL: prURL, BaseRef: "main", HeadRef: "feature", HeadSHA: deliveryGuardHead, State: state, ObservedAt: time.Now().UTC().Format(time.RFC3339)}, nil
	}
	t.Cleanup(func() { fleet.FetchProviderSnapshot = old })
}

func TestPRMergeAllowsMergedTerminalReconciliation(t *testing.T) {
	marker := installTerminalGhAxi(t, "CLOSED", true)
	stubTerminalDeliverySnapshot(t, "MERGED")
	deliveryGuardHome(t, "t-prmerge-terminal-merged")
	if err := newPRMergeCmd().RunE(nil, []string{"t-prmerge-terminal-merged", deliveryGuardPRURL}); err != nil {
		t.Fatalf("pr-merge: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("merge mutation attempted for merged terminal state")
	}
}

func TestPRMergeReportsClosedTerminalReconciliation(t *testing.T) {
	marker := installTerminalGhAxi(t, "CLOSED", false)
	stubTerminalDeliverySnapshot(t, "CLOSED")
	deliveryGuardHome(t, "t-prmerge-terminal-closed")
	err := newPRMergeCmd().RunE(nil, []string{"t-prmerge-terminal-closed", deliveryGuardPRURL})
	if err == nil || !strings.Contains(err.Error(), "delivery did not complete") {
		t.Fatalf("error = %v, want partial delivery refusal", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("merge mutation attempted for closed terminal state")
	}
}

func TestPRMergeRefusesUnenforceableOpenDelivery(t *testing.T) {
	installStuckOpenGhAxi(t)
	stubDeliverySnapshot(t)
	deliveryGuardHome(t, "t-prmerge")

	cmd := newPRMergeCmd()
	err := cmd.RunE(cmd, []string{"t-prmerge", deliveryGuardPRURL})
	if err == nil {
		t.Fatal("pr-merge returned nil for a delivery that did not complete")
	}
	if !strings.Contains(err.Error(), "cannot atomically enforce") {
		t.Fatalf("error = %v, want atomic-constraint refusal", err)
	}
}

func TestPRMergeTeardownRefusesUnenforceableOpenDelivery(t *testing.T) {
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
	if !strings.Contains(err.Error(), "cannot atomically enforce") {
		t.Fatalf("error = %v, want atomic-constraint refusal", err)
	}
}
