package fence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
)

// Fence is one launch's validated profile. Build it with New; it is immutable.
type Fence struct {
	role    Role
	harness string
	profile string
	digest  string
	refuse  []string // directories in which a write must be refused
	roots   []string // the launch's resolved extra writable roots
}

var branchPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// launchPaths is a Launch with every path resolved and validated.
type launchPaths struct {
	home, primary, worktree, gitDir, commonDir string
	stateDir, gateRepo, gateState              string
	files                                      []string
	roots                                      []string
}

// New validates the launch facts and builds the profile. Anything malformed,
// missing, or that would widen the writable set over the primary checkout or the
// git common dir is refused; New never falls back to a looser profile.
func New(l Launch) (*Fence, error) {
	if l.Role != RoleSoldier && l.Role != RoleReviewer {
		return nil, fmt.Errorf("fence: unknown role %q", l.Role)
	}
	if err := Supported(); err != nil {
		return nil, err
	}
	p, err := resolveLaunch(l)
	if err != nil {
		return nil, err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("fence: resolving user home: %w", err)
	}
	userHome, err = resolvePath(userHome, true)
	if err != nil {
		return nil, fmt.Errorf("fence: user home: %w", err)
	}
	dirs, binDirs, err := harnessState(l.Harness, userHome, p.stateDir)
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, goCaches(userHome)...)
	dirs = append(dirs, p.roots...)

	protected := []string{p.primary, p.commonDir, p.gitDir}
	if l.Role == RoleReviewer {
		protected = append(protected, p.worktree)
	}
	if err := checkSeatRoots(p.files, protected); err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	denySessionSockets(&b, sessionSocketDirs())
	// Later rules win in a sandbox profile: broad allows first, then the
	// protected denies, then the seat's own roots and git carve-outs.
	var roots []string // every subpath this profile makes writable
	allowRoots := func(paths ...string) {
		roots = append(roots, paths...)
		allow(&b, "subpath", paths...)
	}
	allowRoots(append([]string{"/private/tmp", "/private/var/folders", "/dev"}, dirs...)...)
	deny(&b, "subpath", protected...)
	if p.home != "" {
		// No seat has a home root, even under a temp root: a soldier writes only
		// its own files (Files, allowed below) and its harness state dir (allowed
		// back further down). The subpath deny also refuses renaming a root
		// away, so a seat cannot replace state/ with a tree of its own.
		// Human words (domain.Words): grantor the Human, channel
		// supervisor-relay:dialog, words=selected, G1058: "A soldier's write
		// fence lets it write only its own task's status, report and request
		// files; it can never write another task's documents, any review
		// verdict, delivery authorization or gate record, or the fleet
		// registries (projects.json, captains.json)."
		deny(&b, "subpath", homeRoots(p.home)...)
	}
	allowFiles(&b, p.files...)
	if p.gateRepo != "" {
		allowRoots(p.gateRepo)
		allowFiles(&b, p.gateState, p.gateState+"-wal", p.gateState+"-shm", p.gateState+"-journal")
	}
	if l.Role == RoleSoldier {
		branch, err := soldierBranch(l.Branch)
		if err != nil {
			return nil, err
		}
		allowRoots(p.worktree, p.gitDir, filepath.Join(p.commonDir, "objects"),
			filepath.Join(p.commonDir, "refs", "remotes"), filepath.Join(p.commonDir, "logs", "refs", "remotes"))
		allow(&b, "literal", gitBranchFiles(p.commonDir, branch)...)
		// A worktree config can set hooks and commands that later run unfenced.
		deny(&b, "literal", filepath.Join(p.gitDir, "config.worktree"))
	}
	if p.home != "" {
		// After every allow, so they hold wherever the home lives, a temp root
		// included. The unfenced munsu builds what a claude or pi process loads
		// in the seat build dir. Every task's seat dir in state/ is refused, so
		// a seat cannot plant or change the config another task's seat (its own
		// reviewer included) starts from; only its own dir is allowed back.
		// Human words (domain.Words): grantor the Human, channel
		// supervisor-relay:dialog, words=selected, G1085 (quoted at
		// harnessState); Supervisor decision G1099.
		deny(&b, "subpath", harness.SeatBuildDir(p.home))
		denySeatDirs(&b, filepath.Join(p.home, "state"))
		if p.stateDir != "" {
			allowRoots(p.stateDir)
		}
	}
	// Last, so nothing above reopens it: a seat's PATH directories, and every
	// ancestor's own name so a directory cannot be renamed or removed away and
	// replaced. An ancestor of a PATH dir that is not itself a writable root is
	// locked: chmod, unlink and rename are all refused, because a mode of 000
	// would take the PATH dir off the seat's PATH. A writable root refuses only
	// unlink (a rename is an unlink of the source name), because the seat fills
	// it (a pi seat's agent dir). The git shim dir is stricter: every ancestor
	// of it, roots and temp roots included, is locked, so a home under a
	// writable root cannot be chmod'ed to hide the shim. That holds only while
	// munsu never chmods, renames or removes a directory above the shim (the
	// home, a temp root); it is wrong the moment munsu chmods a directory above
	// the shim. No lane runs this off darwin; the darwin fence lane covers it.
	pathDirs, locked, removable, err := pathDirDenies(append(append([]string{}, l.PathDirs...), binDirs...), l.ShimDir, roots)
	if err != nil {
		return nil, err
	}
	deny(&b, "subpath", pathDirs...)
	deny(&b, "literal", locked...)
	denyUnlink(&b, removable...)

	// The probe proves the home roots refused: every soldier root (G1058,
	// quoted above), and a reviewer's state/.
	refuse := []string{p.primary, p.commonDir}
	if l.Role == RoleReviewer {
		refuse = append(refuse, p.worktree, p.gitDir)
		if p.home != "" {
			refuse = append(refuse, filepath.Join(p.home, "state"))
		}
	} else {
		refuse = append(refuse, homeRoots(p.home)...)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return &Fence{
		role: l.Role, harness: l.Harness,
		profile: b.String(), digest: hex.EncodeToString(sum[:]), refuse: dedupe(refuse), roots: p.roots,
	}, nil
}

// pathDirDenies returns the PATH directories (and the shim dir) as given and
// symlink-resolved, and the names of all their ancestors split into locked and
// removable. Every ancestor of the shim dir is locked. For any other PATH dir
// an ancestor that is itself a writable root is removable (outside every root
// the profile already refuses it too), and every other ancestor is locked. A
// relative or empty entry names the seat's working directory, which the fence
// cannot refuse, so it is an error: the caller keeps such entries off the
// seat's PATH.
func pathDirDenies(pathDirs []string, shim string, roots []string) (dirs, locked, removable []string, err error) {
	expand := func(in []string) (out []string, err error) {
		for _, d := range in {
			if !filepath.IsAbs(d) {
				return nil, fmt.Errorf("fence: PATH directory %q is not absolute", d)
			}
			d = filepath.Clean(d)
			out = append(out, d)
			if r, err := resolvePath(d, false); err == nil {
				out = append(out, r)
			}
		}
		return dedupe(out), nil
	}
	ancestorsOf := func(in []string) (out []string) {
		for _, d := range in {
			for a := filepath.Dir(d); a != filepath.Dir(a); a = filepath.Dir(a) {
				out = append(out, a)
			}
		}
		return dedupe(out)
	}
	if dirs, err = expand(pathDirs); err != nil {
		return nil, nil, nil, err
	}
	var shimDirs []string
	if shim != "" {
		if shimDirs, err = expand([]string{shim}); err != nil {
			return nil, nil, nil, err
		}
	}
	dirs = dedupe(append(dirs, shimDirs...))
	shimAncestors := ancestorsOf(shimDirs)
	for _, a := range ancestorsOf(dirs) {
		if slices.Contains(roots, a) && !slices.Contains(shimAncestors, a) {
			removable = append(removable, a)
		} else {
			locked = append(locked, a)
		}
	}
	return dirs, locked, removable, nil
}

func resolveLaunch(l Launch) (launchPaths, error) {
	var p launchPaths
	for _, f := range []struct {
		name string
		in   string
		out  *string
	}{
		{"primary checkout", l.Primary, &p.primary},
		{"worktree", l.Worktree, &p.worktree}, {"git dir", l.GitDir, &p.gitDir},
		{"git common dir", l.CommonDir, &p.commonDir},
	} {
		r, err := resolvePath(f.in, true)
		if err != nil {
			return p, fmt.Errorf("fence: %s: %w", f.name, err)
		}
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			return p, fmt.Errorf("fence: %s %s is not a directory", f.name, r)
		}
		*f.out = r
	}
	if l.Role == RoleSoldier || l.Home != "" {
		r, err := resolvePath(l.Home, true)
		if err != nil {
			return p, fmt.Errorf("fence: home: %w", err)
		}
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			return p, fmt.Errorf("fence: home %s is not a directory", r)
		}
		p.home = r
	}
	// A Files entry's parent is resolved and its leaf kept literal: a seat can
	// replace its own file with a link, and the allow must never name the
	// link's target. A write through the link reaches the target, which the
	// sandbox checks on its own.
	for _, f := range l.Files {
		if err := lexicalPath(f); err != nil {
			return p, fmt.Errorf("fence: file: %w", err)
		}
		dir, err := resolvePath(filepath.Dir(f), false)
		if err != nil {
			return p, fmt.Errorf("fence: file: %w", err)
		}
		p.files = append(p.files, filepath.Join(dir, filepath.Base(f)))
	}
	if p.home != "" {
		if err := realHomeRoots(p.home); err != nil {
			return p, err
		}
	}
	if err := resolveStateDir(l, &p); err != nil {
		return p, err
	}
	if err := resolveGate(l, &p); err != nil {
		return p, err
	}
	if l.Role == RoleSoldier {
		if wts := filepath.Join(p.commonDir, "worktrees"); p.gitDir == wts || !within(p.gitDir, wts) {
			return p, fmt.Errorf("fence: git dir %s is not a linked worktree dir under %s/worktrees", p.gitDir, p.commonDir)
		}
		if overlaps(p.worktree, p.primary) || overlaps(p.worktree, p.commonDir) {
			return p, fmt.Errorf("fence: worktree %s overlaps the primary checkout %s or git common dir %s", p.worktree, p.primary, p.commonDir)
		}
	} else if l.Branch != "" {
		return p, fmt.Errorf("fence: a reviewer launch has no task branch")
	}
	if err := resolveWritableRoots(l, &p); err != nil {
		return p, err
	}
	return p, nil
}

// resolveWritableRoots validates a soldier's extra writable roots with the one
// validator, keeping them clear of every path this launch protects.
func resolveWritableRoots(l Launch, p *launchPaths) error {
	if len(l.WritableRoots) == 0 {
		return nil
	}
	if l.Role != RoleSoldier {
		return fmt.Errorf("fence: a reviewer launch has no extra writable roots")
	}
	checkouts := append([]string{p.primary, p.worktree, p.gitDir, p.commonDir, p.gateRepo, p.gateState}, l.Checkouts...)
	roots, err := ValidateWritableRoots(l.WritableRoots, RootFacts{Home: p.home, Checkouts: checkouts})
	if err != nil {
		return err
	}
	p.roots = roots
	return nil
}

// homeRoots are the munsu home directories no seat writes beyond its own
// files: its durable state, data, journal and lock roots.
func homeRoots(home string) []string {
	return []string{filepath.Join(home, "state"), filepath.Join(home, "data"), filepath.Join(home, ".journal"), filepath.Join(home, ".lock")}
}

// realHomeRoots refuses a home root that is a symlink or not a directory. The
// profile denies each root by its name, and the sandbox matches resolved
// paths, so a root linked elsewhere (into a temp root, say) would escape the
// deny while the seat's own files, resolved through the link, stay allowed. A
// missing root is fine: the seat cannot create it. It runs for every seat
// with a home. Human words (domain.Words): for a soldier, G1058, quoted in
// New; for a reviewer, grantor the Human, channel supervisor-relay:dialog,
// words=selected, G1122: "Áp cho cả reviewer (Recommended): reviewer launch
// refuses when home state/, data/, .journal/ or .lock/ is a symlink or not a
// directory; same code path as soldiers".
func realHomeRoots(home string) error {
	for _, r := range homeRoots(home) {
		fi, err := os.Lstat(r)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("fence: home root: %w", err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("fence: home root %s is a symlink or not a directory", r)
		}
	}
	return nil
}

// resolveStateDir validates the per-launch harness state directory: a proper
// subdirectory of the home's state/ (so never a home root), clear of every
// checkout and git path.
func resolveStateDir(l Launch, p *launchPaths) error {
	if l.HarnessStateDir == "" {
		return nil
	}
	if p.home == "" {
		return fmt.Errorf("fence: harness state dir %s needs the home it lives under", l.HarnessStateDir)
	}
	// A link would be resolved and allowed wherever it points inside state/.
	// Human words (domain.Words): grantor the Human, channel
	// supervisor-relay:dialog, words=selected, G1086, whose section 1
	// conditions include "resolveStateDir no symlink": "Duyệt, có R3
	// (Recommended): option 1 + 3 PRs with section 1 conditions; R3 worded as
	// an accident guard (not a boundary), error names munsu report <state>;
	// gate record = munsu task-authority gates, no-mistakes gate state a
	// separate fence task."
	if fi, err := os.Lstat(l.HarnessStateDir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fence: harness state dir %s is a symlink", l.HarnessStateDir)
	}
	r, err := resolvePath(l.HarnessStateDir, false)
	if err != nil {
		return fmt.Errorf("fence: harness state dir: %w", err)
	}
	if state := filepath.Join(p.home, "state"); r == state || !within(r, state) {
		return fmt.Errorf("fence: harness state dir %s is not a proper subdirectory of %s", r, state)
	}
	for _, o := range []string{p.primary, p.worktree, p.gitDir, p.commonDir} {
		if overlaps(r, o) {
			return fmt.Errorf("fence: harness state dir %s overlaps %s", r, o)
		}
	}
	p.stateDir = r
	return nil
}

// resolveGate validates a soldier's no-mistakes write set: both roots or
// neither, clear of every checkout, git path and home root.
func resolveGate(l Launch, p *launchPaths) error {
	if l.GateRepo == "" && l.GateState == "" {
		return nil
	}
	if l.Role != RoleSoldier {
		return fmt.Errorf("fence: a reviewer launch has no gate write set")
	}
	if l.GateRepo == "" || l.GateState == "" {
		return fmt.Errorf("fence: the gate repo and gate state are set together")
	}
	repo, err := resolvePath(l.GateRepo, true)
	if err != nil {
		return fmt.Errorf("fence: gate repo: %w", err)
	}
	if fi, err := os.Stat(repo); err != nil || !fi.IsDir() {
		return fmt.Errorf("fence: gate repo %s is not a directory", repo)
	}
	state, err := resolvePath(l.GateState, false)
	if err != nil {
		return fmt.Errorf("fence: gate state: %w", err)
	}
	roots := append([]string{p.primary, p.worktree, p.gitDir, p.commonDir}, homeRoots(p.home)...)
	for _, g := range []string{repo, state} {
		for _, o := range roots {
			if overlaps(g, o) {
				return fmt.Errorf("fence: gate path %s overlaps %s", g, o)
			}
		}
	}
	p.gateRepo, p.gateState = repo, state
	return nil
}

// checkSeatRoots refuses an explicit writable root (home state, Files) that
// overlaps a path the fence protects. The soldier's own worktree and git
// carve-outs are not seat roots; they are validated in resolveLaunch.
func checkSeatRoots(roots, protected []string) error {
	for _, r := range roots {
		for _, pr := range protected {
			if overlaps(r, pr) {
				return fmt.Errorf("fence: writable root %s overlaps protected path %s", r, pr)
			}
		}
	}
	return nil
}

func soldierBranch(b string) (string, error) {
	if !branchPattern.MatchString(b) || strings.Contains(b, "..") || strings.HasSuffix(b, ".lock") || strings.HasSuffix(b, ".") {
		return "", fmt.Errorf("fence: invalid task branch %q", b)
	}
	for _, c := range strings.Split(b, "/") {
		if strings.HasPrefix(c, ".") || strings.HasSuffix(c, ".lock") {
			return "", fmt.Errorf("fence: invalid task branch %q", b)
		}
	}
	return b, nil
}

// gitBranchFiles are the exact common-dir paths a commit on branch writes: the
// loose ref and its lock, the ref's reflog and its lock, and the intermediate
// directories git creates for a slashed name. The primary's main, HEAD,
// packed-refs, config and hooks stay refused.
func gitBranchFiles(commonDir, branch string) []string {
	var out []string
	for _, tree := range []string{filepath.Join(commonDir, "refs", "heads"), filepath.Join(commonDir, "logs", "refs", "heads")} {
		parts := strings.Split(branch, "/")
		for i := 1; i < len(parts); i++ {
			out = append(out, filepath.Join(tree, filepath.Join(parts[:i]...)))
		}
		ref := filepath.Join(tree, filepath.FromSlash(branch))
		out = append(out, ref, ref+".lock")
	}
	return out
}

// harnessState returns the writable directories of a harness kind beyond the
// launch's own state dir, which New allows. binDirs are the directories in the
// harness's agent dirs that the harness itself puts on its commands' PATH,
// which the seat must not write. An unmodeled kind fails closed.
//
// A claude seat writes its own config dir (CLAUDE_CONFIG_DIR, the launch's
// state dir, which New opens inside state/) and claude's log cache, never ~/.claude or ~/.claude.json: the
// unfenced General's claude loads hooks, settings, skills, plugins and memory
// from those. The seat's dir links the Human's config read-only (the fence
// resolves links). Human words (domain.Words): grantor the Human, channel
// supervisor-relay:dialog, words=selected, G1085: "Quy tắc chung (Recommended):
// Seat có fence không bao giờ ghi chương trình hay cấu hình mà tiến trình ngoài
// fence sẽ chạy, và không gửi input vào session ngoài session của nó."
//
// A pi seat writes only its own agent dir (PI_CODING_AGENT_DIR, the launch's
// state dir, which New opens inside state/), never ~/.pi/agent: the unfenced
// pi runs its install, launcher, extensions and packages from there, executes
// commands named in auth.json and models.json, and reads its settings, trust
// answers, model catalog and extension data there. The seat's dir links them
// read-only (the fence resolves links); its sessions, settings and trust
// answers are its own. Human words (domain.Words): grantor the Human, channel
// supervisor-relay:dialog, words=selected, G1085, quoted above.
//
// Claude puts none on PATH (2.1.296 appends only a host-supplied dir the CLI
// never sets). Pi prepends <agent dir>/bin to every command its bash tool runs,
// where the agent dir is PI_CODING_AGENT_DIR (the launch's state dir) or
// ~/.pi/agent, which also holds the user's pi launcher.
func harnessState(kind, userHome, stateDir string) (dirs, binDirs []string, err error) {
	switch kind {
	case harness.Claude:
		return []string{filepath.Join(userHome, "Library", "Caches", "claude-cli-nodejs")}, nil, nil
	case harness.Pi:
		binDirs = []string{filepath.Join(userHome, ".pi", "agent", "bin")}
		if stateDir != "" {
			binDirs = append(binDirs, filepath.Join(stateDir, "bin"))
		}
		return nil, binDirs, nil
	}
	return nil, nil, fmt.Errorf("fence: no state profile for harness %q", kind)
}

// The parents of other sessions' input sockets, as variables so tests point them
// into scratch: claude's per-process message sockets, and the directory tmux
// makes its tmux-<uid> socket dir in when TMUX_TMPDIR is unset.
var (
	claudeSocketDir = "/private/tmp/cc-socks"
	tmuxTempDir     = "/private/tmp"
)

// sessionSocketDirs are the directories that hold another session's input
// socket: claude's message sockets, and tmux's server sockets under its default
// dir and under TMUX_TMPDIR as munsu's own environment sets it.
func sessionSocketDirs() []string {
	tmux := "tmux-" + strconv.Itoa(os.Getuid())
	dirs := []string{claudeSocketDir, filepath.Join(tmuxTempDir, tmux)}
	if d := os.Getenv("TMUX_TMPDIR"); d != "" {
		if r, err := resolvePath(filepath.Join(d, tmux), false); err == nil {
			dirs = append(dirs, r)
		}
	}
	return dedupe(dirs)
}

// denySessionSockets refuses connecting to any unix socket under dirs, so a
// seat cannot inject messages into another claude session or type into another
// tmux pane. Each dir is its own remote term: two path filters inside one term
// match only together, so the second would never refuse. Human words
// (domain.Words): grantor the Human, channel supervisor-relay:dialog,
// words=selected, G1085: "Quy tắc chung (Recommended): Seat có fence không
// bao giờ ghi chương trình hay cấu hình mà tiến trình ngoài fence sẽ chạy, và
// không gửi input vào session ngoài session của nó."
//
// The herdr socket stays reachable pending Human words on own-pane self-report
// (fence-session-socket part 2).
func denySessionSockets(b *strings.Builder, dirs []string) {
	b.WriteString("(deny network-outbound")
	for _, d := range dirs {
		b.WriteString(" (remote unix-socket (subpath " + quote(d) + "))")
	}
	b.WriteString(")\n")
}

// goCaches returns the Go build cache, module cache (with the checksum db) and
// telemetry directories, from the environment as go itself reads it.
func goCaches(userHome string) []string {
	cache, _ := os.UserCacheDir()
	config, _ := os.UserConfigDir()
	gopath := filepath.Join(userHome, "go")
	if gp := filepath.SplitList(os.Getenv("GOPATH")); len(gp) > 0 && gp[0] != "" {
		gopath = gp[0]
	}
	dirs := []string{
		envOr("GOCACHE", filepath.Join(cache, "go-build")),
		envOr("GOMODCACHE", filepath.Join(gopath, "pkg", "mod")),
		filepath.Join(gopath, "pkg"),
		envOr("GOTELEMETRYDIR", filepath.Join(config, "go", "telemetry")),
	}
	var out []string
	for _, d := range dirs {
		if filepath.IsAbs(d) {
			if r, err := resolvePath(d, false); err == nil {
				out = append(out, r)
			}
		}
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" && v != "off" {
		return v
	}
	return def
}

// resolvePath returns the symlink-resolved absolute path. A path that does not
// exist is resolved through its nearest existing ancestor unless mustExist.
func resolvePath(p string, mustExist bool) (string, error) {
	if err := lexicalPath(p); err != nil {
		return "", err
	}
	r, err := filepath.EvalSymlinks(p)
	if err == nil {
		return r, nil
	}
	if mustExist || !os.IsNotExist(err) {
		return "", err
	}
	parent, err := resolvePath(filepath.Dir(p), false)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(p)), nil
}

// within reports whether child is root or lies under it.
func within(child, root string) bool {
	return child == root || strings.HasPrefix(child, strings.TrimSuffix(root, "/")+"/")
}

func overlaps(a, b string) bool { return within(a, b) || within(b, a) }

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// quote renders s as a profile string literal.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func allow(b *strings.Builder, filter string, paths ...string) {
	rule(b, "allow", "file-write*", filter, paths)
}
func deny(b *strings.Builder, filter string, paths ...string) {
	rule(b, "deny", "file-write*", filter, paths)
}

// denyUnlink refuses removing or renaming each exact path, and nothing else
// about it.
func denyUnlink(b *strings.Builder, paths ...string) {
	rule(b, "deny", "file-write-unlink", "literal", paths)
}

func rule(b *strings.Builder, verb, op, filter string, paths []string) {
	if len(paths) == 0 {
		return
	}
	b.WriteString("(" + verb + " " + op)
	for _, p := range paths {
		b.WriteString(" (" + filter + " " + quote(p) + ")")
	}
	b.WriteString(")\n")
}

// denySeatDirs refuses creating, or writing inside, any per-task seat dir in
// state: <durable key>.<harness.ClaudeConfigDirSuffix or PiAgentDirSuffix>.
func denySeatDirs(b *strings.Builder, state string) {
	suffixes := regexp.QuoteMeta(harness.ClaudeConfigDirSuffix) + "|" + regexp.QuoteMeta(harness.PiAgentDirSuffix)
	b.WriteString(`(deny file-write* (regex (string-append "^" (regex-quote ` + quote(state+"/") + `) ` +
		quote(`[^/]+\.(`+suffixes+`)(/|$)`) + `)))` + "\n")
}

// allowFiles allows each exact file and its atomic-write temp siblings
// (<file>.tmp.<pid>.<hex>). regex-quote keeps a path's metacharacters literal.
func allowFiles(b *strings.Builder, files ...string) {
	for _, f := range files {
		b.WriteString(`(allow file-write* (regex (string-append "^" (regex-quote ` + quote(f) +
			`) "(\\.tmp\\.[0-9]+\\.[0-9a-f]+)?$")))` + "\n")
	}
}
