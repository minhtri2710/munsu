package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// seedCanonicalTask seeds one queued canonical task of the given kind into
// homeDir (clean break: fleet reads only authoritative Task Authority records).
func seedCanonicalTask(t *testing.T, homeDir, id, kind string) {
	t.Helper()
	tid, err := domain.NewTaskID(id)
	if err != nil {
		t.Fatal(err)
	}
	h, err := mhome.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatal(err)
	}
	project, err := domain.NewProjectID("munsu")
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: tid, Owner: "owner",
		Description: "work", Kind: kind, Project: project, Reason: "test",
	}
	if kind == "scout" {
		req.ScoutScope = "investigate scope"
		req.ScoutRuntimeBudgetSecs = 300
	}
	opID, err := domain.NewOperationID("op-create-" + id)
	if err != nil {
		t.Fatal(err)
	}
	op, err := domain.NewOperation(opID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(op, req); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
}

func TestCheckSessionScope_RefusesAmbientGateWithoutProjects(t *testing.T) {
	t.Setenv("NO_MISTAKES_GATE", "")
	if err := checkSessionScope(t.TempDir()); err == nil {
		t.Fatal("expected ambient gate refusal")
	}
}

func TestCheckSessionScope_UsesRegisteredProjectPath(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "demo")
	commonDir := filepath.Join(t.TempDir(), ".no-mistakes", "repos", "gate.git")
	if err := os.MkdirAll(filepath.Dir(commonDir), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "--bare", commonDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".git"), []byte("gitdir: "+commonDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	registry := "- demo - https://github.com/example/demo.git (added 2026-07-18)\n"
	if err := os.WriteFile(filepath.Join(home, "data", "projects.md"), []byte(registry), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NM_HOME", filepath.Dir(filepath.Dir(commonDir)))
	if err := checkSessionScope(home); err == nil {
		t.Fatal("expected registered gate checkout refusal")
	}
}

// captureStdout runs f and returns everything written to stdout.
func captureStdout(f func()) string {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	os.Stdout = w

	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		outC <- buf.String()
	}()

	f()
	_ = w.Close()
	os.Stdout = old
	return <-outC
}

// TestPrintDataFile tests the printDataFile helper.
func TestPrintDataFile_ShowsContent(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "general.md"), []byte("captain: jdoe\nfocus: refactor\n"), 0644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printDataFile(os.Stdout, tmpDir, "general.md")
	})

	if !strings.Contains(output, "=== data/general.md ===") {
		t.Errorf("expected header, got: %s", output)
	}
	if !strings.Contains(output, "captain: jdoe") {
		t.Errorf("expected file content, got: %s", output)
	}
}

func TestPrintDataFile_ShowsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printDataFile(os.Stdout, tmpDir, "general.md")
	})

	if !strings.Contains(output, "ABSENT") || !strings.Contains(output, "general.md") {
		t.Errorf("expected ABSENT marker, got: %s", output)
	}
}

func TestPrintDataFile_TruncatesLongFiles(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}

	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(filepath.Join(dataDir, "long.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printDataFile(os.Stdout, tmpDir, "long.md")
	})

	if !strings.Contains(output, "...(truncated)") {
		t.Errorf("expected truncation marker, got: %s", output)
	}
	// Should print 20 lines plus header line
	gotLines := strings.Split(strings.TrimSpace(output), "\n")
	if len(gotLines) < 21 || len(gotLines) > 23 {
		t.Errorf("expected ~21-22 lines (20 content + header + optional truncation), got %d", len(gotLines))
	}
}

// TestPrintFleetState tests the printFleetState helper.
func TestPrintFleetState_NoTasks(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := mhome.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printFleetState(os.Stdout, tmpDir)
	})

	if !strings.Contains(output, "(no in-flight tasks)") {
		t.Errorf("expected no-tasks message, got: %s", output)
	}
}

func TestPrintFleetState_NoStateDir(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := mhome.Init(tmpDir); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printFleetState(os.Stdout, tmpDir)
	})

	if !strings.Contains(output, "(no in-flight tasks)") {
		t.Errorf("expected no-tasks message, got: %s", output)
	}
}

func TestPrintFleetState_ShowsTasks(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := mhome.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// A canonical task with a status log tail.
	taskID := "task-abc-123"
	seedCanonicalTask(t, tmpDir, taskID, "deploy")
	if err := mhome.AppendStatus(tmpDir, taskID, "working processed event"); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printFleetState(os.Stdout, tmpDir)
	})

	// Should show the task with canonical phase.
	if !strings.Contains(output, taskID) {
		t.Errorf("expected task ID in output, got: %s", output)
	}
	if !strings.Contains(output, "queued") {
		t.Errorf("expected canonical queued phase in output, got: %s", output)
	}
}

func TestPrintFleetState_IgnoresNonMetaFiles(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := mhome.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a non-meta file that should be ignored
	if err := os.WriteFile(filepath.Join(stateDir, ".lock"), []byte("some data\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a status file without a corresponding meta (should be ignored by the loop)
	if err := os.WriteFile(filepath.Join(stateDir, "orphan.status"), []byte("working\n"), 0644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(func() {
		printFleetState(os.Stdout, tmpDir)
	})

	if !strings.Contains(output, "(no in-flight tasks)") {
		t.Errorf("expected no-tasks message when only non-meta files exist, got: %s", output)
	}
}

// TestPrintFleetState_TaskNoStatus tests that printFleetState shows a task with empty status.
func TestPrintFleetState_TaskNoStatus(t *testing.T) {
	tmpDir := t.TempDir()
	if _, err := mhome.Init(tmpDir); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	taskID := "task-no-status"
	seedCanonicalTask(t, tmpDir, taskID, "deploy")

	output := captureStdout(func() {
		printFleetState(os.Stdout, tmpDir)
	})

	if !strings.Contains(output, taskID) {
		t.Errorf("expected task ID in output, got: %s", output)
	}
	// Canonical phase is the authoritative state (no status tail needed).
	if !strings.Contains(output, "queued") {
		t.Errorf("expected canonical queued phase, got: %s", output)
	}
}

// TestSupervisionBlockHeader checks the header line for every harness.
func TestSupervisionBlockHeader(t *testing.T) {
	harnesses := []string{"claude", "codex", "grok", "pi", "opencode", "unknown"}
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			output := captureStdout(func() {
				printSupervisionBlock(os.Stdout, h, true)
			})
			if !strings.Contains(output, "primary harness: "+h) {
				t.Errorf("expected harness name %q in header, got: %s", h, output)
			}
			if !strings.Contains(output, "Claim:   munsu wake claim --consumer") {
				t.Errorf("expected Claim line, got: %s", output)
			}
			if !strings.Contains(output, "Guard:   munsu guard") {
				t.Errorf("expected Guard line, got: %s", output)
			}
		})
	}
}

func TestSupervisionBlock_LockReadOnly(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock(os.Stdout, "claude", false)
	})
	if !strings.Contains(output, "read-only") {
		t.Errorf("expected read-only lock warning, got: %s", output)
	}
	if !strings.Contains(output, "do not drain, arm, or repair") {
		t.Errorf("expected drain/arm/repair warning, got: %s", output)
	}
}

func TestSupervisionBlock_LockAcquired(t *testing.T) {
	output := captureStdout(func() {
		printSupervisionBlock(os.Stdout, "codex", true)
	})
	if !strings.Contains(output, "owns normal supervision") {
		t.Errorf("expected 'owns normal supervision', got: %s", output)
	}
}

func TestSupervisionBlock_UsesPersistentWatcherGuidance(t *testing.T) {
	for _, h := range []string{"claude", "codex", "grok", "pi", "opencode", "unknown"} {
		t.Run(h, func(t *testing.T) {
			output := captureStdout(func() {
				printSupervisionBlock(os.Stdout, h, true)
			})
			if !strings.Contains(output, "munsu watch ensure") {
				t.Errorf("expected watch ensure guidance, got: %s", output)
			}
			if !strings.Contains(output, "munsu watch run") {
				t.Errorf("expected one-cycle watch run guidance, got: %s", output)
			}
			for _, legacy := range []string{"fm_watch_arm_pi", "watch-arm", "re-arms automatically", "extension background wake"} {
				if strings.Contains(output, legacy) {
					t.Errorf("output contains legacy watcher guidance %q: %s", legacy, output)
				}
			}
		})
	}
}

func TestSupervisionMode_AllKnown(t *testing.T) {
	harnesses := []string{"claude", "codex", "grok", "pi", "opencode"}
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			if got := supervisionMode(h); got != "persistent daemon" {
				t.Errorf("supervisionMode(%q) = %q, want persistent daemon", h, got)
			}
		})
	}
}

func TestSupervisionMode_Unknown(t *testing.T) {
	if got := supervisionMode("nonexistent"); got != "persistent daemon" {
		t.Errorf("supervisionMode('nonexistent') = %q, want persistent daemon", got)
	}
}

func TestEnsureWatcherForSession_StartsOnlyForOwnedInFlightFleet(t *testing.T) {
	tmp := t.TempDir()
	if _, err := mhome.Init(tmp); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	seedCanonicalTask(t, tmp, "task-1", "ship")

	calls := 0
	result := ensureWatcherForSession(tmp, true, func(home string) WatchEnsureResult {
		calls++
		return WatchEnsureResult{State: "started"}
	})
	if calls != 1 || result.State != "started" {
		t.Fatalf("calls=%d result=%+v, want one started ensure", calls, result)
	}

	calls = 0
	result = ensureWatcherForSession(tmp, false, func(home string) WatchEnsureResult {
		calls++
		return WatchEnsureResult{State: "started"}
	})
	if calls != 0 || result.State != "read-only" {
		t.Fatalf("read-only calls=%d result=%+v", calls, result)
	}
}

func TestEnsureWatcherForSession_IdleFleetDoesNotStart(t *testing.T) {
	tmp := t.TempDir()
	if _, err := mhome.Init(tmp); err != nil {
		t.Fatal(err)
	}
	calls := 0
	result := ensureWatcherForSession(tmp, true, func(home string) WatchEnsureResult {
		calls++
		return WatchEnsureResult{State: "started"}
	})
	if calls != 0 || result.State != "idle" {
		t.Fatalf("calls=%d result=%+v, want idle no-op", calls, result)
	}
}

func TestEnsureWatcherForSession_HealthyWatcherIsReported(t *testing.T) {
	tmp := t.TempDir()
	if _, err := mhome.Init(tmp); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmp, "state")
	os.MkdirAll(stateDir, 0755)
	seedCanonicalTask(t, tmp, "task-1", "scout")

	result := ensureWatcherForSession(tmp, true, func(home string) WatchEnsureResult {
		return WatchEnsureResult{State: "healthy"}
	})
	if result.State != "healthy" {
		t.Fatalf("result=%+v, want healthy", result)
	}
}

func TestRunSessionStartReportsRuntimeIdentityBeforeScopeRefusal(t *testing.T) {
	home := t.TempDir()
	running := writeExecutable(t, home, "bin/munsu", "current")
	shadow := writeExecutable(t, home, "shadow/munsu", "shadow")
	saved := defaultRuntimeIdentityProbe
	defaultRuntimeIdentityProbe = func() runtimeIdentityProbe {
		p := saved()
		p.executable = func() (string, error) { return running, nil }
		p.lookPath = func(string) (string, error) { return shadow, nil }
		p.buildInfo = cleanBuildInfo
		p.integrationStatus = func(string, string, string, Scope) (*IntegrationResult, error) {
			return &IntegrationResult{Harness: "pi", Scope: ScopeProject, State: "absent"}, nil
		}
		return p
	}
	defer func() { defaultRuntimeIdentityProbe = saved }()
	t.Setenv("NO_MISTAKES_GATE", "1")

	var buf bytes.Buffer
	res, err := RunSessionStartWithWatcher(&buf, home, func(string) WatchEnsureResult {
		t.Fatal("watcher ensure must not run after scope refusal")
		return WatchEnsureResult{}
	}, nil, reclaimEvery)
	if err == nil {
		t.Fatal("expected scope refusal")
	}
	if res.RuntimeIdentity == nil || findSkew(res.RuntimeIdentity.Skew, SkewPathShadowing) == nil {
		t.Fatalf("refused session missing typed path_shadowing skew: %+v", res.RuntimeIdentity)
	}
	out := buf.String()
	if !strings.Contains(out, "--- Runtime Identity ---") || !strings.Contains(out, "skew: path_shadowing") {
		t.Fatalf("refused session did not print runtime identity before returning: %s", out)
	}
	if _, statErr := os.Stat(filepath.Join(home, "state", ".lock")); !os.IsNotExist(statErr) {
		t.Fatalf("session lock should not be acquired before diagnostics/scope refusal: %v", statErr)
	}
}

func TestRunSessionStartBootstrapFailureReleasesSessionLock(t *testing.T) {
	home := t.TempDir()
	if _, err := mhome.Init(home); err != nil {
		t.Fatal(err)
	}
	savedGate, hadGate := os.LookupEnv("NO_MISTAKES_GATE")
	_ = os.Unsetenv("NO_MISTAKES_GATE")
	defer func() {
		if hadGate {
			_ = os.Setenv("NO_MISTAKES_GATE", savedGate)
		} else {
			_ = os.Unsetenv("NO_MISTAKES_GATE")
		}
	}()
	bootstrapErr := errors.New("bootstrap failed")
	savedBootstrap := sessionStartBootstrap
	sessionStartBootstrap = func(string, bool, []string, *RuntimeIdentity) (*Result, error) {
		return nil, bootstrapErr
	}
	defer func() { sessionStartBootstrap = savedBootstrap }()

	var buf bytes.Buffer
	res, err := RunSessionStartWithWatcher(&buf, home, func(string) WatchEnsureResult {
		t.Fatal("watcher ensure must not run after bootstrap failure")
		return WatchEnsureResult{}
	}, nil)
	if err == nil || !errors.Is(err, bootstrapErr) {
		t.Fatalf("error = %v, want bootstrap failure", err)
	}
	if res.LockAcquired {
		t.Fatal("aborted session must not report lock ownership")
	}
	acquired, err := orchestrator.AcquireSession(home)
	if err != nil {
		t.Fatalf("reacquire session lock: %v", err)
	}
	if !acquired {
		t.Fatal("expected session lock to be released")
	}
	t.Cleanup(func() { _ = orchestrator.ReleaseSession(home) })
}

func TestRunSessionStartBootstrapFailureSurfacesReleaseError(t *testing.T) {
	home := t.TempDir()
	if _, err := mhome.Init(home); err != nil {
		t.Fatal(err)
	}
	savedGate, hadGate := os.LookupEnv("NO_MISTAKES_GATE")
	_ = os.Unsetenv("NO_MISTAKES_GATE")
	defer func() {
		if hadGate {
			_ = os.Setenv("NO_MISTAKES_GATE", savedGate)
		} else {
			_ = os.Unsetenv("NO_MISTAKES_GATE")
		}
	}()
	bootstrapErr := errors.New("bootstrap failed")
	releaseErr := errors.New("release failed")
	savedBootstrap := sessionStartBootstrap
	savedRelease := sessionStartRelease
	sessionStartBootstrap = func(string, bool, []string, *RuntimeIdentity) (*Result, error) {
		return nil, bootstrapErr
	}
	sessionStartRelease = func(string) error { return releaseErr }
	defer func() {
		sessionStartBootstrap = savedBootstrap
		sessionStartRelease = savedRelease
		_ = orchestrator.ReleaseSession(home)
	}()

	var buf bytes.Buffer
	res, err := RunSessionStartWithWatcher(&buf, home, nil, nil)
	if err == nil || !errors.Is(err, bootstrapErr) || !errors.Is(err, releaseErr) {
		t.Fatalf("error = %v, want bootstrap and release errors", err)
	}
	if !res.LockAcquired {
		t.Fatal("lock ownership must remain true when release fails")
	}
}

func TestRunSessionStartReportsRuntimeIdentityBeforeWatcherEnsure(t *testing.T) {
	home := t.TempDir()
	// #407 integrated Fleet-backed reads (scope + registry) require a canonical
	// home; init the temp dir truthfully before overlaying the in-flight task.
	if _, err := mhome.Init(home); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	seedCanonicalTask(t, home, "task-1", "scout")
	// session-start acquires state/.lock and, by design, holds it for the
	// process lifetime -- a real `munsu session-start` is a process, and exit
	// drops the flock. This test is not a process, and on Windows the still-open
	// handle also blocks TempDir's RemoveAll, so release it here (#549 group 10).
	t.Cleanup(func() { _ = mhome.ReleaseSessionLock(home) })
	running := writeExecutable(t, home, "bin/munsu", "current")
	shadow := writeExecutable(t, home, "shadow/munsu", "shadow")

	saved := defaultRuntimeIdentityProbe
	defaultRuntimeIdentityProbe = func() runtimeIdentityProbe {
		p := saved()
		p.executable = func() (string, error) { return running, nil }
		p.lookPath = func(string) (string, error) { return shadow, nil }
		p.buildInfo = cleanBuildInfo
		p.integrationStatus = func(string, string, string, Scope) (*IntegrationResult, error) {
			return &IntegrationResult{Harness: "pi", Scope: ScopeProject, State: "absent"}, nil
		}
		return p
	}
	defer func() { defaultRuntimeIdentityProbe = saved }()

	var buf bytes.Buffer
	ensured := false
	res, err := RunSessionStartWithWatcher(&buf, home, func(string) WatchEnsureResult {
		ensured = true
		if !strings.Contains(buf.String(), "skew: path_shadowing") {
			t.Fatalf("watcher ensure ran before runtime identity skew was printed; output so far:\n%s", buf.String())
		}
		return WatchEnsureResult{State: "healthy"}
	}, nil, reclaimEvery)
	if err != nil {
		t.Fatalf("RunSessionStartWithWatcher: %v", err)
	}
	if !ensured {
		t.Fatal("expected watcher ensure to run for in-flight scout")
	}
	if res.RuntimeIdentity == nil || findSkew(res.RuntimeIdentity.Skew, SkewPathShadowing) == nil {
		t.Fatalf("session result missing typed path_shadowing skew: %+v", res.RuntimeIdentity)
	}
	out := buf.String()
	identityIdx := strings.Index(out, "--- Runtime Identity ---")
	watcherIdx := strings.Index(out, "--- Watcher Ensure ---")
	if identityIdx < 0 || watcherIdx < 0 || identityIdx > watcherIdx {
		t.Fatalf("runtime identity must print before watcher ensure; output:\n%s", out)
	}
}

// --- Captain Liveness section tests ---

func TestPrintCaptainLiveness_NilSeamIsNoop(t *testing.T) {
	var buf bytes.Buffer
	res := printCaptainLiveness(&buf, t.TempDir(), true, nil)
	if res != nil {
		t.Errorf("expected nil result for nil seam, got %+v", res)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for nil seam, got %q", buf.String())
	}
}

func TestPrintCaptainLiveness_NoCaptains(t *testing.T) {
	var buf bytes.Buffer
	fn := func(string, bool) CaptainLivenessResult { return CaptainLivenessResult{} }
	printCaptainLiveness(&buf, t.TempDir(), true, fn)
	if !strings.Contains(buf.String(), "(no captains registered)") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestPrintCaptainLiveness_DeadSurfacesDiagnostic(t *testing.T) {
	var buf bytes.Buffer
	fn := func(string, bool) CaptainLivenessResult {
		return CaptainLivenessResult{
			Probes:  []CaptainProbe{{ID: "sm-1", Status: "alive"}, {ID: "sm-2", Status: "dead"}},
			HasDead: true,
		}
	}
	res := printCaptainLiveness(&buf, t.TempDir(), true, fn)
	out := buf.String()
	if !strings.Contains(out, "sm-1: alive") || !strings.Contains(out, "sm-2: dead") {
		t.Errorf("output missing probes: %q", out)
	}
	if !strings.Contains(out, "SECOND_LIVENESS: dead captain endpoint(s) detected") {
		t.Errorf("output missing SECOND_LIVENESS line: %q", out)
	}
	if !strings.Contains(out, "relaunch with: munsu captain recover") {
		t.Errorf("output missing relaunch hint: %q", out)
	}
	if res == nil || !res.HasDead {
		t.Errorf("result = %+v, want HasDead", res)
	}
}

func TestPrintCaptainLiveness_ReadOnlyDoesNotHintRecover(t *testing.T) {
	var buf bytes.Buffer
	fn := func(string, bool) CaptainLivenessResult {
		return CaptainLivenessResult{
			Probes:  []CaptainProbe{{ID: "sm-1", Status: "dead"}},
			HasDead: true,
		}
	}
	printCaptainLiveness(&buf, t.TempDir(), false, fn)
	out := buf.String()
	if !strings.Contains(out, "read-only: run 'munsu captain recover'") {
		t.Errorf("read-only output missing manual hint: %q", out)
	}
}

func TestPrintCaptainLiveness_RecoverSummaryPrinted(t *testing.T) {
	var buf bytes.Buffer
	fn := func(string, bool) CaptainLivenessResult {
		return CaptainLivenessResult{
			Probes:  []CaptainProbe{{ID: "sm-1", Status: "dead"}},
			HasDead: true,
			Recover: &CaptainRecoverSummary{Relaunched: 1, Alive: 0, Seeded: 0, Failed: 0, Entries: []string{"sm-1: relaunched"}},
		}
	}
	printCaptainLiveness(&buf, t.TempDir(), true, fn)
	out := buf.String()
	if !strings.Contains(out, "recover: relaunched=1") {
		t.Errorf("output missing recover summary: %q", out)
	}
	if !strings.Contains(out, "sm-1: relaunched") {
		t.Errorf("output missing recover entry line: %q", out)
	}
}

// reclaimNone and reclaimEvery stand in for the composition root's
// reclaimer. They live in this untagged file because the untagged and
// windows tests use them too, and a helper defined only under the
// integration tag is invisible to those builds.
func reclaimNone(_ string, reclaim func() error) (bool, error) { return true, reclaim() }
func reclaimEvery(string, func() error) (bool, error)          { return false, nil }
