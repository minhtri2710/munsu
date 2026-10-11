//go:build integration

package fleet

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestDeliverPrevalidatesTheTaskBeforeAnyIntent is the table of the
// pre-journal refusals of Deliver. A block row proves the refusal names its own
// reason and that no merge, journal record or authorization follows; the allow
// row is the nearest valid delivery. The task-state, hold and delivery-index
// conditions are owned by Canonical.DeliveryAuthorizationBlocker; the hold row
// pins that Deliver consumes it.
func TestDeliverPrevalidatesTheTaskBeforeAnyIntent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, c *taskauthority.Canonical, taskID, wt string)
		head    string
		wantErr string
		wantIs  error
	}{
		{
			name: "a matching delivery hold blocks",
			setup: func(t *testing.T, c *taskauthority.Canonical, taskID, _ string) {
				hold := taskauthority.CanonicalAddHoldRequest{
					HomeID: c.HomeID(), HoldID: "delivery-hold",
					Scope:   taskauthority.DispatchHoldScope{TaskIDs: []string{taskID}},
					Actions: []taskauthority.DispatchAction{taskauthority.DispatchActionDelivery},
					Reason:  "freeze delivery",
				}
				if _, err := c.AddHold(mustFleetOperation(t, "op-add-delivery-hold", hold), hold); err != nil {
					t.Fatal(err)
				}
			},
			wantIs: taskauthority.ErrDispatchHeld,
		},
		{
			name:    "an identity head that differs from the bound worktree head blocks",
			head:    "9999888877776666555544443333222211110000",
			wantErr: "the bound worktree holds now",
		},
		{
			name: "a bound worktree git cannot read blocks",
			setup: func(t *testing.T, _ *taskauthority.Canonical, _, wt string) {
				if err := os.RemoveAll(wt); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "delivery of task t1: observing worktree HEAD",
		},
		{name: "a free task whose identity head is the worktree head delivers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, homeDir := newFleetCanonical(t)
			taskID := "t1"
			wt := mustWorkingShipTask(t, c, taskID)
			mustRecordApprovingVerdict(t, c, taskID, deliveryTestHead)
			seedSourceContract(t, c, taskID, deliveryTestGitHubForge)
			provider := installScriptedProviderFor(t, "open-then-merged")
			if tc.setup != nil {
				tc.setup(t, c, taskID, wt)
			}
			req := deliverRequest()
			if tc.head != "" {
				req.Identity.HeadSHA = tc.head
			}

			_, err := Deliver(homeDir, taskID, req)
			if tc.wantErr == "" && tc.wantIs == nil {
				if err != nil || provider.merges != 1 {
					t.Fatalf("Deliver = %v with %d merges, want one merge", err, provider.merges)
				}
				return
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Deliver err = %v, want %v %q", err, tc.wantIs, tc.wantErr)
			}
			if provider.merges != 0 {
				t.Fatalf("merges = %d, want 0", provider.merges)
			}
			if active := listActiveDeliveryJournals(t, homeDir); len(active) != 0 {
				t.Fatalf("active journals = %v, want none (no intent for an unauthorizable task)", active)
			}
			if files := listDeliveryJournalFiles(t, homeDir); len(files) != 0 {
				t.Fatalf("journal records = %v, want none", files)
			}
			if cur, err := c.DeliveryCurrency(mustFleetTaskID(t, taskID)); err != nil || cur.Authorization != nil {
				t.Fatalf("authorization currency = %+v, %v, want none issued", cur, err)
			}
		})
	}
}
