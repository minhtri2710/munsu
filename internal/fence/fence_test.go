package fence

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/harness"
)

// layout is one launch's directory tree. common sits inside primary, as a
// checkout's .git does; the worktree is a sibling of primary.
type layout struct{ root, home, primary, common, gitDir, worktree string }

func newLayout(t *testing.T) layout { return newSplitLayout(t, "") }

// newSplitLayout places the home and the worktree under out when it is set and
// every other path under a fresh temp root; out "" keeps all of them in the
// temp root.
func newSplitLayout(t *testing.T, out string) layout {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		out = root
	}
	l := layout{
		root:     root,
		home:     filepath.Join(out, "home"),
		primary:  filepath.Join(root, "primary"),
		common:   filepath.Join(root, "primary", ".git"),
		gitDir:   filepath.Join(root, "primary", ".git", "worktrees", "wt"),
		worktree: filepath.Join(out, "wt"),
	}
	dirs := []string{l.primary, l.common, l.gitDir, l.worktree}
	// The home's directories as createLayout makes them, named apart from
	// homeRoots so a root the profile stops denying still exists here.
	for _, r := range []string{"state", "data", ".journal", ".lock", "config", "projects"} {
		dirs = append(dirs, filepath.Join(l.home, r))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func (l layout) soldier() Launch {
	return Launch{Role: RoleSoldier, Harness: harness.Claude, Home: l.home, Primary: l.primary,
		Worktree: l.worktree, GitDir: l.gitDir, CommonDir: l.common, Branch: "mu/T-1"}
}

func (l layout) reviewer() Launch {
	return Launch{Role: RoleReviewer, Harness: harness.Claude, Home: l.home, Primary: l.primary,
		Worktree: l.worktree, GitDir: l.gitDir, CommonDir: l.common}
}

func (l layout) stateDir() string { return filepath.Join(l.home, "state", "T-1.pi-agent") }

func TestUnsupportedErrorNamesTheGOOS(t *testing.T) {
	got := (&UnsupportedError{GOOS: "plan9"}).Error()
	if want := "fence: no write fence implemented for GOOS plan9"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestWrapRefusesUnwrappableArgv(t *testing.T) {
	f := &Fence{profile: "(version 1)\n"}
	for _, argv := range [][]string{nil, {}, {""}, {"-p", "x"}} {
		if got, err := f.Wrap(argv); err == nil || !strings.Contains(err.Error(), "cannot wrap argv") {
			t.Errorf("Wrap(%q) = %q, %v; want a cannot-wrap refusal", argv, got, err)
		}
	}
}

func TestWrapPutsTheExactProfileBeforeTheProgram(t *testing.T) {
	f := &Fence{profile: "(version 1)\n(allow default)\n"}
	got, err := f.Wrap([]string{"/bin/echo", "a b", "-c"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{sandboxExec, "-p", f.profile, "/bin/echo", "a b", "-c"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("Wrap = %q, want %q", got, want)
	}
}

func TestSoldierBranch(t *testing.T) {
	for _, ok := range []string{"mu/T-1", "a", "feature/a.b_c-d", "mu/T-1.x"} {
		if got, err := soldierBranch(ok); err != nil || got != ok {
			t.Errorf("soldierBranch(%q) = %q, %v; want it accepted", ok, got, err)
		}
	}
	for _, bad := range []string{"", "a b", `a"b`, "a*", "a//b", "a/", "/a", "a..b", "x.lock", "x.", "a/.hidden", "a.lock/b", ".hidden", "a\\b", "a\nb"} {
		if _, err := soldierBranch(bad); err == nil || !strings.Contains(err.Error(), "invalid task branch") {
			t.Errorf("soldierBranch(%q) err = %v, want an invalid-task-branch refusal", bad, err)
		}
	}
}

func TestGitBranchFilesAreExactlyTheCommitWriteSet(t *testing.T) {
	got := gitBranchFiles("/c", "mu/T-1")
	want := []string{
		"/c/refs/heads/mu", "/c/refs/heads/mu/T-1", "/c/refs/heads/mu/T-1.lock",
		"/c/logs/refs/heads/mu", "/c/logs/refs/heads/mu/T-1", "/c/logs/refs/heads/mu/T-1.lock",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("gitBranchFiles = %q, want %q", got, want)
	}
}

func TestHarnessStateRefusals(t *testing.T) {
	if _, _, err := harnessState(harness.Codex, "/u", "/state/x"); err == nil || !strings.Contains(err.Error(), "no state profile") {
		t.Errorf("unmodeled harness: err = %v", err)
	}
	dirs, bins, err := harnessState(harness.Pi, "/u", "/state/x")
	// Never ~/.pi/agent; New allows the seat's own agent dir. Its bin dirs stay refused.
	if err != nil || len(dirs) != 0 || strings.Join(bins, " ") != "/u/.pi/agent/bin /state/x/bin" {
		t.Errorf("pi state = %q, %q, %v", dirs, bins, err)
	}
	// Never ~/.claude or ~/.claude.json: only the log cache. The seat's own
	// config dir is the launch's state dir, which New opens inside state/.
	dirs, bins, err = harnessState(harness.Claude, "/u", "/state/x")
	if err != nil || strings.Join(dirs, " ") != "/u/Library/Caches/claude-cli-nodejs" || len(bins) != 0 {
		t.Errorf("claude state = %q, %q, %v", dirs, bins, err)
	}
}

func TestPathDirDenies(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		shim string
		want string // the refusal, or "" when the list is accepted
	}{
		{"a relative entry", []string{"/usr/bin", "bin"}, "", `PATH directory "bin" is not absolute`},
		{"an empty entry", []string{""}, "", `PATH directory "" is not absolute`},
		{"a dot entry", []string{"."}, "", `PATH directory "." is not absolute`},
		{"a relative shim dir", []string{"/usr/bin"}, "git-shim/bin", `PATH directory "git-shim/bin" is not absolute`},
		{"absolute entries", []string{"/usr/bin", "/usr/local/bin/"}, "", ""},
		{"an absolute shim dir", []string{"/usr/bin", "/usr/local/bin/"}, "/usr/local/git-shim/bin", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirs, locked, removable, err := pathDirDenies(tt.in, tt.shim, nil)
			ancestors := append(locked, removable...)
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("err = %v, want %q", err, tt.want)
				}
				return
			}
			if err != nil || !slices.Contains(dirs, "/usr/local/bin") || !slices.Contains(ancestors, "/usr/local") || slices.Contains(ancestors, "/") {
				t.Fatalf("pathDirDenies = %q, %q, %v; want the cleaned dirs and their ancestors below the root", dirs, ancestors, err)
			}
		})
	}
}

// Every ancestor of the git shim dir is locked against chmod as well as
// removal, writable roots included. For any other PATH dir an ancestor that is
// itself a writable root refuses only removal, so munsu can still chmod and
// fill it, and every other ancestor is locked.
func TestPathDirDeniesSplitsAncestorsByWritableRoot(t *testing.T) {
	tests := []struct {
		name      string
		dirs      []string
		shim      string
		roots     []string
		locked    []string
		removable []string
	}{
		{
			name:      "flat roots",
			dirs:      []string{"/pdd/state/T-1.pi-agent/bin", "/pdd/work/node_modules/.bin", "/pdd/other/bin"},
			shim:      "/pdd/git-shim/bin",
			roots:     []string{"/pdd/state", "/pdd/state/T-1.pi-agent", "/pdd/work"},
			locked:    []string{"/pdd", "/pdd/git-shim", "/pdd/other", "/pdd/work/node_modules"},
			removable: []string{"/pdd/state", "/pdd/state/T-1.pi-agent", "/pdd/work"},
		},
		{
			name:   "a shim under nested roots locks the home and the root above it",
			shim:   "/pdd/tmp/home/git-shim/bin",
			roots:  []string{"/pdd/tmp", "/pdd/tmp/home/state"},
			locked: []string{"/pdd", "/pdd/tmp", "/pdd/tmp/home", "/pdd/tmp/home/git-shim"},
		},
		{
			name:      "another PATH dir under the same nested roots keeps the inner root unlink-only",
			dirs:      []string{"/pdd/tmp/home/state/T-1.pi-agent/bin"},
			shim:      "/pdd/tmp/home/git-shim/bin",
			roots:     []string{"/pdd/tmp", "/pdd/tmp/home/state"},
			locked:    []string{"/pdd", "/pdd/tmp", "/pdd/tmp/home", "/pdd/tmp/home/git-shim", "/pdd/tmp/home/state/T-1.pi-agent"},
			removable: []string{"/pdd/tmp/home/state"},
		},
		{
			name:      "without a shim dir a root above a PATH dir stays unlink-only",
			dirs:      []string{"/pdd/tmp/home/state/x/bin"},
			roots:     []string{"/pdd/tmp", "/pdd/tmp/home/state"},
			locked:    []string{"/pdd", "/pdd/tmp/home", "/pdd/tmp/home/state/x"},
			removable: []string{"/pdd/tmp", "/pdd/tmp/home/state"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, locked, removable, err := pathDirDenies(tt.dirs, tt.shim, tt.roots)
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(locked)
			slices.Sort(removable)
			if !slices.Equal(locked, tt.locked) {
				t.Errorf("locked = %q, want %q", locked, tt.locked)
			}
			if !slices.Equal(removable, tt.removable) {
				t.Errorf("removable = %q, want %q", removable, tt.removable)
			}
		})
	}
}

func TestResolvePathRefusals(t *testing.T) {
	root := t.TempDir()
	for name, p := range map[string]string{
		"relative":          "rel/path",
		"empty":             "",
		"unclean":           root + "/a/../b",
		"trailing slash":    root + "/",
		"control character": root + "/a\x01b",
		"delete character":  root + "/a\x7fb",
		"root":              "/",
	} {
		if got, err := resolvePath(p, false); err == nil {
			t.Errorf("%s: resolvePath(%q) = %q, want a refusal", name, p, got)
		}
	}
	if _, err := resolvePath(filepath.Join(root, "missing"), true); err == nil {
		t.Error("a missing path resolved though it must exist")
	}
	if got, err := resolvePath("/fence-no-such-top-dir", false); err == nil {
		t.Errorf("a missing top-level path resolved through the filesystem root: %q", got)
	}
	if _, err := resolvePath(filepath.Join(root, "missing", "deeper"), false); err != nil {
		t.Errorf("a missing path resolved through its nearest ancestor failed: %v", err)
	}
}

func TestResolveLaunchResolvesSymlinks(t *testing.T) {
	l := newLayout(t)
	link := filepath.Join(l.root, "primary-link")
	if err := os.Symlink(l.primary, link); err != nil {
		t.Fatal(err)
	}
	launch := l.soldier()
	launch.Primary = link
	p, err := resolveLaunch(launch)
	if err != nil || p.primary != l.primary {
		t.Fatalf("primary = %q, %v; want the symlink target %q", p.primary, err, l.primary)
	}
}

func TestResolveLaunchRefusals(t *testing.T) {
	l := newLayout(t)
	file := filepath.Join(l.root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// bare has a common dir outside the primary, so a worktree can overlap one
	// of them and not the other.
	bare := filepath.Join(l.root, "common.git")
	bareGit := filepath.Join(bare, "worktrees", "wt")
	if err := os.MkdirAll(bareGit, 0o755); err != nil {
		t.Fatal(err)
	}
	inPrimary := filepath.Join(l.primary, "sub")
	inBare := filepath.Join(bare, "wt")
	for _, d := range []string{inPrimary, inBare} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setBare := func(x *Launch) { x.CommonDir, x.GitDir = bare, bareGit }

	tests := []struct {
		name string
		mut  func(*Launch)
		want string
	}{
		{"relative primary", func(x *Launch) { x.Primary = "primary" }, "fence: primary checkout: path"},
		{"missing primary", func(x *Launch) { x.Primary = filepath.Join(l.root, "nope") }, "fence: primary checkout:"},
		{"primary is a file", func(x *Launch) { x.Primary = file }, "primary checkout " + file + " is not a directory"},
		{"relative worktree", func(x *Launch) { x.Worktree = "wt" }, "fence: worktree: path"},
		{"worktree is a file", func(x *Launch) { x.Worktree = file }, "worktree " + file + " is not a directory"},
		{"relative git dir", func(x *Launch) { x.GitDir = "g" }, "fence: git dir: path"},
		{"git dir is a file", func(x *Launch) { x.GitDir = file }, "git dir " + file + " is not a directory"},
		{"relative common dir", func(x *Launch) { x.CommonDir = "c" }, "fence: git common dir: path"},
		{"common dir is a file", func(x *Launch) { x.CommonDir = file }, "git common dir " + file + " is not a directory"},
		{"soldier without a home", func(x *Launch) { x.Home = "" }, "fence: home: path"},
		{"soldier home is a file", func(x *Launch) { x.Home = file }, "home " + file + " is not a directory"},
		{"soldier home is missing", func(x *Launch) { x.Home = filepath.Join(l.root, "nope") }, "fence: home:"},
		{"relative file", func(x *Launch) { x.Files = []string{"verdict.json"} }, "fence: file: path"},
		{"git dir outside the common dir", func(x *Launch) { x.GitDir = l.worktree }, "is not a linked worktree dir under"},
		{"git dir is the worktrees dir itself", func(x *Launch) { x.GitDir = filepath.Join(l.common, "worktrees") }, "is not a linked worktree dir under"},
		{"worktree inside the primary", func(x *Launch) { x.Worktree = inPrimary }, "overlaps the primary checkout"},
		{"worktree contains the primary", func(x *Launch) { x.Worktree = l.root }, "overlaps the primary checkout"},
		{"worktree inside the common dir only", func(x *Launch) { setBare(x); x.Worktree = inBare }, "overlaps the primary checkout"},
		{"worktree contains the common dir only", func(x *Launch) { setBare(x); x.Worktree = bare }, "overlaps the primary checkout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launch := l.soldier()
			tt.mut(&launch)
			if _, err := resolveLaunch(launch); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("resolveLaunch err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
	if overlaps(inBare, l.primary) || overlaps(inPrimary, bare) {
		t.Fatal("overlap fixtures must each touch only one of the primary and the common dir")
	}

	t.Run("a reviewer has no task branch", func(t *testing.T) {
		launch := l.reviewer()
		launch.Branch = "mu/T-1"
		if _, err := resolveLaunch(launch); err == nil || !strings.Contains(err.Error(), "a reviewer launch has no task branch") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a reviewer needs no home and no linked git dir", func(t *testing.T) {
		launch := l.reviewer()
		launch.Home = ""
		launch.GitDir = l.worktree
		if p, err := resolveLaunch(launch); err != nil || p.home != "" {
			t.Fatalf("resolveLaunch = %+v, %v; want a home-less reviewer accepted", p, err)
		}
	})
	t.Run("a reviewer's named home must exist", func(t *testing.T) {
		launch := l.reviewer()
		launch.Home = filepath.Join(l.root, "nope")
		if _, err := resolveLaunch(launch); err == nil || !strings.Contains(err.Error(), "fence: home:") {
			t.Fatalf("err = %v", err)
		}
	})
}

// A seat can replace its own file with a link. The next launch's Files entry
// must still name the literal path, never the link's target (here the project
// registry), so the allow rule cannot reach the target. Each role has the
// link row and the nearest valid one, a regular file at the same name. New
// refuses off darwin, so the rows read the resolved launch and the rule text
// New writes from it.
func TestFilesAreNamedLiterally(t *testing.T) {
	for _, tt := range []struct {
		name string
		role Role
		file string
		link bool
	}{
		{"a soldier's ack log that is a link to the registry", RoleSoldier, "T-1.acks", true},
		{"a soldier's ack log that is a regular file", RoleSoldier, "T-1.acks", false},
		{"a reviewer's verdict that is a link to the registry", RoleReviewer, "R-1.verdict.json", true},
		{"a reviewer's verdict that is a regular file", RoleReviewer, "R-1.verdict.json", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			l := newLayout(t)
			registry := filepath.Join(l.home, "state", "fleet-registry", "projects.json")
			if err := os.MkdirAll(filepath.Dir(registry), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(registry, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(l.home, "state", tt.file)
			plant := func() error { return os.WriteFile(file, nil, 0o600) }
			if tt.link {
				plant = func() error { return os.Symlink(registry, file) }
			}
			if err := plant(); err != nil {
				t.Fatal(err)
			}
			launch := l.soldier()
			if tt.role == RoleReviewer {
				launch = l.reviewer()
			}
			launch.Files = []string{file}
			p, err := resolveLaunch(launch)
			if err != nil {
				t.Fatalf("resolveLaunch: %v", err)
			}
			var rule strings.Builder
			allowFiles(&rule, p.files...)
			if !slices.Equal(p.files, []string{file}) || !strings.Contains(rule.String(), quote(file)) || strings.Contains(rule.String(), "projects.json") {
				t.Fatalf("files = %q, rule = %s; want only the literal %s named", p.files, rule.String(), file)
			}
		})
	}
}

// A home root that is a link escapes the profile's denies, which name it, for
// both roles (G1058, G1122). Each role has a blocked row and the nearest
// allowed one.
func TestLinkedHomeRootRefusals(t *testing.T) {
	for _, tt := range []struct {
		name   string
		role   Role
		root   string
		link   bool
		refuse bool
	}{
		{"a soldier whose state root is a link", RoleSoldier, "state", true, true},
		{"a soldier whose journal root is a link", RoleSoldier, ".journal", true, true},
		{"a soldier whose config root is a link", RoleSoldier, "config", true, true},
		// G1122 names a reviewer's four durable roots only.
		{"a reviewer whose config root is a link", RoleReviewer, "config", true, false},
		{"a soldier whose home roots are directories", RoleSoldier, "state", false, false},
		{"a reviewer whose state root is a link", RoleReviewer, "state", true, true},
		{"a reviewer whose data root is a link", RoleReviewer, "data", true, true},
		{"a reviewer whose home roots are directories", RoleReviewer, "state", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			l := newLayout(t)
			root := filepath.Join(l.home, tt.root)
			if tt.link {
				real := filepath.Join(l.root, tt.root+"-real")
				if err := os.Mkdir(real, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(root); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, root); err != nil {
					t.Fatal(err)
				}
			}
			launch := l.soldier()
			if tt.role == RoleReviewer {
				launch = l.reviewer()
			}
			_, err := resolveLaunch(launch)
			if !tt.refuse && err != nil {
				t.Fatalf("resolveLaunch: %v", err)
			}
			if tt.refuse && (err == nil || !strings.Contains(err.Error(), "home root "+root+" is a symlink or not a directory")) {
				t.Fatalf("err = %v, want the linked %s refused", err, tt.root)
			}
		})
	}
}

func TestResolveStateDirRefusals(t *testing.T) {
	l := newLayout(t)
	state := filepath.Join(l.home, "state")
	tests := []struct {
		name string
		mut  func(*Launch)
		want string
	}{
		{"relative", func(x *Launch) { x.HarnessStateDir = "state/T" }, "fence: harness state dir: path"},
		{"the home's state root itself", func(x *Launch) { x.HarnessStateDir = state }, "is not a proper subdirectory"},
		{"outside home/state", func(x *Launch) { x.HarnessStateDir = filepath.Join(l.home, "data", "T.pi-agent") }, "is not a proper subdirectory"},
		{"sibling sharing the state prefix", func(x *Launch) { x.HarnessStateDir = state + "-x" }, "is not a proper subdirectory"},
		// G1086: a link would be resolved and allowed wherever it points inside
		// state/; the accepted row below is its nearest valid case.
		{"a link to another state dir", func(x *Launch) {
			other := filepath.Join(state, "T-9.pi-agent")
			if err := os.Mkdir(other, 0o755); err != nil {
				t.Fatal(err)
			}
			x.HarnessStateDir = filepath.Join(state, "T-2.pi-agent")
			if err := os.Symlink(other, x.HarnessStateDir); err != nil {
				t.Fatal(err)
			}
		}, "harness state dir " + filepath.Join(state, "T-2.pi-agent") + " is a symlink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launch := l.soldier()
			launch.Harness = harness.Pi
			tt.mut(&launch)
			_, err := resolveLaunch(launch)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
	// Each protected path is relocated to contain the state dir, so only that
	// path's overlap guard can refuse. A soldier's git dir always lies inside
	// its common dir, so the git dir row is a reviewer's.
	overlapRows := []struct {
		name     string
		reviewer bool
		move     func(x *Launch, dir string)
	}{
		{"primary", false, func(x *Launch, d string) { x.Primary = d }},
		{"worktree", false, func(x *Launch, d string) { x.Worktree = d }},
		{"common dir", false, func(x *Launch, d string) { x.CommonDir = d; x.GitDir = filepath.Join(d, "worktrees", "wt") }},
		{"git dir", true, func(x *Launch, d string) { x.GitDir = d }},
	}
	for _, row := range overlapRows {
		t.Run("overlaps the "+row.name, func(t *testing.T) {
			m := newLayout(t)
			dir := filepath.Join(m.home, "state", "under")
			if err := os.MkdirAll(filepath.Join(dir, "worktrees", "wt"), 0o755); err != nil {
				t.Fatal(err)
			}
			launch := m.soldier()
			if row.reviewer {
				launch = m.reviewer()
			}
			launch.Harness = harness.Pi
			row.move(&launch, dir)
			launch.HarnessStateDir = filepath.Join(dir, "T.pi-agent")
			_, err := resolveLaunch(launch)
			if err == nil || !strings.Contains(err.Error(), "harness state dir "+launch.HarnessStateDir+" overlaps "+dir) {
				t.Fatalf("err = %v, want a state-dir overlap refusal naming %s", err, dir)
			}
		})
	}
	t.Run("a reviewer with no home has nothing to live under", func(t *testing.T) {
		launch := l.reviewer()
		launch.Harness = harness.Pi
		launch.Home = ""
		launch.HarnessStateDir = l.stateDir()
		if _, err := resolveLaunch(launch); err == nil || !strings.Contains(err.Error(), "needs the home it lives under") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a proper subdirectory is accepted and resolved", func(t *testing.T) {
		launch := l.soldier()
		launch.Harness = harness.Pi
		launch.HarnessStateDir = l.stateDir()
		p, err := resolveLaunch(launch)
		if err != nil || p.stateDir != l.stateDir() {
			t.Fatalf("stateDir = %q, %v", p.stateDir, err)
		}
	})
}

func TestResolveGateRefusals(t *testing.T) {
	l := newLayout(t)
	gateRepo := filepath.Join(l.root, "gate", "repo.git")
	if err := os.MkdirAll(gateRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(l.root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gate := func(x *Launch) { x.GateRepo = gateRepo }
	// bare keeps the common dir outside the primary so each overlaps alone.
	bare := filepath.Join(l.root, "common.git")
	bareGit := filepath.Join(bare, "worktrees", "wt")
	if err := os.MkdirAll(bareGit, 0o755); err != nil {
		t.Fatal(err)
	}
	setBare := func(x *Launch) { x.CommonDir, x.GitDir = bare, bareGit }

	t.Run("a valid gate set resolves", func(t *testing.T) {
		launch := l.soldier()
		gate(&launch)
		p, err := resolveLaunch(launch)
		if err != nil || p.gateRepo != gateRepo {
			t.Fatalf("gate = %q, %v", p.gateRepo, err)
		}
	})
	tests := []struct {
		name string
		mut  func(*Launch)
		want string
	}{
		{"a reviewer has none", func(x *Launch) { *x = l.reviewer(); gate(x) }, "a reviewer launch has no gate write set"},
		{"relative repo", func(x *Launch) { gate(x); x.GateRepo = "repo.git" }, "fence: gate repo: path"},
		{"missing repo", func(x *Launch) { gate(x); x.GateRepo = filepath.Join(l.root, "nope") }, "fence: gate repo:"},
		{"repo is a file", func(x *Launch) { gate(x); x.GateRepo = file }, "gate repo " + file + " is not a directory"},
		{"repo inside the primary", func(x *Launch) { gate(x); setBare(x); x.GateRepo = l.primary }, "gate path " + l.primary + " overlaps " + l.primary},
		{"repo containing the worktree", func(x *Launch) { gate(x); x.GateRepo = l.root }, "gate path " + l.root + " overlaps"},
	}
	for _, r := range homeRoots(l.home) {
		tests = append(tests, struct {
			name string
			mut  func(*Launch)
			want string
		}{"repo inside home root " + filepath.Base(r), func(x *Launch) { gate(x); x.GateRepo = r }, "overlaps " + r})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launch := l.soldier()
			tt.mut(&launch)
			if _, err := resolveLaunch(launch); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestGoCaches(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	cache, _ := os.UserCacheDir()
	config, _ := os.UserConfigDir()
	set := func(gocache, modcache, gopath, telemetry string) {
		t.Setenv("GOCACHE", gocache)
		t.Setenv("GOMODCACHE", modcache)
		t.Setenv("GOPATH", gopath)
		t.Setenv("GOTELEMETRYDIR", telemetry)
	}
	join := func(dirs []string) string { return strings.Join(dirs, "\n") }

	set("", "", "", "")
	def := []string{
		filepath.Join(cache, "go-build"), filepath.Join(home, "go", "pkg", "mod"),
		filepath.Join(home, "go", "pkg"), filepath.Join(config, "go", "telemetry"),
	}
	if got := goCaches(home); join(got) != join(def) {
		t.Fatalf("defaults = %q, want %q", got, def)
	}
	t.Run("off counts as unset", func(t *testing.T) {
		set("off", "off", "", "off")
		if got := goCaches(home); join(got) != join(def) {
			t.Fatalf("got %q, want the defaults %q", got, def)
		}
	})
	t.Run("the environment wins and GOPATH's first entry is used", func(t *testing.T) {
		set(home+"/gc", home+"/gm", home+"/gp1"+string(os.PathListSeparator)+home+"/gp2", home+"/gt")
		want := []string{home + "/gc", home + "/gm", home + "/gp1/pkg", home + "/gt"}
		if got := goCaches(home); join(got) != join(want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	t.Run("a relative path is dropped, not resolved against the cwd", func(t *testing.T) {
		set("rel/gc", home+"/gm", "", home+"/gt")
		got := goCaches(home)
		for _, d := range got {
			if strings.Contains(d, "rel/gc") {
				t.Fatalf("relative GOCACHE survived: %q", got)
			}
		}
		if len(got) != 3 {
			t.Fatalf("got %q, want the other three roots", got)
		}
	})
}

func TestNewRefusesAnUnknownRole(t *testing.T) {
	l := newLayout(t)
	for _, role := range []Role{"", "captain"} {
		launch := l.soldier()
		launch.Role = role
		f, err := New(launch)
		if f != nil || err == nil || err.Error() != fmt.Sprintf("fence: unknown role %q", role) {
			t.Fatalf("New(role %q) = %v, %v; want the unknown role refusal", role, f, err)
		}
	}
}

func TestCheckSeatRootsRefusals(t *testing.T) {
	protected := []string{"/p/primary", "/p/common"}
	for _, tc := range []struct {
		name  string
		roots []string
		want  string
	}{
		{"a root inside a protected path", []string{"/s/state", "/p/primary/v.json"}, "fence: writable root /p/primary/v.json overlaps protected path /p/primary"},
		{"a root containing a protected path", []string{"/p"}, "fence: writable root /p overlaps protected path /p/primary"},
		{"disjoint roots", []string{"/s/state", "/p/other"}, ""},
	} {
		err := checkSeatRoots(tc.roots, protected)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
			t.Fatalf("%s: checkSeatRoots = %v; want %q", tc.name, err, tc.want)
		}
	}
}

// rootsEnv points the user home and PATH at fresh directories under root so the
// validator's home-relative denylist and PATH rule are the test's own.
func rootsEnv(t *testing.T, root string) (userHome, pathDir string) {
	t.Helper()
	userHome, pathDir = filepath.Join(root, "userhome"), filepath.Join(root, "pathbin")
	for _, d := range []string{userHome, pathDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("PATH", pathDir)
	return userHome, pathDir
}

func mkRoot(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidateWritableRootsRefusals(t *testing.T) {
	l := newLayout(t)
	userHome, pathDir := rootsEnv(t, l.root)
	cache := mkRoot(t, filepath.Join(l.root, "npm-cache"))
	file := filepath.Join(l.root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	other := mkRoot(t, filepath.Join(l.root, "other-project"))
	munsuHome := mkRoot(t, filepath.Join(l.root, "mh", "home"))
	facts := RootFacts{Home: munsuHome, Checkouts: []string{l.primary, other}}

	t.Run("a clear root resolves", func(t *testing.T) {
		got, err := ValidateWritableRoots([]string{cache}, facts)
		if err != nil || len(got) != 1 || got[0] != cache {
			t.Fatalf("roots = %q, %v", got, err)
		}
	})
	// A case variant names the same directory on a case-insensitive filesystem
	// (APFS, NTFS). On a case-sensitive host the variant does not exist, so its
	// rows only prove it is never accepted; no lane runs the alias there.
	sshDir := mkRoot(t, filepath.Join(userHome, ".ssh"))
	mkRoot(t, filepath.Join(userHome, ".sshcache"))
	mkRoot(t, filepath.Join(userHome, ".npm"))
	homeVariant := filepath.Join(filepath.Dir(userHome), strings.ToUpper(filepath.Base(userHome)))
	sshVariant := filepath.Join(userHome, ".SSH")
	wantHomeVariant, wantSSHVariant := "is the user home or an ancestor of it", "overlaps protected path "+sshDir
	if _, err := os.Stat(sshVariant); err != nil {
		wantHomeVariant, wantSSHVariant = "fence-writable-roots: "+homeVariant+":", "fence-writable-roots: "+sshVariant+":"
	}
	tests := []struct{ name, root, want string }{ // an empty want must be accepted
		{"relative", "npm-cache", "is not absolute and clean"},
		{"a case variant of the user home", homeVariant, wantHomeVariant},
		{"a case variant of the ssh keys", sshVariant, wantSSHVariant},
		{"a sibling of the ssh keys with another name", filepath.Join(userHome, ".sshcache"), ""},
		{"docker registry auth", filepath.Join(userHome, ".docker"), "overlaps protected path " + filepath.Join(userHome, ".docker")},
		{"aws credentials", filepath.Join(userHome, ".aws"), "overlaps protected path " + filepath.Join(userHome, ".aws")},
		{"kube config", filepath.Join(userHome, ".kube"), "overlaps protected path " + filepath.Join(userHome, ".kube")},
		{"azure credentials", filepath.Join(userHome, ".azure"), "overlaps protected path " + filepath.Join(userHome, ".azure")},
		{"the password store", filepath.Join(userHome, ".password-store"), "overlaps protected path " + filepath.Join(userHome, ".password-store")},
		{"a package cache beside them", filepath.Join(userHome, ".npm"), ""},
		{"the filesystem root", string(filepath.Separator), "path is the filesystem root"},
		{"missing", filepath.Join(l.root, "absent"), "fence-writable-roots: " + filepath.Join(l.root, "absent") + ":"},
		{"a file", file, file + " is not a directory"},
		{"the user home", userHome, "is the user home or an ancestor of it"},
		{"an ancestor of the user home", l.root, "is the user home or an ancestor of it"},
		{"inside the munsu home", filepath.Join(munsuHome, "state"), "overlaps protected path " + munsuHome},
		{"containing the munsu home", filepath.Dir(munsuHome), "overlaps protected path " + munsuHome},
		{"inside a checkout", filepath.Join(l.primary, "node_modules"), "overlaps protected path " + l.primary},
		{"a registered project's checkout", other, "overlaps protected path " + other},
		{"user ssh keys", filepath.Join(userHome, ".ssh"), "overlaps protected path " + filepath.Join(userHome, ".ssh")},
		{"a claude state dir", filepath.Join(userHome, ".claude-work"), "overlaps protected path " + filepath.Join(userHome, ".claude-work")},
		{"a launch agent dir", filepath.Join(userHome, "Library", "LaunchAgents", "x"), "overlaps protected path"},
		{"a PATH entry", filepath.Join(pathDir, "sub"), "overlaps protected path " + pathDir},
		{"a system tool dir", "/usr/local/share", "overlaps protected path /usr/local"},
	}
	if err := os.MkdirAll(filepath.Join(userHome, ".claude-work"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		if !filepath.IsAbs(tt.root) && tt.name != "relative" {
			continue // a unix-style absolute path is not absolute on this OS
		}
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateWritableRoots([]string{tt.root}, facts)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("err = %v, want %s accepted", err, tt.root)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestResolveLaunchWritableRoots(t *testing.T) {
	l := newLayout(t)
	rootsEnv(t, l.root)
	cache := mkRoot(t, filepath.Join(l.root, "npm-cache"))
	gateRepo := mkRoot(t, filepath.Join(l.root, "gate", "repo.git"))

	t.Run("a soldier's roots resolve into the launch", func(t *testing.T) {
		launch := l.soldier()
		launch.WritableRoots = []string{cache}
		p, err := resolveLaunch(launch)
		if err != nil || len(p.roots) != 1 || p.roots[0] != cache {
			t.Fatalf("roots = %q, %v", p.roots, err)
		}
	})
	tests := []struct {
		name string
		mut  func(*Launch)
		want string
	}{
		{"a reviewer has none", func(x *Launch) { *x = l.reviewer(); x.WritableRoots = []string{cache} }, "a reviewer launch has no extra writable roots"},
		{"the soldier's own worktree", func(x *Launch) { x.WritableRoots = []string{l.worktree} }, "overlaps protected path " + l.worktree},
		{"the gate repo", func(x *Launch) {
			x.GateRepo = gateRepo
			x.WritableRoots = []string{gateRepo}
		}, "overlaps protected path " + gateRepo},
		{"a registered project's checkout", func(x *Launch) { x.Checkouts = []string{cache}; x.WritableRoots = []string{cache} }, "overlaps protected path " + cache},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launch := l.soldier()
			tt.mut(&launch)
			if _, err := resolveLaunch(launch); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
