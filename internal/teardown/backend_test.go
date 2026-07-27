package teardown

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeTeardown struct {
	probeErr, disposeErr error
	alive                bool
}

func (f fakeTeardown) Probe(string, map[string]string) (EndpointStatus, error) {
	return EndpointStatus{Alive: f.alive}, f.probeErr
}
func (f fakeTeardown) Dispose(string, map[string]string, DisposeRequest) error { return f.disposeErr }
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
	if _, err := RunWithBackend(opts, fakeTeardown{probeErr: errors.New("probe failed")}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf("meta removed: %v", err)
	}
}
func TestRunWithBackendPreservesMetaOnDisposeError(t *testing.T) {
	opts, meta := teardownFixture(t)
	if _, err := RunWithBackend(opts, fakeTeardown{alive: true, disposeErr: errors.New("dispose failed")}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf("meta removed: %v", err)
	}
}
