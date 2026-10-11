//go:build darwin

package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
)

func TestProbeFenceRecordsTheProjectsWritableRoots(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	cache, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	overlay := fleetconfig.ProjectOverlay{FenceWritableRoots: []string{cache}}

	t.Run("a soldier's fence carries the roots", func(t *testing.T) {
		r := f.runner("direct-PR")
		withOverlay(t, r, overlay)
		if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
			t.Fatal(err)
		}
		if got := r.fenceRecord.WritableRoots; len(got) != 1 || got[0] != cache {
			t.Fatalf("record = %+v, want the root %s", r.fenceRecord, cache)
		}
	})
	t.Run("a root inside the primary checkout refuses the launch", func(t *testing.T) {
		r := f.runner("direct-PR")
		withOverlay(t, r, fleetconfig.ProjectOverlay{FenceWritableRoots: []string{filepath.Join(f.primary, "node_modules")}})
		if err := r.probeFence(BoundWorktree{path: f.worktree}); err == nil || !strings.Contains(err.Error(), "overlaps protected path "+f.primary) {
			t.Fatalf("err = %v, want the root inside the primary refused", err)
		}
	})
	t.Run("a reviewer's fence carries none", func(t *testing.T) {
		r := f.runner("direct-PR")
		withOverlay(t, r, overlay)
		launchDir := filepath.Join(f.home, "review", "task-1", "1")
		if err := os.MkdirAll(filepath.Join(f.home, "state"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(launchDir, 0o755); err != nil {
			t.Fatal(err)
		}
		r.review = &ReviewLaunch{LaunchDir: launchDir, VerdictFile: reviewVerdictFile(launchDir)}
		if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
			t.Fatal(err)
		}
		if len(r.fenceRecord.WritableRoots) != 0 {
			t.Fatalf("record = %+v, a reviewer gets no extra roots", r.fenceRecord)
		}
	})
}

// The launch hands the fence the PATH its script builds: the shim dir and every
// inherited directory are refused, and the seat's own worktree still is not.
func TestProbeFenceRefusesTheSeatsPathDirs(t *testing.T) {
	isolateHuman(t)
	f := newFenceFixture(t)
	inherited, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", inherited+string(os.PathListSeparator)+os.Getenv("PATH"))
	shim := gitShimDir(f.home)
	if err := os.MkdirAll(shim, 0o755); err != nil {
		t.Fatal(err)
	}
	r := f.runner("direct-PR")
	if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
		t.Fatal(err)
	}
	touch := func(dir string) (string, error) {
		argv, err := r.fence.Wrap([]string{"/usr/bin/touch", filepath.Join(dir, "gh")})
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}
	for _, dir := range []string{shim, inherited} {
		if out, err := touch(dir); err == nil || !strings.Contains(out, "Operation not permitted") {
			t.Errorf("write in PATH dir %s was not refused by the fence: %v %s", dir, err, out)
		}
	}
	if out, err := touch(f.worktree); err != nil {
		t.Errorf("write in the worktree was refused: %v %s", err, out)
	}
}
