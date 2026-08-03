//go:build integration

package fleet

import (
	"errors"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// mustPreparedTask seeds one ship task in an in-memory Authority with a
// delivery preparation at the given head, mirroring the pr-check flow (Task
// 7.5).
func mustPreparedTask(t *testing.T, taskID, headSHA string) *taskauthority.Authority {
	t.Helper()
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := auth.Create(taskauthority.CreateRequest{
		OperationID: "op-create-" + taskID,
		Actor:       taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:      taskID,
		Owner:       "owner",
		Kind:        "ship",
		Reason:      "create",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := auth.PrepareDelivery(taskauthority.PrepareDeliveryRequest{
		OperationID:        "op-prepare-" + taskID,
		Actor:              taskauthority.Actor{ID: "owner", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: 1,
		State:              taskauthority.DeliveryPrepareStateReviewReady,
		HeadSHA:            headSHA,
		Identity:           snapshotFromIdentity(testIdentityAtHead(headSHA)),
		Reason:             "pr-check",
	}); err != nil {
		t.Fatalf("PrepareDelivery: %v", err)
	}
	return auth
}

// testIdentityAtHead builds a full delivery identity at the given head.
func testIdentityAtHead(headSHA string) *domain.DeliveryIdentity {
	ident := validIdentity()
	ident.HeadSHA = headSHA
	return ident
}

// --- StoreDeliveryPrepare ---

func TestStoreDeliveryPrepareCommitsRecordAndProjectsMeta(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-store-prepare"
	ident := validIdentity()
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"kind": "ship"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := preparedCheckAuth(t, taskID)

	res, err := StoreDeliveryPrepare(homeDir, auth, taskID, ident)
	if err != nil {
		t.Fatalf("StoreDeliveryPrepare: %v", err)
	}
	if res.Revision != 2 || res.Replayed {
		t.Fatalf("prepare result = %+v, want revision 2", res)
	}

	// The authoritative aggregate carries the generation-bound prepare record.
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryPrepare == nil || agg.DeliveryPrepare.HeadSHA != ident.HeadSHA ||
		agg.DeliveryPrepare.State != taskauthority.DeliveryPrepareStateReviewReady {
		t.Fatalf("delivery prepare = %+v", agg.DeliveryPrepare)
	}

	// The .meta projection carries the identity keys and review-ready state.
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta["pr_url"] != ident.URL || meta["pr_head"] != ident.HeadSHA || meta["pr"] != ident.URL {
		t.Fatalf("meta identity projection = %v", meta)
	}
	if meta[MetaDeliveryState] != string(DeliveryStateReviewReady) {
		t.Fatalf("delivery_state = %q, want %q", meta[MetaDeliveryState], DeliveryStateReviewReady)
	}
}

// TestStoreDeliveryPrepareIdempotentRePrepare proves re-preparing the same
// identity is an in-value no-op (no revision advance) and a changed head must
// acknowledge the prior prepared head (force-with-lease).
func TestStoreDeliveryPrepareIdempotentRePrepare(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-reprepare"
	ident := validIdentity()
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"kind": "ship"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := preparedCheckAuth(t, taskID)

	if _, err := StoreDeliveryPrepare(homeDir, auth, taskID, ident); err != nil {
		t.Fatal(err)
	}
	res, err := StoreDeliveryPrepare(homeDir, auth, taskID, ident)
	if err != nil {
		t.Fatal(err)
	}
	if res.Revision != 2 {
		t.Fatalf("no-op re-prepare advanced revision to %d, want 2", res.Revision)
	}

	// A changed head is re-prepared explicitly through the wrapper (the
	// wrapper acknowledges the committed prior head from the aggregate).
	newIdent := validIdentity()
	newIdent.HeadSHA = testNewHeadIdentity().HeadSHA
	if _, err := StoreDeliveryPrepare(homeDir, auth, taskID, newIdent); err != nil {
		t.Fatalf("explicit re-prepare: %v", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryPrepare.HeadSHA != newIdent.HeadSHA {
		t.Fatalf("re-prepared head = %q, want %q", agg.DeliveryPrepare.HeadSHA, newIdent.HeadSHA)
	}
}

func TestStoreDeliveryPrepareNilAuthFailsClosed(t *testing.T) {
	if _, err := StoreDeliveryPrepare(t.TempDir(), nil, "task", validIdentity()); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

func TestStoreDeliveryPrepareMissingTaskFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-missing"
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"kind": "ship"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := taskauthority.New(taskauthority.NewMemStore())
	if _, err := StoreDeliveryPrepare(homeDir, auth, taskID, validIdentity()); !errors.Is(err, taskauthority.ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}
}

// --- StoreDeliveryCompletion ---

func TestStoreDeliveryCompletionCommitsTerminalAndProjectsMeta(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-store-complete"
	ident := validIdentity()
	if err := home.WriteMeta(homeDir, taskID, ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := mustPreparedTask(t, taskID, ident.HeadSHA)

	res, err := StoreDeliveryCompletion(homeDir, auth, taskID, taskauthority.DeliveryTerminalDone, ident)
	if err != nil {
		t.Fatalf("StoreDeliveryCompletion: %v", err)
	}
	if res.Revision != 3 || res.Replayed {
		t.Fatalf("complete result = %+v, want revision 3", res)
	}

	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryTerminal == nil || agg.DeliveryTerminal.Terminal != taskauthority.DeliveryTerminalDone ||
		agg.DeliveryTerminal.HeadSHA != ident.HeadSHA {
		t.Fatalf("delivery terminal = %+v", agg.DeliveryTerminal)
	}
	if agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("phase changed to %s; delivery evidence is not a competing phase mutation", agg.Phase)
	}

	// The .meta projection carries the identity keys; done carries no
	// delivery_state meta value (the terminal record is authoritative).
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta["pr_head_sha"] != ident.HeadSHA || meta["pr"] != ident.URL {
		t.Fatalf("meta terminal projection = %v", meta)
	}

	// Repeating the same terminal state is an idempotent no-op.
	again, err := StoreDeliveryCompletion(homeDir, auth, taskID, taskauthority.DeliveryTerminalDone, ident)
	if err != nil {
		t.Fatal(err)
	}
	if again.Revision != 3 {
		t.Fatalf("no-op replay advanced revision to %d, want 3", again.Revision)
	}
}

func TestStoreDeliveryCompletionChangedHeadFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-head-bound"
	ident := validIdentity()
	if err := home.WriteMeta(homeDir, taskID, ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := mustPreparedTask(t, taskID, ident.HeadSHA)

	// Terminal evidence with a changed head fails closed (binds the exact
	// prepared head).
	changed := validIdentity()
	changed.HeadSHA = testNewHeadIdentity().HeadSHA
	if _, err := StoreDeliveryCompletion(homeDir, auth, taskID, taskauthority.DeliveryTerminalDone, changed); err == nil {
		t.Fatal("expected error for changed terminal head")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryTerminal != nil {
		t.Fatalf("changed-head completion committed a terminal record: %+v", agg.DeliveryTerminal)
	}
}

// TestStoreDeliveryCompletionResolvedNotCompletion proves the wrapper and the
// Authority reject resolved as a delivery terminal transition: ship completion
// is delivered/done, never resolved.
func TestStoreDeliveryCompletionResolvedNotCompletion(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-resolved"
	ident := validIdentity()
	if err := home.WriteMeta(homeDir, taskID, ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := mustPreparedTask(t, taskID, ident.HeadSHA)

	if _, err := StoreDeliveryCompletion(homeDir, auth, taskID, "resolved", ident); err == nil {
		t.Fatal("expected error for resolved terminal state")
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryTerminal != nil {
		t.Fatalf("resolved completion committed a terminal record: %+v", agg.DeliveryTerminal)
	}
}

// TestStoreDeliveryCompletionUnpreparedFailsClosed proves a done report on a
// task that was never pr-checked fails closed with a typed precondition
// error: delivery completion requires the generation-bound preparation.
func TestStoreDeliveryCompletionUnpreparedFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-unprepared"
	ident := validIdentity()
	if err := home.WriteMeta(homeDir, taskID, ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := preparedCheckAuth(t, taskID) // task exists but was never prepared

	if _, err := StoreDeliveryCompletion(homeDir, auth, taskID, taskauthority.DeliveryTerminalDone, ident); !errors.Is(err, taskauthority.ErrPrecondition) {
		t.Fatalf("unprepared completion error = %v, want ErrPrecondition", err)
	}
}

func TestStoreDeliveryCompletionNilAuthFailsClosed(t *testing.T) {
	if _, err := StoreDeliveryCompletion(t.TempDir(), nil, "task", taskauthority.DeliveryTerminalDone, validIdentity()); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}

// --- StoreDeliveryCompletion cross-home ---

// TestStoreDeliveryCompletionCrossHomeTaskHome proves the composed Authority
// targets the resolved task home (cross-home delivery): the authoritative
// terminal record lands in the task-home store, while the command home is
// untouched.
func TestStoreDeliveryCompletionCrossHomeTaskHome(t *testing.T) {
	commandHome := t.TempDir()
	taskHome := t.TempDir()
	taskID := "test-cross-home-complete"
	ident := validIdentity()
	if err := home.WriteMeta(taskHome, taskID, ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := mustPreparedTask(t, taskID, ident.HeadSHA)

	res, err := StoreDeliveryCompletion(taskHome, auth, taskID, taskauthority.DeliveryTerminalDone, ident)
	if err != nil {
		t.Fatalf("StoreDeliveryCompletion: %v", err)
	}
	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryTerminal == nil || res.Revision != 3 {
		t.Fatalf("cross-home terminal commit = %+v (result %+v)", agg.DeliveryTerminal, res)
	}

	// The command home carries no task meta.
	if _, err := home.ReadMeta(commandHome, taskID); err == nil {
		t.Error("command home must not own the task meta")
	}
	// The task home projection carries the identity keys.
	meta, err := home.ReadMeta(taskHome, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta["pr_head_sha"] != ident.HeadSHA {
		t.Fatalf("task-home projection head = %q", meta["pr_head_sha"])
	}
}

// --- PrepareDelivery (fleet verification wrapper) ---

// TestPrepareDelivery_VerifiedMergedDelivers proves the parent-owned
// verification function routes the delivered terminal transition through the
// composed Authority after provider verification succeeds: the authoritative
// terminal record commits with the delivered state and the delivery_state
// meta key is projected.
func TestPrepareDelivery_VerifiedMergedDelivers(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-prepare-delivered"
	ident := validIdentity()
	meta := ident.ToMeta()
	meta["kind"] = "ship"
	meta[MetaDeliveryState] = string(DeliveryStateReviewReady)
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := mustPreparedTask(t, taskID, ident.HeadSHA)

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 42, URL: ident.URL,
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: ident.HeadSHA, State: "MERGED", Merged: true,
			MergedSHA:  "mergemergemergemergemergemergemergemergemerge",
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	result, err := PrepareDelivery(homeDir, taskID, auth)
	if err != nil {
		t.Fatalf("PrepareDelivery: %v", err)
	}
	if !result.IdentityVerified || !result.HeadImmutable || !result.ChecksGreen {
		t.Fatalf("prepare result = %+v, want all checks green", result)
	}
	if result.DeliveryState != string(DeliveryStateDelivered) {
		t.Fatalf("delivery state = %q, want %q", result.DeliveryState, DeliveryStateDelivered)
	}

	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryTerminal == nil || agg.DeliveryTerminal.Terminal != taskauthority.DeliveryTerminalDelivered {
		t.Fatalf("delivery terminal = %+v, want delivered", agg.DeliveryTerminal)
	}

	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if readMeta[MetaDeliveryState] != string(DeliveryStateDelivered) {
		t.Fatalf("delivery_state projection = %q, want %q", readMeta[MetaDeliveryState], DeliveryStateDelivered)
	}
}

// TestPrepareDelivery_IdentityMismatchLeavesStateUnchanged proves failed
// provider verification commits nothing: the prior authoritative phase and
// the delivery_state projection are unchanged (no terminal record).
func TestPrepareDelivery_IdentityMismatchLeavesStateUnchanged(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-prepare-mismatch"
	ident := validIdentity()
	meta := ident.ToMeta()
	meta["kind"] = "ship"
	meta[MetaDeliveryState] = string(DeliveryStateReviewReady)
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	auth := mustPreparedTask(t, taskID, ident.HeadSHA)

	savedSnap := FetchProviderSnapshot
	FetchProviderSnapshot = func(url string) (*ProviderSnapshot, error) {
		return &ProviderSnapshot{
			Provider: "github", Owner: "minhtri2710", Repo: "munsu",
			Number: 99, URL: "https://github.com/minhtri2710/munsu/pull/99",
			BaseRef: "main", HeadRef: "feature/test",
			HeadSHA: ident.HeadSHA, State: "MERGED", Merged: true,
			ObservedAt: "2026-07-19T12:00:00Z",
		}, nil
	}
	defer func() { FetchProviderSnapshot = savedSnap }()

	if _, err := PrepareDelivery(homeDir, taskID, auth); err == nil {
		t.Fatal("expected identity mismatch error")
	}

	agg, err := auth.Get(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryTerminal != nil || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("failed verification mutated the authoritative state: %+v", agg)
	}
	readMeta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if readMeta[MetaDeliveryState] != string(DeliveryStateReviewReady) {
		t.Fatalf("delivery_state projection changed to %q", readMeta[MetaDeliveryState])
	}
}

// TestPrepareDeliveryNilAuthFailsClosed proves the parent verification fails
// closed without a composed Authority instead of falling back to a meta
// write.
func TestPrepareDeliveryNilAuthFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	ident := validIdentity()
	if err := home.WriteMeta(homeDir, "task", ident.ToMeta()); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if _, err := PrepareDelivery(homeDir, "task", nil); err == nil {
		t.Fatal("expected error when no authority is composed")
	}
}
