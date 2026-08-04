package fleet

import (
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

func TestLaunchReadinessUsesSnapshotCaptainProfileModel(t *testing.T) {
	oldLookPath := captainLookPath
	captainLookPath = func(string) (string, error) { return "/test/bin/pi", nil }
	t.Cleanup(func() { captainLookPath = oldLookPath })
	parent := t.TempDir()
	captainHome := captainHomeWithSnapshot(t, config.CaptainProfile{Harness: "pi", Model: "cline-pass/deepseek-v4-flash", Effort: "medium"})

	step := (&RecoverTransaction{}).stepLaunchReadiness(parent, Info{Home: captainHome})
	if step.State != StepOk {
		t.Fatalf("step=%+v", step)
	}
	if !strings.Contains(step.Detail, "cline-pass/deepseek-v4-flash") || strings.Contains(step.Detail, "no model override") {
		t.Fatalf("detail=%q", step.Detail)
	}
}
