package taskauthorityfs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// bindUpdate stages one committed worktree binding for the current generation
// of taskID: the aggregate gains the binding and advances revision, the lease
// marker is staged in the same transaction, and one typed binding audit event
// is appended.
func bindUpdate(opID, taskID string, binding taskauthority.WorktreeBinding) func(tx *taskauthority.Tx) error {
	return func(tx *taskauthority.Tx) error {
		cur, ok := tx.Current(taskID)
		if !ok {
			return errors.New(taskID + " missing")
		}
		updated := cur
		updated.Worktree = &binding
		updated.Revision++
		if err := tx.PutAggregate(updated); err != nil {
			return err
		}
		if err := tx.PutLeaseMarker(taskauthority.LeaseMarker{
			TaskID:         taskID,
			TaskGeneration: cur.Generation,
			LeaseID:        binding.LeaseID,
			FenceToken:     binding.FenceToken,
		}); err != nil {
			return err
		}
		return tx.AppendAudit(taskauthority.AuditEvent{
			OperationID: opID,
			Actor:       taskauthority.Actor{ID: "general", Rank: "general"},
			Kind:        taskauthority.AuditBinding,
			TaskID:      taskID,
			Generation:  cur.Generation,
			Reason:      "bind",
			At:          time.Now().UnixNano(),
		})
	}
}

// testWorktreeBinding builds a valid worktree binding payload.
func testWorktreeBinding(leaseID, fenceToken string) taskauthority.WorktreeBinding {
	return taskauthority.WorktreeBinding{
		RepositoryIdentity: "repo-identity",
		Path:               "/tmp/wt",
		GitDir:             "/repo/.git/worktrees/wt",
		CommonDir:          "/repo/.git",
		Head:               strings.Repeat("a", 40),
		LeaseID:            leaseID,
		FenceToken:         fenceToken,
		BoundAtUnix:        time.Now().Unix(),
	}
}

// markerRel returns the versioned rel path of one worktree lease marker.
func markerRel(t *testing.T, taskID string, generation taskauthority.Generation, leaseID string) string {
	t.Helper()
	rel, err := WorktreeLeaseRelPath(taskID, generation, leaseID)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

// assertLeaseMarker asserts the lease marker file exists at its versioned
// path with the home-compatible content: task identity, decimal-string
// generation, lease id, and fence token.
func assertLeaseMarker(t *testing.T, home, taskID string, generation taskauthority.Generation, leaseID, fenceToken string) {
	t.Helper()
	rel := markerRel(t, taskID, generation, leaseID)
	data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("lease marker missing at %s: %v", rel, err)
	}
	var marker struct {
		TaskID         string `json:"task_id"`
		TaskGeneration string `json:"task_generation"`
		LeaseID        string `json:"lease_id"`
		FenceToken     string `json:"fence_token"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("lease marker at %s is corrupt: %v", rel, err)
	}
	if marker.TaskID != taskID || marker.TaskGeneration != generation.String() || marker.LeaseID != leaseID || marker.FenceToken != fenceToken {
		t.Fatalf("lease marker at %s = %+v, want task=%s gen=%s lease=%s fence=%s", rel, marker, taskID, generation, leaseID, fenceToken)
	}
}

// TestWorktreeBindingCommitsLeaseMarkerWithBinding proves the filesystem
// adapter commits the lease marker and the aggregate binding in one
// transaction: the canonical view exposes the binding, the marker file is
// durable at its versioned path in the home-compatible format, and the typed
// binding audit event committed.
func TestWorktreeBindingCommitsLeaseMarkerWithBinding(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	if _, err := s.Update(testOp("op-create", "t1"), createUpdate("op-create", "t1")); err != nil {
		t.Fatal(err)
	}
	binding := testWorktreeBinding("lease-1", "fence-1")
	receipt, err := s.Update(testOp("op-bind", "t1"), bindUpdate("op-bind", "t1", binding))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TaskID != "t1" || receipt.Generation != 1 || receipt.Revision != 2 || receipt.Phase != taskauthority.PhaseQueued || receipt.Replayed || receipt.Reopened {
		t.Fatalf("bind receipt = %+v, want revision 2 queued outcome", receipt)
	}

	v := mustView(t, openStore(t, home))
	cur, ok := v.Current("t1")
	if !ok || cur.Worktree == nil || cur.Worktree.LeaseID != "lease-1" || cur.Revision != 2 {
		t.Fatalf("current aggregate after bind = %+v", cur)
	}
	assertLeaseMarker(t, home, "t1", 1, "lease-1", "fence-1")

	var auditFound bool
	for _, ev := range v.Audit {
		if ev.OperationID == "op-bind" && ev.Kind == taskauthority.AuditBinding && ev.TaskID == "t1" && ev.Generation == 1 {
			auditFound = true
		}
	}
	if !auditFound {
		t.Fatalf("binding audit event missing: %+v", v.Audit)
	}
	assertNoManifests(t, home)

	// A fresh Store instance on the same home sees the durable marker and
	// binding (no recovery required).
	assertLeaseMarker(t, home, "t1", 1, "lease-1", "fence-1")
	s2 := openStore(t, home)
	if _, err := s2.Update(testOp("op-create", "t1"), func(tx *taskauthority.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

// TestWorktreeBindingLeaseMarkerReplaysAfterReopen proves replaying the
// committed bind operation returns the original receipt without re-running
// the callback and without rewriting the marker.
func TestWorktreeBindingLeaseMarkerReplaysAfterReopen(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	if _, err := s.Update(testOp("op-create", "t1"), createUpdate("op-create", "t1")); err != nil {
		t.Fatal(err)
	}
	binding := testWorktreeBinding("lease-1", "fence-1")
	op := testOp("op-bind", "t1")
	first, err := s.Update(op, bindUpdate("op-bind", "t1", binding))
	if err != nil {
		t.Fatal(err)
	}
	s2 := openStore(t, home)
	runs := 0
	second, err := s2.Update(op, func(tx *taskauthority.Tx) error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 0 || !second.Replayed {
		t.Fatalf("replay runs=%d Replayed=%v, want 0/true", runs, second.Replayed)
	}
	if second.OperationID != first.OperationID || second.Digest != first.Digest || second.CommittedAt != first.CommittedAt ||
		second.Revision != first.Revision || second.Phase != first.Phase {
		t.Fatalf("replay receipt identity differs: %+v vs %+v", first, second)
	}
	assertLeaseMarker(t, home, "t1", 1, "lease-1", "fence-1")
}

// TestWorktreeBindingCrashRecoveryConvergesEveryStage proves the lease
// marker record recovers deterministically at every journal stage: a crash
// before the pending manifest leaves no mutation, and a crash at every later
// stage converges to the committed binding, marker, receipt, and audit when a
// fresh Store reads the home.
func TestWorktreeBindingCrashRecoveryConvergesEveryStage(t *testing.T) {
	cases := []struct {
		name       string
		stage      faultStage
		afterWrite int
	}{
		{"before manifest", faultStageBeforeManifest, 0},
		{"after pending manifest", faultStageAfterManifest, 0},
		{"after first data write", faultStageAfterDataWrite, 1},
		{"after second data write", faultStageAfterDataWrite, 2},
		{"after third data write", faultStageAfterDataWrite, 3},
		{"after fourth data write", faultStageAfterDataWrite, 4},
		{"after fifth data write", faultStageAfterDataWrite, 5},
		{"before commit marker", faultStageBeforeCommit, 0},
		{"after commit marker", faultStageAfterCommit, 0},
		{"during cleanup", faultStageDuringCleanup, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			s := openStore(t, home)
			if _, err := s.Update(testOp("op-create", "t1"), createUpdate("op-create", "t1")); err != nil {
				t.Fatal(err)
			}
			binding := testWorktreeBinding("lease-1", "fence-1")
			op := testOp("op-bind", "t1")
			s2 := openStore(t, home)
			s2.fault = &faultInjector{stage: tc.stage, afterWrite: tc.afterWrite, err: errInjected}
			_, err := s2.Update(op, bindUpdate("op-bind", "t1", binding))
			if err == nil {
				// The fault never fired (a data-write index beyond the entry
				// count); the transaction committed normally.
				assertCommittedBind(t, openStore(t, home), op, "t1", "lease-1", "fence-1")
				return
			}
			if !errors.Is(err, errInjected) {
				t.Fatalf("Update error = %v, want injected crash", err)
			}

			s3 := openStore(t, home)
			if tc.stage == faultStageBeforeManifest {
				v := mustView(t, s3)
				cur, _ := v.Current("t1")
				if cur.Worktree != nil || cur.Revision != 1 {
					t.Fatalf("before-manifest crash must leave the pre-bind aggregate: %+v", cur)
				}
				if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(markerRel(t, "t1", 1, "lease-1")))); !os.IsNotExist(err) {
					t.Fatalf("before-manifest crash must leave no lease marker (stat err = %v)", err)
				}
				return
			}
			assertCommittedBind(t, s3, op, "t1", "lease-1", "fence-1")
			assertNoManifests(t, home)

			// Recovery is idempotent: the second view sees identical state.
			first := mustView(t, s3)
			second := mustView(t, s3)
			if !reflect.DeepEqual(first, second) {
				t.Fatal("repeated views after recovery differ")
			}
		})
	}
}

// assertCommittedBind asserts a fully committed bind transaction: the current
// aggregate carries the binding at revision 2, the lease marker is durable,
// and the durable receipt replays.
func assertCommittedBind(t *testing.T, s *Store, op taskauthority.Operation, taskID, leaseID, fenceToken string) {
	t.Helper()
	v := mustView(t, s)
	cur, ok := v.Current(taskID)
	if !ok || cur.Worktree == nil || cur.Worktree.LeaseID != leaseID || cur.Worktree.FenceToken != fenceToken || cur.Revision != 2 {
		t.Fatalf("current aggregate after bind = %+v", cur)
	}
	assertLeaseMarker(t, s.homeDir, taskID, cur.Generation, leaseID, fenceToken)
	receipt, ok := findReceipt(v.Receipts, op.ID)
	if !ok || receipt.Digest != op.Digest || receipt.TaskID != taskID || receipt.Revision != 2 {
		t.Fatalf("bind receipt = %+v", receipt)
	}
	var auditFound bool
	for _, ev := range v.Audit {
		if ev.OperationID == op.ID && ev.Kind == taskauthority.AuditBinding {
			auditFound = true
		}
	}
	if !auditFound {
		t.Fatalf("binding audit event missing: %+v", v.Audit)
	}
}

// TestWorktreeBindingManifestIncludesLeaseMarker proves the pending manifest
// pins the lease marker entry alongside the aggregate, pointer, audit, and
// receipt, and the marker payload is the home-compatible document.
func TestWorktreeBindingManifestIncludesLeaseMarker(t *testing.T) {
	home := t.TempDir()
	s := openStore(t, home)
	if _, err := s.Update(testOp("op-create", "t1"), createUpdate("op-create", "t1")); err != nil {
		t.Fatal(err)
	}
	binding := testWorktreeBinding("lease-1", "fence-1")
	op := testOp("op-bind", "t1")
	s2 := openStore(t, home)
	s2.fault = &faultInjector{stage: faultStageAfterManifest, err: errInjected}
	if _, err := s2.Update(op, bindUpdate("op-bind", "t1", binding)); !errors.Is(err, errInjected) {
		t.Fatalf("Update error = %v, want injected crash", err)
	}

	m := readManifest(t, home, "op-bind")
	if m.ExpectedGeneration != 1 {
		t.Fatalf("ExpectedGeneration = %d, want 1", m.ExpectedGeneration)
	}
	markerPath := markerRel(t, "t1", 1, "lease-1")
	markerEntry, ok := findManifestEntry(m.After, markerPath)
	if !ok {
		t.Fatalf("lease marker entry missing from manifest after entries: %+v", m.After)
	}
	if DigestHex([]byte(markerEntry.Payload)) != markerEntry.Digest {
		t.Fatalf("lease marker payload does not match its digest")
	}
	var marker struct {
		TaskID         string `json:"task_id"`
		TaskGeneration string `json:"task_generation"`
		LeaseID        string `json:"lease_id"`
		FenceToken     string `json:"fence_token"`
	}
	if err := json.Unmarshal([]byte(markerEntry.Payload), &marker); err != nil {
		t.Fatal(err)
	}
	if marker.TaskID != "t1" || marker.TaskGeneration != "1" || marker.LeaseID != "lease-1" || marker.FenceToken != "fence-1" {
		t.Fatalf("manifest lease marker payload = %+v", marker)
	}
	if m.Audit == nil || m.Audit.Kind != taskauthority.AuditBinding {
		t.Fatalf("manifest audit = %+v, want binding kind", m.Audit)
	}
}

// TestWorktreeLeaseRelPathValidatesIdentities proves the marker path builder
// fails closed on unsafe task ids, generations, and lease ids.
func TestWorktreeLeaseRelPathValidatesIdentities(t *testing.T) {
	if _, err := WorktreeLeaseRelPath("../escape", 1, "lease-1"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("unsafe task id error = %v, want ErrInvalidPath", err)
	}
	if _, err := WorktreeLeaseRelPath("t1", 0, "lease-1"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("zero generation error = %v, want ErrInvalidPath", err)
	}
	for _, leaseID := range []string{"", ".", "..", "../x", "a/b", ".hidden", "a\\b"} {
		if _, err := WorktreeLeaseRelPath("t1", 1, leaseID); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("unsafe lease id %q error = %v, want ErrInvalidPath", leaseID, err)
		}
	}
	rel, err := WorktreeLeaseRelPath("t1", 3, "lease-3")
	if err != nil {
		t.Fatal(err)
	}
	if rel != filepath.ToSlash(filepath.Join("state", ".task-authority", "v1", "worktree-leases", "t1", "3", "lease-3.json")) {
		t.Fatalf("marker rel path = %q", rel)
	}
}

// TestEncodeLeaseMarkerRejectsInvalidMarkers proves the marker encoder fails
// closed on invalid payloads.
func TestEncodeLeaseMarkerRejectsInvalidMarkers(t *testing.T) {
	if _, err := EncodeLeaseMarker(taskauthority.LeaseMarker{TaskID: "t1", TaskGeneration: 1, LeaseID: "l", FenceToken: "f"}); err != nil {
		t.Fatalf("valid marker encoding error = %v", err)
	}
	if _, err := EncodeLeaseMarker(taskauthority.LeaseMarker{TaskGeneration: 1, LeaseID: "l", FenceToken: "f"}); err == nil {
		t.Fatal("marker without task id encoded successfully")
	}
	if _, err := EncodeLeaseMarker(taskauthority.LeaseMarker{TaskID: "t1", LeaseID: "l", FenceToken: "f"}); err == nil {
		t.Fatal("marker without generation encoded successfully")
	}
}
