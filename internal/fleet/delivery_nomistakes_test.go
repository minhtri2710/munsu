//go:build integration

package fleet

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// TestProbeNoMistakesTool_Absent verifies that a missing binary returns Absent.
func TestProbeNoMistakesTool_Absent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	result := ProbeNoMistakesTool(config.ToolEntry{})
	if result.State != backend.Absent {
		t.Errorf("expected Absent, got %v (%s)", result.State, result.Detail)
	}
}

// TestProbeNoMistakesTool_Unsupported verifies that an old version returns Unsupported.
func TestProbeNoMistakesTool_Unsupported(t *testing.T) {
	testutil.PrependPath(t, createFakeNoMistakesVersion(t, "0.5.0"))

	result := ProbeNoMistakesTool(config.ToolEntry{})
	// v0.5.0 is below MinNoMistakesVersion (1.20.0), should be Unsupported
	if result.State != backend.Unsupported {
		t.Errorf("expected Unsupported for old version, got %v (%s)", result.State, result.Detail)
	}
}

// TestProbeNoMistakesTool_Failed_MalformedVersion verifies malformed version output.
func TestProbeNoMistakesTool_Failed_MalformedVersion(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := `#!/bin/sh
echo "not-a-valid-version-string"
exit 0
`
	testutil.WriteFakeExecutable(t, binPath, script)
	testutil.PrependPath(t, tmpDir)

	result := ProbeNoMistakesTool(config.ToolEntry{})
	if result.State != backend.Failed {
		t.Errorf("expected Failed for malformed version, got %v (%s)", result.State, result.Detail)
	}
}

// TestProbeNoMistakesTool_Failed_EmptyVersion verifies empty version output.
func TestProbeNoMistakesTool_Failed_EmptyVersion(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := "#!/bin/sh\nexit 0\n"
	testutil.WriteFakeExecutable(t, binPath, script)
	testutil.PrependPath(t, tmpDir)

	result := ProbeNoMistakesTool(config.ToolEntry{})
	// Binary exists but produces no --version output
	if result.State != backend.Failed {
		t.Errorf("expected Failed for empty version, got %v (%s)", result.State, result.Detail)
	}
}

// TestProbeNoMistakesTool_Failed_VersionCommandFails verifies version command failure.
func TestProbeNoMistakesTool_Failed_VersionCommandFails(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := "#!/bin/sh\nexit 1\n"
	testutil.WriteFakeExecutable(t, binPath, script)
	testutil.PrependPath(t, tmpDir)

	result := ProbeNoMistakesTool(config.ToolEntry{})
	if result.State != backend.Failed {
		t.Errorf("expected Failed when version command fails, got %v (%s)", result.State, result.Detail)
	}
}

// TestProbeNoMistakesTool_Ready verifies that the actual no-mistakes binary is Ready.
func TestProbeNoMistakesTool_Ready(t *testing.T) {
	if _, err := exec.LookPath("no-mistakes"); err != nil {
		t.Skip("no-mistakes not on PATH")
	}
	result := ProbeNoMistakesTool(config.ToolEntry{})
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

// TestProbeNoMistakesTool_Ready_FakeBinary verifies a properly constructed fake binary.
func TestProbeNoMistakesTool_Ready_FakeBinary(t *testing.T) {
	testutil.PrependPath(t, createFakeNoMistakesReady(t))

	result := ProbeNoMistakesTool(config.ToolEntry{})
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

// createFakeNoMistakesVersion creates a fake no-mistakes binary that reports
// the given semver version string.
func createFakeNoMistakesVersion(t *testing.T, version string) string {
	t.Helper()
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "no-mistakes")
	script := "#!/bin/sh\necho \"no-mistakes version v" + version + " (test)\"\nexit 0\n"
	testutil.WriteFakeExecutable(t, binPath, script)
	return tmpDir
}
