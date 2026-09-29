package backend_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestResolveExplicitIdentity(t *testing.T) {
	testutil.TempHome(t)
	testutil.ClearEnv(t)

	// Controlled PATH: the requested binary must be verifiably present, and
	// no real tmux install is required (Resolve verifies the capability).
	fakeBin := t.TempDir()
	testutil.WriteFakeExecutable(t, filepath.Join(fakeBin, "tmux"), "#!/bin/sh\nexit 0")
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+oldPath)

	bk, name, err := backend.Resolve("tmux")
	if err != nil {
		t.Fatalf("Resolve tmux failed: %v", err)
	}
	if bk == nil || name != "tmux" {
		t.Errorf("expected name=tmux, got name=%s", name)
	}
}

func TestResolveEmptyIdentityFailsClosed(t *testing.T) {
	testutil.TempHome(t)
	testutil.ClearEnv(t)

	bk, name, err := backend.Resolve("")
	if err == nil {
		t.Fatalf("expected typed failure for empty requested identity, got %q (%T) — no auto-detect", name, bk)
	}
}
