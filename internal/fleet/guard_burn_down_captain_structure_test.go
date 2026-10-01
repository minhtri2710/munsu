package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestGuardBurnDownValidateRefusesNonDirectoryState(t *testing.T) {
	homePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(homePath, "state"), []byte("not a directory\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"data", "config"} {
		if err := os.Mkdir(filepath.Join(homePath, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(homePath, "AGENTS.md"), []byte("# captain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := home.SeedCaptainProvenance(homePath, "captain"); err != nil {
		t.Fatal(err)
	}

	err := Validate(homePath, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "state/ exists but is not a directory") {
		t.Fatalf("Validate error = %v, want non-directory state refusal", err)
	}
}

func TestGuardBurnDownEnsureCaptainIntegrationRefusesNilCapability(t *testing.T) {
	homePath := t.TempDir()
	if err := home.SeedCaptainProvenance(homePath, "captain"); err != nil {
		t.Fatal(err)
	}

	err := ensureCaptainIntegration(homePath, "pi", nil)
	if err == nil || err.Error() != "captain integration capability is required" {
		t.Fatalf("ensureCaptainIntegration error = %v, want nil-capability refusal", err)
	}
}
