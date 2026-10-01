package fence

import (
	"fmt"
	"os"
	"path/filepath"
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
	dirs = append(dirs, homeRoots(l.home)...)
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
	if _, _, err := harnessState(harness.Claude, "/u", "/state/x"); err == nil || !strings.Contains(err.Error(), "has no per-launch state dir") {
		t.Errorf("claude with a state dir: err = %v", err)
	}
	if _, _, err := harnessState(harness.Codex, "/u", ""); err == nil || !strings.Contains(err.Error(), "no state profile") {
		t.Errorf("unmodeled harness: err = %v", err)
	}
	dirs, files, err := harnessState(harness.Pi, "/u", "/state/x")
	if err != nil || strings.Join(dirs, " ") != "/u/.pi/agent /state/x" || len(files) != 0 {
		t.Errorf("pi state = %q, %q, %v", dirs, files, err)
	}
	dirs, files, err = harnessState(harness.Claude, "/u", "")
	if err != nil || len(dirs) != 2 || strings.Join(files, " ") != "/u/.claude.json" {
		t.Errorf("claude state = %q, %q, %v", dirs, files, err)
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
	gateState := filepath.Join(l.root, "gate", "state.db")
	file := filepath.Join(l.root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gate := func(x *Launch) { x.GateRepo, x.GateState = gateRepo, gateState }
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
		if err != nil || p.gateRepo != gateRepo || p.gateState != gateState {
			t.Fatalf("gate = %q %q, %v", p.gateRepo, p.gateState, err)
		}
	})
	tests := []struct {
		name string
		mut  func(*Launch)
		want string
	}{
		{"a reviewer has none", func(x *Launch) { *x = l.reviewer(); gate(x) }, "a reviewer launch has no gate write set"},
		{"a reviewer with only a repo still has none", func(x *Launch) { *x = l.reviewer(); x.GateRepo = gateRepo }, "a reviewer launch has no gate write set"},
		{"repo without state", func(x *Launch) { x.GateRepo = gateRepo }, "set together"},
		{"state without repo", func(x *Launch) { x.GateState = gateState }, "set together"},
		{"relative repo", func(x *Launch) { gate(x); x.GateRepo = "repo.git" }, "fence: gate repo: path"},
		{"missing repo", func(x *Launch) { gate(x); x.GateRepo = filepath.Join(l.root, "nope") }, "fence: gate repo:"},
		{"repo is a file", func(x *Launch) { gate(x); x.GateRepo = file }, "gate repo " + file + " is not a directory"},
		{"relative state", func(x *Launch) { gate(x); x.GateState = "state.db" }, "fence: gate state: path"},
		{"repo inside the primary", func(x *Launch) { gate(x); setBare(x); x.GateRepo = l.primary }, "gate path " + l.primary + " overlaps " + l.primary},
		{"repo containing the worktree", func(x *Launch) { gate(x); x.GateRepo = l.root }, "gate path " + l.root + " overlaps"},
		{"state inside the worktree", func(x *Launch) { gate(x); x.GateState = filepath.Join(l.worktree, "state.db") }, "overlaps " + l.worktree},
		{"state inside the git dir", func(x *Launch) { gate(x); setBare(x); x.GateState = filepath.Join(bareGit, "state.db") }, "overlaps " + bareGit},
		{"state inside the common dir", func(x *Launch) { gate(x); setBare(x); x.GateState = filepath.Join(bare, "state.db") }, "overlaps " + bare},
	}
	for _, r := range homeRoots(l.home) {
		tests = append(tests, struct {
			name string
			mut  func(*Launch)
			want string
		}{"repo inside home root " + filepath.Base(r), func(x *Launch) { gate(x); x.GateRepo = r }, "overlaps " + r})
		tests = append(tests, struct {
			name string
			mut  func(*Launch)
			want string
		}{"state inside home root " + filepath.Base(r), func(x *Launch) { gate(x); x.GateState = filepath.Join(r, "state.db") }, "overlaps " + r})
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
