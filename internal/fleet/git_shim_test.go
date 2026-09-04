package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionGitShim(t *testing.T) {
	home := t.TempDir()

	shimDir, err := provisionGitShim(home)
	if err != nil {
		t.Fatalf("provisionGitShim: %v", err)
	}
	wantDir := filepath.Join(home, "state", "shim", "bin")
	if shimDir != wantDir {
		t.Fatalf("shimDir = %q, want %q", shimDir, wantDir)
	}

	shimPath := filepath.Join(shimDir, "git")
	info, err := os.Stat(shimPath)
	if err != nil {
		t.Fatalf("stat shim: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("shim mode = %v, want executable", info.Mode())
	}
	body, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	if !strings.HasPrefix(script, "#!/usr/bin/env bash\n") {
		t.Fatalf("shim missing bash shebang: %q", script)
	}
	if !strings.Contains(script, "git-guard") || !strings.Contains(script, `"$@"`) {
		t.Fatalf("shim must re-invoke munsu git-guard with the git args, got: %q", script)
	}

	// Idempotent: a second provision of the same home leaves identical content.
	if _, err := provisionGitShim(home); err != nil {
		t.Fatalf("second provisionGitShim: %v", err)
	}
	body2, _ := os.ReadFile(shimPath)
	if string(body2) != script {
		t.Fatalf("shim content changed across provisions:\n%s\n---\n%s", script, body2)
	}
}

func TestProvisionGitShimFailsClosedWhenDirUnavailable(t *testing.T) {
	// A home path that is a regular file cannot host state/shim/bin: MkdirAll
	// fails and provisioning returns the error so the caller fails the launch.
	fileHome := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileHome, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := provisionGitShim(fileHome); err == nil {
		t.Fatal("provisionGitShim on a file home = nil error, want fail-closed")
	}
}

func TestSoldierLaunchScriptPrependsGitShim(t *testing.T) {
	home := t.TempDir()
	wt := t.TempDir()
	artifact, err := buildLaunchArtifact(LaunchArtifactInput{
		WorktreePath:   wt,
		HomeDir:        home,
		TaskID:         "ship-shim",
		SnapshotDigest: "digest",
		LaunchBin:      "/bin/true",
		LaunchArgs:     []string{"--go"},
		LaunchID:       "lid",
		Generation:     "1",
		EndpointFence:  "fence",
	})
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	body, err := os.ReadFile(artifact.ScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	shimDir := filepath.Join(home, "state", "shim", "bin")
	if !strings.Contains(script, "export PATH=") || !strings.Contains(script, shimDir) {
		t.Fatalf("soldier launch script missing git-shim PATH prepend for %q:\n%s", shimDir, script)
	}
	if _, err := os.Stat(filepath.Join(shimDir, "git")); err != nil {
		t.Fatalf("soldier launch did not provision the git shim: %v", err)
	}
}

func TestCaptainLaunchScriptPrependsGitShim(t *testing.T) {
	cwd := t.TempDir()
	cmd, err := buildLaunchScript("/usr/local/bin/pi", []string{"# charter"}, cwd, cwd)
	if err != nil {
		t.Fatalf("buildLaunchScript: %v", err)
	}
	_ = cmd
	body, err := os.ReadFile(filepath.Join(cwd, ".captain-launch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	shimDir := filepath.Join(cwd, "state", "shim", "bin")
	if !strings.Contains(script, "export PATH=") || !strings.Contains(script, shimDir) {
		t.Fatalf("captain launch script missing git-shim PATH prepend for %q:\n%s", shimDir, script)
	}
	if _, err := os.Stat(filepath.Join(shimDir, "git")); err != nil {
		t.Fatalf("captain launch did not provision the git shim: %v", err)
	}
}
