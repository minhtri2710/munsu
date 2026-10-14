//go:build integration

package fleet

import (
	"errors"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestMergeStatus_MissingIdentityIsUnverifiable pins the retained read-only
// provider-neutral status seam: a task without a delivery identity is
// classified unverifiable (exit 2), never merged.
func TestMergeStatus_MissingIdentityIsUnverifiable(t *testing.T) {
	err := MergeStatus(t.TempDir(), "missing-task")
	var statusErr *MergeStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("MergeStatus error = %T %v, want *MergeStatusError", err, err)
	}
	if !statusErr.Unverifiable {
		t.Fatalf("MergeStatus error = %v, want unverifiable classification", err)
	}
}

const mergeStatusMergeCommit = "0123456789abcdef0123456789abcdef01234567"

func mergeStatusIdentity() domain.DeliveryIdentity {
	return domain.DeliveryIdentity{
		Provider:   "github",
		Owner:      "owner",
		Repo:       "repo",
		Number:     7,
		URL:        "https://github.com/owner/repo/pull/7",
		BaseRef:    deliveryTestBase,
		HeadRef:    "feature/delivery",
		HeadSHA:    deliveryTestHead,
		CapturedAt: "2026-08-05T00:00:00Z",
	}
}

// inFlightGitHubDelivery leaves t1 as pr-merge does between authorizing and
// merging: a delivery authorization, no outcome, and no pr_* keys in .meta.
func inFlightGitHubDelivery(t *testing.T) string {
	t.Helper()
	c, homeDir := newFleetCanonical(t)
	mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
	agg, err := c.Get(mustFleetTaskID(t, "t1"))
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalDeliveryAuthorizationRequest{
		HomeID:        c.HomeID(),
		TaskID:        mustFleetTaskID(t, "t1"),
		Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Kind:          taskauthority.DeliveryAuthorizationProviderMerge,
		Identity:      mergeStatusIdentity(),
		Preconditions: []taskauthority.DeliveryPrecondition{taskauthority.DeliveryPreconditionPRMergeable},
		Words:         deliveryWords(),
	}
	if _, err := c.AuthorizeDelivery(mustFleetOperation(t, "op-inflight-t1", req), req); err != nil {
		t.Fatalf("AuthorizeDelivery: %v", err)
	}
	return homeDir
}

// TestMergeStatusReadsTheCanonicalDeliveryIdentity pins merge-status to the
// identity of the task's current delivery authorization: it answers while a
// delivery is in flight, before any pr_* projection exists, and decides
// "merged" through classifyLanding.
func TestMergeStatusReadsTheCanonicalDeliveryIdentity(t *testing.T) {
	cases := []struct {
		name         string
		authorized   bool
		reply        string
		wantErr      string
		unverifiable bool
	}{
		{name: "in-flight delivery reports merged", authorized: true, reply: `{"state":"MERGED","headRefOid":"` + deliveryTestHead + `","mergeCommit":{"oid":"` + mergeStatusMergeCommit + `"}}`},
		{name: "open delivery is not merged", authorized: true, reply: `{"state":"OPEN","headRefOid":"` + deliveryTestHead + `"}`, wantErr: "MR 7 is open and not merged"},
		{name: "closed delivery is not merged", authorized: true, reply: `{"state":"CLOSED","headRefOid":"` + deliveryTestHead + `"}`, wantErr: "MR 7 is closed but not merged"},
		{name: "merged at another head is unverifiable", authorized: true, reply: `{"state":"MERGED","headRefOid":"` + strings.Repeat("c", 40) + `","mergeCommit":{"oid":"` + mergeStatusMergeCommit + `"}}`, wantErr: "PR head SHA mismatch", unverifiable: true},
		{name: "no authorization is unverifiable", wantErr: "t1 has no delivery authorization", unverifiable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var homeDir string
			if tc.authorized {
				homeDir = inFlightGitHubDelivery(t)
			} else {
				c, dir := newFleetCanonical(t)
				mustWorkingDeliveryTask(t, c, "t1", deliveryTestGitHubForge)
				homeDir = dir
			}
			gh := installFakeGH(t, ghReply{match: "pr view 7 --repo owner/repo", stdout: tc.reply})
			err := MergeStatus(homeDir, "t1")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("MergeStatus = %v, want merged", err)
				}
				if calls := gh.calls(t); len(calls) == 0 {
					t.Fatal("MergeStatus answered without reading the provider")
				}
				return
			}
			var statusErr *MergeStatusError
			if !errors.As(err, &statusErr) || !strings.Contains(err.Error(), tc.wantErr) || statusErr.Unverifiable != tc.unverifiable {
				t.Fatalf("MergeStatus = %v, want %q with unverifiable=%v", err, tc.wantErr, tc.unverifiable)
			}
		})
	}
}
