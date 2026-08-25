package harness

import (
	"strings"
	"testing"
)

// LaunchString builds the shell command string to launch a harness agent
// using the template defaults for model and effort (test helper).
func LaunchString(h string, tmpl Template) string {
	return LaunchStringWith(h, tmpl, tmpl.DefaultModel, tmpl.DefaultEffort)
}

// LaunchStringFromAdapter builds the launch string for a harness using its
// adapter from the registry (test helper).
func LaunchStringFromAdapter(h string) string {
	a, ok := GetAdapter(h)
	if !ok {
		return ""
	}
	return LaunchString(h, a.LaunchTemplate)
}

func TestBuildHarnessLaunch_Agy(t *testing.T) {
	tmpl := Templates[Agy]
	cmd := LaunchString(Agy, tmpl)

	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		t.Error("agy launch command should contain --dangerously-skip-permissions")
	}
	// DefaultModel is omitted; agy uses its runtime default
	if strings.Contains(cmd, "--model") {
		t.Error("agy launch command should NOT contain --model when DefaultModel is empty")
	}
	// Ensure single-token flags are NOT quoted
	if strings.Contains(cmd, `"--dangerously-skip-permissions"`) {
		t.Error("agy launch command should NOT quote single-token flags")
	}
}

func TestBuildHarnessLaunch_FromAdapter_Claude(t *testing.T) {
	cmd := LaunchStringFromAdapter(Claude)
	if cmd == "" {
		t.Fatal("LaunchStringFromAdapter returned empty for claude")
	}
	if !strings.Contains(cmd, "--model") {
		t.Error("claude launch should contain --model")
	}
	if !strings.Contains(cmd, "claude-sonnet-4-20250515") {
		t.Error("claude launch should contain default model")
	}
}

func TestBuildHarnessLaunch_FromAdapter_Codex(t *testing.T) {
	cmd := LaunchStringFromAdapter(Codex)
	if cmd == "" {
		t.Fatal("LaunchStringFromAdapter returned empty for codex")
	}
	if !strings.Contains(cmd, "--model") {
		t.Error("codex launch should contain --model")
	}
	if !strings.Contains(cmd, "--effort") {
		t.Error("codex launch should contain --effort")
	}
}

func TestBuildHarnessLaunch_FromAdapter_Unknown(t *testing.T) {
	cmd := LaunchStringFromAdapter("unknown")
	if cmd != "" {
		t.Errorf("LaunchStringFromAdapter('unknown') = %q, want empty", cmd)
	}
}

func TestBuildHarnessLaunch_FromAdapter_Pi(t *testing.T) {
	cmd := LaunchStringFromAdapter(Pi)
	if cmd == "" {
		t.Fatal("LaunchStringFromAdapter returned empty for pi")
	}
	// Pi has --model but no default model, so --model should not appear
	if strings.Contains(cmd, "--model") {
		t.Error("pi launch should NOT contain --model when DefaultModel is empty")
	}
	// Pi has --thinking as effort flag but no default effort, so it should not appear
	if strings.Contains(cmd, "--thinking") {
		t.Error("pi launch should NOT contain --thinking when DefaultEffort is empty")
	}
}

func TestBuildHarnessLaunch_FromAdapter_Grok(t *testing.T) {
	cmd := LaunchStringFromAdapter(Grok)
	if cmd == "" {
		t.Fatal("LaunchStringFromAdapter returned empty for grok")
	}
	// Grok has --model but no default model
	if strings.Contains(cmd, "--model") {
		t.Error("grok launch should NOT contain --model when DefaultModel is empty")
	}
	// Grok has --reasoning-effort but no default effort
	if strings.Contains(cmd, "--reasoning-effort") {
		t.Error("grok launch should NOT contain --reasoning-effort when DefaultEffort is empty")
	}
}

func TestBuildHarnessLaunch_FromAdapter_Agy(t *testing.T) {
	cmd := LaunchStringFromAdapter(Agy)
	if cmd == "" {
		t.Fatal("LaunchStringFromAdapter returned empty for agy")
	}
	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		t.Error("agy launch should contain --dangerously-skip-permissions")
	}
	// Agy has --model but no default model
	if strings.Contains(cmd, "--model") {
		t.Error("agy launch should NOT contain --model when DefaultModel is empty")
	}
}
