//go:build integration

package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
)

// captureOutput runs fn with os.Stdout/os.Stderr redirected.
func captureOutput(fn func()) (stdout, stderr string) {
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	os.Stdout = wOut
	os.Stderr = wErr

	fn()

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var outBuf, errBuf bytes.Buffer
	io.Copy(&outBuf, rOut)
	io.Copy(&errBuf, rErr)

	return outBuf.String(), errBuf.String()
}

// TestInitNonInteractive verifies that init defaults to skip when
// stdin is not a TTY and no --skill flag or MUNSU_INIT_SKILL env is set.
func TestInitNonInteractive(t *testing.T) {
	savedChoice := skillChoice
	skillChoice = ""
	t.Cleanup(func() { skillChoice = savedChoice })

	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	stdout, stderr := captureOutput(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"init"})
		if err := root.Execute(); err != nil {
			t.Errorf("init failed: %v", err)
		}
	})

	if t.Failed() {
		t.Fatalf("init execution error (see above)")
	}

	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	if !strings.Contains(stdout, "Skipping skill install.") {
		t.Errorf("expected 'Skipping skill install.' in non-TTY mode, got: %s", stdout)
	}
}

// TestInitSkillLocalFlag verifies that --skill local installs skills
// without prompting, even when stdin is not a TTY.
func TestInitSkillLocalFlag(t *testing.T) {
	savedChoice := skillChoice
	skillChoice = skillLocal
	t.Cleanup(func() { skillChoice = savedChoice })

	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	stdout, stderr := captureOutput(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"init", "--skill", "local"})
		if err := root.Execute(); err != nil {
			t.Errorf("init --skill local failed: %v", err)
		}
	})

	if t.Failed() {
		t.Fatalf("init execution error (see above)")
	}

	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	if !strings.Contains(stdout, "Installed skill") {
		t.Errorf("expected 'Installed skill' with --skill local, got: %s", stdout)
	}

	// Verify the skill was actually written to the local home
	skillDir := filepath.Join(tmpDir, ".agents", "skills", "munsu-ops")
	info, err := os.Stat(skillDir)
	if err != nil {
		t.Fatalf("expected munsu-ops skill dir at %s: %v", skillDir, err)
	}
	if !info.IsDir() {
		t.Errorf("expected %s to be a directory", skillDir)
	}
}

// TestInitSkillGlobalFlag verifies that --skill global installs skills
// without prompting, even when stdin is not a TTY.
func TestInitSkillGlobalFlag(t *testing.T) {
	savedChoice := skillChoice
	skillChoice = skillGlobal
	t.Cleanup(func() { skillChoice = savedChoice })

	tmpHome, err := os.MkdirTemp("", "munsu-global-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpHome) })

	// Redirect $HOME so global install writes to our temp dir
	t.Setenv("HOME", tmpHome)
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	stdout, stderr := captureOutput(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"init", "--skill", "global"})
		if err := root.Execute(); err != nil {
			t.Errorf("init --skill global failed: %v", err)
		}
	})

	if t.Failed() {
		t.Fatalf("init execution error (see above)")
	}

	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	if !strings.Contains(stdout, "Installed skill") {
		t.Errorf("expected 'Installed skill' with --skill global, got: %s", stdout)
	}

	// Verify the skill was written to $HOME/.agents/skills/
	skillDir := filepath.Join(tmpHome, ".agents", "skills", "munsu-ops")
	info, err := os.Stat(skillDir)
	if err != nil {
		t.Fatalf("expected munsu-ops skill dir at %s: %v", skillDir, err)
	}
	if !info.IsDir() {
		t.Errorf("expected %s to be a directory", skillDir)
	}
}

// TestInitEnvOverride verifies that MUNSU_INIT_SKILL=skip env var
// overrides interactive prompt behavior.
func TestInitEnvOverride(t *testing.T) {
	savedChoice := skillChoice
	skillChoice = ""
	t.Cleanup(func() { skillChoice = savedChoice })

	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	t.Setenv("MUNSU_INIT_SKILL", "skip")

	stdout, stderr := captureOutput(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"init"})
		if err := root.Execute(); err != nil {
			t.Errorf("init with MUNSU_INIT_SKILL=skip failed: %v", err)
		}
	})

	if t.Failed() {
		t.Fatalf("init execution error (see above)")
	}

	t.Logf("stdout: %s", stdout)
	t.Logf("stderr: %s", stderr)

	if !strings.Contains(stdout, "Skipping skill install.") {
		t.Errorf("expected 'Skipping skill install.' with MUNSU_INIT_SKILL=skip, got: %s", stdout)
	}
}

// TestInitWritesFleetBaseDocument verifies init authors config/base.json with
// the detected SoldierHarness and the fixed captain default so init'd homes
// resolve a captain harness (fail-closed-able). The flat soldier-harness pin
// remains for diagnostics compatibility.
func TestInitWritesFleetBaseDocument(t *testing.T) {
	savedChoice := skillChoice
	skillChoice = ""
	t.Cleanup(func() { skillChoice = savedChoice })
	t.Setenv("MUNSU_INIT_SKILL", "skip")

	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	captureOutput(func() {
		root := NewRootCommand()
		root.SetArgs([]string{"init"})
		if err := root.Execute(); err != nil {
			t.Errorf("init failed: %v", err)
		}
	})
	if t.Failed() {
		t.Fatalf("init execution error (see above)")
	}

	base, err := config.LoadFleetBase(tmpDir)
	if err != nil {
		t.Fatalf("loading fleet base after init: %v", err)
	}
	if base.CaptainProfile.Harness != harness.Pi {
		t.Fatalf("base captainProfile = %+v, want fixed default %q", base.CaptainProfile, harness.Pi)
	}
	// SoldierHarness mirrors the flat pin (or stays empty when Detect found none).
	if got, err := config.Get(tmpDir, "soldier-harness"); err == nil {
		if base.Config.SoldierHarness != got {
			t.Fatalf("base SoldierHarness = %q, flat pin = %q", base.Config.SoldierHarness, got)
		}
	}
}
