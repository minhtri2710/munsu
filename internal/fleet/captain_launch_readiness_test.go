package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchReadinessUsesParentCaptainProfileModel(t *testing.T) {
	parent := t.TempDir()
	captainHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "config", "captain-harness"), []byte("pi cline-pass/deepseek-v4-flash medium\n"), 0600); err != nil {
		t.Fatal(err)
	}

	step := (&RecoverTransaction{}).stepLaunchReadiness(parent, Info{Home: captainHome})
	if step.State != StepOk {
		t.Fatalf("step=%+v", step)
	}
	if !strings.Contains(step.Detail, "cline-pass/deepseek-v4-flash") || strings.Contains(step.Detail, "no model override") {
		t.Fatalf("detail=%q", step.Detail)
	}
}
