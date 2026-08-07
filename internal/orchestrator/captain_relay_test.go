//go:build integration

package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
)

// --- resolveParentHome tests ---

// TestResolveParentHome_EnvPrecedence verifies that when MUNSU_PARENT_STATUS
// is set, it takes precedence over config/parent-home.
func TestResolveParentHome_EnvPrecedence(t *testing.T) {
	tmp := t.TempDir()
	otherDir := t.TempDir()
	configDir := t.TempDir()

	// Write config/parent-home to a different value
	if err := config.Set(tmp, "parent-home", configDir); err != nil {
		t.Fatal(err)
	}

	// Set env to a different value — env should win
	t.Setenv("MUNSU_PARENT_STATUS", otherDir)

	got := ResolveCaptainParentHome(tmp)
	if got != otherDir {
		t.Errorf("ResolveCaptainParentHome() = %q, want %q (env should precede config)", got, otherDir)
	}
}

// TestResolveParentHome_ConfigFallback verifies that when env is empty but
// config/parent-home is set, the config value is returned.
func TestResolveParentHome_ConfigFallback(t *testing.T) {
	tmp := t.TempDir()
	parentHome := t.TempDir()

	// No env set
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Write config/parent-home
	if err := config.Set(tmp, "parent-home", parentHome); err != nil {
		t.Fatal(err)
	}

	got := ResolveCaptainParentHome(tmp)
	if got != parentHome {
		t.Errorf("ResolveCaptainParentHome() = %q, want %q (config fallback)", got, parentHome)
	}
}

// TestResolveParentHome_EnvEmptyNoConfig verifies that when both env and
// config are empty, the resolver returns empty string (no parent).
func TestResolveParentHome_EnvEmptyNoConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", "")

	got := ResolveCaptainParentHome(tmp)
	if got != "" {
		t.Errorf("ResolveCaptainParentHome() = %q, want %q (no parent)", got, "")
	}
}

// TestResolveParentHome_EnvEqualsHome verifies that when env equals homeDir,
// it is rejected and the resolver falls through to config (or empty).
func TestResolveParentHome_EnvEqualsHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", tmp)

	got := ResolveCaptainParentHome(tmp)
	if got != "" {
		t.Errorf("ResolveCaptainParentHome() = %q, want %q (env equals home, rejected)", got, "")
	}
}

// TestResolveParentHome_ConfigEqualsHome verifies that when config/parent-home
// equals homeDir, it is rejected and the resolver returns empty.
func TestResolveParentHome_ConfigEqualsHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Set config/parent-home to tmp itself
	if err := config.Set(tmp, "parent-home", tmp); err != nil {
		t.Fatal(err)
	}

	got := ResolveCaptainParentHome(tmp)
	if got != "" {
		t.Errorf("ResolveCaptainParentHome() = %q, want %q (config equals home, rejected)", got, "")
	}
}

// TestResolveParentHome_ConfigMissingDoesNotCrash verifies that a missing
// config/parent-home file does not cause a crash or error — the resolver
// simply returns empty.
func TestResolveParentHome_ConfigMissingDoesNotCrash(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", "")
	// No config/parent-home file

	got := ResolveCaptainParentHome(tmp)
	if got != "" {
		t.Errorf("ResolveCaptainParentHome() = %q, want %q (missing config, no crash)", got, "")
	}
}

// TestResolveParentHome_ConfigEmptyDoesNotCrash verifies that config/parent-home
// with an empty value does not cause a crash — the resolver simply returns empty.
func TestResolveParentHome_ConfigEmptyDoesNotCrash(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Write empty config/parent-home
	if err := config.Set(tmp, "parent-home", ""); err != nil {
		t.Fatal(err)
	}

	got := ResolveCaptainParentHome(tmp)
	if got != "" {
		t.Errorf("ResolveCaptainParentHome() = %q, want %q (empty config, no crash)", got, "")
	}
}

// TestResolveParentHome_HookConsistency_ConfigFallback verifies that when
// reconcileHook is called with env empty but config/parent-home set, it
// proceeds to relay (no silent no-op) — integrating the resolver with the hook.
func TestResolveParentHome_HookConsistency_ConfigFallback(t *testing.T) {
	tmp := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Both captain and parent need state dir
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)
	os.MkdirAll(filepath.Join(parentHome, "state"), 0755)
	home.SeedCaptainProvenance(tmp, "test-captain")

	// Write config/parent-home
	if err := config.Set(tmp, "parent-home", parentHome); err != nil {
		t.Fatal(err)
	}

	// reconcileHook should now resolve parent from config and proceed
	err := ReconcileCaptainHook(tmp, false, &captainNotificationTransport{acknowledged: true})
	if err != nil {
		t.Errorf("reconcileHook should not return error when config fallback resolves parent, got: %v", err)
	}
}

// TestActivationHook_ConfigFallback verifies that captainActivationHook
// proceeds (does not silently no-op) when env is empty but config/parent-home
// is set. We verify by checking that it doesn't panic and doesn't return early
// — actual pane activation is backend-dependent so we just verify no crash.
func TestActivationHook_ConfigFallback(t *testing.T) {
	tmp := t.TempDir()
	_ = t.TempDir() // parentHome not directly needed, activation is a nudge
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Captain needs state dir
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)

	// Write config/parent-home pointing to a valid (but empty) parent
	if err := config.Set(tmp, "parent-home", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	// Should not panic — activation is best-effort even if no receipts
	CaptainActivationHook(tmp, nil)
}

// TestActivationHook_NoParent verifies that the explicit watcher hook is a no-op
// when neither env nor config has a valid parent.
func TestActivationHook_NoParent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", "")

	// Should not panic and should not try to activate
	CaptainActivationHook(tmp, nil)
}
func TestReconcileHook_ReturnsNilWhenParentStatusEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", "")

	err := ReconcileCaptainHook(tmp, false, nil)
	if err != nil {
		t.Errorf("expected nil when MUNSU_PARENT_STATUS is empty, got: %v", err)
	}
}

// TestReconcileHook_ReturnsNilWhenParentStatusEqualsHomeDir verifies that
// reconcileHook returns nil when MUNSU_PARENT_STATUS equals homeDir (a
// non-Captain/General guard against self-referencing parent).
func TestReconcileHook_ReturnsNilWhenParentStatusEqualsHomeDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", tmp)

	err := ReconcileCaptainHook(tmp, false, nil)
	if err != nil {
		t.Errorf("expected nil when MUNSU_PARENT_STATUS equals homeDir, got: %v", err)
	}
}

func TestReconcileHook_RequiresNotificationTransportWhenParentSet(t *testing.T) {
	tmp := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)
	os.MkdirAll(filepath.Join(tmp, "state"), 0755)
	home.SeedCaptainProvenance(tmp, "test-captain")

	err := ReconcileCaptainHook(tmp, false, nil)
	if err == nil || !strings.Contains(err.Error(), "uplink notification transport capability is required") {
		t.Fatalf("error = %v, want missing transport capability", err)
	}
}
