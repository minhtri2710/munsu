package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

func TestCheckScopeGate_YoloDoesNotBypassGate(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("NO_MISTAKES_GATE", "")
	r := &Runner{args: Args{Yolo: true}, projPath: repo}
	if err := r.checkScopeGate(); err == nil {
		t.Fatal("expected gate refusal even with yolo")
	}
}

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
const piReadyCapture = `spec-driven-development, spec-to-code-compliance, stitch-design-taste,
supply-chain-risk-auditor, tasks-axi, tdd, teach, test-driven-development,
to-spec, to-tickets, triage, using-agent-skills, variant-analysis, vuln-report,
wayfinder, wizard, wooyun-legacy, writing-beats, writing-fragments,
writing-great-skills, writing-shape, zeroize-audit

[Extensions]
  @eko24ive/pi-ask:src, @ff-labs/pi-fff@latest:src,
@heyhuynhgiabuu/pi-search@latest:dist, @heyhuynhgiabuu/pi-task:dist,
@juicesharp/rpiv-advisor, @ogulcancelik/pi-herdr,
@sting8k/pi-vcc@latest,
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
/Users/beowulf/.treehouse/test-worktree/munsu
→ Trust
  Trust parent folder (...)
  Trust (this session only)
  Do not trust
`

func TestPiReadyPatterns(t *testing.T) {
	patterns := harness.GetReadyPatterns(harness.Pi)
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
	if !harness.IsTrustPrompt(agyTrustCapture, harness.Agy) {
		t.Error("IsTrustPrompt should detect trust in agy capture")
	}

	// Verify trust is NOT detected in pi capture.
	if harness.IsTrustPrompt(piReadyCapture, harness.Pi) {
		t.Error("IsTrustPrompt should NOT detect trust in pi capture")
	}

	// Verify trust is NOT detected in pi capture when checking agy patterns.
	if harness.IsTrustPrompt(piReadyCapture, harness.Agy) {
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
	if !harness.IsTrustPrompt(piTrustCapture, harness.Pi) {
		t.Error("IsTrustPrompt should detect trust in pi capture")
	}

	// Verify trust is NOT detected in agy capture when checking pi patterns.
	if harness.IsTrustPrompt(agyTrustCapture, harness.Pi) {
		t.Error("IsTrustPrompt should NOT detect trust in agy capture with pi harness")
	}

	// Verify trust is NOT detected in pi ready capture (not a trust dialog).
	if harness.IsTrustPrompt(piReadyCapture, harness.Pi) {
		t.Error("IsTrustPrompt should NOT detect trust in pi ready capture")
	}
}

func TestAgyReadyPatterns(t *testing.T) {
	patterns := harness.GetReadyPatterns(harness.Agy)
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
	if len(harness.DefaultReadyPatterns) == 0 {
		t.Fatal("DefaultReadyPatterns should not be empty")
	}
	for _, p := range harness.DefaultReadyPatterns {
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

func TestRun_NoMistakesPreflightFailsBeforeSessionAllocation(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("instructions"), 0644)
	os.MkdirAll(filepath.Join(homeDir, "data", "test-task"), 0755)
	os.WriteFile(filepath.Join(homeDir, "data", "test-task", "brief.md"), []byte("brief"), 0644)

	calledNewWindow := false
	fake := &fakeBackend{newWindow: func(session, name string) (string, error) {
		calledNewWindow = true
		return "", fmt.Errorf("must not allocate a session")
	}}
	preflightCalled := false
	r := NewRunner(Args{
		ID:          "test-task",
		ProjectName: "test-project",
		Mode:        "no-mistakes",
		HomeDir:     homeDir,
		Session:     fake,
		NoMistakesPreflight: func(repoPath string) error {
			preflightCalled = true
			if repoPath != projectDir {
				t.Fatalf("repoPath=%q, want %q", repoPath, projectDir)
			}
			return fmt.Errorf("incompatible no-mistakes gate agent")
		},
	})
	r.projPath = projectDir
	r.effectiveMode = "no-mistakes"

	err := r.preflightNoMistakes()
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("preflight error=%v", err)
	}
	if !preflightCalled {
		t.Fatal("preflight was not called")
	}
	if calledNewWindow {
		t.Fatal("session allocation occurred before compatibility preflight")
	}
}

func TestCheckNoMistakesCompatibility(t *testing.T) {
	tests := []struct {
		name                       string
		hasDocs                    bool
		disableProjectSettings     bool
		disableProjectSettingsYAML string // if non-empty, write this raw yaml instead of bool helper
		agents                     []string
		available                  map[string]bool
		wantErr                    bool
	}{
		{name: "no instruction files", agents: []string{"pi"}},
		{name: "pi incompatible", hasDocs: true, agents: []string{"pi"}, available: map[string]bool{"pi": true}, wantErr: true},
		{name: "pi ok with disable_project_settings", hasDocs: true, disableProjectSettings: true, agents: []string{"pi"}, available: map[string]bool{"pi": true}},
		{name: "codex compatible", hasDocs: true, agents: []string{"codex"}, available: map[string]bool{"codex": true}},
		{name: "fallback claude compatible", hasDocs: true, agents: []string{"pi", "claude"}, available: map[string]bool{"pi": true, "claude": true}},
		{name: "neutralizer unavailable", hasDocs: true, agents: []string{"codex", "pi"}, available: map[string]bool{"pi": true}, wantErr: true},
		{name: "codex override defeats neutralization", hasDocs: true, agents: []string{"codex"}, available: map[string]bool{"codex": true}, wantErr: true},
		{name: "claude override defeats neutralization", hasDocs: true, agents: []string{"claude"}, available: map[string]bool{"claude": true}, wantErr: true},
		{name: "disable_project_settings overrides codex defeat", hasDocs: true, disableProjectSettings: true, agents: []string{"codex"}, available: map[string]bool{"codex": true}},
		{name: "malformed no-mistakes yaml still requires neutralizer", hasDocs: true, disableProjectSettingsYAML: "disable_project_settings: [", agents: []string{"pi"}, available: map[string]bool{"pi": true}, wantErr: true},
		{name: "disable_project_settings false keeps preflight", hasDocs: true, disableProjectSettingsYAML: "disable_project_settings: false\n", agents: []string{"pi"}, available: map[string]bool{"pi": true}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if tc.hasDocs {
				os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("instructions"), 0644)
			}
			if tc.disableProjectSettingsYAML != "" {
				os.WriteFile(filepath.Join(repo, ".no-mistakes.yaml"), []byte(tc.disableProjectSettingsYAML), 0644)
			} else if tc.disableProjectSettings {
				os.WriteFile(filepath.Join(repo, ".no-mistakes.yaml"), []byte("disable_project_settings: true\n"), 0644)
			}
			cfg := noMistakesConfig{Agents: tc.agents}
			switch tc.name {
			case "codex override defeats neutralization", "disable_project_settings overrides codex defeat":
				cfg.AgentArgsOverride = map[string][]string{"codex": {"-c", "project_doc_max_bytes=4096"}}
			case "claude override defeats neutralization":
				cfg.AgentArgsOverride = map[string][]string{"claude": {"--setting-sources", "user,project"}}
			}
			err := checkNoMistakesCompatibility(repo, cfg, func(agent string) bool {
				return tc.available[agent]
			})
			if tc.wantErr && err == nil {
				t.Fatal("expected incompatibility error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestProjectSettingsDisabled(t *testing.T) {
	repo := t.TempDir()
	if projectSettingsDisabled(repo) {
		t.Fatal("missing yaml should be false")
	}
	os.WriteFile(filepath.Join(repo, ".no-mistakes.yaml"), []byte("disable_project_settings: true\n"), 0644)
	if !projectSettingsDisabled(repo) {
		t.Fatal("expected true")
	}
	os.WriteFile(filepath.Join(repo, ".no-mistakes.yaml"), []byte("disable_project_settings: false\n"), 0644)
	if projectSettingsDisabled(repo) {
		t.Fatal("expected false")
	}
}

func TestRun_ValidateMode(t *testing.T) {
	t.Setenv("MUNSU_ROLE", "general")
	t.Chdir(t.TempDir())
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
	tmpDir := createFakeNoMistakes(t, true, true)
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
	tmpDir := createFakeNoMistakes(t, true, true)
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

// TestEffectiveModeForSpawn_ExplicitNoMistakesNeverFallsBackToDirectPR verifies
// that explicit --mode=no-mistakes never returns "direct-PR" on failure.
func TestEffectiveModeForSpawn_ExplicitNoMistakesNeverFallsBackToDirectPR(t *testing.T) {
	t.Run("absent binary", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		mode, err := effectiveModeForSpawn(t.TempDir(), Args{Mode: "no-mistakes"})
		if err == nil {
			t.Fatalf("expected error, got mode=%q", mode)
		}
		if mode != "" {
			t.Errorf("mode must be empty on error, got %q", mode)
		}
	})

	t.Run("unsupported version", func(t *testing.T) {
		tmpDir := createFakeNoMistakesVersion(t, "0.5.0")
		t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

		mode, err := effectiveModeForSpawn(t.TempDir(), Args{Mode: "no-mistakes"})
		if err == nil {
			t.Fatalf("expected error for unsupported version, got mode=%q", mode)
		}
		if mode != "" {
			t.Errorf("mode must be empty on error, got %q", mode)
		}
	})

	t.Run("failed probe", func(t *testing.T) {
		tmpDir := t.TempDir()
		binPath := filepath.Join(tmpDir, "no-mistakes")
		if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

		mode, err := effectiveModeForSpawn(t.TempDir(), Args{Mode: "no-mistakes"})
		if err == nil {
			t.Fatalf("expected error for failed probe, got mode=%q", mode)
		}
		if mode != "" {
			t.Errorf("mode must be empty on error, got %q", mode)
		}
	})
}

// TestEffectiveModeForSpawn_ProjectNoMistakesNeverFallsBackToDirectPR verifies
// that project-registry no-mistakes never returns "direct-PR" on failure.
func TestEffectiveModeForSpawn_ProjectNoMistakesNeverFallsBackToDirectPR(t *testing.T) {
	t.Run("absent binary", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		mode, err := effectiveModeForSpawn(t.TempDir(), Args{ProjectMode: "no-mistakes"})
		if err == nil {
			t.Fatalf("expected error, got mode=%q", mode)
		}
		if mode != "" {
			t.Errorf("mode must be empty on error, got %q", mode)
		}
	})

	t.Run("unsupported version", func(t *testing.T) {
		tmpDir := createFakeNoMistakesVersion(t, "0.5.0")
		t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

		mode, err := effectiveModeForSpawn(t.TempDir(), Args{ProjectMode: "no-mistakes"})
		if err == nil {
			t.Fatalf("expected error for unsupported version, got mode=%q", mode)
		}
		if mode != "" {
			t.Errorf("mode must be empty on error, got %q", mode)
		}
	})
}

// TestEffectiveModeForSpawn_ConfigNoMistakesNeverFallsBackToDirectPR verifies
// that config/default-mode no-mistakes never returns "direct-PR" on failure.
func TestEffectiveModeForSpawn_ConfigNoMistakesNeverFallsBackToDirectPR(t *testing.T) {
	t.Run("absent binary", func(t *testing.T) {
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "default-mode"), []byte("no-mistakes"), 0644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", t.TempDir())

		// Use ResolveDeliveryMode directly (config/default-mode is step 3)
		mode, err := ResolveDeliveryMode(homeDir, "", "")
		if err == nil {
			t.Fatalf("expected error, got mode=%q", mode)
		}
		if mode != "" {
			t.Errorf("mode must be empty on error, got %q", mode)
		}
	})

	t.Run("unsupported version", func(t *testing.T) {
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "default-mode"), []byte("no-mistakes"), 0644); err != nil {
			t.Fatal(err)
		}
		tmpDir := createFakeNoMistakesVersion(t, "0.5.0")
		t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

		mode, err := ResolveDeliveryMode(homeDir, "", "")
		if err == nil {
			t.Fatalf("expected error for unsupported version, got mode=%q", mode)
		}
		if mode != "" {
			t.Errorf("mode must be empty on error, got %q", mode)
		}
	})
}

// TestResolveDeliveryMode_AutoFallbackOnIncompatible verifies that auto mode
// falls back to direct-PR when no-mistakes is on PATH but incompatible.
func TestResolveDeliveryMode_AutoFallbackOnIncompatible(t *testing.T) {
	tmpDir := createFakeNoMistakesVersion(t, "0.5.0")
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	mode, err := ResolveDeliveryMode(t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("auto should fallback to direct-PR for incompatible version, got %q", mode)
	}
}

// createFakeNoMistakesVersion creates a fake no-mistakes binary that reports
// the given semver version string.
func createFakeNoMistakesVersion(t *testing.T, version string) string {
	t.Helper()
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := "#!/bin/sh\necho \"no-mistakes version v" + version + " (test)\"\nexit 0\n"
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return tmpDir
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
	t.Setenv("MUNSU_ROLE", "general")
	t.Chdir(t.TempDir())
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

func TestRun_LifecycleGuardRefusesAbsentBacklogTask(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_ROLE", "general")

	// Explicitly configure manual backend — this test spawns against a manually
	// written backlog and expects native parser behavior.
	configDir := filepath.Join(tmpDir, "config")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644)

	// Create brief file so preflightBrief passes
	briefDir := filepath.Join(tmpDir, "data", "test-task")
	if err := os.MkdirAll(briefDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(briefDir, "brief.md"), []byte("# test brief"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Args{
		ID:          "test-task",
		ProjectName: "test-project",
		HomeDir:     tmpDir,
		Session:     &fakeBackend{},
	})

	if err == nil {
		t.Fatal("expected error from lifecycle guard for absent backlog task, got nil")
	}
	if !strings.Contains(err.Error(), "not found in backlog") {
		t.Errorf("error should mention backlog absence\n got: %v", err)
	}
}

// setupManualHome creates a minimal home directory with an explicit manual
// backlog-backend config. Tests that write directly to backlog.md must use
// this to avoid routing reads through tasks-axi in ModeAuto.
func setupManualHome(t *testing.T) string {
	t.Helper()
	homeDir := t.TempDir()
	configDir := filepath.Join(homeDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return homeDir
}

func TestCheckBacklogAuthority_RefusesBlockedTask(t *testing.T) {
	tmpDir := setupManualHome(t)
	t.Chdir(tmpDir)

	// Create backlog with a blocked item
	backlogPath := filepath.Join(tmpDir, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlogPath, []byte("# Backlog\n\n## 2025-01-01\n- [!] lifecycle-e2e: End-to-end lifecycle test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{args: Args{ID: "lifecycle-e2e"}, homeDir: tmpDir}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for blocked task, got nil")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error should mention blocked state\n got: %v", err)
	}
}

func TestCheckBacklogAuthority_RefusesDoneTask(t *testing.T) {
	tmpDir := setupManualHome(t)
	t.Chdir(tmpDir)

	// Create backlog with a done item
	backlogPath := filepath.Join(tmpDir, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlogPath, []byte("# Backlog\n\n## 2025-01-01\n- [x] done-task: This task is done\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{args: Args{ID: "done-task"}, homeDir: tmpDir}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for done task, got nil")
	}
	if !strings.Contains(err.Error(), "done") {
		t.Errorf("error should mention done state\n got: %v", err)
	}
}

func TestCheckBacklogAuthority_ReopenBypassesDone(t *testing.T) {
	tmpDir := setupManualHome(t)
	t.Chdir(tmpDir)

	// Create backlog with a done item
	backlogPath := filepath.Join(tmpDir, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlogPath, []byte("# Backlog\n\n## 2025-01-01\n- [x] done-task: This task is done\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{args: Args{ID: "done-task", Reopen: true}, homeDir: tmpDir}
	err := r.checkBacklogAuthority()
	if err != nil {
		t.Fatalf("expected no error with --reopen for done task, got: %v", err)
	}
}

func TestCheckBacklogAuthority_AllowsInFlightWithoutLiveMeta(t *testing.T) {
	tmpDir := setupManualHome(t)
	t.Chdir(tmpDir)

	// Create backlog with an in-flight item and no meta/window (tasks-axi start before spawn).
	backlogPath := filepath.Join(tmpDir, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlogPath, []byte("# Backlog\n\n## 2025-01-01\n- [-] live-task: Currently in-flight\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{args: Args{ID: "live-task"}, homeDir: tmpDir}
	if err := r.checkBacklogAuthority(); err != nil {
		t.Fatalf("in-flight without live meta must ALLOW spawn, got: %v", err)
	}
}

func TestCheckBacklogAuthority_RefusesInFlightWithLiveMeta(t *testing.T) {
	tmpDir := setupManualHome(t)
	t.Chdir(tmpDir)

	backlogPath := filepath.Join(tmpDir, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlogPath, []byte("# Backlog\n\n## 2025-01-01\n- [-] live-task: Currently in-flight\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := task.WriteMeta(tmpDir, "live-task", map[string]string{"window": "@1"}); err != nil {
		t.Fatal(err)
	}

	r := &Runner{args: Args{ID: "live-task"}, homeDir: tmpDir}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for in-flight with live meta, got nil")
	}
	if !strings.Contains(err.Error(), "live session") && !strings.Contains(err.Error(), "live soldier") {
		t.Errorf("error should mention live session\n got: %v", err)
	}
}

func TestCheckBacklogAuthority_RefusesAlreadyLiveMeta(t *testing.T) {
	tmpDir := setupManualHome(t)
	t.Chdir(tmpDir)

	// Create backlog with a queued item
	backlogPath := filepath.Join(tmpDir, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlogPath, []byte("# Backlog\n\n## 2025-01-01\n- [ ] queued-task: Ready to go\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create meta file simulating already-live soldier session
	if err := task.WriteMeta(tmpDir, "queued-task", map[string]string{"window": "@1"}); err != nil {
		t.Fatal(err)
	}

	r := &Runner{args: Args{ID: "queued-task"}, homeDir: tmpDir}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for already-live task, got nil")
	}
	if !strings.Contains(err.Error(), "live soldier session") {
		t.Errorf("error should mention live session\n got: %v", err)
	}
}

func TestCheckBacklogAuthority_RefusesDuplicateID(t *testing.T) {
	tmpDir := setupManualHome(t)
	t.Chdir(tmpDir)

	// Create backlog with duplicate IDs
	backlogPath := filepath.Join(tmpDir, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlogPath), 0755); err != nil {
		t.Fatal(err)
	}
	data := "# Backlog\n\n## 2025-01-01\n- [ ] dup-task: First entry\n- [ ] dup-task: Duplicate entry\n"
	if err := os.WriteFile(backlogPath, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{args: Args{ID: "dup-task"}, homeDir: tmpDir}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for duplicate ID, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate entries\n got: %v", err)
	}
}

func TestCreateSessionUsesProjectPrefixedSoldierTabLabel(t *testing.T) {
	var gotName string
	fake := &fakeBackend{newWindow: func(session, name string) (string, error) {
		gotName = name
		return "win-1", nil
	}}
	r := NewRunner(Args{
		ID:          "W 1",
		ProjectName: "API Platform",
		HomeDir:     t.TempDir(),
		Session:     fake,
	})

	if err := r.createSession(); err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if gotName != "mu-api-platform-w-1" {
		t.Fatalf("tab label = %q, want %q", gotName, "mu-api-platform-w-1")
	}
}

func TestAuthorizeSpawnRejectsRegularSoldier(t *testing.T) {
	err := authorizeSpawn("soldier", t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "regular soldiers cannot spawn") {
		t.Fatalf("authorizeSpawn() error = %v, want regular-soldier refusal", err)
	}
}

func TestCheckSpawnAuthorityRejectsManagedHerdrSoldierEvenWithoutRole(t *testing.T) {
	homeDir := t.TempDir()
	if err := task.WriteMeta(homeDir, "soldier-1", map[string]string{
		"kind":          "ship",
		"herdr_pane_id": "w1:p9",
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "w1:p9")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("MUNSU_ROLE", "")
	r := &Runner{homeDir: homeDir}
	err := r.checkSpawnAuthority()
	if err == nil || !strings.Contains(err.Error(), "managed soldier endpoints cannot spawn") {
		t.Fatalf("checkSpawnAuthority() error = %v, want managed-endpoint refusal", err)
	}
}

func TestCurrentEndpointKindFindsCaptainHerdrPane(t *testing.T) {
	homeDir := t.TempDir()
	if err := task.WriteMeta(homeDir, "captain:sm-1", map[string]string{
		"kind":          "captain",
		"herdr_pane_id": "w1:p8",
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "w1:p8")
	t.Setenv("TMUX_PANE", "")
	kind, found, err := currentEndpointKind(homeDir)
	if err != nil || !found || kind != "captain" {
		t.Fatalf("currentEndpointKind() = %q, %v, %v; want captain, true, nil", kind, found, err)
	}
}

func TestCurrentEndpointKindFindsTmuxWindowForPane(t *testing.T) {
	homeDir := t.TempDir()
	if err := task.WriteMeta(homeDir, "soldier-2", map[string]string{
		"kind":   "ship",
		"window": "munsu:@7",
	}); err != nil {
		t.Fatal(err)
	}
	original := tmuxWindowForPane
	t.Cleanup(func() { tmuxWindowForPane = original })
	tmuxWindowForPane = func(pane string) (string, error) {
		if pane != "%3" {
			t.Fatalf("pane = %q, want %%3", pane)
		}
		return "@7", nil
	}
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("TMUX_PANE", "%3")
	kind, found, err := currentEndpointKind(homeDir)
	if err != nil || !found || kind != "ship" {
		t.Fatalf("currentEndpointKind() = %q, %v, %v; want ship, true, nil", kind, found, err)
	}
}

func TestCurrentEndpointKindFailsClosedWhenTmuxPaneCannotResolve(t *testing.T) {
	original := tmuxWindowForPane
	t.Cleanup(func() { tmuxWindowForPane = original })
	tmuxWindowForPane = func(pane string) (string, error) {
		return "", fmt.Errorf("lookup failed")
	}
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("TMUX_PANE", "%3")
	if _, _, err := currentEndpointKind(t.TempDir()); err == nil || !strings.Contains(err.Error(), "lookup failed") {
		t.Fatalf("currentEndpointKind() error = %v, want lookup failure", err)
	}
}

func TestAuthorizeSpawnAllowsValidatedCaptain(t *testing.T) {
	homeDir := t.TempDir()
	if err := captain.SeedProvenance(homeDir, "sm-1"); err != nil {
		t.Fatal(err)
	}
	if err := authorizeSpawn("captain", homeDir, homeDir); err != nil {
		t.Fatalf("authorizeSpawn() error = %v, want validated captain allowed", err)
	}
}

func TestAuthorizeSpawnRejectsCaptainOutsideItsHome(t *testing.T) {
	homeDir := t.TempDir()
	if err := captain.SeedProvenance(homeDir, "sm-1"); err != nil {
		t.Fatal(err)
	}
	err := authorizeSpawn("captain", homeDir, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "must spawn from its home") {
		t.Fatalf("authorizeSpawn() error = %v, want captain cwd refusal", err)
	}
}

func TestAuthorizeSpawnRejectsUnknownRole(t *testing.T) {
	err := authorizeSpawn("delegate", t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unknown MUNSU_ROLE") {
		t.Fatalf("authorizeSpawn() error = %v, want unknown-role refusal", err)
	}
}

func TestAuthorizeSpawnRejectsLinkedWorktreeWithoutRole(t *testing.T) {
	primary := t.TempDir()
	runGit(t, primary, "init")
	runGit(t, primary, "config", "user.email", "test@example.com")
	runGit(t, primary, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(primary, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, primary, "add", "README.md")
	runGit(t, primary, "commit", "-m", "init")
	worktreeDir := filepath.Join(t.TempDir(), "soldier-worktree")
	runGit(t, primary, "worktree", "add", worktreeDir)

	err := authorizeSpawn("", t.TempDir(), worktreeDir)
	if err == nil || !strings.Contains(err.Error(), "linked-worktree callers cannot spawn") {
		t.Fatalf("authorizeSpawn() error = %v, want linked-worktree refusal", err)
	}
}

func TestAuthorizeSpawnAllowsCaptainPrimaryCheckout(t *testing.T) {
	primary := t.TempDir()
	runGit(t, primary, "init")
	if err := authorizeSpawn("general", t.TempDir(), primary); err != nil {
		t.Fatalf("authorizeSpawn() error = %v, want general allowed", err)
	}
}

func TestBootstrapWindowExportsSoldierRole(t *testing.T) {
	worktreeDir := t.TempDir()
	var sent string
	r := &Runner{
		homeDir:    t.TempDir(),
		wtPath:     worktreeDir,
		launchBin:  "pi",
		launchArgs: []string{"--model", "gpt-5", "--thinking", "high", "test prompt"},
		windowID:   "win-1",
		endpoints:  fakeEndpointCapabilities{backend: &fakeBackend{sendKeys: func(windowID, text string) error { sent = text; return nil }}},
		endpoint:   CreatedEndpoint{Backend: "test", Handle: "win-1"},
	}
	r.bootstrapWindow()

	script, err := os.ReadFile(filepath.Join(worktreeDir, ".soldier-launch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "export MUNSU_ROLE=soldier") {
		t.Fatalf("launch script missing soldier role:\n%s", script)
	}
	if sent == "" {
		t.Fatal("bootstrapWindow did not send launch command")
	}
	// Verify prompt arg is in the script.
	if !strings.Contains(string(script), "test prompt") {
		t.Fatalf("launch script must contain prompt argument:\n%s", script)
	}
}

func TestPreflightDelivery_BlocksOnDirectPRWithoutGhAuth(t *testing.T) {
	// direct-PR preflight should fail when gh is not on PATH
	t.Setenv("PATH", t.TempDir())
	r := &Runner{
		effectiveMode: "direct-PR",
		projPath:      "",
	}
	err := r.preflightDelivery()
	if err == nil {
		t.Fatal("expected preflight error for direct-PR without gh auth")
	}
	if !strings.Contains(err.Error(), "gh-auth") {
		t.Errorf("error should mention gh-auth, got: %v", err)
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error should say blocked, got: %v", err)
	}
}

func TestPreflightDelivery_LocalOnlyAlwaysPasses(t *testing.T) {
	r := &Runner{
		effectiveMode: "local-only",
	}
	err := r.preflightDelivery()
	if err != nil {
		t.Errorf("local-only should always pass, got: %v", err)
	}
}

func TestPreflightDelivery_UnknownModeError(t *testing.T) {
	r := &Runner{
		effectiveMode: "bogus-mode",
	}
	err := r.preflightDelivery()
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if !strings.Contains(err.Error(), "unknown delivery mode") {
		t.Errorf("error should mention unknown delivery mode, got: %v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestWaitForHarnessReady_FailurePatternDetected(t *testing.T) {
	fake := &fakeBackend{
		capture: func(windowID string, lines int) (string, error) {
			return "Auth required: please set ANTHROPIC_API_KEY", nil
		},
	}
	r := &Runner{
		harness: "claude", endpoints: fakeEndpointCapabilities{backend: fake}, endpoint: CreatedEndpoint{Backend: "test", Handle: "win-1"}, windowID: "win-1",
	}
	err := r.waitForHarnessReady(5)
	if err == nil {
		t.Fatal("expected failure pattern error, got nil")
	}
	if !strings.Contains(err.Error(), "Auth required") {
		t.Errorf("error should contain failure pattern, got: %v", err)
	}
}

func TestWaitForHarnessReady_ReadyPatternSuccess(t *testing.T) {
	fake := &fakeBackend{
		capture: func(windowID string, lines int) (string, error) {
			return "> ready", nil
		},
	}
	r := &Runner{
		harness: "pi", endpoints: fakeEndpointCapabilities{backend: fake}, endpoint: CreatedEndpoint{Backend: "test", Handle: "win-1"}, windowID: "win-1",
	}
	if err := r.waitForHarnessReady(5); err != nil {
		t.Fatalf("expected ready success, got: %v", err)
	}
}

func TestWaitForHarnessReady_Timeout(t *testing.T) {
	fake := &fakeBackend{
		capture: func(windowID string, lines int) (string, error) {
			return "Starting...", nil // never shows ready or failure pattern
		},
	}
	r := &Runner{
		harness: "pi", endpoints: fakeEndpointCapabilities{backend: fake}, endpoint: CreatedEndpoint{Backend: "test", Handle: "win-1"}, windowID: "win-1",
	}
	err := r.waitForHarnessReady(2)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "not ready after") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestWaitAndInjectBrief_FailurePatternTearsDown(t *testing.T) {
	teardownCalled := false
	homeDir := t.TempDir()
	fake := &fakeBackend{
		capture: func(windowID string, lines int) (string, error) {
			return "AuthenticationError: model `gpt-5.2-codex` not found", nil
		},
		teardown: func(windowID string) error {
			teardownCalled = true
			return nil
		},
	}
	dataDir := filepath.Join(homeDir, "data", "handshake-test")
	_ = os.MkdirAll(dataDir, 0755)
	r := &Runner{
		homeDir: homeDir, harness: "codex", endpoints: fakeEndpointCapabilities{backend: fake}, endpoint: CreatedEndpoint{Backend: "test", Handle: "win-1"}, windowID: "win-1",
		briefData: []byte("# test brief"),
	}
	r.args.ID = "handshake-test"
	err := r.waitAndInjectBrief()
	if err == nil {
		t.Fatal("expected handshake failure error, got nil")
	}
	if !strings.Contains(err.Error(), "handshake failed") {
		t.Errorf("expected handshake failure error, got: %v", err)
	}
	if !teardownCalled {
		t.Error("teardown was not called after failure pattern detection")
	}
	// Verify failure evidence was persisted
	failPath := filepath.Join(dataDir, "ready-fail.txt")
	if _, statErr := os.Stat(failPath); statErr != nil {
		t.Errorf("failure evidence file not written: %v", statErr)
	}
}

func TestCheckCaptainBacklogAuthority_SkippedWhenForceSet(t *testing.T) {
	r := &Runner{
		args:      Args{Force: true},
		spawnRole: "captain",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("expected no error when --force is set, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_SkippedWhenNotCaptain(t *testing.T) {
	r := &Runner{
		args:      Args{},
		spawnRole: "general",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("expected no error when role is not captain, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_RefusesAbsentTask(t *testing.T) {
	homeDir := t.TempDir()
	restore := mockReadBacklogTaskState("", "", false, nil)
	defer restore()
	r := &Runner{
		args:      Args{ID: "absent-task"},
		spawnRole: "captain",
		homeDir:   homeDir,
	}
	err := r.checkCaptainBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for absent task, got nil")
	}
	if !strings.Contains(err.Error(), "not found in backlog") {
		t.Errorf("error should mention not found, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_RefusesBlockedTask(t *testing.T) {
	restore := mockReadBacklogTaskState("queued", "some-dependency", true, nil)
	defer restore()
	r := &Runner{
		args:      Args{ID: "blocked-task"},
		spawnRole: "captain",
	}
	err := r.checkCaptainBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for blocked task, got nil")
	}
	if !strings.Contains(err.Error(), "blocked-by") {
		t.Errorf("error should mention blocked-by, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_RefusesDoneTask(t *testing.T) {
	restore := mockReadBacklogTaskState("done", "", true, nil)
	defer restore()
	r := &Runner{
		args:      Args{ID: "done-task"},
		spawnRole: "captain",
	}
	err := r.checkCaptainBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for done task, got nil")
	}
	if !strings.Contains(err.Error(), "done") {
		t.Errorf("error should mention done, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_RefusesLiveSessionWithWindow(t *testing.T) {
	homeDir := t.TempDir()
	_ = task.WriteMeta(homeDir, "live-task", map[string]string{"kind": "ship", "window": "default:w1:p1"})
	r := &Runner{
		args:      Args{ID: "live-task"},
		spawnRole: "captain",
		homeDir:   homeDir,
	}
	err := r.checkCaptainBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for live session with window, got nil")
	}
	if !strings.Contains(err.Error(), "live soldier session") {
		t.Errorf("error should mention live soldier session, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_AllowsKindOnlyMetaWithoutWindow(t *testing.T) {
	homeDir := t.TempDir()
	_ = task.WriteMeta(homeDir, "pre-spawn", map[string]string{"kind": "ship"})
	restore := mockReadBacklogTaskState("in_flight", "", true, nil)
	defer restore()
	r := &Runner{
		args:      Args{ID: "pre-spawn"},
		spawnRole: "captain",
		homeDir:   homeDir,
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("kind-only meta without window must ALLOW start→spawn, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_AllowsReadyTask(t *testing.T) {
	restore := mockReadBacklogTaskState("queued", "", true, nil)
	defer restore()
	r := &Runner{
		args:      Args{ID: "ready-task"},
		spawnRole: "captain",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("expected no error for queued task, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_AllowsInFlightWithoutLiveMeta(t *testing.T) {
	restore := mockReadBacklogTaskState("in-flight", "", true, nil)
	defer restore()
	r := &Runner{
		args:      Args{ID: "in-flight-task"},
		spawnRole: "captain",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("in-flight backlog without live meta must ALLOW spawn, got: %v", err)
	}
}

func TestCheckCaptainBacklogAuthority_AllowsTasksAxiInFlightUnderscore(t *testing.T) {
	restore := mockReadBacklogTaskState("in_flight", "", true, nil)
	defer restore()
	r := &Runner{
		args:      Args{ID: "started-task"},
		spawnRole: "captain",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("tasks-axi in_flight without live meta must ALLOW spawn, got: %v", err)
	}
}

// mockReadBacklogTaskState replaces readBacklogTaskState for testing
// and returns a restore function.
func mockReadBacklogTaskState(state, blocked string, found bool, err error) func() {
	original := readBacklogTaskState
	readBacklogTaskState = func(homeDir, id string) (string, string, bool, error) {
		return state, blocked, found, err
	}
	return func() { readBacklogTaskState = original }
}

// createFakeNoMistakes writes a fake no-mistakes binary to a temp directory
// and returns that directory. The binary handles --version and axi status --help
// when the corresponding flags are true.
func createFakeNoMistakes(t *testing.T, respondVersion, respondAxi bool) string {
	t.Helper()
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")

	var script string
	if respondVersion {
		script += `case "--version" in
  "$1")
    echo "no-mistakes version v1.40.0 (test)"
    exit 0
    ;;
esac
`
	}
	if respondAxi {
		script += `case "$1" in
  axi)
    case "$2" in
      status)
        case "$3" in
          --help)
            echo "Show the active run in detail"
            echo "Usage:"
            echo "  no-mistakes axi status [flags]"
            exit 0
            ;;
        esac
        ;;
    esac
    ;;
esac
`
	}
	script += "exit 0\n"

	content := "#!/bin/sh\n" + script
	if err := os.WriteFile(binPath, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

func TestSpawn_PostCreateVerificationFailure_NoMetaNoSpawnedStatus(t *testing.T) {
	t.Setenv("MUNSU_ROLE", "general")
	t.Chdir(t.TempDir())
	homeDir := t.TempDir()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("GEMINI_API_KEY", "test-key")

	configDir := filepath.Join(homeDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644); err != nil {
		t.Fatal(err)
	}

	backlogPath := filepath.Join(homeDir, "data", "backlog.md")
	if err := os.MkdirAll(filepath.Dir(backlogPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlogPath, []byte("# Backlog\n\n## 2025-01-01\n- [ ] reconcile-task: Ready\n"), 0644); err != nil {
		t.Fatal(err)
	}

	projectDir := filepath.Join(homeDir, "projects", "test-proj")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), []byte("# instructions"), 0644); err != nil {
		t.Fatal(err)
	}
	cmdInit := exec.Command("git", "init")
	cmdInit.Dir = projectDir
	if err := cmdInit.Run(); err != nil {
		t.Fatal(err)
	}
	cmdCommit := exec.Command("git", "commit", "--allow-empty", "-m", "initial commit")
	cmdCommit.Dir = projectDir
	cmdCommit.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	if err := cmdCommit.Run(); err != nil {
		t.Fatal(err)
	}
	projectsPath := filepath.Join(homeDir, "data", "projects.md")
	if err := os.MkdirAll(filepath.Dir(projectsPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectsPath, []byte("# Projects\n\n- test-proj [local-only] - Test project (added 2025-01-01)\n"), 0644); err != nil {
		t.Fatal(err)
	}

	briefDir := filepath.Join(homeDir, "data", "reconcile-task")
	if err := os.MkdirAll(briefDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(briefDir, "brief.md"), []byte("# test brief"), 0644); err != nil {
		t.Fatal(err)
	}

	fakeBk := &fakeBackend{
		newWindow: func(session, name string) (string, error) {
			return "default:w6F:p3", nil
		},
		alive: func(windowID string) bool {
			return false // pane failed verification immediately
		},
	}

	args := Args{
		ID:          "reconcile-task",
		ProjectName: "test-proj",
		HarnessFlag: "pi",
		HomeDir:     homeDir,
		Session:     fakeBk,
		Mode:        "local-only",
	}

	_, err := Run(args)
	if err == nil {
		t.Fatal("Run expected error when post-create verification fails, got nil")
	}
	if !strings.Contains(err.Error(), "failed verification") {
		t.Errorf("expected failed verification error, got: %v", err)
	}

	metaPath := filepath.Join(homeDir, "state", "reconcile-task.meta")
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Errorf("task meta file should NOT exist on failed verification: %s", metaPath)
	}

	statusLines, _ := task.ReadStatus(homeDir, "reconcile-task")
	for _, l := range statusLines {
		if strings.Contains(l, "working: spawned") {
			t.Errorf("status log should NOT contain 'working: spawned', got: %s", l)
		}
	}
}

// TestRegression_ResolveSkillsWithoutSrcwalk proves that resolveSkills produces
// a valid skill catalog without requiring srcwalk. This is a focused regression
// guard for the remove-srcwalk-integration task.
func TestRegression_ResolveSkillsWithoutSrcwalk(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		wantGhAxi bool
		wantQmd   bool
	}{
		{name: "ship", kind: "ship", wantGhAxi: true, wantQmd: false},
		{name: "scout", kind: "scout", wantGhAxi: false, wantQmd: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{
				args: Args{
					Kind: tc.kind,
					Mode: "direct-PR",
				},
				effectiveMode: "direct-PR",
				spawnRole:     "soldier",
			}

			required, optional, diags := r.resolveSkills()
			if len(diags) > 0 {
				t.Fatalf("resolveSkills returned diagnostics: %v", diags)
			}

			// Verify srcwalk is NOT in required or optional.
			for _, s := range required {
				if s.Name == "srcwalk" {
					t.Error("srcwalk must NOT be in required skills")
				}
			}
			for _, s := range optional {
				if s.Name == "srcwalk" {
					t.Error("srcwalk must NOT be in optional skills")
				}
			}

			// Verify expected required skills.
			var foundGhAxi, foundQmd bool
			for _, s := range required {
				if s.Name == "gh-axi" {
					foundGhAxi = true
					if !s.Applicable {
						t.Error("gh-axi must be applicable")
					}
				}
				if s.Name == "qmd" {
					foundQmd = true
					if !s.Applicable {
						t.Error("qmd must be applicable")
					}
				}
			}

			if tc.wantGhAxi && !foundGhAxi {
				t.Error("gh-axi must be in required skills")
			}
			if tc.wantQmd && !foundQmd {
				t.Error("qmd must be in required skills")
			}
		})
	}
}
