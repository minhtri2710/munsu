package taskauthorityfs

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// confirmSpawnBinding builds a valid endpoint binding payload for the
// ConfirmSpawn crash matrix.
func confirmSpawnBinding() taskauthority.EndpointBinding {
	return taskauthority.EndpointBinding{
		Backend:      "herdr",
		Handle:       "session:pane-1",
		LeaseID:      "lease-1",
		FenceToken:   "fence-1",
		SessionOwner: "session",
		WorkspaceID:  "workspace-1",
		TabID:        "tab-1",
		BoundAtUnix:  time.Now().Unix(),
	}
}

// TestConfirmSpawnCrashRecoveryLeavesTaskQueuedOrWorking proves the atomic
// endpoint-binding + working transition recovers deterministically: a crash
// before the pending manifest leaves the task queued with no endpoint
// binding, and a crash at every later stage converges to the fully committed
// working transition when a fresh Store reads the home. This is the durable
// form of "failed endpoint persistence leaves the task non-working".
func TestConfirmSpawnCrashRecoveryLeavesTaskQueuedOrWorking(t *testing.T) {
	cases := []struct {
		name       string
		stage      faultStage
		afterWrite int
	}{
		{"before manifest", faultStageBeforeManifest, 0},
		{"after pending manifest", faultStageAfterManifest, 0},
		{"after aggregate data write", faultStageAfterDataWrite, 1},
		{"after current pointer write", faultStageAfterDataWrite, 2},
		{"before commit marker", faultStageBeforeCommit, 0},
		{"after commit marker", faultStageAfterCommit, 0},
		{"during cleanup", faultStageDuringCleanup, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			s := openStore(t, home)
			a := taskauthority.New(s)
			if _, err := a.Create(taskauthority.CreateRequest{
				OperationID: "op-create", Actor: taskauthority.Actor{ID: "general", Rank: "general"},
				TaskID: "t1", Owner: "owner", Description: "work", Kind: "ship", Project: "proj",
				Reason: "create",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.BindWorktree(taskauthority.BindWorktreeRequest{
				OperationID: "op-bind-wt", Actor: taskauthority.Actor{ID: "general", Rank: "general"},
				TaskID: "t1", ExpectedGeneration: 1,
				Binding: testWorktreeBinding("wt-lease-1", "wt-fence-1"),
				Reason:  "spawn",
			}); err != nil {
				t.Fatal(err)
			}

			s.fault = &faultInjector{stage: tc.stage, afterWrite: tc.afterWrite, err: errInjected}
			_, err := a.ConfirmSpawn(taskauthority.ConfirmSpawnRequest{
				OperationID: "op-confirm", Actor: taskauthority.Actor{ID: "general", Rank: "general"},
				TaskID: "t1", ExpectedGeneration: 1,
				Binding: confirmSpawnBinding(),
				Reason:  "spawned",
			})
			if err == nil {
				// The fault never fired (a data-write index beyond the entry
				// count); the transaction committed normally.
				assertCommittedConfirmSpawn(t, openStore(t, home), "t1")
				return
			}
			if !errors.Is(err, errInjected) {
				t.Fatalf("ConfirmSpawn error = %v, want injected crash", err)
			}

			s3 := openStore(t, home)
			if tc.stage == faultStageBeforeManifest {
				v := mustView(t, s3)
				cur, _ := v.Current("t1")
				if cur.Phase != taskauthority.PhaseQueued || cur.Endpoint != nil || cur.Revision != 2 {
					t.Fatalf("before-manifest crash must leave the task queued with no endpoint binding: %+v", cur)
				}
				return
			}
			assertCommittedConfirmSpawn(t, s3, "t1")
			assertNoManifests(t, home)

			// Recovery is idempotent: a second view sees identical state.
			first := mustView(t, s3)
			second := mustView(t, s3)
			if !reflect.DeepEqual(first, second) {
				t.Fatal("repeated views after recovery differ")
			}
		})
	}
}

// assertCommittedConfirmSpawn asserts a fully committed ConfirmSpawn
// transaction: the current aggregate is working with the endpoint binding at
// revision 3, and the durable receipt replays the original outcome.
func assertCommittedConfirmSpawn(t *testing.T, s *Store, taskID string) {
	t.Helper()
	v := mustView(t, s)
	cur, ok := v.Current(taskID)
	if !ok || cur.Phase != taskauthority.PhaseWorking || cur.Endpoint == nil ||
		cur.Endpoint.Backend != "herdr" || cur.Endpoint.Handle != "session:pane-1" ||
		cur.Endpoint.LeaseID != "lease-1" || cur.Endpoint.FenceToken != "fence-1" ||
		cur.Revision != 3 {
		t.Fatalf("current aggregate after confirm spawn = %+v", cur)
	}
	if cur.Worktree == nil {
		t.Fatal("worktree binding must survive confirm spawn")
	}
	receipt, ok := findReceipt(v.Receipts, "op-confirm")
	if !ok || receipt.Digest == "" || receipt.TaskID != taskID || receipt.Revision != 3 || receipt.Phase != taskauthority.PhaseWorking {
		t.Fatalf("confirm receipt = %+v", receipt)
	}
	var auditFound bool
	for _, ev := range v.Audit {
		if ev.OperationID == "op-confirm" && ev.Kind == taskauthority.AuditLifecycle &&
			ev.TaskID == taskID && ev.Before == taskauthority.PhaseQueued && ev.After == taskauthority.PhaseWorking {
			auditFound = true
		}
	}
	if !auditFound {
		t.Fatalf("confirm audit event missing: %+v", v.Audit)
	}
}
