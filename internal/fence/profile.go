package fence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	dirs, files, binDirs, err := harnessState(l.Harness, userHome, p.stateDir)
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, goCaches(userHome)...)
	dirs = append(dirs, p.roots...)

	// Only a soldier writes the home: its munsu commands commit canonical
	// documents in-process. A reviewer has no home root, so it cannot write a
	// task's documents or its ReviewVerdict.
	var seat []string
	if l.Role == RoleSoldier {
		seat = homeRoots(p.home)
	}
	protected := []string{p.primary, p.commonDir, p.gitDir}
	if l.Role == RoleReviewer {
		protected = append(protected, p.worktree)
	}
	if err := checkSeatRoots(append(append([]string{}, seat...), p.files...), protected); err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	// Later rules win in a sandbox profile: broad allows first, then the
	// protected denies, then the seat's own roots and git carve-outs.
	var roots []string // every subpath this profile makes writable
	allowRoots := func(paths ...string) {
		roots = append(roots, paths...)
		allow(&b, "subpath", paths...)
	}
	allowRoots(append([]string{"/private/tmp", "/private/var/folders", "/dev"}, dirs...)...)
	allowFiles(&b, files...)
	deny(&b, "subpath", protected...)
	if l.Role == RoleReviewer && p.home != "" {
		// A reviewer's home roots stay refused even under a temp root; only its
		// own harness state dir, inside state/, is carved back out.
		deny(&b, "subpath", homeRoots(p.home)...)
		if p.stateDir != "" {
			allowRoots(p.stateDir)
		}
	}
	allowRoots(seat...)
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
	// Last, so nothing above reopens it: a seat's PATH directories, and every
	// ancestor's own name so a directory cannot be renamed or removed away and
	// replaced. An ancestor strictly inside a writable root is the seat's to
	// reach but never to change, so chmod, unlink and rename are all refused: a
	// mode of 000 would take the PATH dir off the seat's PATH. A writable root
	// itself and everything above it refuse only unlink (a rename is an unlink
	// of the source name), because munsu chmods and fills them. No lane runs
	// this off darwin; the darwin fence lane covers it.
	pathDirs, locked, removable, err := pathDirDenies(append(append([]string{}, l.PathDirs...), binDirs...), roots)
	if err != nil {
		return nil, err
	}
	deny(&b, "subpath", pathDirs...)
	deny(&b, "literal", locked...)
	denyUnlink(&b, removable...)

	refuse := []string{p.primary, p.commonDir}
	if l.Role == RoleReviewer {
		refuse = append(refuse, p.worktree, p.gitDir)
		if p.home != "" {
			refuse = append(refuse, filepath.Join(p.home, "state"))
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return &Fence{
		role: l.Role, harness: l.Harness,
		profile: b.String(), digest: hex.EncodeToString(sum[:]), refuse: dedupe(refuse), roots: p.roots,
	}, nil
}

// pathDirDenies returns the PATH directories as given and symlink-resolved, and
// the names of all their ancestors split by the writable roots: locked are those
// strictly inside a root and neither a root nor above one, removable the rest
// (outside every root the profile already refuses them). A relative or empty entry names
// the seat's working directory, which the fence cannot refuse, so it is an
// error: the caller keeps such entries off the seat's PATH.
func pathDirDenies(pathDirs, roots []string) (dirs, locked, removable []string, err error) {
	for _, d := range pathDirs {
		if !filepath.IsAbs(d) {
			return nil, nil, nil, fmt.Errorf("fence: PATH directory %q is not absolute", d)
		}
		d = filepath.Clean(d)
		dirs = append(dirs, d)
		if r, err := resolvePath(d, false); err == nil {
			dirs = append(dirs, r)
		}
	}
	dirs = dedupe(dirs)
	var ancestors []string
	for _, d := range dirs {
		for a := filepath.Dir(d); a != filepath.Dir(a); a = filepath.Dir(a) {
			ancestors = append(ancestors, a)
		}
	}
	for _, a := range dedupe(ancestors) {
		atOrAboveRoot := slices.ContainsFunc(roots, func(r string) bool { return within(r, a) })
		insideRoot := slices.ContainsFunc(roots, func(r string) bool { return within(a, r) })
		if insideRoot && !atOrAboveRoot {
			locked = append(locked, a)
		} else {
			removable = append(removable, a)
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
	for _, f := range l.Files {
		r, err := resolvePath(f, false)
		if err != nil {
			return p, fmt.Errorf("fence: file: %w", err)
		}
		p.files = append(p.files, r)
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

// homeRoots are the munsu home directories a soldier writes and a reviewer must
// not: its durable state, data, journal and lock roots.
func homeRoots(home string) []string {
	return []string{filepath.Join(home, "state"), filepath.Join(home, "data"), filepath.Join(home, ".journal"), filepath.Join(home, ".lock")}
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

// harnessState returns the writable state of a harness kind, including the
// launch's own state dir for a kind that has one: directories and single files
// (with their atomic temp siblings). binDirs are the directories inside those
// roots that the harness itself puts on its commands' PATH, which the seat must
// not write. An unmodeled kind fails closed.
//
// Claude puts none on PATH (2.1.296 appends only a host-supplied dir the CLI
// never sets). Pi prepends <agent dir>/bin to every command its bash tool runs,
// where the agent dir is PI_CODING_AGENT_DIR (the launch's state dir) or
// ~/.pi/agent, which also holds the user's pi launcher.
func harnessState(kind, userHome, stateDir string) (dirs, files, binDirs []string, err error) {
	if stateDir != "" && kind != harness.Pi {
		return nil, nil, nil, fmt.Errorf("fence: harness %q has no per-launch state dir", kind)
	}
	switch kind {
	case harness.Claude:
		return []string{filepath.Join(userHome, ".claude"), filepath.Join(userHome, "Library", "Caches", "claude-cli-nodejs")},
			[]string{filepath.Join(userHome, ".claude.json")}, nil, nil
	case harness.Pi:
		agent := filepath.Join(userHome, ".pi", "agent")
		dirs, binDirs = []string{agent}, []string{filepath.Join(agent, "bin")}
		if stateDir != "" {
			dirs, binDirs = append(dirs, stateDir), append(binDirs, filepath.Join(stateDir, "bin"))
		}
		return dirs, nil, binDirs, nil
	}
	return nil, nil, nil, fmt.Errorf("fence: no state profile for harness %q", kind)
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

// allowFiles allows each exact file and its atomic-write temp siblings
// (<file>.tmp.<pid>.<hex>). regex-quote keeps a path's metacharacters literal.
func allowFiles(b *strings.Builder, files ...string) {
	for _, f := range files {
		b.WriteString(`(allow file-write* (regex (string-append "^" (regex-quote ` + quote(f) +
			`) "(\\.tmp\\.[0-9]+\\.[0-9a-f]+)?$")))` + "\n")
	}
}
