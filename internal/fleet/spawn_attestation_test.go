//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// mustSpawnedAttestationTask seeds a task through create + worktree binding +
// confirm spawn (the working task the attestation binds to) and returns the
// Runner with an attestation ready to accept. The canonical cutover removed
// the legacy delivery-plan/attestation aggregate evidence (no canonical
// primitive survived Task 8.2): the accepted attestation is a runtime
// observation projected into .meta, and the canonical aggregate pins the
// generation-scoped bindings and working phase only.
func mustSpawnedAttestationTask(t *testing.T, homeDir, taskID string) (*Runner, *taskauthority.Canonical) {
	t.Helper()
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, taskID, "ship", "test-proj")
	bindWorktreeForSpawnFixture(t, auth, taskID)
	r := &Runner{
		homeDir:       homeDir,
		args:          Args{ID: taskID, ProjectName: "test-proj", Authority: auth},
		windowID:      "session:pane-1",
		effectiveMode: "direct-PR",
		endpoint: CreatedEndpoint{
			Backend: "herdr",
			Handle:  "session:pane-1",
		},
	}
	spawned, err := r.confirmSpawn()
	if err != nil {
		t.Fatalf("confirmSpawn: %v", err)
	}
	if spawned.Generation != 1 {
		t.Fatalf("confirmSpawn generation = %d, want 1", spawned.Generation)
	}
	r.attestation = CreateCapabilityAttestation(
		"test-proj", homeDir, "pi", "pi",
		"no-mistakes", "direct-PR", "no-mistakes not on PATH; defaulting to direct-PR",
		nil,
	)
	return r, auth
}

// TestSpawnAttachAttestationCommitsAcceptedEvidence proves the post-confirm
// spawn step accepts the capability attestation: the canonical aggregate
// stays working on the exact generation the ConfirmSpawn receipt supplied,
// and the .meta projection carries the accepted attestation reference and the
// delivery plan.
func TestSpawnAttachAttestationCommitsAcceptedEvidence(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-spawn-attach"
	r, auth := mustSpawnedAttestationTask(t, homeDir, taskID)

	if err := r.attachAttestation(1); err != nil {
		t.Fatalf("attachAttestation: %v", err)
	}

	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseWorking {
		t.Fatalf("attestation acceptance must not change phase: %q", agg.Phase)
	}

	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaCapabilityAttestation] == "" || meta[MetaRequestedMode] != "no-mistakes" || meta[MetaEffectiveMode] != "direct-PR" {
		t.Fatalf("projection meta = %v", meta)
	}
}

// TestSpawnWriteTaskMetaKeepsAttestationRuntimeOnly proves the pre-transition
// side file writes runtime observations only: the attestation fields are not
// written before the acceptance, and appear only after attachAttestation
// projects them.
func TestSpawnWriteTaskMetaKeepsAttestationRuntimeOnly(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-spawn-side-file"
	r, auth := mustSpawnedAttestationTask(t, homeDir, taskID)

	if err := r.writeTaskMeta(); err != nil {
		t.Fatalf("writeTaskMeta: %v", err)
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	for _, key := range []string{MetaCapabilityAttestation, MetaRequestedMode, MetaEffectiveMode, MetaFallbackReason} {
		if _, ok := meta[key]; ok {
			t.Errorf("writeTaskMeta must not write %q before the acceptance", key)
		}
	}
	if meta["mode"] != "direct-PR" {
		t.Errorf("runtime observation mode = %q, want direct-PR", meta["mode"])
	}

	// After the post-confirm acceptance, the projection carries the fields.
	if err := r.attachAttestation(1); err != nil {
		t.Fatalf("attachAttestation: %v", err)
	}
	meta, err = home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaEffectiveMode] != "direct-PR" || meta[MetaRequestedMode] != "no-mistakes" {
		t.Fatalf("projected meta = %v", meta)
	}
	if _, err := auth.Get(mustTaskID(t, taskID)); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

// TestSpawnAttachAttestationRejectedKeepsObservationRuntimeOnly proves a
// rejected acceptance leaves the runtime observation outside the canonical
// aggregate: a malformed attestation reference fails closed and the task
// stays working with its bindings unchanged.
func TestSpawnAttachAttestationRejectedKeepsObservationRuntimeOnly(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-spawn-rejected"
	r, auth := mustSpawnedAttestationTask(t, homeDir, taskID)

	// An attestation with an empty project cannot be accepted; it stays a
	// runtime observation.
	r.attestation = CreateCapabilityAttestation(
		"", homeDir, "pi", "pi",
		"no-mistakes", "direct-PR", "no-mistakes not on PATH; defaulting to direct-PR",
		nil,
	)
	if err := r.attachAttestation(1); err == nil {
		t.Fatal("expected rejection for malformed attestation reference")
	}

	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 3 {
		t.Fatalf("revision = %d, want 3 (create, bind worktree, confirm spawn)", agg.Revision)
	}
	// The task is still working: the acceptance is evidence, not a lifecycle gate.
	if agg.Phase != taskauthority.PhaseWorking {
		t.Fatalf("phase = %q, want working", agg.Phase)
	}
}

// TestSpawnAttachAttestationFailsClosedWithoutAuthority proves the spawn
// attestation step fails closed when no Authority is composed.
func TestSpawnAttachAttestationFailsClosedWithoutAuthority(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-spawn-no-auth"
	r := &Runner{
		homeDir: homeDir,
		args:    Args{ID: taskID}, // no Authority composed
		attestation: CreateCapabilityAttestation(
			"test-proj", homeDir, "pi", "pi",
			"no-mistakes", "direct-PR", "no-mistakes not on PATH; defaulting to direct-PR",
			nil,
		),
	}
	if err := r.attachAttestation(1); err == nil || !strings.Contains(err.Error(), "not composed") {
		t.Fatalf("attachAttestation without Authority error = %v, want composition failure", err)
	}
}

// TestSpawnAttachAttestationProjectionFailureNeverRollsBack proves the typed
// partial projection outcome: when the .meta projection cannot be written the
// acceptance stays projected-or-failed without mutating the canonical
// aggregate (the canonical surface carries no attestation state to roll back).
func TestSpawnAttachAttestationProjectionFailureNeverRollsBack(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-spawn-proj-fail"
	r, auth := mustSpawnedAttestationTask(t, homeDir, taskID)

	// Block the projection write by making state a regular file.
	if err := os.WriteFile(filepath.Join(homeDir, "state"), []byte("block"), 0644); err != nil {
		t.Fatal(err)
	}
	err := r.attachAttestation(1)
	var typed *AttestationProjectionError
	if !errors.As(err, &typed) {
		t.Fatalf("attachAttestation error = %v, want *AttestationProjectionError", err)
	}
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if agg.Phase != taskauthority.PhaseWorking || agg.Revision != 3 {
		t.Fatalf("canonical aggregate must stay working at revision 3 across the projection failure: %+v", agg)
	}
}
