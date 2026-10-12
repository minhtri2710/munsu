package fleet

import "testing"

func TestGuardBurnDownRequireHealthyPiIntegrationRefusesNilCapability(t *testing.T) {
	captainHome := captainHomeWithHarness(t, "pi")
	err := RequireHealthyCaptainIntegration(captainHome, nil)
	if err == nil || err.Error() != "captain integration status capability is required" {
		t.Fatalf("RequireHealthyCaptainIntegration error = %v, want nil-capability refusal", err)
	}
}

func TestGuardBurnDownCheckAliveWithProbeRefusesNilCapability(t *testing.T) {
	parentHome, captainHome, captainID := setupTestHomes(t)
	errState, err := checkAliveWithProbe(parentHome, Info{ID: captainID, Home: captainHome}, nil)
	if err == nil || err.Error() != "captain probe endpoint capability is required" {
		t.Fatalf("checkAliveWithProbe state=%v error=%v, want nil-capability refusal", errState, err)
	}
}
