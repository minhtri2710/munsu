//go:build integration

package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
)

func writeModelAllowlist(t *testing.T, homeDir, content string) {
	t.Helper()
	if err := os.MkdirAll(config.ConfigDir(homeDir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.ConfigDir(homeDir), harness.ModelAllowlistKey), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func spawnContext(t *testing.T, homeDir string) {
	t.Helper()
	t.Setenv("MUNSU_ROLE", "general")
	t.Chdir(t.TempDir())
	_ = homeDir
}

func assertNoTaskSideEffects(t *testing.T, homeDir, id string) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(homeDir, "state", id+".meta"),
		filepath.Join(homeDir, "state", id+".status"),
		filepath.Join(homeDir, "state", ".task-authority", "aggregates", id),
		filepath.Join(homeDir, "data", id),
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("side effect occurred: %s exists", path)
		} else if !os.IsNotExist(err) {
			t.Errorf("checking %s: %v", path, err)
		}
	}
}

func TestSpawn_DeniedExplicitModelFailsClosedBeforeSideEffects(t *testing.T) {
	homeDir := t.TempDir()
	// Policy allows only the fleet canonical model; the explicit request is denied.
	writeModelAllowlist(t, homeDir, "pi:opencode-go/deepseek-v4-flash\n")
	spawnContext(t, homeDir)

	windowCreated := false
	fake := &fakeBackend{newWindow: func(session, name string) (string, error) {
		windowCreated = true
		return "", fmt.Errorf("must not allocate a session")
	}}

	_, err := Spawn(Args{
		ID:          "denied-task",
		ProjectName: "test-project",
		Mode:        "direct-PR",
		HarnessFlag: harness.Pi,
		ModelFlag:   "claude-sonnet-4-20250515",
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: fake},
	})
	if err == nil {
		t.Fatal("expected model allowlist denial")
	}
	if !strings.Contains(err.Error(), "model allowlist") {
		t.Fatalf("error should mention model allowlist, got: %v", err)
	}
	if !strings.Contains(err.Error(), "claude-sonnet-4-20250515") {
		t.Fatalf("error should name the denied model, got: %v", err)
	}
	if windowCreated {
		t.Fatal("session allocation occurred before model allowlist enforcement")
	}
	assertNoTaskSideEffects(t, homeDir, "denied-task")
}

func TestSpawn_AutoSelectedDeniedModelFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	// The model is auto-selected (adapter template default for codex), not explicit.
	writeModelAllowlist(t, homeDir, "pi:opencode-go/deepseek-v4-flash\n")
	spawnContext(t, homeDir)

	_, err := Spawn(Args{
		ID:          "auto-denied-task",
		ProjectName: "test-project",
		Mode:        "direct-PR",
		HarnessFlag: harness.Codex, // no --model: resolves to template default gpt-5.2-codex
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
	})
	if err == nil {
		t.Fatal("expected auto-selected model denial")
	}
	if !strings.Contains(err.Error(), "gpt-5.2-codex") {
		t.Fatalf("error should name the auto-selected model, got: %v", err)
	}
	assertNoTaskSideEffects(t, homeDir, "auto-denied-task")
}

func TestSpawn_AllowedModelPassesAllowlist(t *testing.T) {
	homeDir := t.TempDir()
	writeModelAllowlist(t, homeDir, "pi:claude-sonnet-4-20250515\n")
	spawnContext(t, homeDir)

	// The allowlist check passes; the run proceeds to the brief-exists phase,
	// which is the first failure for a bare fixture home.
	_, err := Spawn(Args{
		ID:          "allowed-task",
		ProjectName: "test-project",
		Mode:        "direct-PR",
		HarnessFlag: harness.Pi,
		ModelFlag:   "claude-sonnet-4-20250515",
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
	})
	if err == nil {
		t.Fatal("expected brief-exists failure (allowlist passed)")
	}
	if strings.Contains(err.Error(), "model allowlist") {
		t.Fatalf("allowed model should pass the allowlist, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no brief found") {
		t.Fatalf("run should proceed past the allowlist to the brief check, got: %v", err)
	}
}

func TestSpawn_AbsentPolicyPreservesCompatibility(t *testing.T) {
	homeDir := t.TempDir()
	spawnContext(t, homeDir)

	_, err := Spawn(Args{
		ID:          "no-policy-task",
		ProjectName: "test-project",
		Mode:        "direct-PR",
		HarnessFlag: harness.Pi,
		ModelFlag:   "claude-sonnet-4-20250515",
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
	})
	if err == nil {
		t.Fatal("expected brief-exists failure")
	}
	if strings.Contains(err.Error(), "model allowlist") {
		t.Fatalf("absent policy must preserve compatibility, got: %v", err)
	}
}

func TestSpawn_EmptyPolicyFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	writeModelAllowlist(t, homeDir, "# comments only\n\n")
	spawnContext(t, homeDir)

	_, err := Spawn(Args{
		ID:          "empty-policy-task",
		ProjectName: "test-project",
		Mode:        "direct-PR",
		HarnessFlag: harness.Pi,
		ModelFlag:   "claude-sonnet-4-20250515",
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
	})
	if err == nil {
		t.Fatal("expected empty-policy failure")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error should report the empty policy, got: %v", err)
	}
	assertNoTaskSideEffects(t, homeDir, "empty-policy-task")
}

func TestSpawn_MalformedPolicyFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	writeModelAllowlist(t, homeDir, "not-an-identity\n")
	spawnContext(t, homeDir)

	_, err := Spawn(Args{
		ID:          "malformed-policy-task",
		ProjectName: "test-project",
		Mode:        "direct-PR",
		HarnessFlag: harness.Pi,
		ModelFlag:   "claude-sonnet-4-20250515",
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
	})
	if err == nil {
		t.Fatal("expected malformed-policy failure")
	}
	if !strings.Contains(err.Error(), "must be <harness>:<model>") {
		t.Fatalf("error should name the malformed identity rule, got: %v", err)
	}
	assertNoTaskSideEffects(t, homeDir, "malformed-policy-task")
}

// TestCaptainLaunch_DeniedModelFailsClosed verifies the captain launch path
// shares the same validation seam before any pane side effects.
func TestCaptainLaunch_DeniedModelFailsClosed(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	deniedModel := "claude-sonnet-4-20250515"
	writeModelAllowlist(t, parent, "pi:opencode-go/deepseek-v4-flash\n")

	captainHome := filepath.Join(t.TempDir(), "captains", "alpha")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeCanonicalPiIntegration(t, captainHome)

	// The model comes only from the published-snapshot CaptainProfile.
	_, _, err := buildLaunchArgs(captainHome, harness.Pi, config.CaptainProfile{Harness: harness.Pi, Model: deniedModel}, parent)
	if err == nil {
		t.Fatal("expected captain launch denial")
	}
	if !strings.Contains(err.Error(), "model allowlist") {
		t.Fatalf("captain launch error should mention model allowlist, got: %v", err)
	}
	if !strings.Contains(err.Error(), deniedModel) {
		t.Fatalf("captain launch error should name the denied model, got: %v", err)
	}
}

func TestCaptainLaunch_AllowedModelPasses(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	writeModelAllowlist(t, parent, "pi:opencode-go/deepseek-v4-flash\n")

	captainHome := filepath.Join(t.TempDir(), "captains", "alpha")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeCanonicalPiIntegration(t, captainHome)

	// The model comes only from the published-snapshot CaptainProfile.
	_, args, err := buildLaunchArgs(captainHome, harness.Pi, config.CaptainProfile{Harness: harness.Pi, Model: "opencode-go/deepseek-v4-flash"}, parent)
	if err != nil {
		t.Fatalf("allowed captain model should pass: %v", err)
	}
	found := false
	for i, a := range args {
		if a == "--model" && i+1 < len(args) && args[i+1] == "opencode-go/deepseek-v4-flash" {
			found = true
		}
	}
	if !found {
		t.Fatalf("args should carry the allowed model, got %v", args)
	}
}

// TestRecoverLaunchReadiness_DeniedModelFailsClosed verifies the captain
// recover/relaunch (retry) path shares the same validation seam.
func TestRecoverLaunchReadiness_DeniedModelFailsClosed(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	writeModelAllowlist(t, parent, "pi:opencode-go/deepseek-v4-flash\n")

	oldLookPath := captainLookPath
	captainLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	defer func() { captainLookPath = oldLookPath }()

	captainHome := captainHomeWithSnapshot(t, config.CaptainProfile{Harness: harness.Pi, Model: "claude-sonnet-4-20250515"})
	tx := &RecoverTransaction{}
	res := tx.stepLaunchReadiness(parent, Info{ID: "alpha", Home: captainHome})
	if res.State != StepFailed {
		t.Fatalf("launch readiness state = %q, want failed; detail: %s", res.State, res.Detail)
	}
	if !strings.Contains(res.Detail, "model allowlist") {
		t.Fatalf("launch readiness detail should mention model allowlist, got: %s", res.Detail)
	}
}

func TestRecoverLaunchReadiness_AllowedModelPasses(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	writeModelAllowlist(t, parent, "pi:opencode-go/deepseek-v4-flash\n")

	oldLookPath := captainLookPath
	captainLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	defer func() { captainLookPath = oldLookPath }()

	captainHome := captainHomeWithSnapshot(t, config.CaptainProfile{Harness: harness.Pi, Model: "opencode-go/deepseek-v4-flash"})
	tx := &RecoverTransaction{}
	res := tx.stepLaunchReadiness(parent, Info{ID: "alpha", Home: captainHome})
	if res.State != StepOk {
		t.Fatalf("launch readiness state = %q, want ok; detail: %s", res.State, res.Detail)
	}
	if !strings.Contains(res.Detail, "opencode-go/deepseek-v4-flash") {
		t.Fatalf("launch readiness detail should carry the allowed model, got: %s", res.Detail)
	}
}

// TestSpawn_UnresolvedModelFailsClosed verifies blocker 1: with a policy
// present, an effective model that cannot be resolved to a concrete value must
// fail closed instead of passing unvalidated. pi has no template default model,
// so a pi spawn without --model/dispatch/project model leaves the identity
// unresolved; the policy must deny it because the runtime default cannot be
// verified against the allowlist.
func TestSpawn_UnresolvedModelFailsClosed(t *testing.T) {
	homeDir := t.TempDir()
	writeModelAllowlist(t, homeDir, "pi:opencode-go/deepseek-v4-flash\n")
	spawnContext(t, homeDir)

	_, err := Spawn(Args{
		ID:          "unresolved-model-task",
		ProjectName: "test-project",
		Mode:        "direct-PR",
		HarnessFlag: harness.Pi, // no --model, no dispatch, no template default for pi
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
	})
	if err == nil {
		t.Fatal("expected model allowlist failure for unresolved identity")
	}
	if !strings.Contains(err.Error(), "model allowlist") {
		t.Fatalf("error should mention model allowlist, got: %v", err)
	}
	assertNoTaskSideEffects(t, homeDir, "unresolved-model-task")
}

// TestCaptainLaunch_NoModelWithPolicyFailsClosed verifies blocker 1 on the
// captain launch path: a policy is present but the captain profile resolves no
// model (captain-harness without model token and no config/model), so the
// runtime default would bypass the policy. Launch must fail closed.
func TestCaptainLaunch_NoModelWithPolicyFailsClosed(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	writeModelAllowlist(t, parent, "pi:opencode-go/deepseek-v4-flash\n")

	captainHome := filepath.Join(t.TempDir(), "captains", "alpha")
	if err := os.MkdirAll(captainHome, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(captainHome, "AGENTS.md"), []byte("# alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeCanonicalPiIntegration(t, captainHome)

	_, _, err := buildLaunchArgs(captainHome, harness.Pi, config.CaptainProfile{Harness: harness.Pi}, parent)
	if err == nil {
		t.Fatal("expected captain launch denial for unresolved model under active policy")
	}
	if !strings.Contains(err.Error(), "model allowlist") {
		t.Fatalf("captain launch error should mention model allowlist, got: %v", err)
	}
}

// TestRecoverLaunchReadiness_NoModelWithPolicyFailsClosed verifies blocker 1 on
// the captain recover/relaunch readiness path: an active policy with an
// unresolved captain model must mark launch readiness failed, not ok.
func TestRecoverLaunchReadiness_NoModelWithPolicyFailsClosed(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	writeModelAllowlist(t, parent, "pi:opencode-go/deepseek-v4-flash\n")

	oldLookPath := captainLookPath
	captainLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	defer func() { captainLookPath = oldLookPath }()

	captainHome := captainHomeWithSnapshot(t, config.CaptainProfile{Harness: harness.Pi})
	tx := &RecoverTransaction{}
	res := tx.stepLaunchReadiness(parent, Info{ID: "alpha", Home: captainHome})
	if res.State != StepFailed {
		t.Fatalf("launch readiness state = %q, want failed for unresolved model under active policy; detail: %s", res.State, res.Detail)
	}
	if !strings.Contains(res.Detail, "model allowlist") {
		t.Fatalf("launch readiness detail should mention model allowlist, got: %s", res.Detail)
	}
}

// TestSpawn_AllowlistResolutionCachedForPreflightAndLaunch verifies blocker 2:
// the exact harness/model must be resolved once and cached on the Runner so the
// identity validated by the allowlist is the same one used for preflight and
// launch. After allowlist validation the resolved identity must be observable
// on the runner state.
func TestSpawn_AllowlistResolutionCachedForPreflightAndLaunch(t *testing.T) {
	homeDir := t.TempDir()
	writeSpawnSnapshotDocuments(t, homeDir) // project beta resolves codex/beta-model
	spawnContext(t, homeDir)
	writeModelAllowlist(t, homeDir, "codex:beta-model\n")

	r := NewRunner(Args{ID: "beta-task", ProjectName: "beta", HomeDir: homeDir, TaskDescription: "beta work"})
	r.homeDir = homeDir
	if err := r.resolveDispatchPolicy(); err != nil {
		t.Fatalf("resolveDispatchPolicy: %v", err)
	}
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if r.projectConfig.Soldier.Harness != "codex" || r.projectConfig.Soldier.Model != "beta-model" {
		t.Fatalf("unexpected project config selection: %+v", r.projectConfig.Soldier)
	}
	if err := r.resolveEffectiveIdentity(); err != nil {
		t.Fatalf("resolveEffectiveIdentity: %v", err)
	}
	if err := r.checkModelAllowlist(); err != nil {
		t.Fatalf("allowed identity should pass the allowlist: %v", err)
	}
	if r.harness != "codex" || r.model != "beta-model" {
		t.Fatalf("allowlist resolution must be cached for preflight/launch: harness=%q model=%q", r.harness, r.model)
	}
}

// TestSpawn_ProjectConfigModelValidatedNotTemplateDefault verifies blocker 2:
// when project config selects a model, the allowlist must validate that exact
// model, not fall back to validating the adapter template default. codex's
// template default is gpt-5.2-codex; beta's project model is beta-model.
func TestSpawn_ProjectConfigModelValidatedNotTemplateDefault(t *testing.T) {
	t.Run("project model denied when only template default allowed", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSpawnSnapshotDocuments(t, homeDir)
		spawnContext(t, homeDir)
		writeModelAllowlist(t, homeDir, "codex:gpt-5.2-codex\n") // only the template default

		_, err := Spawn(Args{
			ID:          "beta-denied",
			ProjectName: "beta",
			Mode:        "direct-PR",
			HomeDir:     homeDir,
			Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
		})
		if err == nil {
			t.Fatal("expected denial: project model must be validated, not the template default")
		}
		if !strings.Contains(err.Error(), "beta-model") {
			t.Fatalf("denial should name the project-config model beta-model, got: %v", err)
		}
		if !strings.Contains(err.Error(), "model allowlist") {
			t.Fatalf("error should mention model allowlist, got: %v", err)
		}
		assertNoTaskSideEffects(t, homeDir, "beta-denied")
	})
	t.Run("project model allowed passes even though template default would be denied", func(t *testing.T) {
		homeDir := t.TempDir()
		writeSpawnSnapshotDocuments(t, homeDir)
		spawnContext(t, homeDir)
		writeModelAllowlist(t, homeDir, "codex:beta-model\n") // only the project model

		_, err := Spawn(Args{
			ID:          "beta-allowed",
			ProjectName: "beta",
			Mode:        "direct-PR",
			HomeDir:     homeDir,
			Endpoints:   fakeEndpointCapabilities{backend: &fakeBackend{}},
		})
		if err == nil {
			t.Fatal("expected brief-exists failure (allowlist passed for project model)")
		}
		if strings.Contains(err.Error(), "model allowlist") {
			t.Fatalf("project model should pass the allowlist, got: %v", err)
		}
		if !strings.Contains(err.Error(), "no brief found") {
			t.Fatalf("run should proceed past the allowlist to the brief check, got: %v", err)
		}
	})
}

// TestSpawn_DispatchSelectionResolvedOnce verifies blocker 2's quota property:
// the dispatch/quota selector runs exactly once across identity resolution,
// allowlist validation, preflight, and launch, and the identity used for launch
// is identical to the one validated. A quota-balanced profile makes each
// selector invocation exec quota-axi; a fake quota-axi on PATH counts calls.
func TestSpawn_DispatchSelectionResolvedOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake quota-axi executable is POSIX-only")
	}
	homeDir := t.TempDir()
	base := fleetconfig.FleetBaseDocument{
		SchemaVersion: fleetconfig.FleetBaseSchemaVersion,
		Config: fleetconfig.ProjectOverlay{
			DefaultMode: "direct-pr",
			Backend:     "tmux",
			DispatchProfiles: []fleetconfig.DispatchProfile{
				{Name: "quota", Match: []string{"*"}, SelectStrategy: "quota-balanced",
					Use: []fleetconfig.DispatchCandidate{
						{Harness: harness.Codex, Model: "q-model"},
						{Harness: harness.Pi, Model: "q-pi"},
					}},
			},
		},
	}
	projects := []testProjectRecord{
		{Name: "quota-proj", Path: filepath.Join(homeDir, "projects", "quota-proj")},
	}
	storeTestDocuments(t, homeDir, base, projects, nil)
	spawnContext(t, homeDir)
	writeModelAllowlist(t, homeDir, "codex:q-model\n")

	// Fake quota-axi: counts invocations and always reports codex with the most
	// remaining general quota, so the selector deterministically picks codex.
	fakeDir := t.TempDir()
	counter := filepath.Join(fakeDir, "quota-count")
	fixture := filepath.Join(fakeDir, "quota.json")
	quotaJSON := `{"providers":[
		{"provider":"codex","state":{"status":"fresh"},"windows":[{"id":"five_hour","kind":"general","percentRemaining":80},{"id":"weekly","kind":"general","percentRemaining":75}]},
		{"provider":"pi","state":{"status":"fresh"},"windows":[{"id":"five_hour","kind":"general","percentRemaining":30},{"id":"seven_day","kind":"general","percentRemaining":40}]}
	]}`
	if err := os.WriteFile(fixture, []byte(quotaJSON), 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\nprintf 'x' >> \"$QUOTA_COUNTER\"\ncat \"$QUOTA_FIXTURE\"\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "quota-axi"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUOTA_COUNTER", counter)
	t.Setenv("QUOTA_FIXTURE", fixture)
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := NewRunner(Args{ID: "quota-task", ProjectName: "quota-proj", HomeDir: homeDir, TaskDescription: "quota work"})
	r.homeDir = homeDir
	if err := r.resolveDispatchPolicy(); err != nil {
		t.Fatalf("resolveDispatchPolicy: %v", err)
	}
	if err := r.resolveMode(); err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if r.projectConfig.Soldier.Harness != "codex" || r.projectConfig.Soldier.Model != "q-model" {
		t.Fatalf("unexpected resolved selection: %+v", r.projectConfig.Soldier)
	}
	if err := r.resolveEffectiveIdentity(); err != nil {
		t.Fatalf("resolveEffectiveIdentity: %v", err)
	}
	if err := r.checkModelAllowlist(); err != nil {
		t.Fatalf("allowed identity should pass the allowlist: %v", err)
	}
	if err := r.preflightHarness(); err != nil {
		// harness binary-absent preflight is expected in CI; resolution already happened.
		t.Logf("preflightHarness: %v (expected in CI without harness binary)", err)
	}
	r.resolveLaunchConfig()
	if r.harness != "codex" || r.model != "q-model" {
		t.Fatalf("launch identity diverged from validated identity: harness=%q model=%q", r.harness, r.model)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("quota-axi was never invoked (selector fell back): %v", err)
	}
	if got := len(data); got != 1 {
		t.Fatalf("quota/dispatch selector invoked %d times, want exactly 1: the selection used for validation must be the same one used for launch", got)
	}
}
