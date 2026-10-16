package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ensureBranchReflog pre-creates a branch reflog as an empty regular file so the
// fence can allow the seat to append to it while refusing the create the first
// checkout -b would otherwise need (B1, fence-reflog-symlink-deputy). It
// fails closed when the reflog, any parent under logs/ or the branch's namespace
// dir under refs/heads is a symlink or not the expected type, and never follows
// a planted link to write the victim.
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
		{name: "symlinked ref namespace dir: refused, nothing created in the target", wantErr: "not a directory", seed: func(t *testing.T, gitDir string) {
			// refs/heads/mu is a symlink to a real dir; the seat's ref would
			// land in that dir.
			target := t.TempDir()
			if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(gitDir, "refs", "heads", "mu")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if ents, _ := os.ReadDir(target); len(ents) != 0 {
					t.Errorf("an entry was created through the symlinked ref namespace dir: %v", ents)
				}
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gitDir := filepath.Join(t.TempDir(), "g.git")
			// The common dir always exists in production.
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
				if fi, err := os.Lstat(filepath.Join(gitDir, "refs", "heads", "mu")); err != nil || !fi.IsDir() {
					t.Fatalf("ref namespace dir is not a directory: %v", err)
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

// The pre-created reflogs are shared per-project state: the Lead dispatches
// the seats of one project in parallel, so a seat that loses the create race
// (EEXIST from Mkdir or the O_EXCL create) must accept the winner's entry
// instead of refusing its own launch.
func TestEnsureReflogAcceptsAConcurrentSpawnCreate(t *testing.T) {
	newRepo := func(t *testing.T) string {
		dir := t.TempDir()
		gitInWorktree(t, dir, "init", "-q", ".")
		gitInWorktree(t, dir, "-c", "user.name=munsu", "-c", "user.email=munsu@example.invalid",
			"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "fixture")
		for _, ref := range []string{"refs/remotes/origin/main", "refs/remotes/origin/feat/x"} {
			gitInWorktree(t, dir, "-c", "core.logAllRefUpdates=false", "update-ref", ref, "HEAD")
		}
		return filepath.Join(dir, ".git")
	}
	for round := 0; round < 5; round++ {
		gitDir := newRepo(t)
		const seats = 8
		start := make(chan struct{})
		errs := make([]error, seats)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = ensureBranchReflog(gitDir, "mu/T-1")
			}()
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d seat %d: ensureBranchReflog = %v", round, i, err)
			}
		}
		fi, err := os.Lstat(filepath.Join(gitDir, "logs", "refs", "heads", "mu", "T-1"))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() != 0 {
			t.Fatalf("round %d: branch reflog = %v, %v; want an empty regular file", round, fi, err)
		}

		gitDir = newRepo(t)
		start = make(chan struct{})
		paths := make([][]string, seats)
		wg = sync.WaitGroup{}
		for i := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				paths[i], errs[i] = ensureRemoteReflogs(gitDir)
			}()
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d seat %d: ensureRemoteReflogs = %v", round, i, err)
			}
			if len(paths[i]) != 2 {
				t.Errorf("round %d seat %d: reflog paths = %v, want the 2 remote-tracking refs", round, i, paths[i])
			}
		}
		for _, ref := range []string{"refs/remotes/origin/main", "refs/remotes/origin/feat/x"} {
			fi, err := os.Lstat(filepath.Join(gitDir, "logs", filepath.FromSlash(ref)))
			if err != nil || !fi.Mode().IsRegular() || fi.Size() != 0 {
				t.Fatalf("round %d: reflog of %s = %v, %v; want an empty regular file", round, ref, fi, err)
			}
		}
	}
}
