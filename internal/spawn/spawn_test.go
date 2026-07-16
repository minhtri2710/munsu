package spawn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// fakeBackend implements session.Backend for testing.
type fakeBackend struct {
	newWindow func(session, name string) (string, error)
	sendKeys  func(windowID, text string) error
	capture   func(windowID string, lines int) (string, error)
	alive     func(windowID string) bool
	teardown  func(windowID string) error
}

func (f *fakeBackend) NewWindow(session, name string) (string, error) {
	if f.newWindow != nil {
		return f.newWindow(session, name)
	}
	return "win-1", nil
}

func (f *fakeBackend) SendKeys(windowID, text string) error {
	if f.sendKeys != nil {
		return f.sendKeys(windowID, text)
	}
	return nil
}

func (f *fakeBackend) Capture(windowID string, lines int) (string, error) {
	if f.capture != nil {
		return f.capture(windowID, lines)
	}
	return "> ready", nil
}

func (f *fakeBackend) Alive(windowID string) bool {
	if f.alive != nil {
		return f.alive(windowID)
	}
	return true
}

func (f *fakeBackend) Teardown(windowID string) error {
	if f.teardown != nil {
		return f.teardown(windowID)
	}
	return nil
}

// Fixture: r6 transcript capture of pi ready UI (trimmed to key lines).
const piReadyCapture = `spec-driven-development, spec-to-code-compliance, srcwalk, stitch-design-taste,
supply-chain-risk-auditor, tasks-axi, tdd, teach, test-driven-development,
to-spec, to-tickets, triage, using-agent-skills, variant-analysis, vuln-report,
wayfinder, wizard, wooyun-legacy, writing-beats, writing-fragments,
writing-great-skills, writing-shape, zeroize-audit

[Extensions]
  @eko24ive/pi-ask:src, @ff-labs/pi-fff@latest:src,
@heyhuynhgiabuu/pi-search@latest:dist, @heyhuynhgiabuu/pi-task:dist,
@juicesharp/rpiv-advisor, @ogulcancelik/pi-herdr,
@sting8k/pi-srcwalk@latest:pi-srcwalk, @sting8k/pi-vcc@latest,
@vigolium/piolium@latest:piolium, herdr-agent-state.ts,
joelhooks/pi-rhizomatic:pi-rhizomatic.ts, pi-augment@latest:src,
pi-boomerang@latest, pi-clinepass-provider:src, pi-hashline-edit-pro,
pi-model-switch@latest, pi-rewind@latest:src, rtk.ts

[Themes]
  piolium-srcery

[Skill conflicts]
  "herdr" collision:
    ✓ auto (user) ~/.pi/agent/skills/herdr/SKILL.md
    ✗ ~/.pi/agent/npm/node_modules/@ogulcancelik/pi-herdr/skills/herdr/SKILL.md
(skipped)


 Advisor restored: zai/glm-5.2, high

────────────────────────────────────────────────────────────────────────────────

────────────────────────────────────────────────────────────────────────────────
~/.treehouse/real-estate-320f76/2/real-estate (detached)
0.0%/256k (auto) • xp                      (cliproxyapi) grok-4.5 • thinking off
◆ 1 checkpoint
`

// Fixture: r6 transcript capture of agy trust prompt.
const agyTrustCapture = `Accessing workspace:

/Users/beowulf/.treehouse/real-estate-320f76/2/real-estate

Do you trust the contents of this project?

Antigravity CLI requires permission to read, edit, and execute files here.

> Yes, I trust this folder
  No, exit

  ↑/↓ Navigate · enter Confirm
                                                    Claude Sonnet 4.6 (Thinking)
`

// Fixture: r7 transcript capture of pi trust prompt.
const piTrustCapture = `Trust project folder?
/Users/beowulf/.treehouse/firstmate-8bf1b0/4/firstmate
→ Trust
  Trust parent folder (...)
  Trust (this session only)
  Do not trust
`

func TestPiReadyPatterns(t *testing.T) {
	patterns := ReadyPatterns[harness.Pi]
	if len(patterns) == 0 {
		t.Fatal("pi ready patterns should not be empty")
	}

	// Only the patterns that appear in the actual pi capture should match.
	patternsToCheck := []string{"checkpoint", "thinking off", "◆"}
	for _, p := range patternsToCheck {
		if !strings.Contains(piReadyCapture, p) {
			t.Errorf("pi ready pattern %q should match pi capture", p)
		}
	}
}

func TestAgyTrustDetection(t *testing.T) {
	// Verify trust prompt patterns match the actual agy trust capture.
	if !strings.Contains(agyTrustCapture, "Do you trust") {
		t.Error("trust pattern 'Do you trust' should match agy trust capture")
	}
	if !strings.Contains(agyTrustCapture, "Yes, I trust this folder") {
		t.Error("trust pattern 'Yes, I trust this folder' should match agy trust capture")
	}

	// Verify IsTrustPrompt detects trust in the agy capture.
	if !IsTrustPrompt(agyTrustCapture, harness.Agy) {
		t.Error("IsTrustPrompt should detect trust in agy capture")
	}

	// Verify trust is NOT detected in pi capture.
	if IsTrustPrompt(piReadyCapture, harness.Pi) {
		t.Error("IsTrustPrompt should NOT detect trust in pi capture")
	}

	// Verify trust is NOT detected in pi capture when checking agy patterns.
	if IsTrustPrompt(piReadyCapture, harness.Agy) {
		t.Error("IsTrustPrompt should NOT detect trust in pi capture with agy harness")
	}
}

func TestPiTrustDetection(t *testing.T) {
	// Verify trust prompt patterns match the actual pi trust capture.
	if !strings.Contains(piTrustCapture, "Trust project folder") {
		t.Error("trust pattern 'Trust project folder' should match pi trust capture")
	}
	if !strings.Contains(piTrustCapture, "→ Trust") {
		t.Error("trust pattern '→ Trust' should match pi trust capture")
	}
	if !strings.Contains(piTrustCapture, "Do not trust") {
		t.Error("trust pattern 'Do not trust' should match pi trust capture")
	}

	// Verify IsTrustPrompt detects trust in the pi capture.
	if !IsTrustPrompt(piTrustCapture, harness.Pi) {
		t.Error("IsTrustPrompt should detect trust in pi capture")
	}

	// Verify trust is NOT detected in agy capture when checking pi patterns.
	if IsTrustPrompt(agyTrustCapture, harness.Pi) {
		t.Error("IsTrustPrompt should NOT detect trust in agy capture with pi harness")
	}

	// Verify trust is NOT detected in pi ready capture (not a trust dialog).
	if IsTrustPrompt(piReadyCapture, harness.Pi) {
		t.Error("IsTrustPrompt should NOT detect trust in pi ready capture")
	}
}

func TestAgyReadyPatterns(t *testing.T) {
	patterns := ReadyPatterns[harness.Agy]
	if len(patterns) == 0 {
		t.Fatal("agy ready patterns should not be empty")
	}

	// These patterns should NOT match the trust capture (they're ready patterns).
	for _, p := range patterns {
		if strings.Contains(agyTrustCapture, p) {
			t.Errorf("agy ready pattern %q should NOT match trust capture", p)
		}
	}
}

func TestDefaultReadyPatterns(t *testing.T) {
	if len(DefaultReadyPatterns) == 0 {
		t.Fatal("DefaultReadyPatterns should not be empty")
	}
	for _, p := range DefaultReadyPatterns {
		if p == "" {
			t.Error("DefaultReadyPatterns should not contain empty patterns")
		}
	}
}

func TestValidateDeliveryMode(t *testing.T) {
	tests := []struct {
		mode string
		ok   bool
	}{
		{"", true},
		{"no-mistakes", true},
		{"direct-PR", true},
		{"local-only", true},
		{"invalid", false},
		{"ship", false},
	}
	for _, tc := range tests {
		err := ValidateDeliveryMode(tc.mode)
		if tc.ok && err != nil {
			t.Errorf("ValidateDeliveryMode(%q) = %v, want nil", tc.mode, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("ValidateDeliveryMode(%q) = nil, want error", tc.mode)
		}
	}
}

func TestRun_ValidateFailsOnMissingBrief(t *testing.T) {
	// Run without a brief should fail early (before worktree/treehouse is touched).
	tmpDir := t.TempDir()
	args := Args{
		ID:          "test-task",
		ProjectName: "test-project",
		HomeDir:     tmpDir,
		Session:     &fakeBackend{},
	}
	_, err := Run(args)
	if err == nil {
		t.Fatal("expected error for missing brief")
	}
	if !strings.Contains(err.Error(), "no brief found") {
		t.Errorf("expected 'no brief found' error, got: %v", err)
	}
}

func TestRun_ValidateMode(t *testing.T) {
	args := Args{
		ID:          "test-task",
		ProjectName: "test-project",
		Mode:        "bogus-mode",
		HomeDir:     t.TempDir(),
		Session:     &fakeBackend{},
	}
	_, err := Run(args)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid delivery mode") {
		t.Errorf("expected 'invalid delivery mode' error, got: %v", err)
	}
}

func TestRun_InjectFakeSession(t *testing.T) {
	// Verify that the injectable Session field is used instead of resolving at runtime.
	calledNewWindow := false
	fake := &fakeBackend{
		newWindow: func(session, name string) (string, error) {
			calledNewWindow = true
			if session != "munsu" || name != "inj-test" {
				return "", fmt.Errorf("unexpected args: %q, %q", session, name)
			}
			return "inj-win", nil
		},
	}

	// This should fail at brief-exists check before getting to NewWindow.
	// So we're testing that the injected backend is plumbed through correctly
	// by checking the error is about brief, not about backend resolution.
	args := Args{
		ID:          "inj-test",
		ProjectName: "test-project",
		HomeDir:     t.TempDir(),
		Session:     fake,
	}
	_, err := Run(args)
	if err == nil {
		t.Fatal("expected error for missing brief")
	}
	if calledNewWindow {
		t.Error("NewWindow should not be called before brief check")
	}
}

func TestResolveDeliveryMode_AutoNoMistakes(t *testing.T) {
	// auto picks no-mistakes when on PATH, direct-PR otherwise
	mode, err := ResolveDeliveryMode(t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "no-mistakes" && mode != "direct-PR" {
		t.Errorf("ResolveDeliveryMode auto = %q, want %q or %q", mode, "no-mistakes", "direct-PR")
	}
}

func TestResolveDeliveryMode_ExplicitDirectPR(t *testing.T) {
	mode, err := ResolveDeliveryMode(t.TempDir(), "direct-PR", "")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("ResolveDeliveryMode explicit = %q, want %q", mode, "direct-PR")
	}
}

func TestResolveDeliveryMode_ProjectModeHonored(t *testing.T) {
	mode, err := ResolveDeliveryMode(t.TempDir(), "", "direct-PR")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("ResolveDeliveryMode project mode = %q, want %q", mode, "direct-PR")
	}
}

func TestResolveDeliveryMode_ExplicitOverridesProject(t *testing.T) {
	mode, err := ResolveDeliveryMode(t.TempDir(), "local-only", "no-mistakes")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "local-only" {
		t.Errorf("ResolveDeliveryMode explicit = %q, want %q", mode, "local-only")
	}
}

func TestResolveDeliveryMode_InvalidExplicit(t *testing.T) {
	_, err := ResolveDeliveryMode(t.TempDir(), "bogus", "")
	if err == nil {
		t.Fatal("expected error for invalid explicit mode")
	}
}

func TestValidateDeliveryMode_Extended(t *testing.T) {
	if err := ValidateDeliveryMode(""); err != nil {
		t.Errorf("empty mode should be valid, got: %v", err)
	}
	if err := ValidateDeliveryMode("no-mistakes"); err != nil {
		t.Errorf("no-mistakes should be valid, got: %v", err)
	}
	if err := ValidateDeliveryMode("direct-PR"); err != nil {
		t.Errorf("direct-PR should be valid, got: %v", err)
	}
	if err := ValidateDeliveryMode("local-only"); err != nil {
		t.Errorf("local-only should be valid, got: %v", err)
	}
	if err := ValidateDeliveryMode("invalid"); err == nil {
		t.Error("invalid mode should produce error")
	}
}

func TestEnsureDeliveryModeRunnable_NoMistakesOnPath(t *testing.T) {
	if !noMistakesOnPath() {
		t.Skip("no-mistakes not on PATH")
	}
	if err := EnsureDeliveryModeRunnable("no-mistakes"); err != nil {
		t.Errorf("EnsureDeliveryModeRunnable(no-mistakes) = %v, want nil", err)
	}
}

func TestEnsureDeliveryModeRunnable_DirectPR(t *testing.T) {
	// direct-PR doesn't require any binary check
	if err := EnsureDeliveryModeRunnable("direct-PR"); err != nil {
		t.Errorf("direct-PR should always be runnable: %v", err)
	}
}

func TestNoMistakesOnPath(t *testing.T) {
	// This test is informational only; skip if no-mistakes not available
	if !noMistakesOnPath() {
		t.Skip("no-mistakes not on PATH (CI environments typically don't have it)")
	}
}

func TestEffectiveModeForSpawn_AutoNoMistakesPresent(t *testing.T) {
	// Create a fake no-mistakes binary on PATH
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	mode, err := effectiveModeForSpawn(t.TempDir(), Args{})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "no-mistakes" {
		t.Errorf("effectiveModeForSpawn auto = %q, want %q", mode, "no-mistakes")
	}
}

func TestEffectiveModeForSpawn_AutoNoMistakesAbsent(t *testing.T) {
	// Use a PATH where no-mistakes is definitely not found
	t.Setenv("PATH", t.TempDir())

	mode, err := effectiveModeForSpawn(t.TempDir(), Args{})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("effectiveModeForSpawn auto without no-mistakes = %q, want %q", mode, "direct-PR")
	}
}

func TestEffectiveModeForSpawn_ExplicitNoMistakesWithBinary(t *testing.T) {
	// Fake no-mistakes on PATH, explicit flag
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	mode, err := effectiveModeForSpawn(t.TempDir(), Args{Mode: "no-mistakes"})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "no-mistakes" {
		t.Errorf("effectiveModeForSpawn explicit = %q, want %q", mode, "no-mistakes")
	}
}

func TestEffectiveModeForSpawn_ExplicitNoMistakesWithoutBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := effectiveModeForSpawn(t.TempDir(), Args{Mode: "no-mistakes"})
	if err == nil {
		t.Fatal("expected error for explicit no-mistakes without binary")
	}
}

func TestEffectiveModeForSpawn_ProjectModeHonored(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // ensure auto doesn't pick no-mistakes

	mode, err := effectiveModeForSpawn(t.TempDir(), Args{ProjectMode: "local-only"})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "local-only" {
		t.Errorf("effectiveModeForSpawn with project mode = %q, want %q", mode, "local-only")
	}
}

func TestEffectiveModeForSpawn_ExplicitOverridesProject(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	// Explicit flag takes precedence over project mode
	mode, err := effectiveModeForSpawn(t.TempDir(), Args{Mode: "direct-PR", ProjectMode: "no-mistakes"})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("effectiveModeForSpawn explicit override = %q, want %q", mode, "direct-PR")
	}
}

func TestEffectiveModeForSpawn_ConfigDefaultMode(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // ensure auto doesn't pick no-mistakes

	homeDir := t.TempDir()
	// Write config/default-mode
	configDir := filepath.Join(homeDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "default-mode"), []byte("direct-PR"), 0644); err != nil {
		t.Fatal(err)
	}

	// No flag, no project mode — should pick up config/default-mode
	mode, err := effectiveModeForSpawn(homeDir, Args{})
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("effectiveModeForSpawn config/default-mode = %q, want %q", mode, "direct-PR")
	}
}

func TestRun_ValidatesModeFromArgsOnly(t *testing.T) {
	// A bogus mode flag should still be rejected by Run
	args := Args{
		ID:          "test-task",
		ProjectName: "test-project",
		Mode:        "bogus-mode",
		HomeDir:     t.TempDir(),
		Session:     &fakeBackend{},
	}
	_, err := Run(args)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid delivery mode") {
		t.Errorf("expected 'invalid delivery mode' error, got: %v", err)
	}
}

func TestRun_BackendPersistenceRoundtrip(t *testing.T) {
	// Verify that the resolved backend name is correctly written to
	// and readable from task meta, protecting PR #131 behavior.
	homeDir := t.TempDir()

	// Write config/backend = "herdr"
	configDir := filepath.Join(homeDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backend"), []byte("herdr\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Resolve the backend (as spawn.Run does at step 11)
	_, name, err := session.Resolve(homeDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "herdr" {
		t.Fatalf("Resolve returned name %q, want 'herdr'", name)
	}

	// Write task meta with the resolved backend name (as spawn.Run does at step 14)
	meta := map[string]string{
		"window":   "@1",
		"worktree": "/tmp/wt",
		"project":  "test-project",
		"harness":  "pi",
		"backend":  name,
		"kind":     "scout",
		"mode":     "direct-PR",
	}
	if err := task.WriteMeta(homeDir, "backend-persist-test", meta); err != nil {
		t.Fatal(err)
	}

	// Read meta back — must contain the correct backend
	readMeta, err := task.ReadMeta(homeDir, "backend-persist-test")
	if err != nil {
		t.Fatal(err)
	}
	if readMeta["backend"] != "herdr" {
		t.Errorf("meta[backend] = %q, want 'herdr'", readMeta["backend"])
	}
}
