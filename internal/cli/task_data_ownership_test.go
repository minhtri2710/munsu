package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// ownershipHome initializes a home and returns an authority over it.
func ownershipHome(t *testing.T, dir string) *taskauthority.Canonical {
	t.Helper()
	if _, err := home.Init(dir); err != nil {
		t.Fatal(err)
	}
	h, err := home.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func ownershipTaskID(t *testing.T, id string) domain.TaskID {
	t.Helper()
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	return tid
}

func ownershipOp(t *testing.T, id string, req domain.Intent) domain.Operation {
	t.Helper()
	opID, err := domain.NewOperationID(id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestTaskDataDirOwnershipFailsClosedWithoutAnAuthority(t *testing.T) {
	// A directory that is not a munsu home cannot answer the question, so
	// every data directory in it keeps its owner and nothing is swept.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "not-a-home"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	owns := taskDataDirOwnership(filepath.Join(dir, "missing"))
	if !owns("any-task") {
		t.Fatal("an unopenable home must report every data directory as owned")
	}
}

func TestTaskDataDirOwnershipFailsClosedOnAnUnreadableID(t *testing.T) {
	dir := t.TempDir()
	ownershipHome(t, dir)
	owns := taskDataDirOwnership(dir)
	if !owns("Not A Task ID/../..") {
		t.Fatal("a directory name that is not a task ID must be reported as owned")
	}
}

func TestTaskDataDirOwnershipReleasesAnUnknownTask(t *testing.T) {
	dir := t.TempDir()
	ownershipHome(t, dir)
	owns := taskDataDirOwnership(dir)
	if owns("never-existed") {
		t.Fatal("a data directory the Authority knows no task for is not owned")
	}
}

func TestTaskDataDirOwnershipKeepsATaskThatHasNotRetired(t *testing.T) {
	dir := t.TempDir()
	auth := ownershipHome(t, dir)
	tid := ownershipTaskID(t, "planned-task")
	req := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "general", Description: "Ready", Kind: "ship", Reason: "test",
	}
	if _, err := auth.Create(ownershipOp(t, "op-create-planned-task", req), req); err != nil {
		t.Fatal(err)
	}

	// This is the shape the sweep cannot see from disk: `munsu brief` has
	// written data/planned-task/brief.md, but the task has never been
	// spawned, so there is no .meta and no .status either.
	owns := taskDataDirOwnership(dir)
	if !owns("planned-task") {
		t.Fatal("a briefed but unspawned task still owns its data directory")
	}
}

func TestTaskDataDirOwnershipReleasesAfterTerminalCleanup(t *testing.T) {
	dir := t.TempDir()
	auth := ownershipHome(t, dir)
	tid := ownershipTaskID(t, "retired-task")
	create := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: tid, Owner: "general", Description: "Ready", Kind: "ship", Reason: "test",
	}
	if _, err := auth.Create(ownershipOp(t, "op-create-retired-task", create), create); err != nil {
		t.Fatal(err)
	}

	agg, err := auth.Get(tid)
	if err != nil {
		t.Fatal(err)
	}
	retire := taskauthority.CanonicalRetireRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Reason:       "test",
	}
	if _, err := auth.Retire(ownershipOp(t, "op-retire-retired-task", retire), retire); err != nil {
		t.Fatal(err)
	}

	if agg, err = auth.Get(tid); err != nil {
		t.Fatal(err)
	}
	begin := taskauthority.CanonicalBeginCleanupRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition:     domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		ClaimOperationID: "op-retire-retired-task",
		ClaimGeneration:  agg.Generation,
		Reason:           "test",
	}
	if _, err := auth.BeginCleanup(ownershipOp(t, "op-begin-retired-task", begin), begin); err != nil {
		t.Fatal(err)
	}

	owns := taskDataDirOwnership(dir)
	if !owns("retired-task") {
		t.Fatal("a retirement whose cleanup is still claimed has not released its data directory")
	}

	if agg, err = auth.Get(tid); err != nil {
		t.Fatal(err)
	}
	complete := taskauthority.CanonicalCompleteCleanupRequest{
		HomeID: auth.HomeID(), TaskID: tid,
		Precondition:     domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		ClaimOperationID: "op-retire-retired-task",
		ClaimGeneration:  agg.Generation,
		Reason:           "test complete",
	}
	if _, err := auth.CompleteCleanup(ownershipOp(t, "op-complete-retired-task", complete), complete); err != nil {
		t.Fatal(err)
	}
	if taskDataDirOwnership(dir)("retired-task") {
		t.Fatal("a completed retirement has released its data directory")
	}

	abortedID := ownershipTaskID(t, "aborted-task")
	createAborted := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: abortedID, Owner: "general", Description: "Aborted", Kind: "ship", Reason: "test"}
	if _, err := auth.Create(ownershipOp(t, "op-create-aborted-task", createAborted), createAborted); err != nil {
		t.Fatal(err)
	}
	abortedAgg, err := auth.Get(abortedID)
	if err != nil {
		t.Fatal(err)
	}
	retireAborted := taskauthority.CanonicalRetireRequest{HomeID: auth.HomeID(), TaskID: abortedID, Precondition: domain.Of(uint64(abortedAgg.Generation), uint64(abortedAgg.Revision)), Reason: "test"}
	if _, err := auth.Retire(ownershipOp(t, "op-retire-aborted-task", retireAborted), retireAborted); err != nil {
		t.Fatal(err)
	}
	abortedAgg, err = auth.Get(abortedID)
	if err != nil {
		t.Fatal(err)
	}
	beginAborted := taskauthority.CanonicalBeginCleanupRequest{HomeID: auth.HomeID(), TaskID: abortedID, Precondition: domain.Of(uint64(abortedAgg.Generation), uint64(abortedAgg.Revision)), ClaimOperationID: "op-retire-aborted-task", ClaimGeneration: abortedAgg.Generation, Reason: "test"}
	if _, err := auth.BeginCleanup(ownershipOp(t, "op-begin-aborted-task", beginAborted), beginAborted); err != nil {
		t.Fatal(err)
	}
	abortedAgg, err = auth.Get(abortedID)
	if err != nil {
		t.Fatal(err)
	}
	abort := taskauthority.CanonicalAbortCleanupRequest{
		HomeID: auth.HomeID(), TaskID: abortedID,
		Precondition:     domain.Of(uint64(abortedAgg.Generation), uint64(abortedAgg.Revision)),
		ClaimOperationID: "op-retire-aborted-task",
		ClaimGeneration:  abortedAgg.Generation,
		Reason:           "test abort",
	}
	if _, err := auth.AbortCleanup(ownershipOp(t, "op-abort-aborted-task", abort), abort); err != nil {
		t.Fatal(err)
	}
	if taskDataDirOwnership(dir)("aborted-task") {
		t.Fatal("an aborted retirement has released its data directory")
	}

	if abortedAgg, err = auth.Get(abortedID); err != nil {
		t.Fatal(err)
	}
	if abortedAgg.CleanupClaim == nil || abortedAgg.CleanupClaim.Status != taskauthority.CleanupAborted {
		t.Fatalf("claim not aborted: %+v", abortedAgg.CleanupClaim)
	}
}
