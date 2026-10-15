//go:build integration

package fleet

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// --- CapabilityAttestation creation tests ---

func TestCreateCapabilityAttestation_BindsFields(t *testing.T) {
	att := CreateCapabilityAttestation("test-project", "/tmp/home", "pi", githubForgeStep)

	if att.Project != "test-project" {
		t.Errorf("Project = %q, want %q", att.Project, "test-project")
	}
	if att.Home != "/tmp/home" {
		t.Errorf("Home = %q, want %q", att.Home, "/tmp/home")
	}
	if att.Harness != "pi" {
		t.Errorf("Harness = %q, want %q", att.Harness, "pi")
	}
	if att.ResolvedConfig["mode"] != "direct-PR" {
		t.Errorf("mode = %q, want %q", att.ResolvedConfig["mode"], "direct-PR")
	}
	if att.Forge.Adapter != githubForgeStep.Adapter {
		t.Errorf("forge = %q, want the configured forge", att.Forge.Adapter)
	}
	if att.Expiry.IsZero() {
		t.Error("Expiry should be set")
	}
}

func TestCreateCapabilityAttestation_ProbesCapabilities(t *testing.T) {
	att := CreateCapabilityAttestation("test-project", "/tmp/home", "pi", githubForgeStep)

	if len(att.Capabilities) == 0 {
		t.Fatal("expected capabilities to be probed")
	}

	names := make(map[string]bool)
	for _, c := range att.Capabilities {
		names[c.Name] = true
	}
	for _, expected := range []string{"github", "git"} {
		if !names[expected] {
			t.Errorf("expected capability %q in attestation", expected)
		}
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
	att := CreateCapabilityAttestation("test-project", "/tmp/home", "pi", githubForgeStep)
	// Force expiry.
	att.Expiry = time.Now().UTC().Add(-1 * time.Hour)

	changed, _ := CheckCapabilityAttestation(att)
	if !changed {
		t.Error("expected changed for expired attestation")
	}
}

func TestCheckCapabilityAttestation_ValidExpiry(t *testing.T) {
	att := CreateCapabilityAttestation("test-project", "/tmp/home", "pi", githubForgeStep)
	// Force a future expiry.
	att.Expiry = time.Now().UTC().Add(24 * time.Hour)

	changed, _ := CheckCapabilityAttestation(att)
	if changed {
		t.Error("expected no change for valid attestation")
	}
}

func TestCheckCapabilityAttestation_ZeroExpiryFailsClosed(t *testing.T) {
	att := CreateCapabilityAttestation("test-project", "/tmp/home", "pi", githubForgeStep)
	// An attestation carrying no expiry at all must read as expired: the
	// field exists to fail closed, so its absence cannot mean "never expires".
	att.Expiry = time.Time{}

	changed, detail := CheckCapabilityAttestation(att)
	if !changed {
		t.Error("expected changed for an attestation with no expiry recorded")
	}
	if detail == "" {
		t.Error("expected a non-empty detail for an attestation with no expiry recorded")
	}
}

func TestHandleLateCapabilityLoss_ZeroExpiryBlocksLaunch(t *testing.T) {
	att := CreateCapabilityAttestation("test-project", "/tmp/home", "pi", githubForgeStep)
	att.Expiry = time.Time{}

	result := HandleLateCapabilityLoss(att)
	t.Logf("zero expiry result: changed=%t canProceed=%t detail=%q blockReason=%q", result.Changed, result.CanProceed, result.Detail, result.BlockReason)
	if !result.Changed {
		t.Error("expected zero-expiry attestation to be treated as changed")
	}
	if result.CanProceed {
		t.Error("expected zero-expiry attestation to block launch")
	}
	if !strings.Contains(result.Detail, "attestation expired") {
		t.Errorf("detail should identify expiry, got: %q", result.Detail)
	}
	if !strings.Contains(result.BlockReason, "parent Decision") {
		t.Errorf("block reason should require a parent Decision, got: %q", result.BlockReason)
	}
}

func TestHandleLateCapabilityLoss_NoChange(t *testing.T) {
	att := CreateCapabilityAttestation("test-project", "/tmp/home", "pi", githubForgeStep)
	att.Expiry = time.Now().UTC().Add(24 * time.Hour)

	result := HandleLateCapabilityLoss(att)
	if result.Changed {
		t.Error("expected no change")
	}
	if !result.CanProceed {
		t.Error("expected can proceed when nothing changed")
	}
}

func TestHandleLateCapabilityLoss_LateLossBlocks(t *testing.T) {
	att := CreateCapabilityAttestation("test-project", "/tmp/home", "pi", githubForgeStep)

	// Force expiry.
	att.Expiry = time.Now().UTC().Add(-1 * time.Hour)

	result := HandleLateCapabilityLoss(att)
	if !result.Changed {
		t.Error("expected changed")
	}
	if result.CanProceed {
		t.Error("late capability loss must block: there is no pre-authorized fallback")
	}
	if !strings.Contains(result.BlockReason, "parent Decision") {
		t.Errorf("block reason should mention parent Decision, got: %s", result.BlockReason)
	}
}

// TestCheckCapabilityAttestationBlocksReadyForgeLoss pins the Ready -> lost
// transition: the attested gitlab forge's executable is gone at re-probe, and
// that loss must count as changed rather than pass.
func TestCheckCapabilityAttestationBlocksReadyForgeLoss(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "glab")
	att := &CapabilityAttestation{
		Forge:  taskauthority.DeliveryStep{Adapter: "gitlab", Path: gone, ProbeState: "ready"},
		Expiry: time.Now().UTC().Add(24 * time.Hour),
		Capabilities: []CapabilityEntry{
			{Name: "gitlab", State: backend.Ready, Path: gone},
		},
	}
	changed, detail := CheckCapabilityAttestation(att)
	if !changed || !strings.Contains(detail, `capability "gitlab" changed from`) {
		t.Fatalf("CheckCapabilityAttestation = %v, %q; want a Ready forge loss", changed, detail)
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
	caps := probeDeliveryCapabilities(githubForgeStep)
	found := make(map[string]bool)
	for _, c := range caps {
		found[c.Name] = true
	}
	for _, name := range []string{"github", "git"} {
		if !found[name] {
			t.Errorf("expected capability %q in probe results", name)
		}
	}
}

// --- LateCapabilityLossResult JSON tests ---

func TestLateCapabilityLossResult_JSON(t *testing.T) {
	result := &LateCapabilityLossResult{
		Changed:     true,
		Detail:      "capability github changed from ready to absent",
		CanProceed:  false,
		BlockReason: "requires parent Decision",
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
