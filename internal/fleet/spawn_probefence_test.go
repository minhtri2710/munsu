package fleet

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// fenceFixture is a primary checkout with one linked worktree and a home.
type fenceFixture struct {
	primary  string
	worktree string
	home     string
}

func newFenceFixture(t *testing.T) fenceFixture {
	t.Helper()
	primary, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitInWorktree(t, primary, "init", "-q", ".")
	gitInWorktree(t, primary, "-c", "user.name=munsu", "-c", "user.email=munsu@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-q", "--allow-empty", "-m", "fixture")
	worktree := filepath.Join(t.TempDir(), "wt")
	gitInWorktree(t, primary, "worktree", "add", "--detach", "-q", worktree, "HEAD")
	worktree, err = canonicalPath(worktree)
	if err != nil {
		t.Fatal(err)
	}
	home, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return fenceFixture{primary: primary, worktree: worktree, home: home}
}

func (f fenceFixture) runner(mode string) *Runner {
	r := NewRunner(Args{ID: "task-1", HomeDir: f.home})
	r.homeDir, r.projPath, r.effectiveMode, r.launchBin = f.home, f.primary, mode, harness.Pi
	r.seatPath = seatPathDirs(f.home)
	r.seatEnv = testSeatEnv()
	return r
}

// withOverlay gives the runner a frozen project config resolved from overlay.
func withOverlay(t *testing.T, r *Runner, overlay fleetconfig.ProjectOverlay) {
	t.Helper()
	snap, err := fleetconfig.NewResolvedSnapshotWithToolProbe(
		fleetconfig.FleetBaseDocument{SchemaVersion: fleetconfig.FleetBaseSchemaVersion, Config: fleetconfig.FleetBaseConfig{Backend: "tmux"}},
		fleetconfig.ProjectFacts{Name: "p", Path: r.projPath, Overlay: overlay}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.projectConfig = SpawnProjectConfig{Frozen: snap, ProjectName: "p", ProjectPath: r.projPath}
	r.projectConfigLoaded = true
}

// allowUnfenced lets the runner's project launch unfenced where the host has no
// fence; on a host with one it changes nothing.
func allowUnfenced(t *testing.T, r *Runner) {
	t.Helper()
	withOverlay(t, r, fleetconfig.ProjectOverlay{FenceUnsupported: "allow"})
}

func TestProbeFenceRefusals(t *testing.T) {
	f := newFenceFixture(t)

	t.Run("bound path is not a git checkout", func(t *testing.T) {
		r := f.runner("direct-PR")
		err := r.probeFence(BoundWorktree{path: filepath.Join(t.TempDir(), "absent")})
		if err == nil || !strings.HasPrefix(err.Error(), "launch fence: ") || !strings.Contains(err.Error(), "repository path") {
			t.Fatalf("err = %v, want the identity classification refusal", err)
		}
	})
	t.Run("primary checkout missing", func(t *testing.T) {
		r := f.runner("direct-PR")
		r.projPath = filepath.Join(t.TempDir(), "absent")
		err := r.probeFence(BoundWorktree{path: f.worktree})
		if err == nil || !strings.Contains(err.Error(), "launch fence: resolving the primary checkout") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a fence that cannot be built refuses the launch", func(t *testing.T) {
		r := f.runner("direct-PR")
		t.Chdir(t.TempDir()) // spawn's meta lock lands under the relative home
		r.homeDir = "relative/home"
		allowUnfenced(t, r)
		err := r.probeFence(BoundWorktree{path: f.worktree})
		if runtime.GOOS != "darwin" {
			if err != nil || r.fenceRecord.Reason == "" {
				t.Fatalf("off darwin: err=%v record=%+v, want the no-fence record", err, r.fenceRecord)
			}
			return
		}
		if err == nil || !strings.HasPrefix(err.Error(), "launch fence: ") || r.fence != nil {
			t.Fatalf("err = %v fence = %v", err, r.fence)
		}
	})
}

func TestProbeFenceRecordsTheSeatFence(t *testing.T) {
	f := newFenceFixture(t)

	for _, tc := range []struct {
		name     string
		mode     string
		review   bool
		wantRole string
	}{
		{"ship soldier", "direct-PR", false, "soldier"},
		{"reviewer", "direct-PR", true, "reviewer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.runner(tc.mode)
			allowUnfenced(t, r)
			if tc.review {
				launchDir := filepath.Join(f.home, "review", "task-1", "1")
				if err := os.MkdirAll(filepath.Join(f.home, "state"), 0o755); err != nil { // the reviewer's probe writes beside its verdict file
					t.Fatal(err)
				}
				if err := os.MkdirAll(launchDir, 0o755); err != nil {
					t.Fatal(err)
				}
				r.review = &ReviewLaunch{LaunchDir: launchDir, VerdictFile: reviewVerdictFile(launchDir)}
			}
			if err := r.probeFence(BoundWorktree{path: f.worktree}); err != nil {
				t.Fatal(err)
			}
			rec := r.fenceRecord
			if runtime.GOOS != "darwin" {
				if rec.Applied || rec.FenceUnsupported != "allow" || !strings.Contains(rec.Reason, runtime.GOOS) || r.fence != nil {
					t.Fatalf("off darwin: record = %+v fence = %v, want the no-fence record naming %s", rec, r.fence, runtime.GOOS)
				}
				return
			}
			if !rec.Applied || rec.Role != tc.wantRole || len(rec.ProfileDigest) != 64 || rec.Reason != "" || r.fence == nil {
				t.Fatalf("record = %+v fence = %v, want an applied %s fence", rec, r.fence, tc.wantRole)
			}
		})
	}
}

func TestProbeFenceOnAHostWithoutAFence(t *testing.T) {
	f := newFenceFixture(t)
	for _, tc := range []struct {
		name    string
		overlay fleetconfig.ProjectOverlay
	}{
		{"no overlay", fleetconfig.ProjectOverlay{}},
		{"not allow", fleetconfig.ProjectOverlay{FenceUnsupported: "deny"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.runner("direct-PR")
			withOverlay(t, r, tc.overlay)
			err := r.probeFence(BoundWorktree{path: f.worktree})
			if runtime.GOOS == "darwin" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "launch fence refused the launch") ||
				!strings.Contains(err.Error(), "fence-unsupported allow") || r.fenceRecord.Reason != "" {
				t.Fatalf("err = %v record = %+v, want the unfenced launch refused", err, r.fenceRecord)
			}
		})
	}
}

// A soldier's fence Files are its status file and ack log, and for a scout its
// current generation's report; the launch creates the meta lock its status
// append locks, which the fence lets it lock but not create.
func TestSoldierFilesAreItsOwnHomeFiles(t *testing.T) {
	for _, tt := range []struct {
		kind   string
		report bool
	}{
		{taskauthority.KindShip, false},
		{taskauthority.KindScout, true},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			homeDir := t.TempDir()
			if _, err := home.Init(homeDir); err != nil {
				t.Fatal(err)
			}
			auth := canonicalAtHome(t, homeDir)
			req := taskauthority.CanonicalCreateRequest{HomeID: auth.HomeID(), TaskID: mustTaskID(t, "task-1"),
				Owner: "general", Description: "d", Kind: tt.kind, Reason: "test"}
			if tt.report {
				req.ScoutScope = "s"
			}
			op, err := domain.NewOperation(mustOpID(t, "op-create"), req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := auth.Create(op, req); err != nil {
				t.Fatal(err)
			}
			r := NewRunner(Args{ID: "task-1", HomeDir: homeDir, Authority: auth})
			r.homeDir, r.kind = homeDir, tt.kind
			got, err := r.soldierFiles()
			if err != nil {
				t.Fatal(err)
			}
			want := []string{filepath.Join(homeDir, "state", "task-1.status"), filepath.Join(homeDir, "state", "task-1.acks")}
			if tt.report {
				want = append(want, filepath.Join(homeDir, "data", "task-1", "report-g1.md"))
			}
			if !slices.Equal(got, want) {
				t.Fatalf("soldierFiles = %q, want %q", got, want)
			}
			if _, err := os.Stat(filepath.Join(homeDir, "state", "task-1.meta.lock")); err != nil {
				t.Fatalf("meta lock: %v", err)
			}
		})
	}
}
