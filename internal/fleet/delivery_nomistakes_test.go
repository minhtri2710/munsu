//go:build integration

package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestEnsureDeliveryModeRunnable_UnexpectedProbeStateRefuses(t *testing.T) {
	err := ensureDeliveryModeRunnableForProbe(ProbeResult{State: backend.State(99)})
	if err == nil || !strings.Contains(err.Error(), "unexpected probe state") {
		t.Fatalf("unexpected probe error = %v, want unexpected state refusal", err)
	}
}

// TestNoMistakesProbe_Absent verifies that a missing binary returns Absent.
func TestNoMistakesProbe_Absent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	result := NoMistakesProbe()
	if result.State != backend.Absent {
		t.Errorf("expected Absent, got %v (%s)", result.State, result.Detail)
	}
}

// TestNoMistakesProbe_Unsupported verifies that an old version returns Unsupported.
func TestNoMistakesProbe_Unsupported(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := `#!/bin/sh
case "$1" in
  --version)
    echo "no-mistakes version v0.5.0 (ancient)"
    exit 0
    ;;
  axi)
    echo "unknown command"
    exit 1
    ;;
esac
exit 1
`
	testutil.WriteFakeExecutable(t, binPath, script)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	result := NoMistakesProbe()
	// v0.5.0 is below MinNoMistakesVersion (1.20.0), should be Unsupported
	if result.State != backend.Unsupported {
		t.Errorf("expected Unsupported for old version, got %v (%s)", result.State, result.Detail)
	}
	if result.Version != "" && result.Version == "0.5.0" {
		// OK
	}
}

// TestNoMistakesProbe_Failed_MalformedVersion verifies malformed version output.
func TestNoMistakesProbe_Failed_MalformedVersion(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := `#!/bin/sh
echo "not-a-valid-version-string"
exit 0
`
	testutil.WriteFakeExecutable(t, binPath, script)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	result := NoMistakesProbe()
	if result.State != backend.Failed {
		t.Errorf("expected Failed for malformed version, got %v (%s)", result.State, result.Detail)
	}
}

// TestNoMistakesProbe_Failed_EmptyVersion verifies empty version output.
func TestNoMistakesProbe_Failed_EmptyVersion(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := "#!/bin/sh\nexit 0\n"
	testutil.WriteFakeExecutable(t, binPath, script)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	result := NoMistakesProbe()
	// Binary exists but produces no --version output
	if result.State != backend.Failed {
		t.Errorf("expected Failed for empty version, got %v (%s)", result.State, result.Detail)
	}
}

// TestNoMistakesProbe_Failed_VersionCommandFails verifies version command failure.
func TestNoMistakesProbe_Failed_VersionCommandFails(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := "#!/bin/sh\nexit 1\n"
	testutil.WriteFakeExecutable(t, binPath, script)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	result := NoMistakesProbe()
	if result.State != backend.Failed {
		t.Errorf("expected Failed when version command fails, got %v (%s)", result.State, result.Detail)
	}
}

// TestNoMistakesProbe_Ready verifies that the actual no-mistakes binary is Ready.
func TestNoMistakesProbe_Ready(t *testing.T) {
	if _, err := exec.LookPath("no-mistakes"); err != nil {
		t.Skip("no-mistakes not on PATH")
	}
	result := NoMistakesProbe()
	if result.State != backend.Ready {
		t.Errorf("expected Ready, got %v (%s)", result.State, result.Detail)
	}
	if result.Path == "" {
		t.Error("expected non-empty path for Ready probe")
	}
	if result.Version == "" {
		t.Error("expected non-empty version for Ready probe")
	}
}

// TestNoMistakesProbe_Ready_FakeBinary verifies a properly constructed fake binary.
func TestNoMistakesProbe_Ready_FakeBinary(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := `#!/bin/sh
case "$1" in
  --version)
    echo "no-mistakes version v1.40.0 (test)"
    exit 0
    ;;
  axi)
    case "$2" in
      status)
        case "$3" in
          --help)
            echo "Show the active run in detail"
            echo "Usage:"
            echo "  no-mistakes axi status [flags]"
            exit 0
            ;;
        esac
        ;;
    esac
    exit 1
    ;;
esac
exit 1
`
	testutil.WriteFakeExecutable(t, binPath, script)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	result := NoMistakesProbe()
	if result.State != backend.Ready {
		t.Errorf("expected Ready for valid fake binary, got %v (%s)", result.State, result.Detail)
	}
	if result.Path == "" {
		t.Error("expected non-empty path")
	}
	if result.Version != "1.40.0" {
		t.Errorf("expected version 1.40.0, got %q", result.Version)
	}
}

// TestPreflight_NoMistakes_IncompatibleVersion verifies preflight rejects old version.
func TestPreflight_NoMistakes_IncompatibleVersion(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := `#!/bin/sh
echo "no-mistakes version v0.5.0 (ancient)"
exit 0
`
	testutil.WriteFakeExecutable(t, binPath, script)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	result, err := Preflight("no-mistakes", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Feasible {
		t.Error("expected preflight to be not feasible for incompatible version")
	}
	// Should have additional compat check failure
	hasCompatCheck := false
	for _, c := range result.Checks {
		if c.Name == "no-mistakes-compat" && !c.OK {
			hasCompatCheck = true
			break
		}
	}
	if !hasCompatCheck {
		t.Errorf("expected no-mistakes-compat failure check, got checks: %v", result.Checks)
	}
}

// TestProbeResult_String verifies probe result String formatting.
func TestProbeResult_String(t *testing.T) {
	tests := []struct {
		p    ProbeResult
		want string
	}{
		{ProbeResult{State: backend.Absent}, "no-mistakes: absent"},
		{ProbeResult{State: backend.Unsupported, Version: "0.5.0"}, "no-mistakes: unsupported (version 0.5.0)"},
		{ProbeResult{State: backend.Ready, Version: "1.40.0", Path: "/usr/bin/no-mistakes"}, "no-mistakes: ready (1.40.0 at /usr/bin/no-mistakes)"},
		{ProbeResult{State: backend.Failed, Detail: "oops"}, "no-mistakes: failed (oops)"},
	}
	for _, tt := range tests {
		got := tt.p.String()
		if got != tt.want {
			t.Errorf("ProbeResult{State:%v}.String() = %q, want %q", tt.p.State, got, tt.want)
		}
	}
}
