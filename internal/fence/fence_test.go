package fence

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	return Launch{Role: RoleSoldier, Harness: harness.Claude, Home: l.home, UserHome: testUserHome(), Primary: l.primary,
		Worktree: l.worktree, GitDir: l.gitDir, CommonDir: l.common, Branch: "mu/T-1"}
}

func (l layout) reviewer() Launch {
	return Launch{Role: RoleReviewer, Harness: harness.Claude, Home: l.home, UserHome: testUserHome(), Primary: l.primary,
		Worktree: l.worktree, GitDir: l.gitDir, CommonDir: l.common}
}

// testUserHome is the Human's home as the test process sees it; the fence
// takes it from its Launch, never from the environment.
func testUserHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return home
}

func (l layout) stateDir() string { return filepath.Join(l.home, "state", "T-1.pi-agent") }

// captain is a captain launch from l.home under a General home beside it, as
// Launch makes it: its docs dir made, its seat socket and pi agent dir named.
func (l layout) captain(t *testing.T) Launch {
	t.Helper()
	parent := filepath.Join(l.root, "general")
	for _, d := range []string{filepath.Join(parent, "state"), filepath.Join(l.home, "state", CaptainDocsDir)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Launch{Role: RoleCaptain, Harness: harness.Pi, Home: l.home, ParentHome: parent, UserHome: testUserHome(),
		Socket: filepath.Join(l.home, "state", ".captain-seat.sock"), HarnessStateDir: filepath.Join(l.home, "state", "captain.pi-agent"),
		GitDir: l.gitDir, CommonDir: l.common}
}

// Each captain refusal has its blocked row; the allowed row is the launch as
// Launch makes it, and as it makes it for a home that is no git checkout.
func TestResolveCaptainRefusals(t *testing.T) {
	l := newLayout(t)
	file := filepath.Join(l.root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(l.home, "state", CaptainDocsDir)
	for _, tt := range []struct {
		name string
		mut  func(*Launch)
		want string
	}{
		{"as Launch makes it", func(*Launch) {}, ""},
		{"a home that is no git checkout", func(x *Launch) { x.GitDir, x.CommonDir = "", "" }, ""},
		{"a soldier's worktree", func(x *Launch) { x.Worktree = l.worktree }, "a captain launch has no worktree"},
		{"a soldier's branch", func(x *Launch) { x.Branch = "mu/T-1" }, "a captain launch has no worktree"},
		{"a home that is a file", func(x *Launch) { x.Home = file }, "home " + file + " is not a directory"},
		{"a General home that is a file", func(x *Launch) { x.ParentHome = file }, "General home " + file + " is not a directory"},
		{"the General home as its own home", func(x *Launch) { x.ParentHome = x.Home }, "is its General home"},
		{"a git common dir that is a file", func(x *Launch) { x.CommonDir = file }, "git common dir " + file + " is not a directory"},
		{"a harness state dir outside state/", func(x *Launch) { x.HarnessStateDir = filepath.Join(l.home, "pi") }, "is not a proper subdirectory"},
		{"a relative seat socket", func(x *Launch) { x.Socket = ".captain-seat.sock" }, "fence: seat socket: path"},
		{"a seat socket outside state/", func(x *Launch) { x.Socket = filepath.Join(l.home, ".captain-seat.sock") }, "is not directly under"},
		{"a seat socket deeper in state/", func(x *Launch) { x.Socket = filepath.Join(docs, "s.sock") }, "is not directly under"},
		{"no docs dir", func(x *Launch) { os.Remove(docs) }, "captain docs dir " + docs + " is missing"},
		{"a docs dir that is a link", func(x *Launch) { os.Remove(docs); os.Symlink(l.root, docs) }, "captain docs dir " + docs + " is missing, a symlink"},
		// Last: the General home keeps the link.
		{"a General home whose state/ is a link", func(x *Launch) {
			moved := filepath.Join(l.root, "moved-state")
			os.Rename(filepath.Join(x.ParentHome, "state"), moved)
			os.Symlink(moved, filepath.Join(x.ParentHome, "state"))
		}, "is a symlink or not a directory"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			os.Remove(docs)
			launch := l.captain(t)
			tt.mut(&launch)
			c, err := resolveCaptain(launch)
			if tt.want == "" {
				if err != nil || c.socket != launch.Socket || c.docs != docs {
					t.Fatalf("resolveCaptain = %+v, %v; want it accepted", c, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("resolveCaptain err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

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

func TestGitBranchRefFilesAreExactlyTheRefWriteSet(t *testing.T) {
	// The ref and its lock only: the namespace dir is pre-created at spawn and
	// refused, and the branch reflog under logs/ is append-only (allowData).
	got := gitBranchRefFiles("/c", "mu/T-1")
	want := []string{"/c/refs/heads/mu/T-1", "/c/refs/heads/mu/T-1.lock"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("gitBranchRefFiles = %q, want %q", got, want)
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
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A PATH link that climbs out of a directory it names: the seat could
	// replace that directory, which no deny names.
	climb := filepath.Join(filepath.Dir(file), "phys")
	if err := os.Mkdir(filepath.Join(filepath.Dir(file), "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/../x", climb); err != nil {
		t.Fatal(err)
	}
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
		{"a dot-dot entry", []string{"/usr/bin", "/usr/../usr/bin"}, "", "is not absolute and clean"},
		{"a dot entry inside a path", []string{"/usr/./bin"}, "", "is not absolute and clean"},
		{"a control character under a missing top-level dir", []string{"/fence-no-such-top-dir/a\x01b"}, "", "control character"},
		{"a link whose target climbs out of a name", []string{"/usr/bin", climb}, "", "past its first name"},
		{"an entry that does not resolve", []string{"/usr/bin", filepath.Join(file, "bin")}, "", "PATH directory " + filepath.Join(file, "bin")},
		{"absolute entries", []string{"/usr/bin", "/usr/local/bin/", "/fence-no-such-top-dir/bin"}, "", ""},
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

// A TMUX_TMPDIR that resolvePath refuses (relative) is left out of the socket
// dirs without error; the nearest valid one, absolute, adds its tmux-<uid> dir
// to the dirs kept from removal.
func TestKeptSocketDirsTMUXTMPDIR(t *testing.T) {
	tmux := "tmux-" + strconv.Itoa(os.Getuid())
	abs := t.TempDir()
	resolved, err := resolvePath(filepath.Join(abs, tmux), false)
	if err != nil {
		t.Fatal(err)
	}
	base := []string{filepath.Join(socketTempDir, "cc-socks"), filepath.Join(socketTempDir, tmux)}
	for _, tt := range []struct {
		name, env string
		want      []string
	}{
		{"a relative TMUX_TMPDIR is left out", filepath.Join("rel", "dir"), base},
		{"an absolute TMUX_TMPDIR adds its tmux dir", abs, append(slices.Clone(base), resolved)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TMUX_TMPDIR", tt.env)
			if keep := keptSocketDirs(); !slices.Equal(keep, tt.want) {
				t.Fatalf("kept dirs = %q, want %q", keep, tt.want)
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
	// A dangling link whose walk walkPath refuses.
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/../x", filepath.Join(root, "phys")); err != nil {
		t.Fatal(err)
	}
	if got, err := resolvePath(filepath.Join(root, "phys"), false); err == nil || !strings.Contains(err.Error(), "past its first name") {
		t.Errorf("resolvePath through a link climbing out of a name = %q, %v; want a .. refusal", got, err)
	}
}

// walkPath resolves as the kernel does and names every link on the way, each
// of which a seat could replace to move where a write through the path lands.
// A .. past a link target's first name, a control character in a target, a
// path resolving to the root, or a chain past the hop bound, is refused.
func TestWalkPathFollowsEveryLink(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	at := func(names ...string) []string {
		var out []string
		for _, n := range names {
			out = append(out, filepath.Join(root, n))
		}
		return out
	}
	link := func(name, target string) string {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(root, name)
	}
	for _, d := range []string{"ereal", filepath.Join("rdir", "bin"), "sub"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link("e2", "ereal")
	link("d2", "dmissing")
	link("ldir", filepath.Join(root, "rdir"))
	// c0 -> c1 -> ... -> c<maxLinkHops> -> c<maxLinkHops+1>, which is missing.
	for i := 0; i <= maxLinkHops; i++ {
		link("c"+strconv.Itoa(i), "c"+strconv.Itoa(i+1))
	}
	for _, tt := range []struct {
		name, path, want string
		links            []string // nil: not checked
		wantErr          string
	}{
		{"a dangling relative target", link("rel", "a/b"), filepath.Join(root, "a", "b"), at("rel"), ""},
		{"a leading .. in the target", link("up", "../"+filepath.Base(root)+"-up"), root + "-up", at("up"), ""},
		{"an existing chain", link("e1", "e2"), filepath.Join(root, "ereal"), at("e1", "e2"), ""},
		{"a dangling chain", link("d1", "d2"), filepath.Join(root, "dmissing"), at("d1", "d2"), ""},
		{"a link into a symlinked dir", link("s", filepath.Join(root, "ldir", "bin")), filepath.Join(root, "rdir", "bin"), at("s", "ldir"), ""},
		{"a .. past the target's first name", link("phys", "sub/../x"), "", nil, "past its first name"},
		{"a control character in a target", link("clink", "a\x01b"), "", nil, "control character"},
		{"a link to the root", link("rlink", "/"), "", nil, "resolves to the filesystem root"},
		{"a climb to the root", link("ulink", strings.Repeat("../", strings.Count(root, "/"))), "", nil, "resolves to the filesystem root"},
		{"a chain at the hop bound", filepath.Join(root, "c1"), filepath.Join(root, "c"+strconv.Itoa(maxLinkHops+1)), nil, ""},
		{"a chain past the hop bound", filepath.Join(root, "c0"), "", nil, "too many links"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, links, _, err := walkPath(tt.path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("walkPath = %q, %v; want an error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("walkPath = %q, %v; want %q", got, err, tt.want)
			}
			if tt.links != nil && !slices.Equal(links, tt.links) {
				t.Fatalf("links = %q, want %q", links, tt.links)
			}
		})
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
		{"a remote reflog outside the remote-tracking logs", func(x *Launch) { x.RemoteReflogs = []string{filepath.Join(l.common, "logs", "refs", "heads", "main")} }, "is not under"},
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
	t.Run("a reviewer has no remote reflogs", func(t *testing.T) {
		launch := l.reviewer()
		launch.RemoteReflogs = []string{filepath.Join(l.common, "logs", "refs", "remotes", "origin", "main")}
		if _, err := resolveLaunch(launch); err == nil || !strings.Contains(err.Error(), "fence: remote reflogs are a soldier's only") {
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

func TestNewRefusesAnUnknownRole(t *testing.T) {
	l := newLayout(t)
	for _, role := range []Role{"", "general"} {
		launch := l.soldier()
		launch.Role = role
		f, err := New(launch)
		if f != nil || err == nil || err.Error() != fmt.Sprintf("fence: unknown role %q", role) {
			t.Fatalf("New(role %q) = %v, %v; want the unknown role refusal", role, f, err)
		}
	}
}

// TestNewRefusesARelativeUserHome runs on every GOOS: the guard precedes the
// platform check, so the refused row is refused here, not by Supported.
func TestNewRefusesARelativeUserHome(t *testing.T) {
	l := newLayout(t)
	for _, tt := range []struct {
		name, home string
		refused    bool
	}{
		{"a relative user home is refused", "relative/home", true},
		{"an absolute user home is not refused by this guard", filepath.Join(l.root, "uhome"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			launch := l.soldier()
			launch.UserHome = tt.home
			_, err := New(launch)
			refused := err != nil && strings.Contains(err.Error(), "fence: user home must be absolute")
			if refused != tt.refused {
				t.Fatalf("New(UserHome %q) err = %v; want refused=%v", tt.home, err, tt.refused)
			}
		})
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
