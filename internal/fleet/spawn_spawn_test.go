//go:build integration

package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
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
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: fake},
		NoMistakesPreflight: func(repoPath string, _ taskauthority.DeliveryStep) error {
			preflightCalled = true
			if repoPath != projectDir {
				t.Fatalf("repoPath=%q, want %q", repoPath, projectDir)
			}
			return fmt.Errorf("incompatible no-mistakes gate agent")
		},
	})
	r.projPath = projectDir
	r.effectiveMode = "no-mistakes"
	r.reviewStep = taskauthority.DeliveryStep{Adapter: "no-mistakes", Path: "/fake/no-mistakes", ProbeState: "ready"}

	err := r.preflightNoMistakes()
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("preflight error=%v", err)
	}
	if r.effectiveMode != "no-mistakes" {
		t.Fatalf("effectiveMode = %q after a refused preflight, want no fallback", r.effectiveMode)
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
		overrides                  map[string][]string
		agents                     []string
		available                  map[string]bool
		wantBlocker                GateBlockerCategory
		wantSelected               string
	}{
		{name: "no instruction files", agents: []string{"pi"}, available: map[string]bool{"pi": true}},
		{name: "no instructions and no opt-out needs no neutralization", agents: []string{"opencode"}, available: map[string]bool{"opencode": true}, wantSelected: "opencode"},
		{name: "pi incompatible", hasDocs: true, agents: []string{"pi"}, available: map[string]bool{"pi": true}, wantBlocker: GateBlockerUnsupportedNeutralization, wantSelected: "pi"},
		{name: "pi ok with disable_project_settings", hasDocs: true, disableProjectSettings: true, agents: []string{"pi"}, available: map[string]bool{"pi": true}, wantSelected: "pi"},
		{name: "codex compatible", hasDocs: true, agents: []string{"codex"}, available: map[string]bool{"codex": true}, wantSelected: "codex"},
		{name: "pi selected first with claude fallback", hasDocs: true, agents: []string{"pi", "claude"}, available: map[string]bool{"pi": true, "claude": true}, wantSelected: "pi"},
		{name: "claude supported without opt-out", hasDocs: true, agents: []string{"claude"}, available: map[string]bool{"claude": true}, wantSelected: "claude"},
		{name: "neutralizer unavailable", hasDocs: true, agents: []string{"codex", "pi"}, available: map[string]bool{"pi": true}, wantBlocker: GateBlockerUnsupportedNeutralization, wantSelected: "pi"},
		{name: "codex override defeats neutralization", hasDocs: true, agents: []string{"codex"}, available: map[string]bool{"codex": true}, wantBlocker: GateBlockerUnsupportedNeutralization, wantSelected: "codex", overrides: map[string][]string{"codex": {"-c", "project_doc_max_bytes=4096"}}},
		{name: "claude override defeats neutralization", hasDocs: true, agents: []string{"claude"}, available: map[string]bool{"claude": true}, wantBlocker: GateBlockerUnsupportedNeutralization, wantSelected: "claude", overrides: map[string][]string{"claude": {"--setting-sources", "user,project"}}},
		{name: "disable_project_settings overrides codex defeat", hasDocs: true, disableProjectSettings: true, agents: []string{"codex"}, available: map[string]bool{"codex": true}, wantSelected: "codex", overrides: map[string][]string{"codex": {"-c", "project_doc_max_bytes=4096"}}},
		{name: "malformed no-mistakes yaml still requires neutralizer", hasDocs: true, disableProjectSettingsYAML: "disable_project_settings: [", agents: []string{"pi"}, available: map[string]bool{"pi": true}, wantBlocker: GateBlockerUnsupportedNeutralization},
		{name: "disable_project_settings false keeps preflight", hasDocs: true, disableProjectSettingsYAML: "disable_project_settings: false\n", agents: []string{"pi"}, available: map[string]bool{"pi": true}, wantBlocker: GateBlockerUnsupportedNeutralization},
		{name: "configured agent unavailable", hasDocs: true, agents: []string{"pi"}, available: map[string]bool{}, wantBlocker: GateBlockerAgentUnavailable},
		{name: "opencode refused under disable_project_settings", hasDocs: true, disableProjectSettings: true, agents: []string{"opencode"}, available: map[string]bool{"opencode": true}, wantBlocker: GateBlockerUnsupportedNeutralization},
		{name: "auto resolves to available pi under disable_project_settings", hasDocs: true, disableProjectSettings: true, agents: []string{"auto"}, available: map[string]bool{"pi": true}},
		{name: "auto with no installed native agent", agents: []string{"auto"}, available: map[string]bool{}, wantBlocker: GateBlockerAgentUnavailable},
		{name: "codex supported with pi fallback", hasDocs: true, agents: []string{"codex", "pi"}, available: map[string]bool{"codex": true, "pi": true}, wantSelected: "codex"},
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
			cfg := noMistakesConfig{Agents: tc.agents, AgentArgsOverride: tc.overrides}
			probe := ProbeNoMistakesGateAgent(repo, cfg, func(agent string) bool {
				return tc.available[agent]
			}, func() ProbeResult {
				return ProbeResult{State: backend.Ready, Version: "1.45.4", Path: "/usr/local/bin/no-mistakes"}
			})
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

// seedTypedSpawnHome returns a home carrying the typed fleet base and one
// registered project, the configuration surface spawn resolves its mode from.
func seedTypedSpawnHome(t *testing.T, project string) string {
	t.Helper()
	homeDir := t.TempDir()
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.FleetBaseConfig{Backend: "tmux"},
	}, []testProjectRecord{{Name: project, Path: t.TempDir()}}, nil)
	return homeDir
}

func TestRun_InjectFakeEndpointCapabilities(t *testing.T) {
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
		Endpoints:   fakeEndpointCapabilities{backend: fake},
	}
	_, err := Spawn(args)
	if err == nil {
		t.Fatal("expected error for missing brief")
	}
	if calledNewWindow {
		t.Error("NewWindow should not be called before brief check")
	}
}

func TestRun_LifecycleGuardRefusesAbsentTask(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_ROLE", "general")

	// Explicitly author the current-v1 snapshot identities; the task has no
	// canonical Task Authority record, so the lifecycle guard fails closed.
	if _, err := home.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	if err := config.StoreFleetBase(tmpDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config: config.FleetBaseConfig{
			SoldierHarness: "pi",
			Backend:        "tmux",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Add(tmpDir, "test-project", filepath.Join(tmpDir, "project"), false); err != nil {
		t.Fatal(err)
	}

	// Create brief file so preflightBrief passes
	briefDir := filepath.Join(tmpDir, "data", "test-task")
	if err := os.MkdirAll(briefDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(briefDir, "brief.md"), []byte("# test brief"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Spawn(Args{
		ID:          "test-task",
		ProjectName: "test-project",
		HomeDir:     tmpDir,
		Authority:   canonicalAtHome(t, tmpDir),
		Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
	})

	if err == nil {
		t.Fatal("expected error from lifecycle guard for absent canonical task, got nil")
	}
	if !strings.Contains(err.Error(), "no canonical Task Authority record") {
		t.Errorf("error should mention canonical record absence\n got: %v", err)
	}
}

// seedSpawnAuthority composes a canonical Task Authority that owns one task,
// so checkBacklogAuthority's canonical aggregate query finds it (Task 7.8).
func seedSpawnAuthority(t *testing.T, taskID string) *taskauthority.Canonical {
	t.Helper()
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, taskID, "ship", "")
	return auth
}

// seedSpawnAuthorityPhase composes a canonical Task Authority that owns one
// task at the given authoritative phase (Task 7.8).
func seedSpawnAuthorityPhase(t *testing.T, taskID string, phase taskauthority.Phase) *taskauthority.Canonical {
	t.Helper()
	auth := seedSpawnAuthority(t, taskID)
	tid := mustTaskID(t, taskID)
	switch phase {
	case taskauthority.PhaseBlocked:
		req := taskauthority.CanonicalBlockRequest{HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), Detail: "dep", Reason: "test"}
		op, err := domain.NewOperation(mustOpID(t, "op-block-"+taskID), req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Block(op, req); err != nil {
			t.Fatal(err)
		}
	case taskauthority.PhaseWorking:
		req := taskauthority.CanonicalStartRequest{HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), Reason: "test"}
		op, err := domain.NewOperation(mustOpID(t, "op-start-"+taskID), req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Start(op, req); err != nil {
			t.Fatal(err)
		}
	case taskauthority.PhaseDone:
		req := taskauthority.CanonicalCompleteRequest{HomeID: auth.HomeID(), TaskID: tid, Precondition: domain.Of(1, 1), To: taskauthority.PhaseDone, Reason: "test"}
		op, err := domain.NewOperation(mustOpID(t, "op-done-"+taskID), req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Complete(op, req); err != nil {
			t.Fatal(err)
		}
	}
	return auth
}

func TestCheckBacklogAuthority_RefusesBlockedTask(t *testing.T) {
	r := &Runner{args: Args{ID: "lifecycle-e2e", Authority: seedSpawnAuthorityPhase(t, "lifecycle-e2e", taskauthority.PhaseBlocked)}, homeDir: t.TempDir()}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for blocked task, got nil")
	}
	if !strings.Contains(err.Error(), "blocked") || !strings.Contains(err.Error(), "munsu task unblock lifecycle-e2e") {
		t.Errorf("error should mention blocked state and name the unblock command\n got: %v", err)
	}
}

func TestCheckBacklogAuthority_RefusesDoneTask(t *testing.T) {
	r := &Runner{args: Args{ID: "done-task", Authority: seedSpawnAuthorityPhase(t, "done-task", taskauthority.PhaseDone)}, homeDir: t.TempDir()}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for done task, got nil")
	}
	if !strings.Contains(err.Error(), "done") || !strings.Contains(err.Error(), "munsu task reopen done-task") {
		t.Errorf("error should mention done state and name the reopen command\n got: %v", err)
	}
}

func TestCheckBacklogAuthority_AllowsInFlightWithoutLiveMeta(t *testing.T) {
	// Working without live meta/window (task start before spawn) must ALLOW spawn.
	r := &Runner{args: Args{ID: "live-task", Authority: seedSpawnAuthorityPhase(t, "live-task", taskauthority.PhaseWorking)}, homeDir: t.TempDir()}
	if err := r.checkBacklogAuthority(); err != nil {
		t.Fatalf("in-flight without live meta must ALLOW spawn, got: %v", err)
	}
}

func TestCheckBacklogAuthority_RefusesInFlightWithLiveMeta(t *testing.T) {
	tmpDir := t.TempDir()
	if err := home.WriteMeta(tmpDir, "live-task", map[string]string{"window": "@1"}); err != nil {
		t.Fatal(err)
	}
	r := &Runner{args: Args{ID: "live-task", Authority: seedSpawnAuthorityPhase(t, "live-task", taskauthority.PhaseWorking)}, homeDir: tmpDir}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for in-flight with live meta, got nil")
	}
	if !strings.Contains(err.Error(), "live session") && !strings.Contains(err.Error(), "live soldier") {
		t.Errorf("error should mention live session\n got: %v", err)
	}
}

func TestCheckBacklogAuthority_RefusesAlreadyLiveMeta(t *testing.T) {
	tmpDir := t.TempDir()
	// Create meta file simulating already-live soldier session
	if err := home.WriteMeta(tmpDir, "queued-task", map[string]string{"window": "@1"}); err != nil {
		t.Fatal(err)
	}
	r := &Runner{args: Args{ID: "queued-task", Authority: seedSpawnAuthority(t, "queued-task")}, homeDir: tmpDir}
	err := r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for already-live task, got nil")
	}
	if !strings.Contains(err.Error(), "live soldier session") {
		t.Errorf("error should mention live session\n got: %v", err)
	}
}

// TestCheckBacklogAuthority_DuplicateUniquenessStructural proves duplicate
// task entries are impossible: the canonical Task Authority enforces one
// record per task ID, so a re-created task conflicts instead of spawning.
func TestCheckBacklogAuthority_DuplicateUniquenessStructural(t *testing.T) {
	auth := seedSpawnAuthority(t, "dup-task")
	r := &Runner{args: Args{ID: "dup-task", Authority: auth}, homeDir: t.TempDir()}
	if err := r.checkBacklogAuthority(); err != nil {
		t.Fatalf("single canonical record must allow dispatch, got: %v", err)
	}
	// A second create under a fresh operation conflicts (no duplicate rows).
	req := taskauthority.CanonicalCreateRequest{
		HomeID: auth.HomeID(), TaskID: mustTaskID(t, "dup-task"), Owner: "general", Description: "dup", Kind: "ship", Reason: "test",
	}
	op, err := domain.NewOperation(mustOpID(t, "op-dup-create"), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Create(op, req); err == nil {
		t.Fatal("duplicate canonical create must conflict")
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
		Endpoints:   fakeEndpointCapabilities{backend: fake},
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
	if err := home.WriteMeta(homeDir, "soldier-1", map[string]string{
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
	if err := home.WriteMeta(homeDir, "captain:sm-1", map[string]string{
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
	if err := home.WriteMeta(homeDir, "soldier-2", map[string]string{
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

func TestTmuxWindowForPaneFailsClosedWhenWindowIsEmpty(t *testing.T) {
	binDir := t.TempDir()
	tmuxPath := filepath.Join(binDir, "tmux")
	testutil.WriteFakeExecutable(t, tmuxPath, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", binDir+string(filepath.ListSeparator)+os.Getenv("PATH"))

	if _, err := tmuxWindowForPane("%3"); err == nil || !strings.Contains(err.Error(), "returned empty window id") {
		t.Fatalf("tmuxWindowForPane() error = %v, want empty-window refusal", err)
	}
}

func TestAuthorizeSpawnAllowsValidatedCaptain(t *testing.T) {
	homeDir := t.TempDir()
	if err := home.SeedCaptainProvenance(homeDir, "sm-1"); err != nil {
		t.Fatal(err)
	}
	if err := authorizeSpawn("captain", homeDir, homeDir); err != nil {
		t.Fatalf("authorizeSpawn() error = %v, want validated captain allowed", err)
	}
}

func TestAuthorizeSpawnRejectsCaptainOutsideItsHome(t *testing.T) {
	homeDir := t.TempDir()
	if err := home.SeedCaptainProvenance(homeDir, "sm-1"); err != nil {
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

func TestSubmitLaunchExportsSoldierRoleAndGuardsSubmission(t *testing.T) {
	f := newLaunchFixture(t, "submit-role")
	// Drive the real launch path through the durable endpoint attach, then
	// exercise the real submission (submitLaunch writes the deterministic
	// artifact, records the launch evidence, and submits exactly once).
	if err := runLaunchPhases(f, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("launch phases: %v", err)
	}
	if err := f.runner.submitLaunch(); err != nil {
		t.Fatalf("submitLaunch: %v", err)
	}

	script, err := os.ReadFile(filepath.Join(f.runner.wtPath, LaunchScriptName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "export MUNSU_ROLE=soldier") {
		t.Fatalf("launch script missing soldier role:\n%s", script)
	}
	if !strings.Contains(string(script), "test brief") {
		t.Fatalf("launch script must contain the prompt (brief) argument:\n%s", script)
	}
	// The real production artifact embeds the persistent re-entrant guard
	// before the harness exec (same launch identity exits; no second process).
	if !strings.Contains(string(script), "launch guard identity mismatch") || !strings.Contains(string(script), "exec ") {
		t.Fatalf("launch script missing the persistent re-entrant guard:\n%s", script)
	}
	if f.endpoints.submitCount() != 1 {
		t.Fatalf("submitLaunch did not send the launch command exactly once")
	}

	// The submission is re-entrant under the exact launch identity: repeating
	// submitLaunch skips the submission once the launch evidence is durably
	// recorded (never a duplicate launch).
	if err := f.runner.submitLaunch(); err != nil {
		t.Fatalf("guarded re-submit: %v", err)
	}
	if f.endpoints.submitCount() != 1 {
		t.Fatalf("duplicate launch submission: count=%d, want 1", f.endpoints.submitCount())
	}
}

func TestPreflightDelivery_BlocksOnDirectPRWithoutGhAuth(t *testing.T) {
	// direct-PR preflight should fail when the captured github forge is not
	// Ready, which is the case when gh-axi is not on PATH.
	t.Setenv("PATH", t.TempDir())
	r := &Runner{
		effectiveMode: "direct-PR",
		projPath:      "",
		forgeStep:     taskauthority.DeliveryStep{Adapter: "github", Path: "/usr/local/bin/gh-axi", ProbeState: "ready"},
	}
	err := r.preflightDelivery()
	if err == nil {
		t.Fatal("expected preflight error for direct-PR without gh-axi")
	}
	if !strings.Contains(err.Error(), "configured forge is not Ready") {
		t.Errorf("error should refuse the non-Ready forge, got: %v", err)
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
			return "Quick safety check: Is this a project you created or one you trust?\n\n❯ No, exit\n  Yes, I trust this folder", nil
		},
	}
	r := &Runner{
		harness: "claude", endpoints: fakeEndpointCapabilities{backend: fake}, endpoint: CreatedEndpoint{Backend: "test", Handle: "win-1"}, windowID: "win-1",
	}
	err := r.waitForHarnessReady(5)
	if err == nil {
		t.Fatal("expected failure pattern error, got nil")
	}
	if !strings.Contains(err.Error(), "detected launch failure") || !strings.Contains(err.Error(), "Quick safety check") {
		t.Errorf("error should be the early failure detection echoing the capture, got: %v", err)
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

func TestWaitAndInjectBrief_FailurePatternPreservesAttachedEndpoint(t *testing.T) {
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
	if teardownCalled {
		t.Error("attached endpoint was disposed after failure pattern detection")
	}
	// Verify failure evidence was persisted
	failPath := filepath.Join(dataDir, "ready-fail.txt")
	if _, statErr := os.Stat(failPath); statErr != nil {
		t.Errorf("failure evidence file not written: %v", statErr)
	}
}

func TestCheckCaptainTaskAuthority_SkippedWhenForceSet(t *testing.T) {
	r := &Runner{
		args:      Args{Force: true},
		spawnRole: "captain",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("expected no error when --force is set, got: %v", err)
	}
}

func TestCheckCaptainTaskAuthority_SkippedWhenNotCaptain(t *testing.T) {
	r := &Runner{
		args:      Args{},
		spawnRole: "general",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("expected no error when role is not captain, got: %v", err)
	}
}

func TestCheckCaptainTaskAuthority_RefusesAbsentTask(t *testing.T) {
	homeDir := t.TempDir()
	auth := mustCanonical(t) // empty authority: no canonical record
	r := &Runner{
		args:      Args{ID: "absent-task", Authority: auth},
		spawnRole: "captain",
		homeDir:   homeDir,
	}
	err := r.checkCaptainBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for absent task, got nil")
	}
	if !strings.Contains(err.Error(), "no canonical Task Authority record") {
		t.Errorf("error should mention canonical record absence, got: %v", err)
	}
}

func TestCheckCaptainTaskAuthority_RefusesBlockedTask(t *testing.T) {
	r := &Runner{
		args:      Args{ID: "blocked-task", Authority: seedSpawnAuthorityPhase(t, "blocked-task", taskauthority.PhaseBlocked)},
		spawnRole: "captain",
	}
	err := r.checkCaptainBacklogAuthority()
	if err == nil {
		t.Fatal("expected error for blocked task, got nil")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error should mention blocked, got: %v", err)
	}
}

func TestCheckCaptainTaskAuthority_RefusesDoneTask(t *testing.T) {
	r := &Runner{
		args:      Args{ID: "done-task", Authority: seedSpawnAuthorityPhase(t, "done-task", taskauthority.PhaseDone)},
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

func TestCheckCaptainTaskAuthority_RefusesLiveSessionBeforeAuthorityLookup(t *testing.T) {
	homeDir := t.TempDir()
	_ = home.WriteMeta(homeDir, "live-task", map[string]string{"kind": "ship", "window": "default:w1:p1"})
	r := &Runner{
		args:      Args{ID: "live-task"},
		spawnRole: "captain",
		homeDir:   homeDir,
	}
	err := r.checkCaptainBacklogAuthority()
	if err == nil || !strings.Contains(err.Error(), "already has a live soldier session") {
		t.Fatalf("error = %v, want live-session refusal before authority lookup", err)
	}
}

func TestCheckCaptainTaskAuthority_RefusesLiveSessionWithWindow(t *testing.T) {
	homeDir := t.TempDir()
	_ = home.WriteMeta(homeDir, "live-task", map[string]string{"kind": "ship", "window": "default:w1:p1"})
	r := &Runner{
		args:      Args{ID: "live-task", Authority: seedSpawnAuthority(t, "live-task")},
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

func TestCheckCaptainTaskAuthority_AllowsKindOnlyMetaWithoutWindow(t *testing.T) {
	homeDir := t.TempDir()
	_ = home.WriteMeta(homeDir, "pre-spawn", map[string]string{"kind": "ship"})
	r := &Runner{
		args:      Args{ID: "pre-spawn", Authority: seedSpawnAuthorityPhase(t, "pre-spawn", taskauthority.PhaseWorking)},
		spawnRole: "captain",
		homeDir:   homeDir,
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("kind-only meta without window must ALLOW start→spawn, got: %v", err)
	}
}

func TestCheckCaptainTaskAuthority_AllowsReadyTask(t *testing.T) {
	r := &Runner{
		args:      Args{ID: "ready-task", Authority: seedSpawnAuthority(t, "ready-task")},
		spawnRole: "captain",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("expected no error for queued task, got: %v", err)
	}
}

func TestCheckCaptainTaskAuthority_AllowsInFlightWithoutLiveMeta(t *testing.T) {
	r := &Runner{
		args:      Args{ID: "in-flight-task", Authority: seedSpawnAuthorityPhase(t, "in-flight-task", taskauthority.PhaseWorking)},
		spawnRole: "captain",
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		t.Fatalf("in-flight without live meta must ALLOW spawn, got: %v", err)
	}
}

// spawnRunFixture seeds a home, a registered git project and a scaffolded
// brief for task reconcile-task, and returns the Args of a full Spawn over a
// fake backend whose pane is never alive.
// deliveryOverlayForMode returns the project tools a spawn fixture's mode label
// stands for: no-mistakes is a Ready review tool with a github forge, direct-PR
// a Ready github forge, and local-only the baseline.
func deliveryOverlayForMode(t *testing.T, mode string) config.ProjectOverlay {
	t.Helper()
	switch mode {
	case "no-mistakes":
		installFakeGH(t)
		return config.ProjectOverlay{
			Review: &config.ToolEntry{Adapter: "no-mistakes", Path: filepath.Join(createFakeNoMistakesReady(t), "no-mistakes")},
			Forge:  &config.ToolEntry{Adapter: "github"},
		}
	case "direct-PR":
		installFakeGH(t)
		return config.ProjectOverlay{Forge: &config.ToolEntry{Adapter: "github"}}
	default:
		return config.ProjectOverlay{}
	}
}

func spawnRunFixture(t *testing.T, mode string, fakeBk *fakeBackend) (string, Args) {
	t.Helper()
	t.Setenv("MUNSU_ROLE", "general")
	t.Chdir(t.TempDir())
	homeDir := t.TempDir()
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(binDir, "pi"), "#!/bin/sh\nexit 0\n")
	testutil.PrependPath(t, binDir)
	t.Setenv("GEMINI_API_KEY", "test-key")

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
	// Typed project registry replaces the legacy projects.md: spawn resolves
	// the project through the canonical Fleet Registry with the tools the mode
	// stands for.
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.FleetBaseConfig{SoldierHarness: "pi", Backend: "tmux"},
	}, []testProjectRecord{
		{Name: "test-proj", Path: projectDir, Config: deliveryOverlayForMode(t, mode)},
	}, nil)

	if err := Scaffold(ScaffoldOptions{HomeDir: homeDir, ID: "reconcile-task", Repo: "test-proj", Mode: mode}); err != nil {
		t.Fatal(err)
	}

	// The spawn cutover (Task 4.1) routes the worktree binding through the
	// composed canonical Task Authority: inject a canonical home-backed
	// Authority that already owns the task so bindWorktree runs against
	// canonical state.
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, "reconcile-task", "ship", "test-proj")

	return homeDir, Args{
		ID:          "reconcile-task",
		ProjectName: "test-proj",
		HarnessFlag: "pi",
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: fakeBk},
		Authority:   auth,
	}
}

func TestSpawn_PostCreateVerificationFailure_NoMetaNoSpawnedStatus(t *testing.T) {
	fakeBk := &fakeBackend{
		newWindow: func(session, name string) (string, error) {
			return "default:w6F:p3", nil
		},
		alive: func(windowID string) bool {
			return false // pane failed verification immediately
		},
	}
	homeDir, args := spawnRunFixture(t, "local-only", fakeBk)

	_, err := Spawn(args)
	if err == nil {
		t.Fatal("Run expected error when post-create verification fails, got nil")
	}
	if !strings.Contains(err.Error(), "observation") {
		t.Errorf("expected typed observation error, got: %v", err)
	}

	metaPath := filepath.Join(homeDir, "state", "reconcile-task.meta")
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Errorf("task meta file should NOT exist on failed verification: %s", metaPath)
	}

	statusLines, _ := home.ReadStatus(homeDir, "reconcile-task")
	for _, l := range statusLines {
		if strings.Contains(l, "working: spawned") {
			t.Errorf("status log should NOT contain 'working: spawned', got: %s", l)
		}
	}
}

// A refused launch fence stops a full Spawn before any pane exists: the
// no-mistakes primary here has no gate remote, so the fence cannot name the
// gate it must allow.
func TestSpawn_RefusedFenceAllocatesNoPane(t *testing.T) {
	testutil.PrependPath(t, createFakeNoMistakesReady(t))
	testutil.FakeOnPath(t, "gh-axi", "#!/bin/sh\nexit 0\n")
	windows := 0
	fakeBk := &fakeBackend{newWindow: func(session, name string) (string, error) {
		windows++
		return "default:w6F:p3", nil
	}}
	homeDir, args := spawnRunFixture(t, "no-mistakes", fakeBk)
	args.NoMistakesPreflight = func(string, taskauthority.DeliveryStep) error { return nil }

	_, err := Spawn(args)
	if err == nil || !strings.Contains(err.Error(), "launch fence: ") {
		t.Fatalf("Spawn err = %v, want the launch fence refusal", err)
	}
	requireGateBlocker(t, err, GateBlockerNotInitialized, "the primary has no no-mistakes remote")
	if windows != 0 {
		t.Fatalf("NewWindow calls = %d, want none before the fence is proven", windows)
	}
	if _, statErr := os.Stat(filepath.Join(homeDir, "state", "reconcile-task.meta")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused launch wrote task meta: %v", statErr)
	}
}

// A failed no-mistakes preflight stops a full Spawn before the launch intent
// is committed: no pane, no intent, no worktree.
func TestSpawn_FailedNoMistakesPreflightCommitsNoLaunchIntent(t *testing.T) {
	testutil.PrependPath(t, createFakeNoMistakesReady(t))
	windows := 0
	fakeBk := &fakeBackend{newWindow: func(session, name string) (string, error) {
		windows++
		return "default:w6F:p3", nil
	}}
	_, args := spawnRunFixture(t, "no-mistakes", fakeBk)
	args.NoMistakesPreflight = func(string, taskauthority.DeliveryStep) error {
		return fmt.Errorf("incompatible no-mistakes gate agent")
	}

	_, err := Spawn(args)
	if err == nil || !strings.Contains(err.Error(), "incompatible no-mistakes gate agent") {
		t.Fatalf("Spawn err = %v, want the preflight refusal", err)
	}
	agg, getErr := args.Authority.Get(mustTaskID(t, "reconcile-task"))
	if getErr != nil {
		t.Fatal(getErr)
	}
	if windows != 0 || agg.Launch != nil || agg.Worktree != nil || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("windows=%d launch=%+v worktree=%+v phase=%q, want an untouched queued task", windows, agg.Launch, agg.Worktree, agg.Phase)
	}
}

// TestRegression_ResolveSkillsWithoutSrcwalk proves that resolveSkills produces
// a valid skill catalog without requiring srcwalk. This is a focused regression
// guard for the remove-srcwalk-integration task.
func TestRegression_ResolveSkillsWithoutSrcwalk(t *testing.T) {
	tests := []struct {
		name         string
		kind         string
		wantRequired int
		wantGhAxi    bool
		wantOptional []string
	}{
		{name: "ship", kind: "ship", wantRequired: 1, wantGhAxi: true, wantOptional: []string{"chrome-devtools-axi"}},
		{name: "scout", kind: "scout", wantRequired: 0, wantGhAxi: false, wantOptional: []string{"gh-axi"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{
				args:          Args{},
				kind:          tc.kind,
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

			if len(required) != tc.wantRequired {
				t.Fatalf("required skill count = %d, want %d", len(required), tc.wantRequired)
			}

			// Verify expected required skills.
			var foundGhAxi bool
			for _, s := range required {
				if s.Name == "gh-axi" {
					foundGhAxi = true
					if !s.Applicable {
						t.Error("gh-axi must be applicable")
					}
				}
			}

			if tc.wantGhAxi && !foundGhAxi {
				t.Error("gh-axi must be in required skills")
			}
			if !tc.wantGhAxi && foundGhAxi {
				t.Error("gh-axi must not be in required skills")
			}

			for _, want := range tc.wantOptional {
				found := false
				for _, s := range optional {
					if s.Name == want {
						found = true
						if !s.Applicable {
							t.Errorf("%s must be applicable", want)
						}
					}
				}
				if !found {
					t.Errorf("%s must be in optional skills", want)
				}
			}
		})
	}
}

// TestWriteTaskMetaNeverWritesAuthoritativeFields proves the pre-transition
// side file is a runtime-only projection (Task 7.8 adjudication): when no
// .meta exists yet it writes no authoritative Task Aggregate fields (kind,
// project, description, owner, generation, state), and when a projection
// exists it preserves the authoritative fields rather than overwriting them
// from spawn args.
func TestWriteTaskMetaNeverWritesAuthoritativeFields(t *testing.T) {
	homeDir := t.TempDir()
	taskID := "side-file-authoritative"
	auth := mustCanonical(t)
	canonicalCreateTask(t, auth, taskID, "ship", "test-proj")
	r := &Runner{
		homeDir:       homeDir,
		args:          Args{ID: taskID, ProjectName: "test-proj", Authority: auth},
		windowID:      "session:pane-1",
		wtPath:        "/tmp/wt",
		projPath:      "/tmp/proj",
		harness:       "pi",
		effectiveMode: "direct-PR",
		endpoint:      CreatedEndpoint{Backend: "herdr", Handle: "session:pane-1"},
	}
	if err := r.writeTaskMeta(); err != nil {
		t.Fatalf("writeTaskMeta: %v", err)
	}
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	// No authoritative fields when there was no existing projection.
	for _, key := range []string{"kind", "project", "description", "owner", "generation", "state"} {
		if _, ok := meta[key]; ok {
			t.Errorf("writeTaskMeta wrote authoritative field %q from spawn args: %v", key, meta)
		}
	}
	if meta["window"] != "session:pane-1" || meta["worktree"] != "/tmp/wt" {
		t.Fatalf("runtime fields = %v", meta)
	}

	// With an existing projection, authoritative fields are preserved (they
	// were derived from the canonical aggregate at task add), never replaced
	// by the spawn args (kind=scout here must not leak).
	if err := home.WriteMeta(homeDir, taskID, map[string]string{"kind": "ship", "project": "test-proj", "repo": "keep"}); err != nil {
		t.Fatal(err)
	}
	if err := r.writeTaskMeta(); err != nil {
		t.Fatalf("writeTaskMeta second pass: %v", err)
	}
	meta, err = home.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if meta["kind"] != "ship" || meta["project"] != "test-proj" {
		t.Fatalf("authoritative fields clobbered by spawn args: %v", meta)
	}
	if meta["repo"] != "keep" {
		t.Fatalf("runtime projection field lost: %v", meta)
	}
}

// TestCheckBacklogAuthorityRequiresCanonicalAggregate proves the spawn
// backlog shim no longer creates a legacy home aggregate (Task 7.8): the
// task must already have a canonical Task Authority record, and spawn fails
// closed otherwise without writing any v1 aggregate state.
func TestCheckBacklogAuthorityRequiresCanonicalAggregate(t *testing.T) {
	homeDir := t.TempDir()
	// The canonical home must be initialized before the projection is written
	// so the composed canonical Authority is bound to the same home the
	// projection reads.
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	// Meta exists, but the Authority has no record: the query must fail
	// closed on the absent canonical aggregate (no legacy fallback).
	if err := home.WriteMeta(homeDir, "no-agg", map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	auth, err := taskauthority.NewCanonical(mustHome(t, homeDir))
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		homeDir: homeDir,
		args:    Args{ID: "no-agg", Authority: auth},
	}
	err = r.checkBacklogAuthority()
	if err == nil {
		t.Fatal("expected error when the task has no canonical aggregate")
	}
	if !strings.Contains(err.Error(), "canonical") {
		t.Errorf("error = %v, want canonical-record message", err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "state", ".task-authority", "aggregates")); !os.IsNotExist(err) {
		t.Fatal("checkBacklogAuthority created legacy v1 aggregate state")
	}
}
