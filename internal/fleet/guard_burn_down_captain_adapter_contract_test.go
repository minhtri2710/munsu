package fleet

import (
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
