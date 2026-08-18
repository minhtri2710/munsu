//go:build integration

package fleet

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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
		t.Error("CreatedAt should be set")
	}
	if att.Expiry == "" {
		t.Error("Expiry should be set")
	}
}

func TestCreateCapabilityAttestation_WithFallbackReason(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "direct-PR", "no-mistakes not on PATH",
		nil,
	)

	if att.FallbackReason != "no-mistakes not on PATH" {
		t.Errorf("FallbackReason = %q, want %q", att.FallbackReason, "no-mistakes not on PATH")
	}
}

func TestCreateCapabilityAttestation_ProbesCapabilities(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		nil,
	)

	if len(att.Capabilities) == 0 {
		t.Fatal("expected capabilities to be probed")
	}

	names := make(map[string]bool)
	for _, c := range att.Capabilities {
		names[c.Name] = true
	}
	for _, expected := range []string{"no-mistakes", "gh-axi", "gh", "git"} {
		if !names[expected] {
			t.Errorf("expected capability %q in attestation", expected)
		}
	}
}

func TestCreateCapabilityAttestation_WithFallbackPolicy(t *testing.T) {
	policy := &FallbackPolicy{AuthorizedMode: "direct-PR", Reason: "test fallback"}
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		policy,
	)

	if att.FallbackPolicy == nil {
		t.Fatal("FallbackPolicy should be set")
	}
	if att.FallbackPolicy.AuthorizedMode != "direct-PR" {
		t.Errorf("AuthorizedMode = %q, want %q", att.FallbackPolicy.AuthorizedMode, "direct-PR")
	}
}

func TestCapabilityEntry_StateValues(t *testing.T) {
	entry := CapabilityEntry{
		Name:    "test-cap",
		State:   backend.Ready,
		Version: "1.0.0",
		Path:    "/usr/local/bin/test-cap",
	}

	if entry.State != backend.Ready {
		t.Errorf("State = %v, want Ready", entry.State)
	}
}

func TestCapabilityAttestation_JSONRoundTrip(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "direct-PR", "reason",
		nil,
	)

	data, err := json.Marshal(att)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var restored CapabilityAttestation
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if restored.Project != att.Project || restored.Home != att.Home {
		t.Errorf("round trip mismatch: %+v vs %+v", restored, att)
	}
	if restored.RequestedMode != att.RequestedMode || restored.EffectiveMode != att.EffectiveMode {
		t.Errorf("mode mismatch: %+v vs %+v", restored, att)
	}
	if restored.FallbackReason != att.FallbackReason {
		t.Errorf("fallback reason mismatch: %+v vs %+v", restored, att)
	}
	if len(restored.Capabilities) != len(att.Capabilities) {
		t.Errorf("capabilities mismatch: %d vs %d", len(restored.Capabilities), len(att.Capabilities))
	}
}

func TestCheckCapabilityAttestation_NilReturnsChanged(t *testing.T) {
	changed, detail := CheckCapabilityAttestation(nil)
	if !changed {
		t.Error("expected changed for nil attestation")
	}
	if detail == "" {
		t.Error("expected detail for nil attestation")
	}
}

func TestCheckCapabilityAttestation_ExpiredReturnsChanged(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		nil,
	)
	// Force expiry.
	att.Expiry = time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	changed, _ := CheckCapabilityAttestation(att)
	if !changed {
		t.Error("expected changed for expired attestation")
	}
}

func TestCheckCapabilityAttestation_ValidExpiry(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		nil,
	)
	// Force a future expiry.
	att.Expiry = time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)

	changed, _ := CheckCapabilityAttestation(att)
	if changed {
		t.Error("expected no change for valid attestation")
	}
}

func TestHandleLateCapabilityLoss_NoChange(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		nil,
	)
	att.Expiry = time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)

	result := HandleLateCapabilityLoss(att)
	if result.Changed {
		t.Error("expected no change")
	}
	if !result.CanProceed {
		t.Error("expected can proceed when nothing changed")
	}
}

func TestHandleLateCapabilityLoss_WithPreAuthorizedFallback(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
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
		t.Error("expected can proceed with pre-authorized fallback")
	}
	if result.FallbackMode != "direct-PR" {
		t.Errorf("FallbackMode = %q, want %q", result.FallbackMode, "direct-PR")
	}
}

func TestHandleLateCapabilityLoss_WithoutPreAuthorization(t *testing.T) {
	att := CreateCapabilityAttestation(
		"test-project", "/tmp/home", "pi", "pi",
		"no-mistakes", "no-mistakes", "",
		nil,
	)

	// Force expiry.
	att.Expiry = time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed")
	}
	if result.CanProceed {
		t.Error("expected cannot proceed without pre-authorization")
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

// --- projectAttestationEvidence projection tests ---

// TestProjectAttestationEvidence_WritesModeFields proves the runtime .meta
// projection helper mirrors the accepted attestation without any authority
// mutation.
func TestProjectAttestationEvidence_WritesModeFields(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-mode-fields"

	att := CreateCapabilityAttestation(
		"test-project", homeDir, "pi", "pi",
		"no-mistakes", "direct-PR", "no-mistakes not on PATH; defaulting to direct-PR",
		nil,
	)
	if projErr := projectAttestationEvidence(homeDir, taskID, att, taskauthority.Generation(1)); projErr != nil {
		t.Fatalf("projectAttestationEvidence: %v", projErr)
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
	if meta[MetaCapabilityAttestation] == "" {
		t.Error("MetaCapabilityAttestation should hold the serialized attestation")
	}
}

func TestProjectAttestationEvidence_NoFallbackReason(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-no-fallback"

	att := CreateCapabilityAttestation(
		"test-project", homeDir, "pi", "pi",
		"direct-PR", "direct-PR", "", nil,
	)
	if projErr := projectAttestationEvidence(homeDir, taskID, att, taskauthority.Generation(1)); projErr != nil {
		t.Fatalf("projectAttestationEvidence: %v", projErr)
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
	if _, ok := meta[MetaFallbackReason]; ok {
		t.Error("MetaFallbackReason should not be set when empty")
	}
}

func TestProjectAttestationEvidence_PreservesExistingMeta(t *testing.T) {
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
	if projErr := projectAttestationEvidence(homeDir, taskID, att, taskauthority.Generation(1)); projErr != nil {
		t.Fatalf("projectAttestationEvidence: %v", projErr)
	}

	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta["kind"] != "ship" {
		t.Errorf("kind should be preserved, got %q", meta["kind"])
	}
	if meta[MetaRequestedMode] != "direct-PR" {
		t.Errorf("MetaRequestedMode = %q, want %q", meta[MetaRequestedMode], "direct-PR")
	}
}

// TestProjectAttestationEvidence_TypedPartialError proves a projection
// failure returns a typed partial error.
func TestProjectAttestationEvidence_TypedPartialError(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "test-projection-failure"

	// Make the state path unwritable so the projection write fails.
	if err := os.WriteFile(filepath.Join(homeDir, "state"), []byte("block"), 0644); err != nil {
		t.Fatal(err)
	}

	att := CreateCapabilityAttestation(
		"test-project", homeDir, "pi", "pi",
		"no-mistakes", "direct-PR", "no-mistakes not on PATH; defaulting to direct-PR",
		nil,
	)
	projErr := projectAttestationEvidence(homeDir, taskID, att, taskauthority.Generation(1))
	var typed *AttestationProjectionError
	if !errors.As(projErr, &typed) {
		t.Fatalf("projection error = %v, want *AttestationProjectionError", projErr)
	}
	if typed.TaskID != taskID {
		t.Fatalf("typed error task = %q, want %q", typed.TaskID, taskID)
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

	if !restored.Changed || restored.Detail != result.Detail || restored.CanProceed != result.CanProceed {
		t.Errorf("round trip mismatch: %+v vs %+v", restored, result)
	}
}
