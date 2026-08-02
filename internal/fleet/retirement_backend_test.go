//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
)

type fakeTeardown struct {
	probeErr, disposeErr error
	returnWorktreeErr    error
	returnWorktreeFn     func(worktreePath string) error
	alive                bool
}

func (f fakeTeardown) RefuseGate() error { return nil }
func (f fakeTeardown) Probe(string, map[string]string) (RetirementEndpointStatus, error) {
	return RetirementEndpointStatus{Alive: f.alive}, f.probeErr
}
func (f fakeTeardown) Dispose(string, map[string]string, DisposeRequest) error { return f.disposeErr }
func (f fakeTeardown) QueryMergeStatus(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
	return QueryDeliveryMergeStatus(ident)
}
func (f fakeTeardown) ReturnWorktree(_, worktreePath string) error {
	if f.returnWorktreeFn != nil {
		return f.returnWorktreeFn(worktreePath)
	}
	return f.returnWorktreeErr
}
func teardownFixture(t *testing.T) (Options, string) {
	t.Helper()
	h := t.TempDir()
	state := filepath.Join(h, "state")
	os.MkdirAll(state, 0700)
	meta := filepath.Join(state, "task.meta")
	os.WriteFile(meta, []byte("kind=ship\nbackend=tmux\nwindow=pane-1\n"), 0600)
	return Options{HomeDir: h, ID: "task", Force: true}, meta
}
func TestRunWithBackendPreservesMetaOnProbeError(t *testing.T) {
	opts, meta := teardownFixture(t)
	if _, err := RetireTask(opts, fakeTeardown{probeErr: errors.New("probe failed")}, fakeRetirementJournals{}, mergeTestAuth(t, "task")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf("meta removed: %v", err)
	}
}
func TestRunWithBackendPreservesMetaOnDisposeError(t *testing.T) {
	opts, meta := teardownFixture(t)
	if _, err := RetireTask(opts, fakeTeardown{alive: true, disposeErr: errors.New("dispose failed")}, fakeRetirementJournals{}, mergeTestAuth(t, "task")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf("meta removed: %v", err)
	}
}

func TestRunWithBackendReturnsWorktreeViaCapability(t *testing.T) {
	opts, meta := teardownFixture(t)

	// Add a worktree path that exists as a directory so the return path runs.
	wtDir := filepath.Join(opts.HomeDir, "worktree")
	os.MkdirAll(wtDir, 0755)
	opts2 := opts
	os.WriteFile(meta, []byte("kind=ship\nbackend=tmux\nwindow=pane-1\nworktree="+wtDir+"\n"), 0600)

	var calls int
	var gotPath string
	fake := fakeTeardown{returnWorktreeFn: func(p string) error { calls++; gotPath = p; return nil }}
	res, err := RetireTask(opts2, fake, fakeRetirementJournals{}, mergeTestAuth(t, "task"))
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
	opts, meta := teardownFixture(t)

	wtDir := filepath.Join(opts.HomeDir, "worktree")
	os.MkdirAll(wtDir, 0755)
	os.WriteFile(meta, []byte("kind=ship\nbackend=tmux\nwindow=pane-1\nworktree="+wtDir+"\n"), 0600)

	var calls int
	fake := fakeTeardown{returnWorktreeFn: func(string) error { calls++; return errors.New("pool full") }}
	_, err := RetireTask(opts, fake, fakeRetirementJournals{}, mergeTestAuth(t, "task"))
	if err == nil || !strings.Contains(err.Error(), "worktree return failed") || !strings.Contains(err.Error(), "lease still held") {
		t.Fatalf("error=%v, want worktree return failure with lease held", err)
	}
	if calls != 1 {
		t.Fatalf("returnWorktree calls=%d, want 1", calls)
	}
	// Meta must survive because teardown aborted before cleanup.
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf("meta removed on worktree failure: %v", err)
	}
}

type fakeRetirementJournals struct{}

func (fakeRetirementJournals) VerifyRetirementContinuity(string, string) error { return nil }
func (fakeRetirementJournals) PrepareForcedRetirementEvidence(string, string) ([]string, error) {
	return nil, nil
}
func (fakeRetirementJournals) FinalizeRetirementJournals(string, string) ([]string, error) {
	return nil, nil
}
