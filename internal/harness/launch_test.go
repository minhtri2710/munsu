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
