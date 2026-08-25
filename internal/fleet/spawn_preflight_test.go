package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestRun_PreflightBlocksBeforeWorktreeAllocation(t *testing.T) {
	// Set up minimal env: harness flag with a known harness but binary will be absent.
	// We use a harness with a binary name that definitely doesn't exist on PATH.
	homeDir := t.TempDir()
	projectDir := t.TempDir()

	// Create minimal project files
	os.MkdirAll(filepath.Join(homeDir, "data", "test-preflight"), 0755)
	os.WriteFile(filepath.Join(homeDir, "data", "test-preflight", "brief.md"), []byte("# test preflight"), 0644)

	// Sanitize PATH so the harness binary won't be found
	cleanPath := t.TempDir()
	t.Setenv("PATH", cleanPath)

	// Save and clear auth env for the test harness
	origAuth := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer os.Setenv("OPENAI_API_KEY", origAuth)

	// Fake backend — should NOT be reached if preflight blocks
	calledNewWindow := false
	fake := &fakeBackend{
		newWindow: func(session, name string) (string, error) {
			calledNewWindow = true
			return "", nil
		},
	}

	r := NewRunner(Args{
		ID:          "test-preflight",
		ProjectName: "test-project",
		HarnessFlag: "codex", // known harness, but binary won't be on sanitized PATH
		HomeDir:     homeDir,
		Endpoints:   fakeEndpointCapabilities{backend: fake},
	})
	r.projPath = projectDir
	r.effectiveMode = "direct-PR"

	// Call the preflight directly (as Runner.Run would)
	err := r.preflightHarness()
	if err == nil {
		t.Fatal("expected preflight error for missing binary")
	}

	// Verify it's a PreflightError with binary-absent reason
	var pfErr *harness.PreflightError
	if !asPreflightError(err, &pfErr) {
		t.Fatalf("error type = %T, want *harness.PreflightError", err)
	}
	if pfErr.Reason != "binary-absent" {
		t.Errorf("PreflightError.Reason = %q, want %q", pfErr.Reason, "binary-absent")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error should mention PATH: %v", err)
	}

	// Session should NOT have been allocated
	if calledNewWindow {
		t.Fatal("session allocation occurred before preflight failure")
	}
}

func TestRun_PreflightUnknownPassesThrough(t *testing.T) {
	// Auth env set so the known check passes; model is always unknown which passes through.
	// Create a fake codex binary on PATH so the binary check also passes.
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "codex")
	testutil.WriteFakeExecutable(t, binPath, "#!/bin/sh\nexit 0\n")
	testutil.PrependPath(t, binDir)

	homeDir := t.TempDir()
	projectDir := t.TempDir()

	os.MkdirAll(filepath.Join(homeDir, "data", "test-unknown"), 0755)
	os.WriteFile(filepath.Join(homeDir, "data", "test-unknown", "brief.md"), []byte("# test unknown"), 0644)

	origAuth := os.Getenv("OPENAI_API_KEY")
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Setenv("OPENAI_API_KEY", origAuth)

	r := NewRunner(Args{
		ID:          "test-unknown",
		ProjectName: "test-project",
		HarnessFlag: "codex",
		HomeDir:     homeDir,
	})
	r.projPath = projectDir
	r.effectiveMode = "direct-PR"

	err := r.preflightHarness()
	if err != nil {
		t.Fatalf("preflightHarness returned error for codex with auth: %v", err)
	}
	if r.harness != "codex" {
		t.Errorf("r.harness = %q, want %q", r.harness, "codex")
	}
}

// asPreflightError is a simple type assertion helper since errors.As isn't imported.
func asPreflightError(err error, target **harness.PreflightError) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*harness.PreflightError)
	if !ok {
		return false
	}
	*target = e
	return true
}
