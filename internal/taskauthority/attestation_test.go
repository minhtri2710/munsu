package taskauthority

import (
	"errors"
	"testing"
)

// mustDeliveryPlan builds a valid bounded delivery plan: requested
// no-mistakes resolving to effective direct-PR with the fallback reason.
func mustDeliveryPlan() DeliveryPlan {
	return DeliveryPlan{
		RequestedMode:  "no-mistakes",
		EffectiveMode:  "direct-PR",
		FallbackReason: "no-mistakes not on PATH; defaulting to direct-PR",
	}
}

// mustAttestationRef builds a valid capability-attestation reference binding
// project, home, and config snapshot digest.
func mustAttestationRef() CapabilityAttestation {
	return CapabilityAttestation{
		Project:      "munsu",
		Home:         "/home/test",
		ConfigDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
}

// attestationRequest builds a valid AttachAttestation request for one task.
func attestationRequest(taskID string, generation Generation, plan DeliveryPlan, ref CapabilityAttestation) AttachAttestationRequest {
	return AttachAttestationRequest{
		OperationID:        "op-attest-" + taskID,
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             taskID,
		ExpectedGeneration: generation,
		DeliveryPlan:       plan,
		Attestation:        ref,
		Reason:             "spawn acceptance",
	}
}

// TestAttachAttestationCommitsDeliveryPlanAndEvidence proves one
// AttachAttestation operation commits the generation-bound delivery plan and
// the capability-attestation reference together with exactly one Revision
// advance, keeps the phase untouched, and persists a typed attestation audit
// event and a durable idempotency receipt.
func TestAttachAttestationCommitsDeliveryPlanAndEvidence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	plan := mustDeliveryPlan()
	ref := mustAttestationRef()

	res, err := a.AttachAttestation(attestationRequest("t1", 1, plan, ref))
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != "t1" || res.Generation != 1 || res.Revision != 2 || res.Phase != PhaseQueued || res.Replayed {
		t.Fatalf("attach result = %+v, want revision 2 queued", res)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("revision = %d, want 2", agg.Revision)
	}
	if agg.Phase != PhaseQueued {
		t.Fatalf("attestation acceptance must not change phase: %q", agg.Phase)
	}
	if agg.DeliveryPlan == nil || agg.DeliveryPlan.RequestedMode != "no-mistakes" || agg.DeliveryPlan.EffectiveMode != "direct-PR" || agg.DeliveryPlan.FallbackReason == "" {
		t.Fatalf("delivery plan = %+v", agg.DeliveryPlan)
	}
	if agg.CapabilityAttestation == nil || agg.CapabilityAttestation.Project != "munsu" || agg.CapabilityAttestation.Home != "/home/test" || agg.CapabilityAttestation.ConfigDigest == "" {
		t.Fatalf("capability attestation reference = %+v", agg.CapabilityAttestation)
	}

	// A typed attestation audit event committed with the mutation.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var attEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditAttestation {
			attEvents = append(attEvents, ev)
		}
	}
	if len(attEvents) != 1 {
		t.Fatalf("attestation audit events = %d, want 1 (%+v)", len(attEvents), v.Audit)
	}
	ev := attEvents[0]
	if ev.OperationID != "op-attest-t1" || ev.Actor.ID != "test" || ev.TaskID != "t1" ||
		ev.Generation != 1 || ev.Reason != "spawn acceptance" {
		t.Fatalf("attestation audit event = %+v", ev)
	}

	// A durable receipt pins the operation.
	var pinned *Receipt
	for i := range v.Receipts {
		if v.Receipts[i].OperationID == "op-attest-t1" {
			pinned = &v.Receipts[i]
		}
	}
	if pinned == nil || pinned.Revision != 2 || pinned.Generation != 1 {
		t.Fatalf("receipts = %+v, want pinned op-attest-t1 revision 2", v.Receipts)
	}
}

// TestAttachAttestationGenerationFence proves the Expected Generation fence
// rejects a stale generation and a missing task, mutating nothing.
func TestAttachAttestationGenerationFence(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	plan := mustDeliveryPlan()
	ref := mustAttestationRef()

	if _, err := a.AttachAttestation(attestationRequest("t1", 7, plan, ref)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation error = %v, want ErrConflict", err)
	}
	if _, err := a.AttachAttestation(attestationRequest("missing", 1, plan, ref)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task error = %v, want ErrNotFound", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 || agg.DeliveryPlan != nil || agg.CapabilityAttestation != nil {
		t.Fatalf("failed acceptance must not mutate: %+v", agg)
	}
}

// TestAttachAttestationIdempotentReplay proves repeating the same Operation
// ID with the same intent replays the original receipt: the delivery plan and
// attestation reference are preserved, no second audit event commits, and the
// Revision does not advance twice.
func TestAttachAttestationIdempotentReplay(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	req := attestationRequest("t1", 1, mustDeliveryPlan(), mustAttestationRef())

	first, err := a.AttachAttestation(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.AttachAttestation(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("second acceptance must report Replayed=true")
	}
	if second.Revision != first.Revision || second.Generation != first.Generation {
		t.Fatalf("replayed result = %+v, want original %+v", second, first)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 2 {
		t.Fatalf("replay advanced revision to %d, want 2", agg.Revision)
	}
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	var attEvents []AuditEvent
	for _, ev := range v.Audit {
		if ev.Kind == AuditAttestation {
			attEvents = append(attEvents, ev)
		}
	}
	if len(attEvents) != 1 {
		t.Fatalf("replay must not commit a second audit event: %d events", len(attEvents))
	}
}

// TestAttachAttestationChangedDigestConflicts proves reusing the Operation ID
// with a changed delivery plan is a typed non-retryable conflict that
// preserves the original plan.
func TestAttachAttestationChangedDigestConflicts(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	plan := mustDeliveryPlan()
	ref := mustAttestationRef()
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, plan, ref)); err != nil {
		t.Fatal(err)
	}

	// Same Operation ID but the effective mode changed (direct-PR → local-only).
	changed := plan
	changed.EffectiveMode = "local-only"
	changed.FallbackReason = "direct-PR unavailable; falling back to local-only"
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, changed, ref)); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed digest error = %v, want ErrOperationConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryPlan == nil || agg.DeliveryPlan.EffectiveMode != "direct-PR" {
		t.Fatalf("original plan must be preserved: %+v", agg.DeliveryPlan)
	}
	if agg.Revision != 2 {
		t.Fatalf("conflicting retry must not advance revision: %d", agg.Revision)
	}
}

// TestAttachAttestationBoundedTransitions proves the delivery mode is bounded
// to one acceptance per generation: a second distinct attachment under a new
// Operation ID fails closed even with identical content, so the delivery mode
// can never be mutated again within the generation.
func TestAttachAttestationBoundedTransitions(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	plan := mustDeliveryPlan()
	ref := mustAttestationRef()
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, plan, ref)); err != nil {
		t.Fatal(err)
	}

	// A second attachment with a fresh Operation ID and identical content must
	// still fail closed: the generation already accepted its plan.
	again := attestationRequest("t1", 1, plan, ref)
	again.OperationID = "op-attest-t1-retry"
	if _, err := a.AttachAttestation(again); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-attachment error = %v, want ErrConflict", err)
	}

	// A second attachment with a changed effective mode must also fail closed.
	changed := plan
	changed.EffectiveMode = "local-only"
	changed.FallbackReason = "mode changed by operator"
	req := attestationRequest("t1", 1, changed, ref)
	req.OperationID = "op-attest-t1-change"
	if _, err := a.AttachAttestation(req); !errors.Is(err, ErrConflict) {
		t.Fatalf("mode change error = %v, want ErrConflict", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.DeliveryPlan == nil || agg.DeliveryPlan.EffectiveMode != "direct-PR" {
		t.Fatalf("bounded plan mutated: %+v", agg.DeliveryPlan)
	}
	if agg.Revision != 2 {
		t.Fatalf("bounded re-attachment advanced revision to %d, want 2", agg.Revision)
	}
}

// TestAttachAttestationRejectsInvalidRecords proves a malformed delivery plan
// or attestation reference is rejected without mutation, keeping the runtime
// observation outside the Aggregate.
func TestAttachAttestationRejectsInvalidRecords(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	ref := mustAttestationRef()

	// Empty requested mode.
	plan := mustDeliveryPlan()
	plan.RequestedMode = ""
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, plan, ref)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty requested mode error = %v, want ErrInvalidInput", err)
	}

	// Empty effective mode.
	plan = mustDeliveryPlan()
	plan.EffectiveMode = ""
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, plan, ref)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty effective mode error = %v, want ErrInvalidInput", err)
	}

	// Modes differ without a fallback reason: a silent mode change is never
	// accepted as authoritative evidence.
	plan = mustDeliveryPlan()
	plan.FallbackReason = ""
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, plan, ref)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing fallback reason error = %v, want ErrInvalidInput", err)
	}

	// Unsafe mode identity.
	plan = mustDeliveryPlan()
	plan.RequestedMode = "no-mistakes/../../evil"
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, plan, ref)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe mode error = %v, want ErrInvalidInput", err)
	}

	// Empty attestation reference project.
	badRef := ref
	badRef.Project = ""
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, plan, badRef)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty reference project error = %v, want ErrInvalidInput", err)
	}

	// Empty attestation reference home.
	badRef = ref
	badRef.Home = ""
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, plan, badRef)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty reference home error = %v, want ErrInvalidInput", err)
	}

	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Revision != 1 || agg.DeliveryPlan != nil || agg.CapabilityAttestation != nil {
		t.Fatalf("rejected acceptance must not mutate: %+v", agg)
	}
}

// TestAttachAttestationAcrossReopen proves the delivery plan is revisioned
// within the Task Generation: a reopened generation starts without a plan and
// accepts its own attestation, leaving the prior generation's plan immutable.
func TestAttachAttestationAcrossReopen(t *testing.T) {
	a := newTestAuthority(t)
	createTask(t, a, "t1")
	if _, err := a.AttachAttestation(attestationRequest("t1", 1, mustDeliveryPlan(), mustAttestationRef())); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Complete(CompleteRequest{
		OperationID: "op-done-t1", Actor: Actor{ID: "test", Rank: "general"},
		TaskID: "t1", ExpectedGeneration: 1, To: PhaseResolved, Reason: "resolved",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := a.Reopen(ReopenRequest{
		OperationID:        "op-reopen-t1",
		Actor:              Actor{ID: "test", Rank: "general"},
		TaskID:             "t1",
		ExpectedGeneration: 1,
		Reason:             "redo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Generation != 2 {
		t.Fatalf("reopen generation = %d, want 2", res.Generation)
	}

	// The new generation carries no delivery plan and accepts its own.
	gen2 := attestationRequest("t1", 2, mustDeliveryPlan(), mustAttestationRef())
	gen2.OperationID = "op-attest-t1-gen2"
	if _, err := a.AttachAttestation(gen2); err != nil {
		t.Fatalf("attach on reopened generation: %v", err)
	}
	agg, err := a.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Generation != 2 || agg.Revision != 2 || agg.DeliveryPlan == nil {
		t.Fatalf("reopened aggregate = %+v", agg)
	}

	// The prior generation's plan stays immutable historical state.
	v, err := a.store.View()
	if err != nil {
		t.Fatal(err)
	}
	old, ok := v.Aggregate("t1", 1)
	if !ok || old.DeliveryPlan == nil || old.DeliveryPlan.EffectiveMode != "direct-PR" {
		t.Fatalf("prior generation plan = %+v", old.DeliveryPlan)
	}
}
