//go:build darwin

package fleet

import (
	"os"
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
