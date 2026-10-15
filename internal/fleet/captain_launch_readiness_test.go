package fleet

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestLaunchReadiness(t *testing.T) {
	const piList = "#!/bin/sh\n[ \"$1\" = --list-models ] || exit 2\n"
	for _, tc := range []struct {
		name       string
		pi         string // fake pi run through the real readiness check; empty stubs the lookup
		want       StepState
		wantDetail string
		notDetail  string
	}{
		{name: "snapshot model is reported", want: StepOk, wantDetail: "cline-pass/deepseek-v4-flash", notDetail: "no model override"},
		{name: "pi has no models", pi: piList + "echo 'No models available. Use /login to log into a provider via OAuth or API key.'\n", want: StepFailed, wantDetail: `harness "pi" cannot run: `},
		{name: "pi lists models", pi: piList + "echo 'anthropic claude-fable-5'\n", want: StepOk, wantDetail: "ready at "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := captainHarnessPath
			t.Cleanup(func() { captainHarnessPath = old })
			captainHarnessPath = func(string) (string, error) { return "/test/bin/pi", nil }
			if tc.pi != "" {
				bin := t.TempDir()
				testutil.WriteFakeExecutable(t, filepath.Join(bin, "pi"), tc.pi)
				testutil.SetPath(t, bin)
				captainHarnessPath = readyHarnessPath
			}
			captainHome := captainHomeWithSnapshot(t, config.CaptainProfile{Harness: "pi", Model: "cline-pass/deepseek-v4-flash", Effort: "medium"})
			step := (&RecoverTransaction{}).stepLaunchReadiness(t.TempDir(), Info{Home: captainHome})
			if step.State != tc.want || !strings.Contains(step.Detail, tc.wantDetail) || (tc.notDetail != "" && strings.Contains(step.Detail, tc.notDetail)) {
				t.Fatalf("step = %+v, want %s with detail containing %q", step, tc.want, tc.wantDetail)
			}
			if tc.want == StepFailed && !strings.Contains(step.Detail, "No models available") {
				t.Fatalf("detail = %q, want pi's own output", step.Detail)
			}
		})
	}
}
