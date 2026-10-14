package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
)

func TestGuardBurnDownBuildLaunchArgsContractGuards(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*harness.Adapter)
		wantError string
	}{
		{
			name: "incomplete contract",
			mutate: func(adapter *harness.Adapter) {
				adapter.CaptainLaunch.CwdAtHome = false
				adapter.CaptainLaunch.PromptArg = false
			},
			wantError: "captain launch: harness \"pi\" has an incomplete captain launch contract",
		},
		{
			name: "project argument",
			mutate: func(adapter *harness.Adapter) {
				adapter.CaptainLaunch.ProjectArg = true
			},
			wantError: "captain launch: harness \"pi\" must not pass a project path arg",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := harness.Adapters[harness.Pi]
			adapter := original
			tc.mutate(&adapter)
			harness.Adapters[harness.Pi] = adapter
			t.Cleanup(func() { harness.Adapters[harness.Pi] = original })

			_, _, err := buildLaunchArgs(t.TempDir(), harness.Pi, config.CaptainProfile{}, t.TempDir())
			if err == nil || err.Error() != tc.wantError {
				t.Fatalf("buildLaunchArgs error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

// TestCaptainLaunchRefusesNonPiHarnessBeforeTheSeat pins the premise that
// makes the seat's harness check unreachable: buildLaunchArgs, which Launch
// calls before prepareCaptainSeat, refuses every harness without a verified
// captain launch contract, and only pi has one.
func TestCaptainLaunchRefusesNonPiHarnessBeforeTheSeat(t *testing.T) {
	for _, tc := range []struct {
		name      string
		harness   string
		wantError string
	}{
		{
			name:      "non-pi harness refused",
			harness:   harness.Claude,
			wantError: "captain launch: harness \"claude\" does not have a verified captain launch contract",
		},
		{
			name:    "pi allowed",
			harness: harness.Pi,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			captainHome := filepath.Join(t.TempDir(), "captains", "alpha")
			if err := os.MkdirAll(captainHome, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(captainHome, CaptainCharterName), []byte("# alpha\n"), 0644); err != nil {
				t.Fatal(err)
			}
			writeCanonicalPiIntegration(t, captainHome)

			_, _, err := buildLaunchArgs(captainHome, tc.harness, config.CaptainProfile{Harness: tc.harness}, parent)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("buildLaunchArgs(%s) error = %v, want nil", tc.harness, err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantError {
				t.Fatalf("buildLaunchArgs(%s) error = %v, want %q", tc.harness, err, tc.wantError)
			}
		})
	}
}
