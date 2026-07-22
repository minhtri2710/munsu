package captain

import (
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/marker"
)

func TestNewEnvelopeID_ProducesHex(t *testing.T) {
	id, err := NewEnvelopeID()
	if err != nil {
		t.Fatalf("NewEnvelopeID: %v", err)
	}
	if len(id) != 32 {
		t.Errorf("envelope ID length=%d, want 32", len(id))
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %c in envelope ID", c)
			break
		}
	}
}

func TestCreateEnvelope_SetsFields(t *testing.T) {
	home := t.TempDir()
	env := &CommandEnvelope{
		TargetCaptainID: "test-cap",
		Message:         "run deploy",
	}
	created, err := CreateEnvelope(home, env)
	if err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}
	if !created {
		t.Fatal("CreateEnvelope returned noop=false, want true")
	}
	if env.EnvelopeID == "" {
		t.Fatal("EnvelopeID should be auto-generated")
	}
	if env.SchemaVersion != SchemaVersionEnvelope {
		t.Errorf("SchemaVersion=%q, want %q", env.SchemaVersion, SchemaVersionEnvelope)
	}
	if env.Status != EnvelopeStatusPending {
		t.Errorf("Status=%q, want pending", env.Status)
	}
	if env.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}

	// Verify file exists.
	path := envelopePath(home, env.EnvelopeID)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("envelope file not created: %v", err)
	}
}

func TestCreateEnvelope_IdempotentNoop(t *testing.T) {
	home := t.TempDir()
	key := "deploy-v2"

	env1 := &CommandEnvelope{
		TargetCaptainID: "cap-a",
		IdempotencyKey:  key,
		Message:         "deploy v2",
	}
	created, err := CreateEnvelope(home, env1)
	if err != nil {
		t.Fatalf("first CreateEnvelope: %v", err)
	}
	if !created {
		t.Fatal("first creation should succeed")
	}

	// Same idempotency key, different message.
	env2 := &CommandEnvelope{
		TargetCaptainID: "cap-a",
		IdempotencyKey:  key,
		Message:         "deploy v2 again",
	}
	created, err = CreateEnvelope(home, env2)
	if err != nil {
		t.Fatalf("second CreateEnvelope: %v", err)
	}
	if created {
		t.Fatal("second create with same idempotency key should be no-op")
	}

	// Only one envelope should exist.
	envs, err := ListPendingEnvelopes(home, "cap-a")
	if err != nil {
		t.Fatalf("ListPendingEnvelopes: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("expected 1 pending envelope, got %d", len(envs))
	}
	if envs[0].Message != "deploy v2" {
		t.Errorf("message changed after idempotent no-op: %q", envs[0].Message)
	}
}

func TestCreateEnvelope_IdempotentNoop_DifferentCaptain(t *testing.T) {
	home := t.TempDir()
	key := "shared-key"

	env1 := &CommandEnvelope{
		TargetCaptainID: "cap-a",
		IdempotencyKey:  key,
		Message:         "for cap-a",
	}
	created, err := CreateEnvelope(home, env1)
	if err != nil {
		t.Fatalf("first CreateEnvelope: %v", err)
	}
	if !created {
		t.Fatal("first creation should succeed")
	}

	// Same idempotency key but different captain — should also be no-op
	// (idempotency is global, not per-captain, since it's the General's key).
	env2 := &CommandEnvelope{
		TargetCaptainID: "cap-b",
		IdempotencyKey:  key,
		Message:         "for cap-b",
	}
	created, err = CreateEnvelope(home, env2)
	if err != nil {
		t.Fatalf("second CreateEnvelope: %v", err)
	}
	if created {
		t.Fatal("second create with same idempotency key should be no-op regardless of target")
	}
}

func TestCreateEnvelope_NoIdempotencyKeyAllowsDuplicates(t *testing.T) {
	home := t.TempDir()
	env1 := &CommandEnvelope{
		TargetCaptainID: "cap-a",
		Message:         "first",
	}
	created, err := CreateEnvelope(home, env1)
	if err != nil {
		t.Fatalf("first CreateEnvelope: %v", err)
	}
	if !created {
		t.Fatal("first should be created")
	}

	env2 := &CommandEnvelope{
		TargetCaptainID: "cap-a",
		Message:         "second",
	}
	created, err = CreateEnvelope(home, env2)
	if err != nil {
		t.Fatalf("second CreateEnvelope: %v", err)
	}
	if !created {
		t.Fatal("second without idempotency key should also be created")
	}

	envs, err := ListPendingEnvelopes(home, "cap-a")
	if err != nil {
		t.Fatalf("ListPendingEnvelopes: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("expected 2 pending envelopes, got %d", len(envs))
	}
}

func TestGetEnvelope_NotFound(t *testing.T) {
	home := t.TempDir()
	env, err := GetEnvelope(home, "nonexistent")
	if err != nil {
		t.Fatalf("GetEnvelope: %v", err)
	}
	if env != nil {
		t.Fatal("expected nil for missing envelope")
	}
}

func TestGetEnvelope_RoundTrip(t *testing.T) {
	home := t.TempDir()
	orig := &CommandEnvelope{
		TargetCaptainID:  "roundtrip",
		IdempotencyKey:   "rt-key",
		TargetTasks:      []string{"task-1", "task-2"},
		AllowedActions:   []string{"deploy", "rollback"},
		ForbiddenActions: []string{"delete"},
		RequireReady:     true,
		Message:          "run deploy",
	}
	created, err := CreateEnvelope(home, orig)
	if err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}
	if !created {
		t.Fatal("should create")
	}

	got, err := GetEnvelope(home, orig.EnvelopeID)
	if err != nil {
		t.Fatalf("GetEnvelope: %v", err)
	}
	if got == nil {
		t.Fatal("GetEnvelope returned nil")
	}
	if got.EnvelopeID != orig.EnvelopeID {
		t.Errorf("EnvelopeID mismatch")
	}
	if got.TargetCaptainID != "roundtrip" {
		t.Errorf("TargetCaptainID=%q", got.TargetCaptainID)
	}
	if len(got.TargetTasks) != 2 || got.TargetTasks[0] != "task-1" {
		t.Errorf("TargetTasks=%v", got.TargetTasks)
	}
	if len(got.AllowedActions) != 2 || got.AllowedActions[0] != "deploy" {
		t.Errorf("AllowedActions=%v", got.AllowedActions)
	}
	if !got.RequireReady {
		t.Error("RequireReady should be true")
	}
}

func TestListPendingEnvelopes_FiltersByCaptainID(t *testing.T) {
	home := t.TempDir()

	for _, id := range []string{"cap-a", "cap-b", "cap-a"} {
		CreateEnvelope(home, &CommandEnvelope{
			TargetCaptainID: id,
			Message:         "msg-" + id,
		})
	}

	capAEnvs, err := ListPendingEnvelopes(home, "cap-a")
	if err != nil {
		t.Fatalf("ListPendingEnvelopes cap-a: %v", err)
	}
	if len(capAEnvs) != 2 {
		t.Fatalf("cap-a: want 2, got %d", len(capAEnvs))
	}

	capBEnvs, err := ListPendingEnvelopes(home, "cap-b")
	if err != nil {
		t.Fatalf("ListPendingEnvelopes cap-b: %v", err)
	}
	if len(capBEnvs) != 1 {
		t.Fatalf("cap-b: want 1, got %d", len(capBEnvs))
	}
}

func TestMarkEnvelopeDelivered(t *testing.T) {
	home := t.TempDir()
	env := &CommandEnvelope{
		TargetCaptainID: "cap-a",
		Message:         "test",
	}
	CreateEnvelope(home, env)

	if err := MarkEnvelopeDelivered(home, env.EnvelopeID); err != nil {
		t.Fatalf("MarkEnvelopeDelivered: %v", err)
	}

	got, _ := GetEnvelope(home, env.EnvelopeID)
	if got.Status != EnvelopeStatusDelivered {
		t.Errorf("status=%q, want delivered", got.Status)
	}
	if got.DeliveredAt == 0 {
		t.Error("DeliveredAt should be set")
	}
}

func TestMarkEnvelopeCompleted(t *testing.T) {
	home := t.TempDir()
	env := &CommandEnvelope{
		TargetCaptainID: "cap-a",
		Message:         "test",
	}
	CreateEnvelope(home, env)

	if err := MarkEnvelopeCompleted(home, env.EnvelopeID); err != nil {
		t.Fatalf("MarkEnvelopeCompleted: %v", err)
	}

	got, _ := GetEnvelope(home, env.EnvelopeID)
	if got.Status != EnvelopeStatusCompleted {
		t.Errorf("status=%q, want completed", got.Status)
	}
}

func TestMarkDelivered_ExcludesFromPending(t *testing.T) {
	home := t.TempDir()
	env := &CommandEnvelope{
		TargetCaptainID: "cap-a",
		Message:         "test",
	}
	CreateEnvelope(home, env)
	MarkEnvelopeDelivered(home, env.EnvelopeID)

	pending, err := ListPendingEnvelopes(home, "cap-a")
	if err != nil {
		t.Fatalf("ListPendingEnvelopes: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("delivered envelope should not be pending, got %d", len(pending))
	}
}

func TestPushEnvelopeToCaptain(t *testing.T) {
	parentHome := t.TempDir()
	captainHome := t.TempDir()
	env := &CommandEnvelope{
		TargetCaptainID: "test-cap",
		Message:         "deploy",
	}
	CreateEnvelope(parentHome, env)

	if err := PushEnvelopeToCaptain(parentHome, captainHome, env); err != nil {
		t.Fatalf("PushEnvelopeToCaptain: %v", err)
	}

	// Captain should be able to read it.
	got, err := GetCaptainEnvelope(captainHome, env.EnvelopeID)
	if err != nil {
		t.Fatalf("GetCaptainEnvelope: %v", err)
	}
	if got == nil {
		t.Fatal("envelope not found in captain home")
	}
	if got.Message != "deploy" {
		t.Errorf("message=%q, want deploy", got.Message)
	}
	if got.SchemaVersion != SchemaVersionEnvelope {
		t.Errorf("schema=%q", got.SchemaVersion)
	}
}

func TestFlushEnvelopeSend_DeliversWhenAlive(t *testing.T) {
	parent := t.TempDir()
	smHome := t.TempDir()
	smID := "live-cap"
	window := "@w"

	env := &CommandEnvelope{
		TargetCaptainID: smID,
		Message:         "run deploy",
	}
	CreateEnvelope(parent, env)

	writeCaptainMeta(t, parent, smID, smHome, window)

	fake := &outboxFakeBackend{alive: true, windowID: window}
	installOutboxBackend(t, fake)

	if err := FlushEnvelopeSend(parent, Info{ID: smID, Home: smHome}); err != nil {
		t.Fatalf("FlushEnvelopeSend: %v", err)
	}

	// Should have sent the marked message.
	if len(fake.sent) != 1 {
		t.Fatalf("sent=%d, want 1: %v", len(fake.sent), fake.sent)
	}
	if !marker.IsFromGeneral(fake.sent[0]) {
		t.Error("delivered message must carry from-general marker")
	}
	if !strings.Contains(fake.sent[0], "run deploy") {
		t.Errorf("message=%q, want 'run deploy'", fake.sent[0])
	}

	// Envelope should be delivered in parent home.
	got, _ := GetEnvelope(parent, env.EnvelopeID)
	if got.Status != EnvelopeStatusDelivered {
		t.Errorf("status=%q, want delivered", got.Status)
	}

	// Should be pushed to captain home.
	capEnv, err := GetCaptainEnvelope(smHome, env.EnvelopeID)
	if err != nil {
		t.Fatalf("GetCaptainEnvelope: %v", err)
	}
	if capEnv == nil {
		t.Fatal("envelope not found in captain home")
	}
}

func TestFlushEnvelopeSend_RetainsWhenDead(t *testing.T) {
	parent := t.TempDir()
	smHome := t.TempDir()
	smID := "dead-cap"
	window := "@dead"

	env := &CommandEnvelope{
		TargetCaptainID: smID,
		Message:         "important",
	}
	CreateEnvelope(parent, env)
	writeCaptainMeta(t, parent, smID, smHome, window)

	fake := &outboxFakeBackend{alive: false, windowID: window}
	installOutboxBackend(t, fake)

	err := FlushEnvelopeSend(parent, Info{ID: smID, Home: smHome})
	if err == nil {
		t.Fatal("expected error when endpoint dead")
	}
	if !strings.Contains(err.Error(), "endpoint-dead") {
		t.Errorf("error should mention endpoint-dead: %v", err)
	}

	// Envelope should still be pending.
	got, _ := GetEnvelope(parent, env.EnvelopeID)
	if got.Status != EnvelopeStatusPending {
		t.Errorf("status=%q, want pending", got.Status)
	}

	// Should not be pushed to captain home.
	capEnv, _ := GetCaptainEnvelope(smHome, env.EnvelopeID)
	if capEnv != nil {
		t.Fatal("envelope should not be pushed to dead captain")
	}
}

func TestFlushEnvelopeSend_DuplicateDeliveryIsNoop(t *testing.T) {
	parent := t.TempDir()
	smHome := t.TempDir()
	smID := "dup-cap"
	window := "@dup"

	env := &CommandEnvelope{
		TargetCaptainID: smID,
		IdempotencyKey:  "dup-test",
		Message:         "run once",
	}
	CreateEnvelope(parent, env)
	writeCaptainMeta(t, parent, smID, smHome, window)

	fake := &outboxFakeBackend{alive: true, windowID: window}
	installOutboxBackend(t, fake)

	// First delivery succeeds.
	if err := FlushEnvelopeSend(parent, Info{ID: smID, Home: smHome}); err != nil {
		t.Fatalf("first flush: %v", err)
	}
	if len(fake.sent) != 1 {
		t.Fatalf("sent=%d after first flush, want 1", len(fake.sent))
	}

	// Second flush: envelope is already delivered, should be no-op.
	if err := FlushEnvelopeSend(parent, Info{ID: smID, Home: smHome}); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	if len(fake.sent) != 1 {
		t.Fatalf("sent=%d after second flush, want 1 (duplicate)", len(fake.sent))
	}
}

func TestFlushEnvelopeSend_NoEnvelopesIsNoop(t *testing.T) {
	parent := t.TempDir()
	smHome := t.TempDir()
	smID := "noop-cap"
	window := "@noop"
	writeCaptainMeta(t, parent, smID, smHome, window)

	fake := &outboxFakeBackend{alive: true, windowID: window}
	installOutboxBackend(t, fake)

	// No envelopes pending — should be nil error, no sends.
	if err := FlushEnvelopeSend(parent, Info{ID: smID, Home: smHome}); err != nil {
		t.Fatalf("FlushEnvelopeSend: %v", err)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("unexpected sends: %v", fake.sent)
	}
}

func TestGetCaptainEnvelope_NotFound(t *testing.T) {
	home := t.TempDir()
	env, err := GetCaptainEnvelope(home, "nonexistent")
	if err != nil {
		t.Fatalf("GetCaptainEnvelope: %v", err)
	}
	if env != nil {
		t.Fatal("expected nil for missing envelope")
	}
}

func TestCreateEnvelope_GeneratesIDWhenEmpty(t *testing.T) {
	home := t.TempDir()
	env := &CommandEnvelope{
		TargetCaptainID: "cap",
		Message:         "test",
		EnvelopeID:      "", // explicitly empty
	}
	created, err := CreateEnvelope(home, env)
	if err != nil {
		t.Fatalf("CreateEnvelope: %v", err)
	}
	if !created {
		t.Fatal("should create")
	}
	if env.EnvelopeID == "" {
		t.Fatal("EnvelopeID should be auto-generated")
	}
}
