package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
		LaunchDir:      wt,
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

// The seat's PATH is the shim, then the absolute entries of the launching
// process's PATH in order. Nothing from the pane's own PATH and no relative or
// empty entry reaches it: the fence denies exactly this list.
func TestSoldierLaunchScriptExportsExactlyTheSeatPathDirs(t *testing.T) {
	home := t.TempDir()
	wt := t.TempDir()
	t.Setenv("PATH", strings.Join([]string{"/munsu/a", ".", "", "rel/bin", "/munsu/b/"}, ":"))
	artifact, err := buildLaunchArtifact(LaunchArtifactInput{
		WorktreePath: wt, LaunchDir: wt, HomeDir: home, TaskID: "ship-shim", SnapshotDigest: "digest",
		LaunchBin: "/bin/true", LaunchArgs: []string{"--go"}, LaunchID: "lid", Generation: "1", EndpointFence: "fence",
	})
	if err != nil {
		t.Fatalf("buildLaunchArtifact: %v", err)
	}
	body, err := os.ReadFile(artifact.ScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	var export string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "export PATH=") {
			export = line
		}
	}
	if export == "" {
		t.Fatalf("no PATH export in the launch script:\n%s", body)
	}
	cmd := exec.Command("/bin/bash", "-c", export+`; printf %s "$PATH"`)
	cmd.Env = []string{"PATH=/pane/extra:.:/usr/bin"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running %q: %v", export, err)
	}
	got := strings.Split(string(out), ":")

	shimDir := filepath.Join(home, "state", "shim", "bin")
	tests := []struct {
		name string
		ok   bool
	}{
		{"a dir only on the pane PATH is absent", !slices.Contains(got, "/pane/extra") && !slices.Contains(got, "/usr/bin")},
		{"a relative . entry is absent", !slices.Contains(got, ".")},
		{"an empty or relative entry is absent", !slices.ContainsFunc(got, func(d string) bool { return !filepath.IsAbs(d) })},
		{"the shim is first and the absolute inherited dirs follow in order", slices.Equal(got, []string{shimDir, "/munsu/a", "/munsu/b"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("seat PATH = %q", got)
			}
		})
	}
}

func TestCaptainLaunchScriptPrependsGitShim(t *testing.T) {
	cwd := t.TempDir()
	cmd, err := buildLaunchScript("/usr/local/bin/pi", []string{"# charter"}, cwd, cwd, filepath.Base(cwd))
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
