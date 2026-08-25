package fleet

import (
	"strings"
	"testing"
)

func TestGuardBurnDownRequireHealthyPiIntegrationRefusesNilCapability(t *testing.T) {
	captainHome := captainHomeWithHarness(t, "pi")
	err := requireHealthyPiIntegration(captainHome, nil)
	if err == nil || err.Error() != "canonical Pi integration status capability is required" {
		t.Fatalf("requireHealthyPiIntegration error = %v, want nil-capability refusal", err)
	}
}

func TestGuardBurnDownCheckAliveWithProbeRefusesNilCapability(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)
	errState, err := checkAliveWithProbe(parentHome, Info{ID: captainID, Home: captainHome}, nil)
	if err == nil || err.Error() != "captain probe endpoint capability is required" {
		t.Fatalf("checkAliveWithProbe state=%v error=%v, want nil-capability refusal", errState, err)
	}
}

func TestGuardBurnDownConvergeRefusesMissingNotificationCapability(t *testing.T) {
	_, err := Converge(t.TempDir(), []Info{{ID: "captain", Home: t.TempDir()}}, ConvergeCapabilities{})
	if err == nil || !strings.Contains(err.Error(), "uplink notification transport capability is required") {
		t.Fatalf("Converge error = %v, want missing-notification refusal", err)
	}
	t.Logf("converge refused before notification side effects: %v", err)
}
