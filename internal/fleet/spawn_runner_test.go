package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

func TestReturnWorktreeOnFailure_PreservesBoundWorktreeOnAggregateReadError(t *testing.T) {
	homeDir := t.TempDir()
	h, err := home.Init(homeDir)
	if err != nil {
		t.Fatalf("home.Init: %v", err)
	}
	authority, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	taskDir := filepath.Join(h.Root(), "state", "task-authority", "tasks", "bound-task")
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		t.Fatalf("MkdirAll task state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "current.json"), []byte("{ malformed"), 0600); err != nil {
		t.Fatalf("WriteFile task state: %v", err)
	}

	called := false
	original := returnWorktree
	t.Cleanup(func() { returnWorktree = original })
	returnWorktree = func(string, string) error {
		called = true
		return nil
	}

	r := &Runner{args: Args{ID: "bound-task", Authority: authority}, homeDir: h.Root(), wtPath: filepath.Join(t.TempDir(), "bound")}
	if err := r.returnWorktreeOnFailure(); err != nil {
		t.Fatalf("returnWorktreeOnFailure: %v", err)
	}
	if called {
		t.Fatal("worktree was returned after the aggregate read failed")
	}
}

func TestReturnWorktreeOnFailureSurfacesReturnError(t *testing.T) {
	want := errors.New("return failed")
	original := returnWorktree
	t.Cleanup(func() { returnWorktree = original })
	returnWorktree = func(string, string) error { return want }

	r := &Runner{homeDir: t.TempDir(), wtPath: filepath.Join(t.TempDir(), "bound")}
	err := r.returnWorktreeOnFailure()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped return error %v", err, want)
	}
}

// TestResolveEffectiveIdentity_SnapshotOnlyFailClosed verifies the soldier
// operation harness resolution consumes the snapshot via
// ResolveSoldierFromSnapshot and fails closed when no typed snapshot carries a
// soldier harness identity. There is no flat-file read and no Detect().
func TestResolveEffectiveIdentity_SnapshotOnlyFailClosed(t *testing.T) {
	t.Run("no typed snapshot and no snapshot harness fails closed", func(t *testing.T) {
		homeDir := t.TempDir() // no fleet base, no published snapshot

		r := NewRunner(Args{ID: "task-x", HomeDir: homeDir})
		r.homeDir = homeDir

		err := r.resolveEffectiveIdentity()
		if err == nil {
			t.Fatal("expected fail-closed error without a snapshot soldier harness")
		}
		if !errors.Is(err, harness.ErrNoSoldierHarnessInSnapshot) {
			t.Fatalf("error = %v, want ErrNoSoldierHarnessInSnapshot", err)
		}
	})

	t.Run("legacy flat-file soldier-harness ignored without typed snapshot", func(t *testing.T) {
		homeDir := t.TempDir()
		// Legacy flat-file config/soldier-harness is present, but no typed
		// snapshot surface exists. The fallback tail must not read it.
		configDir := filepath.Join(homeDir, "config")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "soldier-harness"), []byte("pi\n"), 0644); err != nil {
			t.Fatal(err)
		}

		r := NewRunner(Args{ID: "task-y", HomeDir: homeDir})
		r.homeDir = homeDir

		err := r.resolveEffectiveIdentity()
		if err == nil {
			t.Fatal("expected fail-closed error despite legacy flat-file soldier-harness")
		}
		if !errors.Is(err, harness.ErrNoSoldierHarnessInSnapshot) {
			t.Fatalf("error = %v, want ErrNoSoldierHarnessInSnapshot (flat-file must not be read)", err)
		}
	})
}

// TestResolveHarness_SnapshotOnlyFailClosed verifies the resolveHarness fallback
// tail is snapshot-only and fails closed (ErrNoSoldierHarnessInSnapshot) when no
// typed snapshot identity is available.
func TestResolveHarness_SnapshotOnlyFailClosed(t *testing.T) {
	homeDir := t.TempDir() // no typed snapshot

	r := NewRunner(Args{ID: "task-z", HomeDir: homeDir})
	r.homeDir = homeDir

	err := r.resolveHarness()
	if err == nil {
		t.Fatal("expected fail-closed error without a snapshot soldier harness")
	}
	if !errors.Is(err, harness.ErrNoSoldierHarnessInSnapshot) {
		t.Fatalf("error = %v, want ErrNoSoldierHarnessInSnapshot", err)
	}
}
