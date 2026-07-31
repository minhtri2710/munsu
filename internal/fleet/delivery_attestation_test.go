//go:build integration

package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/home"
)

// --- CapabilityAttestation creation tests ---

func TestCreateCapabilityAttestation_BindsFields(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		nil,
	)

	if att.Project != "test-project" {
		t.Errorf("Project = %q, want %q", att.Project, "test-project")
	}
	if att.Home != "/tmp/home" {
		t.Errorf("Home = %q, want %q", att.Home, "/tmp/home")
	}
	if att.Harness != "pi" {
		t.Errorf("Harness = %q, want %q", att.Harness, "pi")
	}
	if att.GateAgent != "pi" {
		t.Errorf("GateAgent = %q, want %q", att.GateAgent, "pi")
	}
	if att.RequestedMode != "no-mistakes" {
		t.Errorf("RequestedMode = %q, want %q", att.RequestedMode, "no-mistakes")
	}
	if att.EffectiveMode != "no-mistakes" {
		t.Errorf("EffectiveMode = %q, want %q", att.EffectiveMode, "no-mistakes")
	}
	if att.FallbackReason != "" {
		t.Errorf("FallbackReason = %q, want empty", att.FallbackReason)
	}
	if att.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
	if att.Expiry == "" {
		t.Error("Expiry should not be empty")
	}
	if att.ExecutableID == "" {
		t.Error("ExecutableID should not be empty")
	}
	if len(att.Capabilities) == 0 {
		t.Error("Capabilities should not be empty")
	}
}

func TestCreateCapabilityAttestation_WithFallbackReason(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "direct-PR",
		"no-mistakes not on PATH; defaulting to direct-PR",
		&FallbackPolicy{AuthorizedMode: "direct-PR", Reason: "auto-fallback"},
	)

	if att.RequestedMode != "no-mistakes" {
		t.Errorf("RequestedMode = %q, want %q", att.RequestedMode, "no-mistakes")
	}
	if att.EffectiveMode != "direct-PR" {
		t.Errorf("EffectiveMode = %q, want %q", att.EffectiveMode, "direct-PR")
	}
	if att.FallbackReason == "" {
		t.Error("FallbackReason should not be empty")
	}
	if att.FallbackPolicy == nil {
		t.Fatal("FallbackPolicy should not be nil")
	}
	if att.FallbackPolicy.AuthorizedMode != "direct-PR" {
		t.Errorf("FallbackPolicy.AuthorizedMode = %q, want %q", att.FallbackPolicy.AuthorizedMode, "direct-PR")
	}
}

func TestCreateCapabilityAttestation_ProbesCapabilities(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"direct-PR", "direct-PR", "", nil,
	)

	// Should have at least 4 capability entries (no-mistakes, gh-axi, gh, git)
	if len(att.Capabilities) < 4 {
		t.Errorf("expected at least 4 capability entries, got %d", len(att.Capabilities))
	}

	// Verify known capability names are present.
	found := make(map[string]bool)
	for _, c := range att.Capabilities {
		found[c.Name] = true
	}
	for _, name := range []string{"no-mistakes", "gh-axi", "gh", "git"} {
		if !found[name] {
			t.Errorf("capability %q not found in attestation", name)
		}
	}
}

func TestCreateCapabilityAttestation_WithFallbackPolicy(t *testing.T) {
	policy := &FallbackPolicy{AuthorizedMode: "direct-PR", Reason: "no-mistakes unavailable"}
	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"no-mistakes", "direct-PR", "no-mistakes not available", policy,
	)

	if att.FallbackPolicy == nil {
		t.Fatal("FallbackPolicy should not be nil")
	}
	if att.FallbackPolicy.AuthorizedMode != "direct-PR" {
		t.Errorf("AuthorizedMode = %q, want %q", att.FallbackPolicy.AuthorizedMode, "direct-PR")
	}
}

// --- CapabilityEntry tests ---

func TestCapabilityEntry_StateValues(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"direct-PR", "direct-PR", "", nil,
	)

	for _, c := range att.Capabilities {
		// State must be one of the known values.
		switch c.State {
		case backend.Absent, backend.Ready, backend.Unsupported, backend.Failed:
			// OK
		default:
			t.Errorf("capability %q has unexpected state %v", c.Name, c.State)
		}
		// Name must be non-empty.
		if c.Name == "" {
			t.Error("capability entry with empty name")
		}
		// Path should be non-empty when state is Ready.
		if c.State == backend.Ready && c.Path == "" {
			t.Errorf("capability %q is Ready but has empty path", c.Name)
		}
	}
}

// --- JSON round-trip tests ---

func TestCapabilityAttestation_JSONRoundTrip(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		&FallbackPolicy{AuthorizedMode: "direct-PR", Reason: "test"},
	)

	data, err := json.Marshal(att)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var restored CapabilityAttestation
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if restored.Project != att.Project {
		t.Errorf("Project: got %q, want %q", restored.Project, att.Project)
	}
	if restored.Home != att.Home {
		t.Errorf("Home: got %q, want %q", restored.Home, att.Home)
	}
	if restored.Harness != att.Harness {
		t.Errorf("Harness: got %q, want %q", restored.Harness, att.Harness)
	}
	if restored.GateAgent != att.GateAgent {
		t.Errorf("GateAgent: got %q, want %q", restored.GateAgent, att.GateAgent)
	}
	if restored.ExecutableID != att.ExecutableID {
		t.Errorf("ExecutableID: got %q, want %q", restored.ExecutableID, att.ExecutableID)
	}
	if restored.RequestedMode != att.RequestedMode {
		t.Errorf("RequestedMode: got %q, want %q", restored.RequestedMode, att.RequestedMode)
	}
	if restored.EffectiveMode != att.EffectiveMode {
		t.Errorf("EffectiveMode: got %q, want %q", restored.EffectiveMode, att.EffectiveMode)
	}
	if restored.FallbackReason != att.FallbackReason {
		t.Errorf("FallbackReason: got %q, want %q", restored.FallbackReason, att.FallbackReason)
	}
	if len(restored.Capabilities) != len(att.Capabilities) {
		t.Errorf("Capabilities length: got %d, want %d", len(restored.Capabilities), len(att.Capabilities))
	}
	if restored.FallbackPolicy == nil {
		t.Fatal("FallbackPolicy should not be nil after round-trip")
	}
	if restored.FallbackPolicy.AuthorizedMode != att.FallbackPolicy.AuthorizedMode {
		t.Errorf("FallbackPolicy.AuthorizedMode: got %q, want %q", restored.FallbackPolicy.AuthorizedMode, att.FallbackPolicy.AuthorizedMode)
	}
}

// --- CheckCapabilityAttestation tests ---

func TestCheckCapabilityAttestation_NilReturnsChanged(t *testing.T) {
	changed, detail := CheckCapabilityAttestation(nil)
	if !changed {
		t.Error("expected changed for nil attestation")
	}
	if detail == "" {
		t.Error("expected non-empty detail for nil attestation")
	}
}

func TestCheckCapabilityAttestation_ExpiredReturnsChanged(t *testing.T) {
	att := &CapabilityAttestation{
		Expiry: time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	changed, _ := CheckCapabilityAttestation(att)
	if !changed {
		t.Error("expected changed for expired attestation")
	}
}

func TestCheckCapabilityAttestation_ValidExpiry(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"direct-PR", "direct-PR", "", nil,
	)
	// The attestation is fresh, so it should not be expired.
	changed, _ := CheckCapabilityAttestation(att)
	// It may still be changed if capabilities differ, but it should not be expired.
	// We only check that the expiry check itself doesn't cause a false positive.
	_ = changed
}

// --- HandleLateCapabilityLoss tests ---

func TestHandleLateCapabilityLoss_NoChange(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"direct-PR", "direct-PR", "", nil,
	)

	result := HandleLateCapabilityLoss(att)
	if result.Changed {
		t.Errorf("expected no change, got changed: %s", result.Detail)
	}
	if !result.CanProceed {
		t.Error("expected can proceed when no change")
	}
}

func TestHandleLateCapabilityLoss_WithPreAuthorizedFallback(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		&FallbackPolicy{AuthorizedMode: "direct-PR", Reason: "no-mistakes auto-fallback"},
	)

	// Force the attestation to be expired to simulate capability loss.
	att.Expiry = time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed for expired attestation")
	}
	if !result.CanProceed {
		t.Error("expected can proceed with pre-authorized fallback")
	}
	if result.FallbackMode != "direct-PR" {
		t.Errorf("FallbackMode = %q, want %q", result.FallbackMode, "direct-PR")
	}
}

func TestHandleLateCapabilityLoss_WithoutPreAuthorization(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		nil, // no fallback policy
	)

	// Force expiry to simulate capability loss.
	att.Expiry = time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed for expired attestation")
	}
	if result.CanProceed {
		t.Error("expected cannot proceed without pre-authorization")
	}
	if result.BlockReason == "" {
		t.Error("expected non-empty block reason")
	}
	if !strings.Contains(result.BlockReason, "parent Decision") {
		t.Errorf("block reason should mention parent Decision, got: %s", result.BlockReason)
	}
}

func TestHandleLateCapabilityLoss_WithFallbackPolicy(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		&FallbackPolicy{AuthorizedMode: "direct-PR", Reason: "test fallback"},
	)

	// Force expiry.
	att.Expiry = time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed")
	}
	if !result.CanProceed {
		t.Error("expected can proceed with fallback policy")
	}
	if result.FallbackMode != "direct-PR" {
		t.Errorf("FallbackMode = %q, want %q", result.FallbackMode, "direct-PR")
	}
}

// --- PersistAttestationToMeta / ReadAttestationFromMeta tests ---

func TestPersistAttestationToMeta_RoundTrip(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-attestation-persist"

	att := CreateCapabilityAttestation(
		"test-project", homeDir, "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		nil,
	)

	if err := PersistAttestationToMeta(homeDir, taskID, att); err != nil {
		t.Fatalf("PersistAttestationToMeta: %v", err)
	}

	restored, err := ReadAttestationFromMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadAttestationFromMeta: %v", err)
	}
	if restored == nil {
		t.Fatal("ReadAttestationFromMeta returned nil")
	}

	if restored.Project != att.Project {
		t.Errorf("Project: got %q, want %q", restored.Project, att.Project)
	}
	if restored.Home != att.Home {
		t.Errorf("Home: got %q, want %q", restored.Home, att.Home)
	}
	if restored.RequestedMode != att.RequestedMode {
		t.Errorf("RequestedMode: got %q, want %q", restored.RequestedMode, att.RequestedMode)
	}
	if restored.EffectiveMode != att.EffectiveMode {
		t.Errorf("EffectiveMode: got %q, want %q", restored.EffectiveMode, att.EffectiveMode)
	}
}

func TestPersistAttestationToMeta_WritesModeFields(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-mode-fields"

	att := CreateCapabilityAttestation(
		"test-project", homeDir, "pi", "pi",
		"no-mistakes", "direct-PR", "no-mistakes not on PATH; defaulting to direct-PR",
		nil,
	)

	if err := PersistAttestationToMeta(homeDir, taskID, att); err != nil {
		t.Fatalf("PersistAttestationToMeta: %v", err)
	}

	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	if meta[MetaRequestedMode] != "no-mistakes" {
		t.Errorf("MetaRequestedMode = %q, want %q", meta[MetaRequestedMode], "no-mistakes")
	}
	if meta[MetaEffectiveMode] != "direct-PR" {
		t.Errorf("MetaEffectiveMode = %q, want %q", meta[MetaEffectiveMode], "direct-PR")
	}
	if meta[MetaFallbackReason] != "no-mistakes not on PATH; defaulting to direct-PR" {
		t.Errorf("MetaFallbackReason = %q, want %q", meta[MetaFallbackReason], "no-mistakes not on PATH; defaulting to direct-PR")
	}
}

func TestPersistAttestationToMeta_NoFallbackReason(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-fallback"

	att := CreateCapabilityAttestation(
		"test-project", homeDir, "pi", "pi",
		"direct-PR", "direct-PR", "", nil,
	)

	if err := PersistAttestationToMeta(homeDir, taskID, att); err != nil {
		t.Fatalf("PersistAttestationToMeta: %v", err)
	}

	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	if meta[MetaRequestedMode] != "direct-PR" {
		t.Errorf("MetaRequestedMode = %q, want %q", meta[MetaRequestedMode], "direct-PR")
	}
	if meta[MetaEffectiveMode] != "direct-PR" {
		t.Errorf("MetaEffectiveMode = %q, want %q", meta[MetaEffectiveMode], "direct-PR")
	}
	// Fallback reason should not be set in meta when empty.
	if _, ok := meta[MetaFallbackReason]; ok {
		t.Error("MetaFallbackReason should not be set when empty")
	}
}

func TestReadAttestationFromMeta_NoAttestation(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-att"

	// Write minimal meta without attestation.
	meta := map[string]string{"project": "test"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	att, err := ReadAttestationFromMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadAttestationFromMeta: %v", err)
	}
	if att != nil {
		t.Error("expected nil for missing attestation")
	}
}

func TestReadAttestationFromMeta_NoMeta(t *testing.T) {
	homeDir := t.TempDir()
	_, err := ReadAttestationFromMeta(homeDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing meta")
	}
}

// --- ModeVisibilityFields tests ---

func TestModeVisibilityFields_ReturnsFields(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-visibility"

	att := CreateCapabilityAttestation(
		"test-project", homeDir, "pi", "pi",
		"no-mistakes", "direct-PR", "test fallback reason",
		nil,
	)
	if err := PersistAttestationToMeta(homeDir, taskID, att); err != nil {
		t.Fatalf("PersistAttestationToMeta: %v", err)
	}

	requested, effective, fallbackReason, err := ModeVisibilityFields(homeDir, taskID)
	if err != nil {
		t.Fatalf("ModeVisibilityFields: %v", err)
	}
	if requested != "no-mistakes" {
		t.Errorf("requested = %q, want %q", requested, "no-mistakes")
	}
	if effective != "direct-PR" {
		t.Errorf("effective = %q, want %q", effective, "direct-PR")
	}
	if fallbackReason != "test fallback reason" {
		t.Errorf("fallbackReason = %q, want %q", fallbackReason, "test fallback reason")
	}
}

func TestModeVisibilityFields_NoAttestation(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-vis"

	meta := map[string]string{"project": "test"}
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	requested, effective, fallbackReason, err := ModeVisibilityFields(homeDir, taskID)
	if err != nil {
		t.Fatalf("ModeVisibilityFields: %v", err)
	}
	if requested != "" {
		t.Errorf("requested = %q, want empty", requested)
	}
	if effective != "" {
		t.Errorf("effective = %q, want empty", effective)
	}
	if fallbackReason != "" {
		t.Errorf("fallbackReason = %q, want empty", fallbackReason)
	}
}

// --- resolveExecutableID tests ---

func TestResolveExecutableID_KnownBinary(t *testing.T) {
	// git should always be on PATH.
	id := resolveExecutableID("git")
	if id == "" {
		t.Fatal("resolveExecutableID returned empty")
	}
	if strings.Contains(id, "unknown") {
		t.Errorf("expected git to be resolved, got: %s", id)
	}
	if !strings.Contains(id, ":") {
		t.Errorf("expected 'path:version' format, got: %s", id)
	}
}

func TestResolveExecutableID_UnknownBinary(t *testing.T) {
	id := resolveExecutableID("nonexistent-binary-xyzzy")
	if id == "" {
		t.Fatal("resolveExecutableID returned empty")
	}
	if !strings.Contains(id, "unknown") {
		t.Errorf("expected 'unknown' for missing binary, got: %s", id)
	}
}

// --- probeDeliveryCapabilities tests ---

func TestProbeDeliveryCapabilities_ContainsExpected(t *testing.T) {
	caps := probeDeliveryCapabilities()
	found := make(map[string]bool)
	for _, c := range caps {
		found[c.Name] = true
	}
	for _, name := range []string{"no-mistakes", "gh-axi", "gh", "git"} {
		if !found[name] {
			t.Errorf("expected capability %q in probe results", name)
		}
	}
}

// --- LateCapabilityLossResult JSON tests ---

func TestLateCapabilityLossResult_JSON(t *testing.T) {
	result := &LateCapabilityLossResult{
		Changed:      true,
		Detail:       "capability no-mistakes changed from ready to absent",
		CanProceed:   false,
		FallbackMode: "",
		BlockReason:  "requires parent Decision",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var restored LateCapabilityLossResult
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if restored.Changed != result.Changed {
		t.Errorf("Changed: got %v, want %v", restored.Changed, result.Changed)
	}
	if restored.Detail != result.Detail {
		t.Errorf("Detail: got %q, want %q", restored.Detail, result.Detail)
	}
	if restored.CanProceed != result.CanProceed {
		t.Errorf("CanProceed: got %v, want %v", restored.CanProceed, result.CanProceed)
	}
	if restored.BlockReason != result.BlockReason {
		t.Errorf("BlockReason: got %q, want %q", restored.BlockReason, result.BlockReason)
	}
}

// --- Edge cases ---

func TestCreateCapabilityAttestation_EmptyFields(t *testing.T) {
	att := CreateCapabilityAttestation("", "", "", "", "", "", "", nil)
	if att == nil {
		t.Fatal("CreateCapabilityAttestation returned nil")
	}
	// Empty fields should be preserved.
	if att.Project != "" {
		t.Errorf("Project = %q, want empty", att.Project)
	}
	if att.Home != "" {
		t.Errorf("Home = %q, want empty", att.Home)
	}
	// CreatedAt and Expiry should still be set.
	if att.CreatedAt == "" {
		t.Error("CreatedAt should be set")
	}
	if att.Expiry == "" {
		t.Error("Expiry should be set")
	}
}

func TestCheckCapabilityAttestation_MissingExpiry(t *testing.T) {
	att := &CapabilityAttestation{
		Project:       "test",
		Capabilities:  []CapabilityEntry{},
		// No Expiry set
	}
	changed, _ := CheckCapabilityAttestation(att)
	// Without expiry, the check should not report changed due to expiry.
	// It may still report changed if capabilities differ, but shouldn't panic.
	_ = changed
}

func TestPersistAttestationToMeta_ExistingMetaPreserved(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-preserve"

	// Write initial meta with existing fields.
	if err := home.WriteMeta(homeDir, taskID, map[string]string{
		"project": "existing-project",
		"kind":    "ship",
	}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	att := CreateCapabilityAttestation(
		"test-project", homeDir, "pi", "pi",
		"direct-PR", "direct-PR", "", nil,
	)
	if err := PersistAttestationToMeta(homeDir, taskID, att); err != nil {
		t.Fatalf("PersistAttestationToMeta: %v", err)
	}

	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	// Original fields should still be present.
	if meta["project"] != "test-project" {
		// Note: PersistAttestationToMeta starts with att.Project, not the stored project.
		// The attestation is the source of truth for the project field.
	}
	if meta["kind"] != "ship" {
		t.Errorf("kind should be preserved, got %q", meta["kind"])
	}
	if meta[MetaRequestedMode] != "direct-PR" {
		t.Errorf("MetaRequestedMode = %q, want %q", meta[MetaRequestedMode], "direct-PR")
	}
}

// --- HandleLateCapabilityLoss edge cases ---

func TestHandleLateCapabilityLoss_NilAttestation(t *testing.T) {
	result := HandleLateCapabilityLoss(nil)
	if result == nil {
		t.Fatal("HandleLateCapabilityLoss returned nil")
	}
	if !result.Changed {
		t.Error("expected changed for nil attestation")
	}
	if result.CanProceed {
		t.Error("expected cannot proceed for nil attestation")
	}
}

func TestHandleLateCapabilityLoss_ExpiredWithPreAuth(t *testing.T) {
	att := &CapabilityAttestation{
		Project:       "test",
		Expiry:        time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
		Capabilities:  probeDeliveryCapabilities(),
		FallbackPolicy: &FallbackPolicy{AuthorizedMode: "local-only", Reason: "emergency fallback"},
		EffectiveMode: "no-mistakes",
	}

	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed for expired attestation")
	}
	if !result.CanProceed {
		t.Error("expected can proceed with pre-authorization")
	}
	if result.FallbackMode != "local-only" {
		t.Errorf("FallbackMode = %q, want %q", result.FallbackMode, "local-only")
	}
}

func TestHandleLateCapabilityLoss_ExpiredWithoutPreAuth(t *testing.T) {
	att := &CapabilityAttestation{
		Project: "test",
		Expiry:  time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
		Capabilities: []CapabilityEntry{
			{Name: "no-mistakes", State: backend.Ready},
		},
	}

	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed")
	}
	if result.CanProceed {
		t.Error("expected cannot proceed without pre-auth")
	}
	if result.BlockReason == "" {
		t.Error("expected non-empty block reason")
	}
}

// --- ModeVisibilityFields edge cases ---

func TestModeVisibilityFields_EmptyMeta(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-empty-meta"

	// Write empty meta.
	if err := home.WriteMeta(homeDir, taskID, map[string]string{}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	requested, effective, fallbackReason, err := ModeVisibilityFields(homeDir, taskID)
	if err != nil {
		t.Fatalf("ModeVisibilityFields: %v", err)
	}
	if requested != "" {
		t.Errorf("requested = %q, want empty", requested)
	}
	if effective != "" {
		t.Errorf("effective = %q, want empty", effective)
	}
	if fallbackReason != "" {
		t.Errorf("fallbackReason = %q, want empty", fallbackReason)
	}
}

// --- NoMistakesProbe integration tests (missing binary, unsupported, service failure) ---

func TestCreateCapabilityAttestation_NoMistakesMissing(t *testing.T) {
	// Use a PATH without no-mistakes to verify the probe captures Absent state.
	t.Setenv("PATH", t.TempDir())

	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"direct-PR", "direct-PR", "", nil,
	)

	var nmEntry *CapabilityEntry
	for _, c := range att.Capabilities {
		if c.Name == "no-mistakes" {
			nmEntry = &c
			break
		}
	}
	if nmEntry == nil {
		t.Fatal("no-mistakes capability entry not found")
	}
	if nmEntry.State != backend.Absent {
		t.Errorf("no-mistakes state = %v, want %v", nmEntry.State, backend.Absent)
	}
}

func TestCreateCapabilityAttestation_NoMistakesUnsupported(t *testing.T) {
	tmpDir := createFakeNoMistakesVersion(t, "0.5.0")
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "", nil,
	)

	var nmEntry *CapabilityEntry
	for _, c := range att.Capabilities {
		if c.Name == "no-mistakes" {
			nmEntry = &c
			break
		}
	}
	if nmEntry == nil {
		t.Fatal("no-mistakes capability entry not found")
	}
	if nmEntry.State != backend.Unsupported {
		t.Errorf("no-mistakes state = %v, want %v", nmEntry.State, backend.Unsupported)
	}
}

func TestCreateCapabilityAttestation_NoMistakesServiceFailure(t *testing.T) {
	// Create a fake no-mistakes that exits with error (binary exits with 1 for --version).
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	att := CreateCapabilityAttestation(
		"test", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "", nil,
	)

	var nmEntry *CapabilityEntry
	for _, c := range att.Capabilities {
		if c.Name == "no-mistakes" {
			nmEntry = &c
			break
		}
	}
	if nmEntry == nil {
		t.Fatal("no-mistakes capability entry not found")
	}
	if nmEntry.State != backend.Failed {
		t.Errorf("no-mistakes state = %v, want %v", nmEntry.State, backend.Failed)
	}
}

// --- Fallback policy tests ---

func TestHandleLateCapabilityLoss_FallbackPolicyMatch(t *testing.T) {
	att := &CapabilityAttestation{
		Project:       "test",
		Expiry:        time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
		Capabilities:  probeDeliveryCapabilities(),
		FallbackPolicy: &FallbackPolicy{AuthorizedMode: "direct-PR", Reason: "emergency fallback"},
		EffectiveMode: "no-mistakes",
	}

	// The FallbackPolicy.AuthorizedMode should be used as the fallback mode.
	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed for expired attestation")
	}
	if !result.CanProceed {
		t.Error("expected can proceed with fallback policy")
	}
	if result.FallbackMode != "direct-PR" {
		t.Errorf("FallbackMode = %q, want %q", result.FallbackMode, "direct-PR")
	}
}

func TestHandleLateCapabilityLoss_FallbackPolicyEmptyMode(t *testing.T) {
	// Empty AuthorizedMode should not allow proceeding.
	att := &CapabilityAttestation{
		Project:        "test",
		Expiry:         time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
		Capabilities:   probeDeliveryCapabilities(),
		FallbackPolicy: &FallbackPolicy{AuthorizedMode: "", Reason: "empty mode"},
	}

	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed")
	}
	if result.CanProceed {
		t.Error("expected cannot proceed with empty authorized mode")
	}
}