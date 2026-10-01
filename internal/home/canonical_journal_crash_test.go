package home_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	ta "github.com/minhtri2710/munsu/internal/taskauthority"
)

// These tests crash real taskauthority operations inside home.Commit: the
// operation builds and journals its own change-set, the process "dies" after
// the record is durable and k of its n+2 apply steps have run (n item writes,
// the revision write, the record removal), and the reopened home must finish
// the commit exactly once. They live here because only home's test binary can
// place the crash between two of those steps.

// crashCase is one real operation crashed at every cut point. setup commits
// the operation's prerequisites normally; op runs the operation and reports
// whether its outcome was a replay; check asserts the recovered state without
// committing, and after proves the recovered revision and fences hold for new
// mutations. With readRecovers the first read on the crashed instance must
// recover the record in place instead of a reopen.
type crashCase struct {
	setup        func(t *testing.T, c *ta.Canonical)
	op           func(t *testing.T, c *ta.Canonical) (replayed bool, err error)
	check        func(t *testing.T, c *ta.Canonical)
	after        func(t *testing.T, c *ta.Canonical)
	readRecovers bool
}

// runCrashAtEveryStep crashes tc.op after k apply steps for k = 0, 1, ...
// until the operation commits without reaching the cut (k = n+2), and requires
// a change-set of at least two items so at least one cut is a partial apply.
// After each crash it proves the record is recovered exactly once: the scope
// revision advances once, the journal is empty, and replaying the operation
// returns the recovered outcome without committing again.
func runCrashAtEveryStep(t *testing.T, taskID string, tc crashCase) {
	t.Helper()
	for k := 0; ; k++ {
		crashed := false
		t.Run(fmt.Sprintf("crash after %d steps", k), func(t *testing.T) {
			root := t.TempDir()
			h, err := home.Init(root)
			if err != nil {
				t.Fatalf("home.Init: %v", err)
			}
			c := mustCanonical(t, h)
			if tc.setup != nil {
				tc.setup(t, c)
			}
			scope := "task-" + hex.EncodeToString([]byte(taskID))
			before, err := h.ScopeRevision(scope)
			if err != nil {
				t.Fatal(err)
			}

			home.CrashCommitAfter(t, k)
			crashed = crashes(t, func() error { _, err := tc.op(t, c); return err })
			if !crashed {
				return
			}
			if n := journalRecords(t, root); n != 1 {
				t.Fatalf("journal records after crash = %d, want 1 durable record", n)
			}

			rc := c
			if !tc.readRecovers {
				h2, err := home.Open(root)
				if err != nil {
					t.Fatalf("home.Open: %v", err)
				}
				h, rc = h2, mustCanonical(t, h2)
			}
			tc.check(t, rc)
			assertRecoveredOnce(t, h, root, scope, before)

			replayed, err := tc.op(t, rc)
			if err != nil || !replayed {
				t.Fatalf("replay after recovery = replayed %v, %v; want replayed", replayed, err)
			}
			assertRecoveredOnce(t, h, root, scope, before)
			if tc.after != nil {
				tc.after(t, rc)
			}
		})
		if !crashed {
			if n := k - 2; n < 2 {
				t.Fatalf("operation committed with %d items; want a change-set of at least two", n)
			}
			return
		}
	}
}

// crashes runs f and reports whether it died with the simulated crash.
func crashes(t *testing.T, f func() error) (crashed bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if r != home.ErrSimulatedCrash {
				panic(r)
			}
			crashed = true
		}
	}()
	if err := f(); err != nil {
		t.Fatalf("operation: %v", err)
	}
	return false
}

func assertRecoveredOnce(t *testing.T, h *home.Home, root, scope string, before uint64) {
	t.Helper()
	if got, err := h.ScopeRevision(scope); err != nil || got != before+1 {
		t.Fatalf("scope revision = %d, %v; want %d (advanced exactly once)", got, err, before+1)
	}
	if n := journalRecords(t, root); n != 0 {
		t.Fatalf("journal records after recovery = %d, want 0", n)
	}
}

func journalRecords(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, home.JournalDirName))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

func mustCanonical(t *testing.T, h *home.Home) *ta.Canonical {
	t.Helper()
	c, err := ta.NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	return c
}

func mustOp(t *testing.T, id string, intent domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, intent)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func taskID(t *testing.T, v string) domain.TaskID {
	t.Helper()
	id, err := domain.NewTaskID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func homeID(t *testing.T, v string) domain.HomeID {
	t.Helper()
	id, err := domain.NewHomeID(v)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func createReq(t *testing.T, c *ta.Canonical, id string) ta.CanonicalCreateRequest {
	return ta.CanonicalCreateRequest{HomeID: c.HomeID(), TaskID: taskID(t, id), Owner: "owner", Description: "work", Kind: "ship", Reason: "create"}
}

func mustCreate(t *testing.T, c *ta.Canonical, id string) {
	t.Helper()
	req := createReq(t, c, id)
	if _, err := c.Create(mustOp(t, "op-create-"+id, req), req); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
}

const deliveryHead = "abc123def456abc123def456abc123def456abc1"

func worktreeBinding() ta.WorktreeBinding {
	return ta.WorktreeBinding{
		RepositoryIdentity: "repo", Path: "/work/area", GitDir: "/work/area/.git", CommonDir: "/work/shared.git",
		Head: "abc123", LeaseID: "lease-wt", FenceToken: "fence-wt", BoundAtUnix: 1000,
	}
}

func bindWorktreeReq(t *testing.T, c *ta.Canonical, prec domain.Precondition, b ta.WorktreeBinding) ta.CanonicalBindWorktreeRequest {
	return ta.CanonicalBindWorktreeRequest{HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: prec, Binding: b, Reason: "bind worktree"}
}

func bindEndpointReq(t *testing.T, c *ta.Canonical, prec domain.Precondition) ta.CanonicalBindEndpointRequest {
	return ta.CanonicalBindEndpointRequest{HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: prec, Reason: "spawn", Binding: ta.EndpointBinding{
		Backend: "claude", Handle: "handle-1", LeaseID: "lease-ep", FenceToken: "fence-ep", SessionOwner: "owner",
		WorkspaceID: "ws", TabID: "tab", Incarnation: "inc-bind", BoundAtUnix: 2000,
	}}
}

// mustDeliveryTask leaves t1 working at revision 3 with the delivery bindings.
func mustDeliveryTask(t *testing.T, c *ta.Canonical) {
	t.Helper()
	mustCreate(t, c, "t1")
	wt := worktreeBinding()
	wt.Head = deliveryHead
	bw := bindWorktreeReq(t, c, domain.Of(1, 1), wt)
	if _, err := c.BindWorktree(mustOp(t, "op-delivery-bindwt", bw), bw); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	be := bindEndpointReq(t, c, domain.Of(1, 2))
	if _, err := c.BindEndpoint(mustOp(t, "op-delivery-bindep", be), be); err != nil {
		t.Fatalf("BindEndpoint: %v", err)
	}
}

func deliveryIdentity() domain.DeliveryIdentity {
	return domain.DeliveryIdentity{
		Provider: "github", Owner: "minhtri2710", Repo: "munsu", Number: 42,
		URL: "https://github.com/minhtri2710/munsu/pull/42", BaseRef: "main", HeadRef: "feature/delivery",
		HeadSHA: deliveryHead, CapturedAt: "2026-08-05T00:00:00Z",
	}
}

func authorizeReq(t *testing.T, c *ta.Canonical, rev uint64) ta.CanonicalDeliveryAuthorizationRequest {
	return ta.CanonicalDeliveryAuthorizationRequest{
		HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, rev),
		Kind: ta.DeliveryAuthorizationProviderMerge, Identity: deliveryIdentity(),
		Preconditions: []ta.DeliveryPrecondition{ta.DeliveryPreconditionPRMergeable, ta.DeliveryPreconditionPRHeadCurrent},
	}
}

func receiveReq(t *testing.T, c *ta.Canonical) ta.CanonicalReceiveTransferRequest {
	return ta.CanonicalReceiveTransferRequest{
		HomeID: c.HomeID(), TaskID: taskID(t, "t1"), ReservationID: "res-t1", SourceHome: homeID(t, "source-home"),
		SourceGeneration: 3, Definition: ta.TaskDefinition{Owner: "owner", Description: "work", Kind: "ship"}, Reason: "receive",
	}
}

func reserveReq(t *testing.T, c *ta.Canonical) ta.CanonicalReserveTransferRequest {
	return ta.CanonicalReserveTransferRequest{
		HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 1), ReservationID: "res-t1",
		Destination: homeID(t, "dest-home"), FenceToken: "fence-res-t1", Reason: "reserve",
	}
}

func mustGet(t *testing.T, c *ta.Canonical) ta.Aggregate {
	t.Helper()
	agg, err := c.Get(taskID(t, "t1"))
	if err != nil {
		t.Fatalf("read recovered task: %v", err)
	}
	return agg
}

func TestCrashRecoveryCreate(t *testing.T) {
	runCrashAtEveryStep(t, "t1", crashCase{
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := createReq(t, c, "t1")
			out, err := c.Create(mustOp(t, "op-crash-create", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			if agg := mustGet(t, c); agg.Generation != 1 || agg.Revision != ta.FirstRevision {
				t.Fatalf("recovered aggregate = %+v", agg)
			}
		},
		after: func(t *testing.T, c *ta.Canonical) {
			start := ta.CanonicalStartRequest{HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 1), Reason: "start"}
			if _, err := c.Start(mustOp(t, "op-start-recovered", start), start); err != nil {
				t.Fatalf("start after recovery: %v", err)
			}
		},
	})
}

func TestCrashRecoveryStart(t *testing.T) {
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) { mustCreate(t, c, "t1") },
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := ta.CanonicalStartRequest{HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 1), Reason: "start"}
			out, err := c.Start(mustOp(t, "op-crash-start", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			if agg := mustGet(t, c); agg.Phase != ta.PhaseWorking || agg.Revision != 2 {
				t.Fatalf("recovered aggregate = phase %s rev %d, want working/2", agg.Phase, agg.Revision)
			}
		},
		after: func(t *testing.T, c *ta.Canonical) {
			block := ta.CanonicalBlockRequest{HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 2), Detail: "waiting", Reason: "block"}
			if _, err := c.Block(mustOp(t, "op-block-after-recovery", block), block); err != nil {
				t.Fatalf("block after recovery: %v", err)
			}
		},
	})
}

func TestCrashRecoveryBeginSpawn(t *testing.T) {
	launch := func(t *testing.T, c *ta.Canonical) ta.CanonicalBeginSpawnRequest {
		return ta.CanonicalBeginSpawnRequest{
			HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 1), SnapshotDigest: digestOf("snapshot:t1"),
			Backend: "claude", Harness: "pi", Model: "opus", Effort: "high", Mode: "direct-PR", Kind: "ship", Project: "proj",
			ParentTaskID: "parent", LaunchID: "launch-t1", WindowLabel: "window-t1",
			WorktreeReservationID: "wt-res-t1", WorktreeFenceToken: "wt-fence-t1",
			EndpointReservationID: "ep-res-t1", EndpointFenceToken: "ep-fence-t1", EndpointIncarnation: "inc-t1", Reason: "spawn",
		}
	}
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) { mustCreate(t, c, "t1") },
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := launch(t, c)
			out, err := c.BeginSpawn(mustOp(t, "op-crash-begin", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			if agg := mustGet(t, c); agg.Revision != 2 || agg.Launch == nil || agg.Launch.LaunchID != "launch-t1" {
				t.Fatalf("recovered aggregate = revision %d launch %+v, want rev 2 with committed intent", agg.Revision, agg.Launch)
			}
		},
		after: func(t *testing.T, c *ta.Canonical) {
			// The recovered launch fence is active: only the reserved worktree binds.
			bad := bindWorktreeReq(t, c, domain.Of(1, 2), worktreeBinding())
			if _, err := c.BindWorktree(mustOp(t, "op-recovered-bad-wt", bad), bad); !errors.Is(err, ta.ErrConflict) {
				t.Fatalf("worktree binding outside recovered fence = %v, want ErrConflict", err)
			}
			wt := worktreeBinding()
			wt.LeaseID, wt.FenceToken = "wt-res-t1", "wt-fence-t1"
			good := bindWorktreeReq(t, c, domain.Of(1, 2), wt)
			if _, err := c.BindWorktree(mustOp(t, "op-recovered-good-wt", good), good); err != nil {
				t.Fatalf("worktree binding under recovered fence: %v", err)
			}
		},
	})
}

func TestCrashRecoveryBindWorktree(t *testing.T) {
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) { mustCreate(t, c, "t1") },
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := bindWorktreeReq(t, c, domain.Of(1, 1), worktreeBinding())
			out, err := c.BindWorktree(mustOp(t, "op-crash-bind", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			if agg := mustGet(t, c); agg.Worktree == nil || agg.Worktree.LeaseID != "lease-wt" || agg.Revision != 2 {
				t.Fatalf("recovered aggregate = rev %d worktree %+v", agg.Revision, agg.Worktree)
			}
		},
		after: func(t *testing.T, c *ta.Canonical) {
			ep := bindEndpointReq(t, c, domain.Of(1, 2))
			if _, err := c.BindEndpoint(mustOp(t, "op-bind-after-recovery", ep), ep); err != nil {
				t.Fatalf("bind endpoint after recovery: %v", err)
			}
		},
	})
}

func TestCrashRecoveryReserveTransfer(t *testing.T) {
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) { mustCreate(t, c, "t1") },
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := reserveReq(t, c)
			out, err := c.ReserveTransfer(mustOp(t, "op-crash-reserve", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			if agg := mustGet(t, c); agg.Revision != 2 || agg.Transfer == nil || agg.Transfer.ReservationID != "res-t1" {
				t.Fatalf("recovered aggregate = %+v", agg)
			}
			// The fence is active on the recovered reservation.
			start := ta.CanonicalStartRequest{HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 2), Reason: "start"}
			if _, err := c.Start(mustOp(t, "op-post-recover-start", start), start); err == nil {
				t.Fatalf("Start on recovered reserved task = nil error, want fence rejection")
			}
		},
	})
}

func TestCrashRecoveryReceiveTransfer(t *testing.T) {
	runCrashAtEveryStep(t, "t1", crashCase{
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := receiveReq(t, c)
			out, err := c.ReceiveTransfer(mustOp(t, "op-crash-receive", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			// The received generation is present but not current.
			if _, err := c.Get(taskID(t, "t1")); !errors.Is(err, ta.ErrNotFound) {
				t.Fatalf("Get on received-not-current = %v, want ErrNotFound", err)
			}
			if agg, err := c.GetGeneration(taskID(t, "t1"), 1); err != nil || agg.Current || agg.Transfer == nil || agg.Transfer.SourceGeneration != 3 {
				t.Fatalf("GetGeneration(1) = %+v, %v; want received non-current generation", agg, err)
			}
		},
	})
}

func TestCrashRecoveryActivateTransfer(t *testing.T) {
	activate := func(t *testing.T, c *ta.Canonical) ta.CanonicalActivateTransferRequest {
		return ta.CanonicalActivateTransferRequest{HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 1), ReservationID: "res-t1", Reason: "activate"}
	}
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) {
			req := receiveReq(t, c)
			if _, err := c.ReceiveTransfer(mustOp(t, "op-receive-1", req), req); err != nil {
				t.Fatalf("ReceiveTransfer: %v", err)
			}
		},
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := activate(t, c)
			out, err := c.ActivateTransfer(mustOp(t, "op-crash-activate", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			if agg := mustGet(t, c); !agg.Current || agg.Generation != 1 || agg.Revision != 2 {
				t.Fatalf("recovered aggregate = %+v, want current gen 1 rev 2", agg)
			}
		},
	})
}

func TestCrashRecoveryCommitTransfer(t *testing.T) {
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) {
			mustCreate(t, c, "t1")
			req := reserveReq(t, c)
			if _, err := c.ReserveTransfer(mustOp(t, "op-reserve-t1", req), req); err != nil {
				t.Fatalf("ReserveTransfer: %v", err)
			}
		},
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := ta.CanonicalCommitTransferRequest{
				HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 2), ReservationID: "res-t1", FenceToken: "fence-res-t1",
				Evidence: ta.TransferActivationInfo{
					ReservationID: "res-t1", TaskID: "t1", SourceHome: c.HomeID().Value(), SourceGeneration: 1,
					DestinationHome: "dest-home", DestinationGeneration: 1,
					ActivationOperationID: "op-activate-t1", ActivationDigest: digestOf("activate:t1:res-t1"),
				},
				Reason: "commit",
			}
			out, err := c.CommitTransfer(mustOp(t, "op-crash-commit", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			// The superseded source is not current truth; evidence remains by generation.
			if _, err := c.Get(taskID(t, "t1")); !errors.Is(err, ta.ErrNotFound) {
				t.Fatalf("Get after recovered commit = %v, want ErrNotFound", err)
			}
			agg, err := c.GetGeneration(taskID(t, "t1"), 1)
			if err != nil {
				t.Fatalf("GetGeneration: %v", err)
			}
			if agg.Current || agg.Transfer == nil || !agg.Transfer.Transferred {
				t.Fatalf("recovered aggregate not superseded: %+v", agg)
			}
			if agg.Transfer.Activation == nil || agg.Transfer.Activation.DestinationHome != "dest-home" {
				t.Fatalf("recovered activation evidence missing: %+v", agg.Transfer)
			}
		},
	})
}

func TestCrashRecoveryRetire(t *testing.T) {
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) {
			mustCreate(t, c, "t1")
			req := bindWorktreeReq(t, c, domain.Of(1, 1), worktreeBinding())
			if _, err := c.BindWorktree(mustOp(t, "op-bind-wt", req), req); err != nil {
				t.Fatalf("BindWorktree: %v", err)
			}
		},
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := ta.CanonicalRetireRequest{HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 2), Reason: "retire"}
			out, err := c.Retire(mustOp(t, "op-crash-retire", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			agg := mustGet(t, c)
			if agg.Phase != ta.PhaseRetired || agg.Worktree != nil || agg.Revision != 3 {
				t.Fatalf("recovered retired aggregate = %+v", agg)
			}
			if agg.Retirement == nil || agg.Retirement.Worktree == nil || agg.Retirement.Worktree.LeaseID != "lease-wt" {
				t.Fatalf("recovered retirement evidence = %+v", agg.Retirement)
			}
		},
	})
}

func TestCrashRecoveryDeliveryAuthorization(t *testing.T) {
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) { mustDeliveryTask(t, c) },
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := authorizeReq(t, c, 3)
			out, err := c.AuthorizeDelivery(mustOp(t, "op-crash-auth", req), req)
			return out.Replayed, err
		},
		// DeliveryCurrency is the first read after the crash, so the query
		// itself must recover the task, index and evidence.
		readRecovers: true,
		check: func(t *testing.T, c *ta.Canonical) {
			cur, err := c.DeliveryCurrency(taskID(t, "t1"))
			if err != nil {
				t.Fatalf("read recovered currency: %v", err)
			}
			if !cur.Valid || cur.Revision != 4 || cur.Authorization == nil || cur.Authorization.OperationID != "op-crash-auth" {
				t.Fatalf("recovered currency = %+v", cur)
			}
			auth := cur.Authorization
			if auth.OperationID != "op-crash-auth" || auth.Revision != 4 || auth.Identity != deliveryIdentity() {
				t.Fatalf("recovered authorization = %+v", auth)
			}
		},
	})
}

func TestCrashRecoveryDeliveryOutcome(t *testing.T) {
	outcome := func(t *testing.T, c *ta.Canonical) ta.CanonicalDeliveryOutcomeRequest {
		return ta.CanonicalDeliveryOutcomeRequest{
			HomeID: c.HomeID(), TaskID: taskID(t, "t1"), Precondition: domain.Of(1, 4),
			AuthorizationOperationID: "op-auth", Status: ta.DeliveryOutcomeCompleted, Detail: "merge confirmed",
			MergedSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		}
	}
	runCrashAtEveryStep(t, "t1", crashCase{
		setup: func(t *testing.T, c *ta.Canonical) {
			mustDeliveryTask(t, c)
			req := authorizeReq(t, c, 3)
			if _, err := c.AuthorizeDelivery(mustOp(t, "op-auth", req), req); err != nil {
				t.Fatalf("AuthorizeDelivery: %v", err)
			}
		},
		op: func(t *testing.T, c *ta.Canonical) (bool, error) {
			req := outcome(t, c)
			out, err := c.CommitDeliveryOutcome(mustOp(t, "op-crash-outcome", req), req)
			return out.Replayed, err
		},
		check: func(t *testing.T, c *ta.Canonical) {
			got, err := c.DeliveryOutcome(taskID(t, "t1"))
			if err != nil {
				t.Fatalf("read recovered outcome: %v", err)
			}
			if got.OperationID != "op-crash-outcome" || got.Status != ta.DeliveryOutcomeCompleted {
				t.Fatalf("recovered outcome = %+v", got)
			}
			// A distinct incompatible outcome still conflicts after recovery.
			distinct := outcome(t, c)
			distinct.Precondition = domain.Of(1, 5)
			distinct.Status = ta.DeliveryOutcomeRetryable
			if _, err := c.CommitDeliveryOutcome(mustOp(t, "op-after-recovered", distinct), distinct); !errors.Is(err, ta.ErrConflict) {
				t.Fatalf("distinct outcome after recovered terminal = %v, want ErrConflict", err)
			}
		},
	})
}
