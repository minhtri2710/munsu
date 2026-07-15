package harness

import (
	"strings"
	"testing"
)

func TestBuildHarnessLaunch_Agy(t *testing.T) {
	tmpl := Templates[Agy]
	cmd := LaunchString(Agy, tmpl)

	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		t.Error("agy launch command should contain --dangerously-skip-permissions")
	}
	if !strings.Contains(cmd, "-i") {
		t.Error("agy launch command should contain -i")
	}
	if !strings.Contains(cmd, "read and execute .crew-brief.md") {
		t.Error("agy launch command should contain brief command")
	}
	if !strings.Contains(cmd, `"read and execute .crew-brief.md"`) {
		t.Error("agy launch command should quote multi-word brief")
	}
	if strings.Count(cmd, `"read and execute .crew-brief.md"`) != 1 {
		t.Error("agy launch command should have exactly one copy of the quoted brief")
	}
	// Ensure single-token flags are NOT quoted
	if strings.Contains(cmd, `"--dangerously-skip-permissions"`) {
		t.Error("agy launch command should NOT quote single-token flags")
	}
	if strings.Contains(cmd, `"-i"`) {
		t.Error("agy launch command should NOT quote single-token -i")
	}
	// DefaultModel is omitted; agy uses its runtime default
	if strings.Contains(cmd, "--model") {
		t.Error("agy launch command should NOT contain --model when DefaultModel is empty")
	}
}
