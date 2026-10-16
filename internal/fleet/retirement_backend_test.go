//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type fakeTeardown struct {
	probeErr, disposeErr error
	returnWorktreeErr    error
	returnWorktreeFn     func(worktreePath string) error
	alive                bool
	mergeStatus          *domain.PRMergeStatus
}

func (f fakeTeardown) Probe(string, taskauthority.EndpointBinding, string) (RetirementEndpointStatus, error) {
	lifecycle := LifecycleDead
	if f.alive {
		lifecycle = LifecycleAlive
	}
	return RetirementEndpointStatus{Lifecycle: lifecycle, Responsiveness: Responsive, Freshness: FreshnessCurrent, Activity: ActivityUnknown, Source: SourceProbe}, f.probeErr
}
func (f fakeTeardown) Dispose(DisposeRequest) error { return f.disposeErr }
func (f fakeTeardown) QueryMergeStatus(forge taskauthority.DeliveryStep, ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	if f.mergeStatus != nil {
		return f.mergeStatus, nil
	}
	return QueryDeliveryMergeStatus(forge, ident)
}
func (f fakeTeardown) ReturnWorktree(_, _, worktreePath string) error {
	if f.returnWorktreeFn != nil {
		return f.returnWorktreeFn(worktreePath)
	}
	return f.returnWorktreeErr
}
func teardownFixture(t *testing.T) Options {
	t.Helper()
	h := t.TempDir()
	if _, err := mhome.Init(h); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	return Options{HomeDir: h, ID: "task", Force: true}
}
func TestRunWithBackendFailsOnProbeError(t *testing.T) {
	opts := teardownFixture(t)
	if _, err := RetireTask(opts, fakeTeardown{probeErr: errors.New("probe failed")}, fakeRetirementJournals{}, mergeTestAuth(t, opts.HomeDir, "task")); err == nil || !strings.Contains(err.Error(), "probe failed") {
		t.Fatalf("error = %v, want the probe failure", err)
	}
}
func TestRunWithBackendFailsOnDisposeError(t *testing.T) {
	opts := teardownFixture(t)
	if _, err := RetireTask(opts, fakeTeardown{alive: true, disposeErr: errors.New("dispose failed")}, fakeRetirementJournals{}, mergeTestAuth(t, opts.HomeDir, "task")); err == nil || !strings.Contains(err.Error(), "dispose failed") {
		t.Fatalf("error = %v, want the dispose failure", err)
	}
}

func TestRunWithBackendReturnsWorktreeViaCapability(t *testing.T) {
	opts := teardownFixture(t)

	// mergeTestAuth binds this existing directory, so the return path runs.
	wtDir := filepath.Join(opts.HomeDir, "worktree")
	os.MkdirAll(wtDir, 0755)
	opts2 := opts

	var calls int
	var gotPath string
	fake := fakeTeardown{returnWorktreeFn: func(p string) error { calls++; gotPath = p; return nil }}
	res, err := RetireTask(opts2, fake, fakeRetirementJournals{}, mergeTestAuth(t, opts.HomeDir, "task"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("returnWorktree calls=%d, want 1", calls)
	}
	if gotPath != wtDir {
		t.Fatalf("returnWorktree path=%q want %q", gotPath, wtDir)
	}
	found := false
	for _, s := range res.Steps {
		if s == "worktree returned to pool" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing 'worktree returned to pool' step")
	}
}

func TestRunWithBackendWorktreeFailurePreventsCleanup(t *testing.T) {
	opts := teardownFixture(t)

	wtDir := filepath.Join(opts.HomeDir, "worktree")
	os.MkdirAll(wtDir, 0755)

	var calls int
	fake := fakeTeardown{returnWorktreeFn: func(string) error { calls++; return errors.New("pool full") }}
	_, err := RetireTask(opts, fake, fakeRetirementJournals{}, mergeTestAuth(t, opts.HomeDir, "task"))
	if err == nil || !strings.Contains(err.Error(), "worktree return failed") || !strings.Contains(err.Error(), "lease still held") {
		t.Fatalf("error=%v, want worktree return failure with lease held", err)
	}
	if calls != 1 {
		t.Fatalf("returnWorktree calls=%d, want 1", calls)
	}
}

type fakeRetirementJournals struct{}

func (fakeRetirementJournals) VerifyRetirementContinuity(string, string, string) error { return nil }
func (fakeRetirementJournals) PrepareForcedRetirementEvidence(string, string) ([]string, error) {
	return nil, nil
}
func (fakeRetirementJournals) FinalizeRetirementJournals(string, string) ([]string, error) {
	return nil, nil
}

func TestRetireTaskReportsSurvivingTaskProcessWithoutFailingTeardown(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth func(t *testing.T, homeDir string) *taskauthority.Canonical
	}{
		{"with worktree evidence", func(t *testing.T, homeDir string) *taskauthority.Canonical { return mergeTestAuth(t, homeDir, "task") }},
		{"without worktree evidence", func(t *testing.T, homeDir string) *taskauthority.Canonical {
			auth := canonicalMergeTestAuth(t, homeDir, "task")
			seedWorktreeEvidence(t, auth, "task", t.TempDir(), "lease-wt", "fence-wt")
			return auth
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := teardownFixture(t)
			useTaskProcessFakes(t, fakeMarkerInventory{scan: MarkerScan{Marked: []MarkedProcess{markedTaskProcess(800, "task", opts.HomeDir)}}}, func([]int) map[int]processDetail { return nil })
			res, err := RetireTask(opts, fakeTeardown{}, fakeRetirementJournals{}, tc.auth(t, opts.HomeDir))
			if err != nil {
				t.Fatalf("teardown failed: %v", err)
			}
			if want := "task process 800 still running: /usr/bin/node (cwd unknown, listening unknown)"; !strings.Contains(strings.Join(res.Steps, "\n"), want) {
				t.Fatalf("steps = %q, want one containing %q", res.Steps, want)
			}
		})
	}
}
