package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	fleetconfig "github.com/minhtri2710/munsu/internal/config"
)

// readyProbe is a probe stub that reports the installed no-mistakes reference
// (1.45.4) as Ready.
func readyProbe() ProbeResult {
	return ProbeResult{State: backend.Ready, Version: "1.45.4", Path: "/usr/local/bin/no-mistakes"}
}

// TestProbeNoMistakesGateAgent covers the accepted capability preflight
// matrix: pi + disable_project_settings, supported codex/claude, unavailable
// agents, and unsupported neutralization.
func TestProbeNoMistakesGateAgent(t *testing.T) {
	tests := []struct {
		name                       string
		hasDocs                    bool
		disableProjectSettings     bool
		disableProjectSettingsYAML string
		agents                     []string
		available                  map[string]bool
		wantBlocker                GateBlockerCategory
		wantSelected               string
	}{
		{
			name:                   "pi with disable_project_settings is supported",
			hasDocs:                true,
			disableProjectSettings: true,
			agents:                 []string{"pi"},
			available:              map[string]bool{"pi": true},
			wantSelected:           "pi",
		},
		{
			name:         "codex supported without opt-out",
			hasDocs:      true,
			agents:       []string{"codex"},
			available:    map[string]bool{"codex": true},
			wantSelected: "codex",
		},
		{
			name:         "claude supported without opt-out",
			hasDocs:      true,
			agents:       []string{"claude"},
			available:    map[string]bool{"claude": true},
			wantSelected: "claude",
		},
		{
			name:                   "codex and claude supported under opt-out",
			hasDocs:                true,
			disableProjectSettings: true,
			agents:                 []string{"codex", "claude"},
			available:              map[string]bool{"codex": true, "claude": true},
			wantSelected:           "codex",
		},
		{
			name:                   "opencode refused under disable_project_settings",
			hasDocs:                true,
			disableProjectSettings: true,
			agents:                 []string{"opencode"},
			available:              map[string]bool{"opencode": true},
			wantBlocker:            GateBlockerUnsupportedNeutralization,
		},
		{
			name:         "pi without opt-out cannot neutralize instructions",
			hasDocs:      true,
			agents:       []string{"pi"},
			available:    map[string]bool{"pi": true},
			wantBlocker:  GateBlockerUnsupportedNeutralization,
			wantSelected: "pi",
		},
		{
			name:        "configured gate agent unavailable",
			hasDocs:     true,
			agents:      []string{"pi"},
			available:   map[string]bool{},
			wantBlocker: GateBlockerAgentUnavailable,
		},
		{
			name:        "auto with no installed native agent",
			hasDocs:     true,
			agents:      []string{"auto"},
			available:   map[string]bool{},
			wantBlocker: GateBlockerAgentUnavailable,
		},
		{
			name:                   "auto with only pi installed under opt-out",
			hasDocs:                true,
			disableProjectSettings: true,
			agents:                 []string{"auto"},
			available:              map[string]bool{"pi": true},
			wantSelected:           "pi",
		},
		{
			name:                   "auto with only opencode installed under opt-out",
			hasDocs:                true,
			disableProjectSettings: true,
			agents:                 []string{"auto"},
			available:              map[string]bool{"opencode": true},
			wantBlocker:            GateBlockerUnsupportedNeutralization,
		},
		{
			name:         "no instructions and no opt-out needs no neutralization",
			agents:       []string{"opencode"},
			available:    map[string]bool{"opencode": true},
			wantSelected: "opencode",
		},
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
			probe := ProbeNoMistakesGateAgent(repo, noMistakesConfig{Agents: tc.agents}, func(agent string) bool {
				return tc.available[agent]
			}, readyProbe)
			got := GateBlockerNone
			if probe.Blocker != nil {
				got = probe.Blocker.Category
			}
			if got != tc.wantBlocker {
				t.Fatalf("blocker category = %q, want %q (detail: %v)", got, tc.wantBlocker, probe.Blocker)
			}
			if tc.wantSelected != "" && probe.Selected != tc.wantSelected {
				t.Errorf("Selected = %q, want %q", probe.Selected, tc.wantSelected)
			}
		})
	}
}

// TestProbeNoMistakesGateAgent_BlockerCategories verifies the exact blocker
// categories for the remaining failure modes: command failure (CLI not
// runnable) and config mismatch (unknown gate agent).
func TestProbeNoMistakesGateAgent_BlockerCategories(t *testing.T) {
	t.Run("absent binary is command failure", func(t *testing.T) {
		probe := ProbeNoMistakesGateAgent(t.TempDir(), noMistakesConfig{Agents: []string{"pi"}}, func(string) bool { return true }, func() ProbeResult {
			return ProbeResult{State: backend.Absent, Detail: "no-mistakes not found on PATH"}
		})
		if probe.Blocker == nil || probe.Blocker.Category != GateBlockerCommandFailure {
			t.Fatalf("blocker = %v, want command-failure", probe.Blocker)
		}
	})

	t.Run("failed probe is command failure", func(t *testing.T) {
		probe := ProbeNoMistakesGateAgent(t.TempDir(), noMistakesConfig{Agents: []string{"pi"}}, func(string) bool { return true }, func() ProbeResult {
			return ProbeResult{State: backend.Failed, Detail: "cannot check version"}
		})
		if probe.Blocker == nil || probe.Blocker.Category != GateBlockerCommandFailure {
			t.Fatalf("blocker = %v, want command-failure", probe.Blocker)
		}
	})

	t.Run("unsupported version is command failure", func(t *testing.T) {
		probe := ProbeNoMistakesGateAgent(t.TempDir(), noMistakesConfig{Agents: []string{"pi"}}, func(string) bool { return true }, func() ProbeResult {
			return ProbeResult{State: backend.Unsupported, Version: "0.5.0", Detail: "no-mistakes version 0.5.0 < minimum 1.20.0"}
		})
		if probe.Blocker == nil || probe.Blocker.Category != GateBlockerCommandFailure {
			t.Fatalf("blocker = %v, want command-failure", probe.Blocker)
		}
	})

	t.Run("unknown gate agent is config mismatch", func(t *testing.T) {
		probe := ProbeNoMistakesGateAgent(t.TempDir(), noMistakesConfig{Agents: []string{"bogus-agent"}}, func(string) bool { return true }, readyProbe)
		if probe.Blocker == nil || probe.Blocker.Category != GateBlockerConfigMismatch {
			t.Fatalf("blocker = %v, want config-mismatch", probe.Blocker)
		}
		if !strings.Contains(probe.Blocker.Detail, "bogus-agent") {
			t.Errorf("detail should name the unknown agent, got: %s", probe.Blocker.Detail)
		}
	})

	t.Run("guidance names supported delivery-mode alternatives", func(t *testing.T) {
		repo := t.TempDir()
		os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("instructions"), 0644)
		probe := ProbeNoMistakesGateAgent(repo, noMistakesConfig{Agents: []string{"pi"}}, func(string) bool { return true }, readyProbe)
		if probe.Blocker == nil {
			t.Fatal("expected unsupported-neutralization blocker without opt-out and instructions")
		}
		if !strings.Contains(probe.Blocker.Guidance, "direct-PR") {
			t.Errorf("guidance must offer explicit direct-PR selection, got: %s", probe.Blocker.Guidance)
		}
	})
}

// TestRunnerPreflightNoMistakes_DirectPRFallback verifies the explicit
// configured direct-PR policy contract: fallback only under policy, with the
// blocker recorded as audit evidence; without policy the blocker fails closed.
func TestRunnerPreflightNoMistakes_DirectPRFallback(t *testing.T) {
	blocker := &GateBlockerError{
		Category: GateBlockerUnsupportedNeutralization,
		Detail:   "gate agent(s) opencode cannot neutralize",
		Guidance: "select pi, codex, or claude",
	}

	t.Run("no policy fails closed with blocker", func(t *testing.T) {
		r := NewRunner(Args{})
		r.effectiveMode = "no-mistakes"
		r.allowDirectPRFallback = false
		r.args.NoMistakesPreflight = func(string) error { return blocker }

		err := r.preflightNoMistakes()
		if err == nil {
			t.Fatal("expected blocker error without policy")
		}
		var got *GateBlockerError
		if !errors.As(err, &got) || got.Category != GateBlockerUnsupportedNeutralization {
			t.Fatalf("error = %v, want GateBlockerError(unsupported-neutralization)", err)
		}
		if r.effectiveMode != "no-mistakes" {
			t.Errorf("effectiveMode must stay no-mistakes, got %q", r.effectiveMode)
		}
	})

	t.Run("policy falls back to direct-PR with audit evidence", func(t *testing.T) {
		r := NewRunner(Args{})
		r.effectiveMode = "no-mistakes"
		r.requestedMode = "no-mistakes"
		r.allowDirectPRFallback = true
		r.args.NoMistakesPreflight = func(string) error { return blocker }

		if err := r.preflightNoMistakes(); err != nil {
			t.Fatalf("policy fallback must not error: %v", err)
		}
		if r.effectiveMode != "direct-PR" {
			t.Errorf("effectiveMode = %q, want direct-PR", r.effectiveMode)
		}
		if !strings.Contains(r.fallbackReason, "unsupported-neutralization") {
			t.Errorf("fallbackReason must carry the exact blocker category: %q", r.fallbackReason)
		}
		if !strings.Contains(r.fallbackReason, "allow-direct-pr-fallback") {
			t.Errorf("fallbackReason must record the policy basis: %q", r.fallbackReason)
		}
	})

	t.Run("non-blocker error never falls back even with policy", func(t *testing.T) {
		r := NewRunner(Args{})
		r.effectiveMode = "no-mistakes"
		r.allowDirectPRFallback = true
		r.args.NoMistakesPreflight = func(string) error { return errors.New("unrelated failure") }

		err := r.preflightNoMistakes()
		if err == nil || !strings.Contains(err.Error(), "unrelated failure") {
			t.Fatalf("unrelated errors must propagate, got %v", err)
		}
		if r.effectiveMode != "no-mistakes" {
			t.Errorf("effectiveMode must not change, got %q", r.effectiveMode)
		}
	})

	t.Run("non-no-mistakes mode skips preflight", func(t *testing.T) {
		r := NewRunner(Args{})
		r.effectiveMode = "direct-PR"
		called := false
		r.args.NoMistakesPreflight = func(string) error { called = true; return nil }
		if err := r.preflightNoMistakes(); err != nil {
			t.Fatal(err)
		}
		if called {
			t.Fatal("preflight must be skipped outside no-mistakes mode")
		}
	})
}

// TestResolveSpawnProjectConfig_AllowDirectPRFallback verifies the explicit
// configured direct-PR policy flows from the typed fleet base overlay into the
// spawn project config (and thus the Runner), and defaults false.
func TestResolveSpawnProjectConfig_AllowDirectPRFallback(t *testing.T) {
	t.Run("base policy true resolves true", func(t *testing.T) {
		home := t.TempDir()
		storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
			SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
			Config: fleetconfig.ProjectOverlay{
				SoldierHarness:        "pi",
				Backend:               "tmux",
				AllowDirectPRFallback: &[]bool{true}[0],
			},
		}, []testProjectRecord{
			{Name: "alpha", Path: filepath.Join(home, "projects", "alpha")},
		}, nil)

		resolved, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
		if err != nil {
			t.Fatal(err)
		}
		if !resolved.AllowDirectPRFallback {
			t.Error("AllowDirectPRFallback = false, want true from base overlay")
		}
	})

	t.Run("unset policy defaults false", func(t *testing.T) {
		home := t.TempDir()
		storeTestDocuments(t, home, fleetconfig.FleetBaseDocument{
			SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
			Config: fleetconfig.ProjectOverlay{
				SoldierHarness: "pi",
				Backend:        "tmux",
			},
		}, []testProjectRecord{
			{Name: "alpha", Path: filepath.Join(home, "projects", "alpha")},
		}, nil)

		resolved, err := ResolveSpawnProjectConfig(home, Args{ProjectName: "alpha"}, "general")
		if err != nil {
			t.Fatal(err)
		}
		if resolved.AllowDirectPRFallback {
			t.Error("AllowDirectPRFallback = true, want false when unset")
		}
	})
}

// TestResolveDeliveryMode_DirectPRSelection verifies explicit direct-PR
// selection always wins and never requires no-mistakes.
func TestResolveDeliveryMode_DirectPRSelection(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no no-mistakes binary
	mode, err := ResolveDeliveryMode("direct-PR", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "direct-PR" {
		t.Errorf("explicit direct-PR = %q, want direct-PR", mode)
	}
}
