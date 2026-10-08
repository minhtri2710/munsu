package fleet

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestLaunchRecoveryCrashBoundariesNoDuplicates drives a crash at every
// launch boundary and proves recovery with the same Operation ID/generation
// continues the same launch without duplicate worktree, endpoint, launch
// submission, or working records: exactly one Worktree/Endpoint/
// AcquiredEndpoint/Launch/LaunchEvidence record, one endpoint creation, one
// submission, and one queued → working transition.
func TestLaunchRecoveryCrashBoundariesNoDuplicates(t *testing.T) {
	boundaries := []struct {
		crashAfter string
		wantErr    bool // re-acquisition at the pre-bind boundary re-calls the deterministic git-fallback provider
	}{
		{"begin", false},           // before acquisition
		{"contract", false},        // after the durable delivery contract record
		{"acquire", false},         // after worktree acquisition, before bind
		{"bind-worktree", false},   // after worktree bind
		{"manifest", false},        // after canonical anchor and artifact publication
		{"create-session", false},  /* after endpoint create */
		{"attach-endpoint", false}, // after durable attach
		{"submit", false},          // after launch submit
		{"meta", false},            // after metadata, before final bind
		{"confirm", false},         // after final bind
	}
	for _, b := range boundaries {
		t.Run(b.crashAfter, func(t *testing.T) {
			f := newLaunchFixture(t, "recover-"+strings.ReplaceAll(b.crashAfter, "-", ""))

			err := runLaunchPhases(f, b.crashAfter)
			if !errors.Is(err, errCrashSimulated) {
				t.Fatalf("first run stopped at %q with %v, want simulated crash", b.crashAfter, err)
			}
			first := f.aggregate()
			firstPath := f.runner.wtPath
			if b.crashAfter == "manifest" {
				if first.Worktree == nil || first.Worktree.LaunchManifest == nil || first.AcquiredEndpoint != nil || first.LaunchEvidence != nil {
					t.Fatalf("manifest boundary aggregate = %+v, want canonical anchor before endpoint acquisition/submission", first)
				}
				if f.endpoints.createCount() != 0 || f.endpoints.submitCount() != 0 {
					t.Fatalf("endpoint activity before manifest boundary: creates=%d submits=%d", f.endpoints.createCount(), f.endpoints.submitCount())
				}
				if err := VerifyPreparedLaunchArtifacts(first.Worktree.Path, first.Worktree.LaunchManifest.ManifestSHA256); err != nil {
					t.Fatalf("verify manifest-bound artifacts at crash boundary: %v", err)
				}
			}

			// Recovery: re-run the full launch sequence.
			if err := runLaunchPhases(f, ""); err != nil {
				t.Fatalf("recovery run failed: %v", err)
			}
			agg := f.aggregate()
			if agg.Phase != taskauthority.PhaseWorking {
				t.Fatalf("phase = %q, want working", agg.Phase)
			}
			if agg.Worktree == nil || agg.Endpoint == nil || agg.AcquiredEndpoint == nil || agg.LaunchEvidence == nil || agg.Launch == nil {
				t.Fatalf("launch records incomplete: %+v", agg)
			}
			if f.endpoints.createCount() != 1 {
				t.Fatalf("endpoint creates = %d, want 1 (no duplicate endpoint)", f.endpoints.createCount())
			}
			if f.endpoints.submitCount() != 1 {
				t.Fatalf("launch submits = %d, want 1 (no duplicate submission)", f.endpoints.submitCount())
			}
			// One queued -> working transition includes the canonical manifest
			// anchor: revision 8 (create, begin, delivery contract, worktree,
			// manifest evidence, acquired endpoint, launch evidence, endpoint).
			if agg.Revision != 8 {
				t.Fatalf("revision = %d, want 8 (one launch through working)", agg.Revision)
			}
			if agg.DeliveryContract == nil || agg.DeliveryContract.Mode != f.runner.contractMode {
				t.Fatalf("delivery contract = %+v, want mode %q", agg.DeliveryContract, f.runner.contractMode)
			}
			// The re-adopted records are the FIRST attempt's records, never
			// fresh ones (where the first attempt committed them).
			if first.AcquiredEndpoint != nil && agg.AcquiredEndpoint.Handle != first.AcquiredEndpoint.Handle {
				t.Fatalf("acquired endpoint replaced on recovery: %s -> %s", first.AcquiredEndpoint.Handle, agg.AcquiredEndpoint.Handle)
			}
			if first.LaunchEvidence != nil && agg.LaunchEvidence.CommandDigest != first.LaunchEvidence.CommandDigest {
				t.Fatalf("launch evidence replaced on recovery")
			}
			// The reservation-owned worktree is the same path across the crash
			// (git fallback: deterministic reservation-keyed path), so no
			// duplicate lease/path is ever created.
			if b.crashAfter == "acquire" {
				if firstPath == "" || f.runner.wtPath != firstPath {
					t.Fatalf("worktree path changed across recovery: %q -> %q (same reservation must return the same worktree)", firstPath, f.runner.wtPath)
				}
			}
		})
	}
}

// TestLaunchRecoveryPostSubmitPreRecordGuardProvesSingleProcess proves the
// reworked evidence ordering: a crash AFTER the submission was delivered but
// BEFORE the durable launch record may re-submit the exact same command on
// recovery, but the REAL production launch artifact guard proves exactly ONE
// Soldier process launch across the boundary, and RecordLaunch commits exactly
// once. Endpoint command submission count (2) is distinguished from the
// process-launch count (1).
func TestLaunchRecoveryPostSubmitPreRecordGuardProvesSingleProcess(t *testing.T) {
	f := newLaunchFixture(t, "recover-guard")
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)
	// The endpoint executes the production artifact in a real shell (the
	// pane) and reports a crash (error) AFTER the first delivery — the
	// post-Submit/pre-Record boundary. No LaunchEvidence is committed.
	exec := &executingEndpointCapabilities{
		inner:          f.endpoints,
		runCommand:     scriptRunCommand(t),
		firstSubmitErr: true,
	}
	f.runner.endpoints = exec
	if err := runLaunchPhases(f, "submit"); err == nil {
		t.Fatal("first run must fail at the submission (crash before record)")
	}
	agg := f.aggregate()
	if agg.LaunchEvidence != nil {
		t.Fatalf("LaunchEvidence must be ABSENT when the submission did not succeed durably: %+v", agg.LaunchEvidence)
	}
	if n := harnessLaunchCount(t, counter); n != 1 {
		t.Fatalf("harness process launches after first delivery = %d, want 1", n)
	}
	// Recovery: the same command is re-submitted (submission count 2), the
	// production guard sees the same launch identity and no-ops, so exactly
	// one process remains, and RecordLaunch commits once.
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("recovery run failed: %v", err)
	}
	if n := harnessLaunchCount(t, counter); n != 1 {
		t.Fatalf("harness process launches across the boundary = %d, want exactly 1 (no second Soldier process)", n)
	}
	final := f.aggregate()
	if final.LaunchEvidence == nil {
		t.Fatal("LaunchEvidence must commit exactly once after the successful submission")
	}
	if final.Phase != taskauthority.PhaseWorking {
		t.Fatalf("phase = %q, want working", final.Phase)
	}
	if exec.submitCount() != 2 {
		t.Fatalf("endpoint command submissions = %d, want 2 (re-submission allowed; guard bounds the process)", exec.submitCount())
	}
}

func TestLaunchReadinessWaitsForDelayedExactGuard(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	f := newLaunchFixture(t, "guard-delayed-ready")
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)
	caps := &executingEndpointCapabilities{inner: f.endpoints, runCommand: scriptRunCommand(t), delayUntilCapture: 2}
	f.runner.endpoints = caps

	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("launch phases with delayed guard execution: %v", err)
	}
	if caps.captures != 2 {
		t.Fatalf("readiness captures = %d, want 2 (first ready capture precedes guard execution)", caps.captures)
	}
	if caps.submitCount() != 1 {
		t.Fatalf("launch submissions = %d, want 1", caps.submitCount())
	}
	if err := VerifyLaunchArtifacts(f.runner.wtPath, f.runner.manifestSHA256); err != nil {
		t.Fatalf("strict guard verification after readiness: %v", err)
	}
}

func TestLaunchReadinessRejectsUnsafeGuard(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, guardDir, identityPath string)
		want   string
	}{
		{
			name: "identity mismatch",
			mutate: func(t *testing.T, _, identityPath string) {
				if err := os.WriteFile(identityPath, []byte("foreign-launch|1|foreign-fence"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "digest mismatch",
		},
		{
			name: "unexpected child",
			mutate: func(t *testing.T, guardDir, _ string) {
				if err := os.WriteFile(filepath.Join(guardDir, "unowned"), []byte("foreign"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "unexpected contents",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "harness-launches.log")
			harnessDir := fakeHarnessDir(t, "pi", counter)
			gitBin, err := exec.LookPath("git")
			if err != nil {
				t.Fatalf("git on PATH: %v", err)
			}
			f := newLaunchFixture(t, "guard-unsafe")
			testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)
			caps := &executingEndpointCapabilities{
				inner:             f.endpoints,
				runCommand:        scriptRunCommand(t),
				delayUntilCapture: 1,
				afterExecution: func() error {
					manifest, err := ReadManifest(f.runner.wtPath)
					if err != nil {
						return err
					}
					for _, entry := range manifest.Artifacts {
						if isDeferredGuardIdentityPath(entry.Path) {
							identityPath := filepath.Join(f.runner.wtPath, filepath.FromSlash(entry.Path))
							tc.mutate(t, filepath.Dir(identityPath), identityPath)
							return nil
						}
					}
					return fmt.Errorf("manifest lacks deferred guard identity")
				},
			}
			f.runner.endpoints = caps
			err = runLaunchPhases(f, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("launch with unsafe guard error = %v, want refusal containing %q", err, tc.want)
			}
			if caps.captures != 2 {
				t.Fatalf("unsafe guard captures = %d, want one ready capture plus one diagnostic capture", caps.captures)
			}
			if caps.disposes != 0 {
				t.Fatalf("unsafe guard caused %d endpoint disposals, want 0", caps.disposes)
			}
			if f.aggregate().Phase == taskauthority.PhaseWorking {
				t.Fatal("unsafe guard was accepted as a ready launch")
			}
		})
	}
}

func TestLaunchReadinessDoesNotWaitOnMixedGuardAndArtifactFailure(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	f := newLaunchFixture(t, "guard-mixed-failure")
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)
	caps := &executingEndpointCapabilities{
		inner:             f.endpoints,
		runCommand:        scriptRunCommand(t),
		delayUntilCapture: 1,
		afterExecution: func() error {
			manifest, err := ReadManifest(f.runner.wtPath)
			if err != nil {
				return err
			}
			var guardDir string
			for _, entry := range manifest.Artifacts {
				if isDeferredGuardIdentityPath(entry.Path) {
					identityPath := filepath.Join(f.runner.wtPath, filepath.FromSlash(entry.Path))
					guardDir = filepath.Dir(identityPath)
					if err := os.Remove(identityPath); err != nil {
						return err
					}
					break
				}
			}
			if guardDir == "" {
				return fmt.Errorf("manifest lacks deferred guard identity")
			}
			if err := os.Remove(guardDir); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(f.runner.wtPath, BriefName), []byte("tampered brief"), 0o644)
		},
	}
	f.runner.endpoints = caps
	if err := runLaunchPhases(f, "submit"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("submit boundary: %v", err)
	}

	err = f.runner.waitForHarnessReady(5)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 digest mismatch") || !errors.Is(err, errDeferredLaunchGuardAbsent) {
		t.Fatalf("waitForHarnessReady error = %v, want typed absent guard plus immediate anchored-artifact refusal", err)
	}
	if caps.captures != 1 {
		t.Fatalf("captures = %d, want one ready capture and immediate refusal", caps.captures)
	}
	if caps.disposes != 0 {
		t.Fatalf("mixed verification failure caused %d endpoint disposals, want 0", caps.disposes)
	}
}

func TestLaunchReadinessTimesOutWhileGuardAbsentWithoutDisposal(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	f := newLaunchFixture(t, "guard-absent-timeout")
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)
	caps := &executingEndpointCapabilities{
		inner:             f.endpoints,
		runCommand:        scriptRunCommand(t),
		delayUntilCapture: 100,
	}
	f.runner.endpoints = caps
	if err := runLaunchPhases(f, "submit"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("submit boundary: %v", err)
	}

	err = f.runner.waitForHarnessReady(1)
	if err == nil || !strings.Contains(err.Error(), "deferred launch guard identity remained absent") {
		t.Fatalf("waitForHarnessReady error = %v, want bounded refusal after ready capture with absent guard", err)
	}
	if caps.disposes != 0 {
		t.Fatalf("absent guard caused %d endpoint disposals, want 0", caps.disposes)
	}
	if f.aggregate().Phase == taskauthority.PhaseWorking {
		t.Fatal("absent guard was accepted as a ready launch")
	}
	if caps.submitCount() != 1 {
		t.Fatalf("launch submissions = %d, want 1", caps.submitCount())
	}
}

func TestRecordedEndpointWaitsForGuardWithoutResubmission(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	f := newLaunchFixture(t, "guard-recorded-recovery")
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)
	caps := &executingEndpointCapabilities{
		inner:             f.endpoints,
		runCommand:        scriptRunCommand(t),
		delayUntilCapture: 1,
	}
	f.runner.endpoints = caps
	if err := runLaunchPhases(f, "submit"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first submit boundary: %v", err)
	}
	if f.aggregate().LaunchEvidence == nil {
		t.Fatal("successful submission did not record canonical launch evidence")
	}
	if caps.submitCount() != 1 {
		t.Fatalf("initial submissions = %d, want 1", caps.submitCount())
	}

	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("recorded endpoint recovery: %v", err)
	}
	if caps.submitCount() != 1 {
		t.Fatalf("recorded endpoint recovery resubmitted launch: submissions=%d, want 1", caps.submitCount())
	}
	if caps.captures != 1 {
		t.Fatalf("recovery captures = %d, want 1 after the exact submitted script creates its guard", caps.captures)
	}
	if f.aggregate().Phase != taskauthority.PhaseWorking {
		t.Fatalf("recovered phase = %q, want working", f.aggregate().Phase)
	}
}

// TestLaunchRecoveryPostSubmitFailureNamesLiveEndpoint fails Runner.Run at each
// phase after the submission was delivered. The error Run returns keeps the
// cause, names the task, the backend and the endpoint handle, says the soldier
// process may still be running, and gives both ways out: fix the cause and
// re-run `munsu spawn <id> <project>`, or stop the pane by hand. Each row then
// takes only those steps. Where the re-run resumes, it adopts the same endpoint
// and works the task; where the cause persists (the task stays blocked, .meta
// names the window) the re-run is refused with the existing refusal's own reason.
// The submitted launch command executes the real launch artifact, so the harness
// launch count is the number of soldier processes.
func TestLaunchRecoveryPostSubmitFailureNamesLiveEndpoint(t *testing.T) {
	errInjected := errors.New("injected failure")
	for _, tc := range []struct {
		name string
		// inject arms the failure; the endpoint probes of one Run are the
		// create check (1), the handshake (2) and the readiness check (3). The
		// repair it returns is the operator fixing the cause; the hooks are
		// disarmed for every row, which fixes a hook cause.
		inject    func(f *launchFixture) (repair func())
		wantCause func(f *launchFixture, err error) bool
		// refusal is the reason a re-run is refused with because the cause
		// persists; empty when the re-run resumes once the operator fixed the cause.
		refusal string
		// wantSubmits is the number of endpoint submissions once a re-run resumes.
		wantSubmits int
	}{
		{
			// The task is blocked after the process was started, so the
			// evidence is refused as stale. The block stays, so the re-run is
			// refused with the existing blocked-task refusal.
			name: "record launch",
			inject: func(f *launchFixture) func() {
				id := mustTaskID(f.t, f.taskID)
				f.endpoints.onSubmit = func() {
					agg := f.aggregate()
					req := taskauthority.CanonicalBlockRequest{HomeID: f.auth.HomeID(), TaskID: id, Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)), Detail: "test", Reason: "test"}
					if _, err := f.auth.Block(mustFleetOperation(f.t, "block-"+f.taskID, req), req); err != nil {
						f.t.Fatal(err)
					}
				}
				return nil
			},
			wantCause: func(_ *launchFixture, err error) bool { return errors.Is(err, domain.ErrStalePrecondition) },
			refusal:   "is blocked; clear the blocker with 'munsu task unblock post-submit-recordlaunch'",
		},
		{
			// The brief the manifest binds is gone; verify-only recovery refuses
			// before re-adopting the pane or recreating covered files.
			name: "launch manifest",
			inject: func(f *launchFixture) func() {
				f.endpoints.onSubmit = func() {
					wt := f.aggregate().Worktree.Path
					if err := os.Remove(filepath.Join(wt, BriefName)); err != nil {
						f.t.Fatal(err)
					}
				}
				return nil
			},
			wantCause: func(_ *launchFixture, err error) bool {
				return strings.Contains(err.Error(), "verifying artifacts for recorded launch") && errors.Is(err, os.ErrNotExist)
			},
			refusal: "verifying artifacts for recorded launch",
		},
		{
			name: "harness handshake",
			inject: func(f *launchFixture) func() {
				probes := 0
				f.endpoints.onProbe = func() error {
					if probes++; probes == 2 {
						return errInjected
					}
					return nil
				}
				return nil
			},
			wantCause:   func(_ *launchFixture, err error) bool { return errors.Is(err, errInjected) },
			wantSubmits: 1,
		},
		{
			name: "endpoint readiness",
			inject: func(f *launchFixture) func() {
				probes := 0
				f.endpoints.onProbe = func() error {
					if probes++; probes == 3 {
						return errInjected
					}
					return nil
				}
				return nil
			},
			wantCause:   func(_ *launchFixture, err error) bool { return errors.Is(err, errInjected) },
			wantSubmits: 1,
		},
		{
			// The meta path is a directory, so reading it fails; the file never
			// names a window, so the re-run is admitted. The read error is an
			// *fs.PathError for the read of that path on every platform: os.File.Read
			// wraps every failure through File.wrapErr (os/file.go), whatever errno
			// the platform gives for reading a directory.
			name: "task meta",
			inject: func(f *launchFixture) func() {
				path, err := home.MetaFilePath(f.homeDir, f.taskID)
				if err != nil {
					f.t.Fatal(err)
				}
				probes := 0
				f.endpoints.onProbe = func() error {
					if probes++; probes == 3 {
						if err := os.MkdirAll(path, 0o755); err != nil {
							f.t.Fatal(err)
						}
					}
					return nil
				}
				return func() { os.Remove(path) }
			},
			wantCause: func(f *launchFixture, err error) bool {
				path, perr := home.MetaFilePath(f.homeDir, f.taskID)
				var pe *fs.PathError
				return perr == nil && errors.As(err, &pe) && pe.Op == "read" && pe.Path == path
			},
			wantSubmits: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskID := "post-submit-" + strings.ReplaceAll(tc.name, " ", "")
			rf := newRunFixture(t, taskID)
			f := rf.launchFixture
			repair := tc.inject(f)

			_, err := rf.run()
			if err == nil {
				t.Fatal("Run succeeded; the injected failure must fail it")
			}
			if !tc.wantCause(f, err) {
				t.Fatalf("the error does not keep the underlying cause: %v", err)
			}
			msg := err.Error()
			rerun := "munsu spawn " + taskID + " test-proj"
			for _, want := range []string{
				taskID, "backend tmux endpoint pane-1", "may still be running",
				"fix the cause", rerun, "refuses with its own reason", "re-adopts the pane only while the pane is live",
				"stop pane pane-1 on backend tmux by hand",
			} {
				if !strings.Contains(msg, want) {
					t.Fatalf("error %q does not contain %q", msg, want)
				}
			}
			if n := harnessLaunchCount(t, rf.counter); n != 1 {
				t.Fatalf("soldier launches after the failed Run = %d, want 1", n)
			}

			if tc.refusal != "" {
				// The cause persists (the task stays blocked, .meta names the window).
				before := f.aggregate().Revision
				if _, err := rf.run(); err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("re-run = %v, want the refusal %q", err, tc.refusal)
				}
				if after := f.aggregate().Revision; after != before {
					t.Fatalf("a refused re-run changed the task: revision %d -> %d", before, after)
				}
				if n := harnessLaunchCount(t, rf.counter); n != 1 {
					t.Fatalf("soldier launches after the refused re-run = %d, want 1", n)
				}
				return
			}
			// The operator fixes the cause, then re-runs.
			f.endpoints.onSubmit, f.endpoints.onProbe = nil, nil
			if repair != nil {
				repair()
			}
			window, err := rf.run()
			if err != nil {
				t.Fatalf("re-run after the operator fixed the cause: %v", err)
			}
			if window != "pane-1" || f.endpoints.createCount() != 1 || rf.exec.submitCount() != tc.wantSubmits {
				t.Fatalf("re-run: window %q, creates %d, submits %d; want pane-1, 1, %d", window, f.endpoints.createCount(), rf.exec.submitCount(), tc.wantSubmits)
			}
			if n := harnessLaunchCount(t, rf.counter); n != 1 {
				t.Fatalf("soldier launches across the recovery = %d, want exactly 1 (no second process)", n)
			}
			if f.aggregate().Phase != taskauthority.PhaseWorking {
				t.Fatalf("phase = %q, want working", f.aggregate().Phase)
			}
		})
	}
}

// runFixture is a launchFixture that spawns through Runner.Run. Its endpoint
// executes every submitted launch command in a real shell, so counter holds one
// line per soldier process that really started.
type runFixture struct {
	*launchFixture
	exec    *executingEndpointCapabilities
	counter string
	run     func() (string, error)
}

// newRunFixture is a home with one registered project, one queued task and a
// fake pi on PATH, plus run, which spawns the task through a fresh Runner the
// way the CLI does. Only the endpoint capability is a fake.
func newRunFixture(t *testing.T, taskID string) *runFixture {
	t.Helper()
	isolateHuman(t)
	t.Setenv("OPENAI_API_KEY", "test")
	t.Setenv("MUNSU_ROLE", "general")
	t.Chdir(t.TempDir()) // not a linked worktree, so spawn authority passes
	homeDir := t.TempDir()
	repoPath := initRepoForSpawnBinding(t, t.TempDir())
	storeTestDocuments(t, homeDir, config.FleetBaseDocument{
		SchemaVersion: config.FleetBaseSchemaVersion,
		Config:        config.ProjectOverlay{Backend: "tmux", SoldierHarness: "pi", Model: "gpt-5"},
	}, []testProjectRecord{{Name: "test-proj", Path: repoPath}}, nil)
	auth := canonicalAtHome(t, homeDir)
	canonicalCreateTask(t, auth, taskID, "ship", "test-proj")
	if err := Scaffold(ScaffoldOptions{HomeDir: homeDir, ID: taskID, Repo: "test-proj", Mode: "local-only"}); err != nil {
		t.Fatal(err)
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(harnessDir, "pi"), fmt.Sprintf("#!/bin/sh\n[ \"$1\" = --version ] || echo launch >> %q\n", counter))
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)

	endpoints := newReentrantEndpoints()
	executing := &executingEndpointCapabilities{inner: endpoints, runCommand: scriptRunCommand(t)}
	f := &launchFixture{t: t, auth: auth, homeDir: homeDir, repoPath: repoPath, taskID: taskID, endpoints: endpoints}
	return &runFixture{launchFixture: f, exec: executing, counter: counter, run: func() (string, error) {
		return NewRunner(Args{ID: taskID, ProjectName: "test-proj", Mode: "local-only", HomeDir: homeDir, Endpoints: executing, Authority: auth}).Run()
	}}
}

// TestLaunchRecoverySubmitErrorBeforeExecutionRetryable proves a Submit error
// BEFORE the script executed commits no evidence and the same launch retries
// the submission under the same identity (one process, one record).
func TestLaunchRecoverySubmitErrorBeforeExecutionRetryable(t *testing.T) {
	f := newLaunchFixture(t, "recover-submiterr")
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)
	// The first Submit fails BEFORE any script execution (delivery failure).
	exec := &executingEndpointCapabilities{
		inner:          f.endpoints,
		runCommand:     scriptRunCommand(t),
		firstSubmitErr: true,
		skipFirstRun:   true,
	}
	f.runner.endpoints = exec
	if err := runLaunchPhases(f, "submit"); err == nil {
		t.Fatal("first run must fail at the submission")
	}
	if n := harnessLaunchCount(t, counter); n != 0 {
		t.Fatalf("harness launches before delivery = %d, want 0", n)
	}
	if f.aggregate().LaunchEvidence != nil {
		t.Fatal("no evidence may be recorded for an undelivered submission")
	}
	// Recovery retries the same submission: one process, one record.
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("recovery run failed: %v", err)
	}
	if n := harnessLaunchCount(t, counter); n != 1 {
		t.Fatalf("harness process launches = %d, want 1", n)
	}
	if f.aggregate().LaunchEvidence == nil {
		t.Fatal("evidence must commit after the successful retried submission")
	}
}

// TestLaunchRecoveryCallCountsAfterAttach proves the required call counts on
// same-operation/generation re-entry after a mid-flow crash (after the durable
// endpoint attach): worktree acquisition <= 1, endpoint create <= 1, launch
// submit <= 1, final working transition <= 1, and exactly one
// endpoint/Soldier record.
func TestLaunchRecoveryCallCountsAfterAttach(t *testing.T) {
	f := newLaunchFixture(t, "recover-counts")
	if err := runLaunchPhases(f, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// After the attach the worktree is durably bound: recovery must NOT call
	// the worktree provider again. Prove it by holding the pool lock so a
	// second acquisition would fail — recovery must still succeed by adopting
	// the bound worktree.
	holdWorktreePool(t, f)
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("recovery run failed (worktree must be adopted, not re-acquired): %v", err)
	}
	agg := f.aggregate()
	if f.endpoints.createCount() != 1 {
		t.Fatalf("endpoint create = %d, want 1", f.endpoints.createCount())
	}
	if f.endpoints.submitCount() != 1 {
		t.Fatalf("launch submit = %d, want 1", f.endpoints.submitCount())
	}
	if agg.Phase != taskauthority.PhaseWorking || agg.Endpoint == nil {
		t.Fatalf("one final working transition expected: %+v", agg)
	}
	if agg.Endpoint.Handle != "pane-1" {
		t.Fatalf("endpoint record = %+v, want exactly the single created pane", agg.Endpoint)
	}
}

// holdWorktreePool makes a second worktree acquisition fail: acquireWorktree
// takes the worktree-pool lock before it leases, so a held lock refuses it while
// an adopted durable binding never reaches the lock. The primary repo stays
// intact: launch fence resolves it on every re-entry, and the bound linked
// worktree needs it to classify.
func holdWorktreePool(t *testing.T, f *launchFixture) {
	t.Helper()
	lk, err := LockWorktreePool(f.homeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lk.Release() })
}

// TestLaunchRecoveryAfterFinalBindReplaysIdempotently proves recovery after
// the final bind does not duplicate any record: the launch is complete under
// the identical identity and re-entry replays idempotently (task working, one
// of every record, revision unchanged).
func TestLaunchRecoveryAfterFinalBindReplaysIdempotently(t *testing.T) {
	f := newLaunchFixture(t, "recover-final")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("first full run: %v", err)
	}
	first := f.aggregate()
	if first.Phase != taskauthority.PhaseWorking {
		t.Fatalf("phase = %q, want working", first.Phase)
	}
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("idempotent re-entry failed: %v", err)
	}
	second := f.aggregate()
	if second.Revision != first.Revision || second.Phase != first.Phase {
		t.Fatalf("re-entry mutated the aggregate: %+v vs %+v", second, first)
	}
	if second.Endpoint.Handle != first.Endpoint.Handle || second.AcquiredEndpoint.Handle != first.AcquiredEndpoint.Handle {
		t.Fatalf("records replaced on idempotent re-entry")
	}
	if f.endpoints.createCount() != 1 || f.endpoints.submitCount() != 1 {
		t.Fatalf("duplicate create/submit on idempotent re-entry: creates=%d submits=%d", f.endpoints.createCount(), f.endpoints.submitCount())
	}
}

// TestLaunchRecoveryEndpointIdentitySubstitutionFailsClosed proves a recorded
// acquired endpoint can never be substituted: an attach attempt carrying a
// different endpoint identity fails closed (canonical conflict), and a
// tampered recorded identity fails closed on the runner's identity check.
func TestLaunchRecoveryEndpointIdentitySubstitutionFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "recover-ep-sub")
	if err := runLaunchPhases(f, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// Substitution: the runner presents a DIFFERENT created endpoint than the
	// recorded one; attach must fail closed (never overwrite the record).
	f.runner.endpoint.Handle = "pane-substituted"
	if err := f.runner.attachEndpoint(); err == nil {
		t.Fatal("endpoint identity substitution must fail closed")
	} else if !strings.Contains(err.Error(), "does not match created endpoint") {
		t.Fatalf("error = %v, want identity-mismatch refusal", err)
	}

	// Tampered durable record: the committed acquired endpoint carries a
	// different handle; the runner's verification refuses.
	f2 := newLaunchFixture(t, "recover-ep-tamper")
	if err := runLaunchPhases(f2, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	tamperTaskAggregate(t, f2.homeDir, f2.taskID, func(agg *taskauthority.Aggregate) {
		if agg.AcquiredEndpoint != nil {
			agg.AcquiredEndpoint.Handle = "pane-tampered"
		}
	})
	if err := f2.runner.attachEndpoint(); err == nil {
		t.Fatal("tampered acquired endpoint must fail closed")
	}
}

// TestLaunchRecoveryWorktreeIdentitySubstitutionFailsClosed proves the
// intent-owned worktree fence is exact: a retry whose launch derivation
// differs from the committed worktree binding fails closed instead of
// re-binding or adopting a foreign worktree.
func TestLaunchRecoveryWorktreeIdentitySubstitutionFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "recover-wt-sub")
	if err := runLaunchPhases(f, "bind-worktree"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// The retry derives a DIFFERENT intent (changed snapshot): the committed
	// binding's fence can never match the new derivation.
	f.runner.projectConfig.SnapshotDigest = strings.Repeat("c", 64)
	if err := f.runner.beginLaunchIntent(); err == nil {
		t.Fatal("changed intent after binding must fail closed")
	}
	// A direct substitution of the intent fence (simulating a regression that
	// mints a different reservation) fails closed at the bind verification.
	f2 := newLaunchFixture(t, "recover-wt-fence")
	if err := runLaunchPhases(f2, "bind-worktree"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	f2.runner.launch = &taskauthority.LaunchIntent{
		LaunchID:              f2.runner.launch.LaunchID,
		WindowLabel:           f2.runner.launch.WindowLabel,
		WorktreeReservationID: "wtres-foreign",
		WorktreeFenceToken:    "wtfence-foreign",
		EndpointReservationID: f2.runner.launch.EndpointReservationID,
		EndpointFenceToken:    f2.runner.launch.EndpointFenceToken,
		Backend:               f2.runner.launch.Backend,
	}
	if _, err := f2.runner.bindWorktree(); err == nil {
		t.Fatal("worktree fence substitution must fail closed")
	} else if !strings.Contains(err.Error(), "reservation fence") {
		t.Fatalf("error = %v, want worktree reservation fence refusal", err)
	}
}

// TestLaunchRecoveryAdoptedBindingIsReclassified proves the recovery branch of
// bindWorktree re-classifies the path it adopts instead of trusting the
// aggregate. The fence proves the binding belongs to this launch intent; it
// says nothing about what the path IS now, and between two launches the
// worktree can be removed and the path replaced. Only classification catches
// that, and it must catch it before any launch file is written there.
func TestLaunchRecoveryAdoptedBindingIsReclassified(t *testing.T) {
	f := newLaunchFixture(t, "recover-wt-identity")
	if err := runLaunchPhases(f, "bind-worktree"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// An ordinary directory now sits at the bound path; lease and fence token
	// are untouched, so the fence check passes.
	replacement := t.TempDir()
	tamperTaskAggregate(t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) {
		agg.Worktree.Path = replacement
	})
	if _, err := f.runner.bindWorktree(); err == nil {
		t.Fatal("adopting a bound path that is no longer a worktree must fail closed")
	} else if !strings.Contains(err.Error(), "not worktree") {
		t.Fatalf("error = %v, want worktree identity refusal", err)
	}
}

// TestLaunchRecoveryReentrantEndpointRequiredOnReentry proves the
// DEPENDENCY_REQUEST fail-closed contract: when recovery needs an endpoint
// TestLaunchIntentFailsClosedWithoutEndpointCapability proves the mandatory
// reservation-aware endpoint contract is checked BEFORE any acquisition: a
// launch without a composed endpoint capability fails closed at the intent
// phase, before any worktree or endpoint allocation.
func TestLaunchIntentFailsClosedWithoutEndpointCapability(t *testing.T) {
	f := newLaunchFixture(t, "recover-noendpoints")
	f.runner.endpoints = nil
	if err := f.runner.beginLaunchIntent(); err == nil {
		t.Fatal("missing endpoint capabilities must fail closed before acquisition")
	} else if !strings.Contains(err.Error(), "endpoint capabilities are not composed") {
		t.Fatalf("error = %v, want endpoint-composition fail-closed", err)
	}
	agg := f.aggregate()
	if agg.Launch != nil || agg.Worktree != nil || agg.Endpoint != nil {
		t.Fatalf("no acquisition may happen without the endpoint capability: %+v", agg)
	}
}

// TestLaunchRecoveryDeadRecordedEndpointFailsClosedNoReplacement proves a
// recorded endpoint that cannot prove liveness on recovery fails closed with
// NO replacement: recovery never silently creates a second endpoint.
func TestLaunchRecoveryDeadRecordedEndpointFailsClosedNoReplacement(t *testing.T) {
	f := newLaunchFixture(t, "recover-deadep")
	if err := runLaunchPhases(f, "attach-endpoint"); !errors.Is(err, errCrashSimulated) {
		t.Fatalf("first run: %v", err)
	}
	// The recorded endpoint is dead: recovery must fail closed (no
	// replacement) instead of creating a second endpoint.
	f.endpoints.probeAlive = false
	if err := runLaunchPhases(f, ""); err == nil {
		t.Fatal("dead recorded endpoint must fail closed on recovery")
	} else if !strings.Contains(err.Error(), "no replacement") {
		t.Fatalf("error = %v, want no-replacement fail-closed", err)
	}
	if f.endpoints.createCount() != 1 {
		t.Fatalf("endpoint creates = %d, want 1 (no replacement)", f.endpoints.createCount())
	}
}

// TestLaunchWorktreeTreehouseFirstAttemptConsumesReservation proves the FIRST
// worktree acquisition consumes the durable launch reservation identity: the
// treehouse provider passes it as the lease holder (--lease-holder), never an
// unreserved get.
func TestLaunchWorktreeTreehouseFirstAttemptConsumesReservation(t *testing.T) {
	f := newLaunchFixture(t, "wt-treehouse")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "treehouse-args.log")
	fakeTreehouseOnPath(t, logPath)
	if err := f.runner.acquireWorktree(); err != nil {
		t.Fatalf("acquireWorktree: %v", err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logData)
	if !strings.Contains(log, "--lease-holder "+f.runner.wtReservationID()) {
		t.Fatalf("treehouse get did not consume the launch reservation as lease holder: %q", log)
	}
	if !strings.Contains(log, "--lease") {
		t.Fatalf("treehouse get must lease the reservation-owned worktree: %q", log)
	}
}

// TestLaunchWorktreeTreehouseRecoveryFailsClosed proves the treehouse
// provider fails closed on recovery of an acquired-but-unbound reservation
// instead of allocating a replacement (the CLI has no reservation-keyed
// get/recover — DEPENDENCY_REQUEST evidence).
func TestLaunchWorktreeTreehouseRecoveryFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "wt-treehouse-recover")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	// Re-entry (intent pre-existed) is detected by a second begin.
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("re-entry beginLaunchIntent: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "treehouse-args.log")
	fakeTreehouseOnPath(t, logPath)
	if err := f.runner.acquireWorktree(); err == nil {
		t.Fatal("treehouse recovery must fail closed, got nil")
	} else if !strings.Contains(err.Error(), "DEPENDENCY_REQUEST") {
		t.Fatalf("error = %v, want DEPENDENCY_REQUEST fail-closed", err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "get") {
		t.Fatalf("treehouse recovery must never allocate: %q", string(logData))
	}
}

// fakeTreehouseOnPath installs a fake treehouse CLI that appends its arguments
// to logPath and returns an existing directory as the acquired worktree, and
// prepends it to PATH for the duration of the test.
//
// Without it the provider finds no treehouse on windows, falls back to real
// `git worktree`, and the test measures git rather than the fake it installed
// (#549 group 8); testutil.FakeOnPath owns why a shell script needs a companion
// to be visible there. PATH is prepended to rather than replaced because the
// fallback this test is trying not to reach still needs to find git.
func fakeTreehouseOnPath(t *testing.T, logPath string) {
	t.Helper()
	wt := t.TempDir()
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nmkdir -p %q\nprintf '%%s\\n' %q\n", logPath, wt, wt)
	testutil.FakeOnPath(t, "treehouse", content)
}

// TestLaunchArtifactGuardProvesSingleProcessLaunches runs the REAL production
// artifact twice through a real shell with a real (fake) harness binary and
// proves the persistent guard allows exactly ONE Soldier process launch; a
// guard identity mismatch fails closed with no launch.
func TestLaunchArtifactGuardProvesSingleProcessLaunches(t *testing.T) {
	f := newLaunchFixture(t, "artifact-guard")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := f.runner.acquireWorktree(); err != nil {
		t.Fatalf("acquireWorktree: %v", err)
	}
	bound, err := f.runner.bindWorktree()
	if err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	if err := f.runner.buildSoldierPrompt(bound); err != nil {
		t.Fatalf("buildSoldierPrompt: %v", err)
	}
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)

	agg := f.aggregate()
	artifact, err := buildLaunchArtifact(LaunchArtifactInput{
		WorktreePath:   f.runner.wtPath,
		LaunchDir:      f.runner.wtPath,
		HomeDir:        f.homeDir,
		TaskID:         f.taskID,
		SnapshotDigest: f.runner.projectConfig.SnapshotDigest,
		LaunchBin:      f.runner.launchBin,
		LaunchArgs:     f.runner.launchArgs,
		LaunchID:       f.runner.launchID,
		Generation:     agg.Generation.String(),
		EndpointFence:  f.runner.epFenceToken(),
	})
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	// The script must create the persistent guard BEFORE execing the harness.
	script, err := os.ReadFile(artifact.ScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), artifact.GuardName) || !strings.Contains(string(script), "exec ") {
		t.Fatalf("production artifact missing the persistent guard:\n%s", script)
	}
	// First submission: the guard is created and the harness execs exactly once.
	if err := exec.Command(testutil.BashShell(t), "-c", artifact.Command).Run(); err != nil {
		t.Fatalf("first artifact run: %v", err)
	}
	if n := harnessLaunchCount(t, counter); n != 1 {
		t.Fatalf("harness launches after first run = %d, want 1", n)
	}
	// Re-submission of the SAME launch: the guard no-ops, no second process.
	if err := exec.Command(testutil.BashShell(t), "-c", artifact.Command).Run(); err != nil {
		t.Fatalf("guarded re-run: %v", err)
	}
	if n := harnessLaunchCount(t, counter); n != 1 {
		t.Fatalf("harness launches across re-submission = %d, want exactly 1", n)
	}

	// A different identity/fence on the SAME guard fails closed with no launch.
	guardPath := filepath.Join(f.runner.wtPath, artifact.GuardName)
	if err := os.MkdirAll(guardPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(guardPath, "identity"), []byte("launch-other|1|otherfence"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(testutil.BashShell(t), "-c", artifact.Command).Run(); err == nil {
		t.Fatal("different identity/fence on the guard must fail closed")
	}
	if n := harnessLaunchCount(t, counter); n != 1 {
		t.Fatalf("identity mismatch launched a second process: count=%d", n)
	}
}

// TestLaunchArtifactGuardExistsSkipsProcessOnReEntry proves the guard-exists
// case: when the guard marker already exists with the exact identity (a prior
// submission created it), re-running the production artifact exits without
// launching — recovery never starts a second process from a guarded launch.
func TestLaunchArtifactGuardExistsSkipsProcessOnReEntry(t *testing.T) {
	f := newLaunchFixture(t, "artifact-guard-exists")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := f.runner.acquireWorktree(); err != nil {
		t.Fatalf("acquireWorktree: %v", err)
	}
	bound, err := f.runner.bindWorktree()
	if err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	if err := f.runner.buildSoldierPrompt(bound); err != nil {
		t.Fatalf("buildSoldierPrompt: %v", err)
	}
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)

	agg := f.aggregate()
	artifact, err := buildLaunchArtifact(LaunchArtifactInput{
		WorktreePath:   f.runner.wtPath,
		LaunchDir:      f.runner.wtPath,
		HomeDir:        f.homeDir,
		TaskID:         f.taskID,
		SnapshotDigest: f.runner.projectConfig.SnapshotDigest,
		LaunchBin:      f.runner.launchBin,
		LaunchArgs:     f.runner.launchArgs,
		LaunchID:       f.runner.launchID,
		Generation:     agg.Generation.String(),
		EndpointFence:  f.runner.epFenceToken(),
	})
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	// The guard already exists with the exact identity and the process is not
	// provably ready: re-running must NOT launch another process.
	guardPath := filepath.Join(f.runner.wtPath, artifact.GuardName)
	if err := os.MkdirAll(guardPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(guardPath, "identity"), []byte(artifact.GuardIdentity), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(testutil.BashShell(t), "-c", artifact.Command).Run(); err != nil {
		t.Fatalf("guarded re-entry run: %v", err)
	}
	if n := harnessLaunchCount(t, counter); n != 0 {
		t.Fatalf("guarded re-entry launched a process: count=%d, want 0 (fail closed via readiness, never a second process)", n)
	}
}

// TestLaunchArtifactGuardConcurrentSubmissionsSingleProcess proves the atomic
// guard under the concurrent edge case: two SIMULTANEOUS submissions of the
// same launch produce exactly ONE Soldier process launch (the mkdir guard is
// atomic — the loser exits and never execs the harness).
func TestLaunchArtifactGuardConcurrentSubmissionsSingleProcess(t *testing.T) {
	f := newLaunchFixture(t, "artifact-guard-concurrent")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := f.runner.acquireWorktree(); err != nil {
		t.Fatalf("acquireWorktree: %v", err)
	}
	bound, err := f.runner.bindWorktree()
	if err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	if err := f.runner.buildSoldierPrompt(bound); err != nil {
		t.Fatalf("buildSoldierPrompt: %v", err)
	}
	counter := filepath.Join(t.TempDir(), "harness-launches.log")
	harnessDir := fakeHarnessDir(t, "pi", counter)
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git on PATH: %v", err)
	}
	testutil.SetPath(t, append([]string{harnessDir, filepath.Dir(gitBin), requiredSkillStubDir(t)}, testutil.BashShellDirs(t)...)...)
	agg := f.aggregate()
	artifact, err := buildLaunchArtifact(LaunchArtifactInput{
		WorktreePath:   f.runner.wtPath,
		LaunchDir:      f.runner.wtPath,
		HomeDir:        f.homeDir,
		TaskID:         f.taskID,
		SnapshotDigest: f.runner.projectConfig.SnapshotDigest,
		LaunchBin:      f.runner.launchBin,
		LaunchArgs:     f.runner.launchArgs,
		LaunchID:       f.runner.launchID,
		Generation:     agg.Generation.String(),
		EndpointFence:  f.runner.epFenceToken(),
	})
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	// BashShell reports through t, which is only legal on the test goroutine.
	shell := testutil.BashShell(t)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = exec.Command(shell, "-c", artifact.Command).Run()
		}()
	}
	wg.Wait()
	if n := harnessLaunchCount(t, counter); n != 1 {
		t.Fatalf("concurrent submissions launched %d processes, want exactly 1", n)
	}
}

// fakeHarnessDir creates a fake harness binary (binName) that appends a
// marker line to counterPath, so tests can count actual Soldier process
// launches of the real production artifact.
func fakeHarnessDir(t *testing.T, binName, counterPath string) string {
	t.Helper()
	dir := t.TempDir()
	content := fmt.Sprintf("#!/bin/sh\necho launch >> %q\n", counterPath)
	testutil.WriteFakeExecutable(t, filepath.Join(dir, binName), content)
	return dir
}

// harnessLaunchCount returns the number of lines in the harness counter file.
func harnessLaunchCount(t *testing.T, counterPath string) int {
	t.Helper()
	data, err := os.ReadFile(counterPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("reading harness counter: %v", err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// executingEndpointCapabilities is an EndpointCapabilities whose Submit
// executes the submitted launch command through a real shell (the pane) and
// optionally reports a delivery failure (crash) on the first submission.
// skipFirstRun=true simulates a Submit error BEFORE script execution; the
// default (run every submission) simulates a crash AFTER delivery.
type executingEndpointCapabilities struct {
	inner             *reentrantEndpointCapabilities
	runCommand        func(cmd string) error
	firstSubmitErr    bool
	skipFirstRun      bool
	delayUntilCapture int
	pendingCommand    string
	captures          int
	disposes          int
	afterExecution    func() error
	submits           int
}

func (f *executingEndpointCapabilities) CreateReserved(req CreateRequest) (CreatedEndpoint, error) {
	return f.inner.CreateReserved(req)
}
func (f *executingEndpointCapabilities) Submit(ep CreatedEndpoint, text string) error {
	f.submits++
	run := !(f.skipFirstRun && f.submits == 1)
	if run && f.delayUntilCapture == 0 && f.runCommand != nil {
		if err := f.runCommand(text); err != nil {
			return err
		}
		if f.afterExecution != nil {
			if err := f.afterExecution(); err != nil {
				return err
			}
		}
	} else if run {
		f.pendingCommand = text
	}
	if f.firstSubmitErr && f.submits == 1 {
		return errors.New("simulated crash after submission delivery")
	}
	if f.inner.onSubmit != nil {
		f.inner.onSubmit()
	}
	return nil
}
func (f *executingEndpointCapabilities) Probe(ep CreatedEndpoint) (SpawnEndpointObservation, error) {
	return f.inner.Probe(ep)
}
func (f *executingEndpointCapabilities) Capture(ep CreatedEndpoint, n int) (string, error) {
	f.captures++
	if f.pendingCommand != "" && f.captures >= f.delayUntilCapture {
		if f.runCommand != nil {
			if err := f.runCommand(f.pendingCommand); err != nil {
				return "", err
			}
		}
		f.pendingCommand = ""
		if f.afterExecution != nil {
			if err := f.afterExecution(); err != nil {
				return "", err
			}
		}
	}
	return f.inner.Capture(ep, n)
}
func (f *executingEndpointCapabilities) Dispose(ep CreatedEndpoint) error {
	f.disposes++
	return f.inner.Dispose(ep)
}
func (f *executingEndpointCapabilities) submitCount() int { return f.submits }

// scriptRunCommand returns a pane executor that runs the submitted command
// exactly as the pane shell would (sh -c "bash '<script>'").
func scriptRunCommand(t *testing.T) func(cmd string) error {
	t.Helper()
	shell := testutil.BashShell(t)
	return func(cmd string) error {
		return exec.Command(shell, "-c", cmd).Run()
	}
}

// TestLaunchArtifactReentrantGuardRealPath exercises the REAL production
// launch artifact path: buildLaunchArtifact produces the identical artifact
// (same command digest) for the identical immutable inputs, and an artifact
// whose content differs fails closed (identity mismatch, never overwritten).
func TestLaunchArtifactReentrantGuardRealPath(t *testing.T) {
	f := newLaunchFixture(t, "artifact-real")
	if err := f.runner.beginLaunchIntent(); err != nil {
		t.Fatalf("beginLaunchIntent: %v", err)
	}
	if err := f.runner.acquireWorktree(); err != nil {
		t.Fatalf("acquireWorktree: %v", err)
	}
	bound, err := f.runner.bindWorktree()
	if err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}
	if err := f.runner.buildSoldierPrompt(bound); err != nil {
		t.Fatalf("buildSoldierPrompt: %v", err)
	}
	agg := f.aggregate()
	in := LaunchArtifactInput{
		WorktreePath:   f.runner.wtPath,
		LaunchDir:      f.runner.wtPath,
		HomeDir:        f.homeDir,
		TaskID:         f.taskID,
		SnapshotDigest: f.runner.projectConfig.SnapshotDigest,
		LaunchBin:      f.runner.launchBin,
		LaunchArgs:     f.runner.launchArgs,
		LaunchID:       f.runner.launchID,
		Generation:     agg.Generation.String(),
		EndpointFence:  f.runner.epFenceToken(),
	}
	first, err := buildLaunchArtifact(in)
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	// Identical immutable inputs -> identical artifact and digest.
	second, err := buildLaunchArtifact(in)
	if err != nil {
		t.Fatalf("second buildLaunchArtifact: %v", err)
	}
	if first.Command != second.Command || first.CommandDigest != second.CommandDigest {
		t.Fatalf("artifact not deterministic: %+v vs %+v", first, second)
	}
	if !domain.IsSHA256(first.CommandDigest) {
		t.Fatalf("command digest not a sha256: %q", first.CommandDigest)
	}

	// Different immutable inputs (a different prompt) -> the existing artifact
	// fails closed instead of being overwritten: identity mismatch.
	diffIn := in
	diffIn.LaunchArgs = append(append([]string{}, f.runner.launchArgs...), "DIFFERENT PROMPT CONTENT")
	if _, err := buildLaunchArtifact(diffIn); err == nil {
		t.Fatal("changed artifact identity must fail closed")
	} else if !strings.Contains(err.Error(), "different content") {
		t.Fatalf("error = %v, want artifact identity mismatch", err)
	}
}

// TestLaunchRecoveryDuplicateLiveTaskFailsClosed proves a duplicate spawn of
// an already-working task fails closed: the committed launch no longer
// matches a re-derived intent (changed snapshot digest) and no second
// endpoint or working record is ever created.
func TestLaunchRecoveryDuplicateLiveTaskFailsClosed(t *testing.T) {
	f := newLaunchFixture(t, "recover-duplicate")
	if err := runLaunchPhases(f, ""); err != nil {
		t.Fatalf("full run: %v", err)
	}
	before := f.aggregate()
	// A NEW spawn invocation derives a different intent (the operator's
	// snapshot/config changed): the live launch identity cannot match.
	f.runner.projectConfig.SnapshotDigest = strings.Repeat("d", 64)
	if err := f.runner.beginLaunchIntent(); err == nil {
		t.Fatal("duplicate live spawn must fail closed")
	}
	after := f.aggregate()
	if after.Revision != before.Revision {
		t.Fatalf("duplicate spawn mutated the aggregate: rev %d -> %d", before.Revision, after.Revision)
	}
	if after.Endpoint == nil || after.Endpoint.Handle != before.Endpoint.Handle {
		t.Fatalf("duplicate spawn must not create a second endpoint record")
	}
	if f.endpoints.createCount() != 1 {
		t.Fatalf("duplicate spawn created %d endpoints", f.endpoints.createCount())
	}
}

// A committed RecordLaunch whose launch evidence later disappears is lost proof,
// not a retryable cause. The first Run keeps the live pane owned and refuses with
// a typed conflict that offers no re-run. A retry refuses before submitting
// again, and neither attempt creates, disposes, resubmits or backfills evidence.
func TestLaunchRecoveryMissingLaunchProofRefusesRetryWithoutRepair(t *testing.T) {
	rf := newRunFixture(t, "missing-launch-proof")
	f := rf.launchFixture
	probes := 0
	f.endpoints.onProbe = func() error {
		if probes++; probes == 3 {
			tamperTaskAggregate(f.t, f.homeDir, f.taskID, func(agg *taskauthority.Aggregate) { agg.LaunchEvidence = nil })
		}
		return nil
	}

	_, err := rf.run()
	if !errors.Is(err, taskauthority.ErrConflict) || !strings.Contains(err.Error(), "successful launch lacks canonical worktree manifest evidence") {
		t.Fatalf("first Run = %v, want a typed conflict naming the missing launch proof", err)
	}
	msg := err.Error()
	for _, want := range []string{"missing-launch-proof", "backend tmux endpoint pane-1", "may still be running", "stop pane pane-1 on backend tmux by hand"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain %q", msg, want)
		}
	}
	for _, unwanted := range []string{"fix the cause", "re-run"} {
		if strings.Contains(msg, unwanted) {
			t.Fatalf("error %q advises %q, which cannot repair lost launch proof", msg, unwanted)
		}
	}
	agg := f.aggregate()
	if agg.LaunchEvidence != nil || agg.AcquiredEndpoint == nil || agg.AcquiredEndpoint.Handle != "pane-1" || agg.Endpoint != nil || agg.Phase != taskauthority.PhaseQueued {
		t.Fatalf("first Run did not retain pre-confirm endpoint custody: %+v", agg)
	}
	revision := agg.Revision

	f.endpoints.onProbe = nil
	_, err = rf.run()
	if !errors.Is(err, taskauthority.ErrConflict) || !strings.Contains(err.Error(), "already submitted") || !strings.Contains(err.Error(), "stop pane pane-1 on backend tmux by hand") {
		t.Fatalf("retry = %v, want a typed conflict naming the submitted launch and its custody", err)
	}
	if after := f.aggregate(); after.Revision != revision || after.LaunchEvidence != nil || after.Endpoint != nil || after.AcquiredEndpoint == nil || after.AcquiredEndpoint.Handle != "pane-1" {
		t.Fatalf("refused retry changed custody or revision: %+v", after)
	}
	if n := f.endpoints.createCount(); n != 1 {
		t.Fatalf("endpoint creations = %d, want 1", n)
	}
	if n := rf.exec.submitCount(); n != 1 {
		t.Fatalf("launch submissions = %d, want 1", n)
	}
	if rf.exec.disposes != 0 {
		t.Fatalf("live endpoint disposed %d times, want 0", rf.exec.disposes)
	}
	if n := harnessLaunchCount(t, rf.counter); n != 1 {
		t.Fatalf("soldier launches = %d, want 1", n)
	}
	status, probeErr := f.endpoints.Probe(CreatedEndpoint{Backend: agg.AcquiredEndpoint.Backend, Handle: agg.AcquiredEndpoint.Handle})
	if probeErr != nil || status.Lifecycle != LifecycleAlive || status.Responsiveness != Responsive {
		t.Fatalf("retained endpoint status=%+v probe error=%v, want alive and responsive", status, probeErr)
	}
}
