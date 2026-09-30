package fence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
}

var branchPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// launchPaths is a Launch with every path resolved and validated.
type launchPaths struct {
	home, primary, worktree, gitDir, commonDir string
	stateDir, gateRepo, gateState              string
	files                                      []string
}

// New validates the launch facts and builds the profile. Anything malformed,
// missing, or that would widen the writable set over the primary checkout or the
// git common dir is refused; New never falls back to a looser profile.
func New(l Launch) (*Fence, error) {
	if err := platformCheck(); err != nil {
		return nil, err
	}
	if l.Role != RoleSoldier && l.Role != RoleReviewer {
		return nil, fmt.Errorf("fence: unknown role %q", l.Role)
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
	dirs, files, err := harnessState(l.Harness, userHome, p.stateDir)
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, goCaches(userHome)...)

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
	allow(&b, "subpath", append([]string{"/private/tmp", "/private/var/folders", "/dev"}, dirs...)...)
	allowFiles(&b, files...)
	deny(&b, "subpath", protected...)
	if l.Role == RoleReviewer && p.home != "" {
		// A reviewer's home roots stay refused even under a temp root; only its
		// own harness state dir, inside state/, is carved back out.
		deny(&b, "subpath", homeRoots(p.home)...)
		if p.stateDir != "" {
			allow(&b, "subpath", p.stateDir)
		}
	}
	allow(&b, "subpath", seat...)
	allowFiles(&b, p.files...)
	if p.gateRepo != "" {
		allow(&b, "subpath", p.gateRepo)
		allowFiles(&b, p.gateState, p.gateState+"-wal", p.gateState+"-shm", p.gateState+"-journal")
	}
	if l.Role == RoleSoldier {
		branch, err := soldierBranch(l.Branch)
		if err != nil {
			return nil, err
		}
		allow(&b, "subpath", p.worktree, p.gitDir, filepath.Join(p.commonDir, "objects"),
			filepath.Join(p.commonDir, "refs", "remotes"), filepath.Join(p.commonDir, "logs", "refs", "remotes"))
		allow(&b, "literal", gitBranchFiles(p.commonDir, branch)...)
		// A worktree config can set hooks and commands that later run unfenced.
		deny(&b, "literal", filepath.Join(p.gitDir, "config.worktree"))
	}

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
		profile: b.String(), digest: hex.EncodeToString(sum[:]), refuse: dedupe(refuse),
	}, nil
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
	return p, nil
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
// (with their atomic temp siblings). An unmodeled kind fails closed.
func harnessState(kind, userHome, stateDir string) (dirs, files []string, err error) {
	if stateDir != "" && kind != harness.Pi {
		return nil, nil, fmt.Errorf("fence: harness %q has no per-launch state dir", kind)
	}
	switch kind {
	case harness.Claude:
		return []string{filepath.Join(userHome, ".claude"), filepath.Join(userHome, "Library", "Caches", "claude-cli-nodejs")},
			[]string{filepath.Join(userHome, ".claude.json")}, nil
	case harness.Pi:
		dirs = []string{filepath.Join(userHome, ".pi", "agent")}
		if stateDir != "" {
			dirs = append(dirs, stateDir)
		}
		return dirs, nil, nil
	}
	return nil, nil, fmt.Errorf("fence: no state profile for harness %q", kind)
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
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", fmt.Errorf("path %q is not absolute and clean", p)
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("path %q holds a control character", p)
		}
	}
	if p == "/" {
		return "", fmt.Errorf("path is the filesystem root")
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

func allow(b *strings.Builder, filter string, paths ...string) { rule(b, "allow", filter, paths) }
func deny(b *strings.Builder, filter string, paths ...string)  { rule(b, "deny", filter, paths) }

func rule(b *strings.Builder, verb, filter string, paths []string) {
	if len(paths) == 0 {
		return
	}
	b.WriteString("(" + verb + " file-write*")
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
