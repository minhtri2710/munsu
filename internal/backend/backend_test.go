package backend_test

import (
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestFakeSessionBackend(t *testing.T) {
	fake := testutil.NewFakeSessionBackend()
	winID, err := fake.NewWindow("default", "test-worker")
	if err != nil {
		t.Fatalf("NewWindow failed: %v", err)
	}

	if !fake.Alive(winID) {
		t.Error("expected window to be alive")
	}

	if err := fake.SendKeys(winID, "ls -la\n"); err != nil {
		t.Fatalf("SendKeys failed: %v", err)
	}

	if err := fake.Teardown(winID); err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}
	if fake.Alive(winID) {
		t.Error("expected window to be dead after teardown")
	}
}

func TestResolveExplicitIdentity(t *testing.T) {
	home := testutil.TempHome(t)
	testutil.ClearEnv(t)

	bk, name, err := backend.Resolve(home, "tmux")
	if err != nil {
		t.Fatalf("Resolve tmux failed: %v", err)
	}
	if bk == nil || name != "tmux" {
		t.Errorf("expected name=tmux, got name=%s", name)
	}
}

func TestResolveEmptyIdentityFailsClosed(t *testing.T) {
	home := testutil.TempHome(t)
	testutil.ClearEnv(t)

	bk, name, err := backend.Resolve(home, "")
	if err == nil {
		t.Fatalf("expected typed failure for empty requested identity, got %q (%T) — no auto-detect", name, bk)
	}
}

func TestHometag(t *testing.T) {
	home := testutil.TempHome(t)
	tag := backend.Hometag(home)
	if tag == "" {
		t.Error("expected non-empty hometag")
	}

	wsTag := backend.WorkspaceTag(home)
	if wsTag == "" {
		t.Error("expected non-empty workspace tag")
	}
}
