package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/taskauthority"
	"github.com/minhtri2710/munsu/internal/testutil"
)

const fakeNoMistakesScript = `#!/bin/sh
case "$1" in
--version) echo "no-mistakes version v1.99.0" ;;
axi) echo "status" ;;
esac
`

// gateFixture is a primary checkout and an isolated NM_HOME.
type gateFixture struct {
	primary string
	nmHome  string
}

func newGateFixture(t *testing.T) gateFixture {
	t.Helper()
	f := gateFixture{primary: t.TempDir(), nmHome: t.TempDir()}
	t.Setenv("NM_HOME", f.nmHome)
	gitInWorktree(t, f.primary, "init", "-q", ".")
	return f
}

// remote names the gate repo url of the primary's no-mistakes remote.
func (f gateFixture) remote(t *testing.T, url string) {
	t.Helper()
	gitInWorktree(t, f.primary, "remote", "add", "no-mistakes", url)
}

// ensureBranchReflog pre-creates a branch reflog as an empty regular file so the
// fence can allow the seat to append to it while refusing the create the first
// checkout -b or push would otherwise need (B1, fence-reflog-symlink-deputy). It
// fails closed when the reflog or any parent under logs/ is a symlink or not the
// expected type, and never follows a planted link to write the victim.
func TestEnsureBranchReflog(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	reflogIn := func(gitDir string) string {
		return filepath.Join(gitDir, "logs", "refs", "heads", "mu", "T-1")
	}
	for _, tc := range []struct {
		name    string
		seed    func(t *testing.T, gitDir string) // state before the call
		wantErr string                            // substring; "" means it must succeed
	}{
		{name: "absent: creates an empty regular reflog and its parents"},
		{name: "regular file: kept", seed: func(t *testing.T, gitDir string) {
			p := reflogIn(gitDir)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("prior"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink leaf: refused, victim untouched", wantErr: "not a regular file", seed: func(t *testing.T, gitDir string) {
			p := reflogIn(gitDir)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, p); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "directory leaf: refused", wantErr: "not a regular file", seed: func(t *testing.T, gitDir string) {
			if err := os.MkdirAll(reflogIn(gitDir), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked parent: refused (F3), nothing created in the target", wantErr: "not a directory", seed: func(t *testing.T, gitDir string) {
			// logs/refs/heads/mu is a symlink to a real dir; a create through it
			// would land in that dir.
			target := t.TempDir()
			if err := os.MkdirAll(filepath.Join(gitDir, "logs", "refs", "heads"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(gitDir, "logs", "refs", "heads", "mu")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if ents, _ := os.ReadDir(target); len(ents) != 0 {
					t.Errorf("a reflog was created through the symlinked parent: %v", ents)
				}
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gitDir := filepath.Join(t.TempDir(), "g.git")
			// The gitDir (common dir or gate repo) always exists in production.
			if err := os.Mkdir(gitDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.seed != nil {
				tc.seed(t, gitDir)
			}
			err := ensureBranchReflog(gitDir, "mu/T-1")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("ensureBranchReflog = %v, want success", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("ensureBranchReflog = %v, want an error containing %q", err, tc.wantErr)
			}
			if tc.wantErr == "" {
				if fi, err := os.Lstat(reflogIn(gitDir)); err != nil || !fi.Mode().IsRegular() {
					t.Fatalf("reflog is not a regular file: %v", err)
				}
			}
			// Fail-closed must never follow a planted link and write the victim.
			if b, _ := os.ReadFile(victim); string(b) != "safe" {
				t.Errorf("victim was written through a symlink: %q", b)
			}
		})
	}
}

// ensureRemoteReflogs pre-creates the reflog of each existing remote-tracking
// ref (fence-reflog-remotes-residual) under ensureBranchReflog's fail-closed
// rules: a planted link leaf or parent is refused and its target is untouched.
func TestEnsureRemoteReflogs(t *testing.T) {
	newRepo := func(t *testing.T) string {
		dir := t.TempDir()
		gitInWorktree(t, dir, "init", "-q", ".")
		gitInWorktree(t, dir, "-c", "user.name=munsu", "-c", "user.email=munsu@example.invalid",
			"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "fixture")
		// logAllRefUpdates=false: the refs exist with no reflog, as a fetch that wrote none leaves them.
		for _, ref := range []string{"refs/remotes/origin/main", "refs/remotes/origin/feat/x"} {
			gitInWorktree(t, dir, "-c", "core.logAllRefUpdates=false", "update-ref", ref, "HEAD")
		}
		return filepath.Join(dir, ".git")
	}
	reflogIn := func(gitDir, ref string) string {
		return filepath.Join(gitDir, "logs", filepath.FromSlash(ref))
	}
	victimDir := t.TempDir()
	victim := filepath.Join(victimDir, "victim")
	if err := os.WriteFile(victim, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		seed    func(t *testing.T, gitDir string) // state before the call
		want    []string                          // remote-tracking refs whose reflog must be an empty regular file
		wantErr string                            // substring; "" means it must succeed
	}{
		{name: "each remote-tracking ref gets an empty regular reflog", want: []string{"refs/remotes/origin/feat/x", "refs/remotes/origin/main"}},
		{name: "no remote-tracking ref: nothing is created", seed: func(t *testing.T, gitDir string) {
			if err := os.RemoveAll(filepath.Join(gitDir, "refs", "remotes")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink leaf: refused, victim untouched", wantErr: "not a regular file", seed: func(t *testing.T, gitDir string) {
			p := reflogIn(gitDir, "refs/remotes/origin/main")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, p); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked parent: refused, nothing created in the target", wantErr: "not a directory", seed: func(t *testing.T, gitDir string) {
			p := reflogIn(gitDir, "refs/remotes/origin")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victimDir, p); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gitDir := newRepo(t)
			if tc.seed != nil {
				tc.seed(t, gitDir)
			}
			paths, err := ensureRemoteReflogs(gitDir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.wantErr == "" && len(paths) != len(tc.want) {
				t.Errorf("paths = %v, want %d reflogs", paths, len(tc.want))
			}
			for _, ref := range tc.want {
				fi, err := os.Lstat(reflogIn(gitDir, ref))
				if err != nil || !fi.Mode().IsRegular() || fi.Size() != 0 {
					t.Errorf("reflog of %s = %v, %v; want an empty regular file", ref, fi, err)
				}
			}
			if ents, _ := os.ReadDir(victimDir); len(ents) != 1 {
				t.Errorf("entries in the link target = %v; a reflog was written through a link", ents)
			}
			if b, _ := os.ReadFile(victim); string(b) != "safe" {
				t.Errorf("victim = %q, want untouched", b)
			}
		})
	}
}

func (f gateFixture) repos(t *testing.T) string {
	t.Helper()
	repos := filepath.Join(f.nmHome, "repos")
	if err := os.MkdirAll(repos, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalPath(repos)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func requireGateBlocker(t *testing.T, err error, category GateBlockerCategory, detail string) {
	t.Helper()
	var blocker *GateBlockerError
	if !errors.As(err, &blocker) {
		t.Fatalf("error = %v (%T), want a gate blocker", err, err)
	}
	if blocker.Category != category || !strings.Contains(blocker.Detail, detail) {
		t.Fatalf("blocker = %s %q, want %s containing %q", blocker.Category, blocker.Detail, category, detail)
	}
}

func TestProjectGate(t *testing.T) {
	t.Run("primary without a no-mistakes remote", func(t *testing.T) {
		f := newGateFixture(t)
		_, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "the primary has no no-mistakes remote")
		var blocker *GateBlockerError
		errors.As(err, &blocker)
		if !strings.Contains(blocker.Guidance, "no-mistakes init") || !strings.Contains(blocker.Guidance, f.primary) {
			t.Fatalf("guidance = %q, want the init command for the primary", blocker.Guidance)
		}
	})

	t.Run("repos directory missing", func(t *testing.T) {
		f := newGateFixture(t)
		f.remote(t, filepath.Join(f.nmHome, "repos", "p.git"))
		_, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "the no-mistakes repos directory is unreadable")
	})

	t.Run("remote outside the repos directory", func(t *testing.T) {
		f := newGateFixture(t)
		f.repos(t)
		elsewhere := t.TempDir()
		f.remote(t, elsewhere)
		_, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "is not a gate repo under")
	})

	t.Run("remote that does not exist", func(t *testing.T) {
		f := newGateFixture(t)
		repos := f.repos(t)
		f.remote(t, filepath.Join(repos, "missing.git"))
		_, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "is not a gate repo under")
	})

	t.Run("remote nested below a repos entry", func(t *testing.T) {
		f := newGateFixture(t)
		nested := filepath.Join(f.repos(t), "a", "b.git")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		f.remote(t, nested)
		_, err := projectGate(f.primary)
		requireGateBlocker(t, err, GateBlockerNotInitialized, "is not a gate repo under")
	})

	t.Run("gate repo directly under repos", func(t *testing.T) {
		f := newGateFixture(t)
		repos := f.repos(t)
		gate := filepath.Join(repos, "p.git")
		if err := os.MkdirAll(gate, 0o755); err != nil {
			t.Fatal(err)
		}
		f.remote(t, gate)
		repo, err := projectGate(f.primary)
		if err != nil {
			t.Fatalf("projectGate: %v", err)
		}
		if repo != gate {
			t.Fatalf("gate repo = %q, want %q", repo, gate)
		}
	})
}

func TestDefaultNoMistakesPreflight(t *testing.T) {
	review := taskauthority.DeliveryStep{Adapter: "no-mistakes"}
	t.Run("unreadable config", func(t *testing.T) {
		f := newGateFixture(t)
		testutil.FakeOnPath(t, "no-mistakes", fakeNoMistakesScript)
		if err := os.Mkdir(filepath.Join(f.nmHome, "config.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		requireGateBlocker(t, defaultNoMistakesPreflight(f.primary, review), GateBlockerConfigMismatch, "reading no-mistakes config")
	})

	t.Run("no-mistakes missing", func(t *testing.T) {
		f := newGateFixture(t)
		testutil.SetPath(t, t.TempDir())
		requireGateBlocker(t, defaultNoMistakesPreflight(f.primary, review), GateBlockerCommandFailure, "no-mistakes CLI not runnable")
	})

	t.Run("gate not initialized", func(t *testing.T) {
		f := newGateFixture(t)
		testutil.FakeOnPath(t, "no-mistakes", fakeNoMistakesScript)
		testutil.FakeOnPath(t, "pi", "#!/bin/sh\n")
		requireGateBlocker(t, defaultNoMistakesPreflight(f.primary, review), GateBlockerNotInitialized, "the primary has no no-mistakes remote")
	})

	t.Run("initialized gate", func(t *testing.T) {
		f := newGateFixture(t)
		testutil.FakeOnPath(t, "no-mistakes", fakeNoMistakesScript)
		testutil.FakeOnPath(t, "pi", "#!/bin/sh\n")
		gate := filepath.Join(f.repos(t), "p.git")
		if err := os.MkdirAll(gate, 0o755); err != nil {
			t.Fatal(err)
		}
		f.remote(t, gate)
		if err := defaultNoMistakesPreflight(f.primary, review); err != nil {
			t.Fatalf("preflight: %v", err)
		}
	})
}
