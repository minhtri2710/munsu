package fleet

import (
	"strings"
	"testing"
)

func TestGuardBurnDownSeedCaptainRefusesEmptyParentHome(t *testing.T) {
	homePath := t.TempDir() + "/captain"
	err := SeedCaptain(CaptainSeedOptions{ID: "captain", Home: homePath, Integration: fakeIntegrationPort{}})
	if err == nil || !strings.Contains(err.Error(), "empty charter requires parent home") {
		t.Fatalf("SeedCaptain error = %v, want empty-parent refusal", err)
	}
}

func TestGuardBurnDownRegisterRefusesEmptyIDOrHome(t *testing.T) {
	for name, inputs := range map[string][2]string{
		"empty id":   [2]string{"", "/captain"},
		"empty home": [2]string{"captain", ""},
	} {
		id, homePath := inputs[0], inputs[1]
		t.Run(name, func(t *testing.T) {
			err := Register(t.TempDir(), id, homePath, "", "")
			if err == nil || err.Error() != "register requires id and home path" {
				t.Fatalf("Register error = %v, want empty-input refusal", err)
			}
		})
	}
}

func TestGuardBurnDownUnregisterRefusesEmptyID(t *testing.T) {
	err := Unregister(t.TempDir(), "")
	if err == nil || err.Error() != "unregister requires id" {
		t.Fatalf("Unregister error = %v, want empty-id refusal", err)
	}
}
