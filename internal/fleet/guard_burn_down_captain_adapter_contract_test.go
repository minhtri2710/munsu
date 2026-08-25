package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
)

func TestGuardBurnDownBuildLaunchArgsContractGuards(t *testing.T) {
	if os.Getenv("MUNSU_GUARD_ADAPTER_CASE") != "" {
		return
	}
	coverDir := os.Getenv("MUNSU_GUARD_ADAPTER_COVER_DIR")
	if coverDir == "" {
		coverDir = t.TempDir()
	}
	if err := os.MkdirAll(coverDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		caseName   string
		wantError  string
		profileOut string
	}{
		{name: "incomplete contract", caseName: "incomplete", wantError: "captain launch: harness \"pi\" has an incomplete captain launch contract", profileOut: "incomplete.out"},
		{name: "project argument", caseName: "project-arg", wantError: "captain launch: harness \"pi\" must not pass a project path arg", profileOut: "project-arg.out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := filepath.Join(coverDir, tc.profileOut)
			cmd := exec.Command(os.Args[0], "-test.run=^TestGuardBurnDownBuildLaunchArgsContractGuardsChild$", "-test.coverprofile="+profile)
			cmd.Env = append(os.Environ(), "MUNSU_GUARD_ADAPTER_CASE="+tc.caseName)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("adapter child failed: %v\n%s", err, output)
			}
			if _, err := os.Stat(profile); err != nil {
				t.Fatalf("adapter child coverage profile missing: %v", err)
			}
		})
	}
}

func TestGuardBurnDownBuildLaunchArgsContractGuardsChild(t *testing.T) {
	caseName := os.Getenv("MUNSU_GUARD_ADAPTER_CASE")
	if caseName == "" {
		return
	}

	adapter := harness.Adapters[harness.Pi]
	switch caseName {
	case "incomplete":
		adapter.CaptainLaunch.CwdAtHome = false
		adapter.CaptainLaunch.PromptArg = false
	case "project-arg":
		adapter.CaptainLaunch.ProjectArg = true
	default:
		t.Fatalf("unknown adapter child case %q", caseName)
	}
	harness.Adapters[harness.Pi] = adapter

	_, _, err := buildLaunchArgs(t.TempDir(), harness.Pi, config.CaptainProfile{}, t.TempDir())
	var want string
	switch caseName {
	case "incomplete":
		want = "captain launch: harness \"pi\" has an incomplete captain launch contract"
	case "project-arg":
		want = "captain launch: harness \"pi\" must not pass a project path arg"
	}
	if err == nil || err.Error() != want {
		t.Fatalf("buildLaunchArgs error = %v, want %q", err, want)
	}
	if !strings.Contains(err.Error(), "captain launch: harness \"pi\"") {
		t.Fatalf("buildLaunchArgs error = %v, want captain launch refusal", err)
	}
}
