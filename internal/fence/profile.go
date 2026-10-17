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
	// readRefuse are the existing credential paths whose read must be refused.
	readRefuse []string
	roots      []string // the launch's resolved extra writable roots
}

var branchPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// launchPaths is a Launch with every path resolved and validated.
type launchPaths struct {
	home, primary, worktree, gitDir, commonDir string
	stateDir                                   string
	files, remoteReflogs                       []string
	roots                                      []string
}

// New validates the launch facts and builds the profile. Anything malformed,
// missing, or that would widen the writable set over the primary checkout or the
// git common dir is refused; New never falls back to a looser profile.
func New(l Launch) (*Fence, error) {
	if l.Role != RoleSoldier && l.Role != RoleReviewer && l.Role != RoleCaptain {
		return nil, fmt.Errorf("fence: unknown role %q", l.Role)
	}
	if !filepath.IsAbs(l.UserHome) {
		return nil, fmt.Errorf("fence: user home must be absolute: %q", l.UserHome)
	}
	if err := Supported(); err != nil {
		return nil, err
	}
	if l.Role == RoleCaptain {
		return newCaptain(l)
	}
	p, err := resolveLaunch(l)
	if err != nil {
		return nil, err
	}
	userHome, err := resolvePath(l.UserHome, true)
	if err != nil {
		return nil, fmt.Errorf("fence: user home: %w", err)
	}
	dirs, binDirs, err := harnessState(l.Harness, userHome, p.stateDir)
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, p.roots...)

	protected := []string{p.primary, p.commonDir, p.gitDir}
	if l.Role == RoleReviewer {
		protected = append(protected, p.worktree)
	}
	if err := checkSeatRoots(p.files, protected); err != nil {
		return nil, err
	}

	var branch string
	if l.Role == RoleSoldier {
		if branch, err = soldierBranch(l.Branch); err != nil {
			return nil, err
		}
	}

	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	readRefuse := denyCredentialReads(&b, userHome)
	// Observe only: the report modifier logs every outbound connect to the
	// Sandbox log (log show --predicate 'sender == "Sandbox"') and refuses
	// nothing. It sits before the socket denies, which are last-match-wins,
	// so those denies still decide.
	b.WriteString("(allow network-outbound (with report))\n")
	denySocketConnects(&b, "")
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
		// registries (projects.json, captains.json)." config/ and projects/
		// hold the configuration and the project clones that unfenced munsu
		// reads and runs, so no seat writes them either. Human words
		// (domain.Words): grantor the Human, channel supervisor-relay:dialog,
		// words=selected, G1085: "Quy tắc chung (Recommended): Seat có fence
		// không bao giờ ghi chương trình hay cấu hình mà tiến trình ngoài fence
		// sẽ chạy, và không gửi input vào session ngoài session của nó."
		// G1058 (quoted above) for a soldier, and the same for a reviewer: a seat
		// writes nothing else in its home, so a new top-level name there is
		// refused. identity.json, AGENTS.md and the tasks' worktrees are
		// configuration and programs that unfenced munsu, the General and the
		// daemon read and run: G1085 (quoted above). A soldier's worktree is
		// allowed back below; a reviewer's Files and harness state dir are too.
		// Human words (domain.Words): grantor the Human, channel direct chat
		// with the Lead, 2026-10-13, words=selected, 1A:
		// "Xác nhận cả 7 refusal đã ghi theo tự quyết đi" (ratifies the "tự
		// quyết đi" record: decide the deferred items).
		deny(&b, "subpath", p.home)
	}
	allowFiles(&b, p.files...)
	// Words: Human, typed, 2026-10-15: "xóa no-mistake đi, nó chỉ là 1 plugin tích hợp rời nếu user cần thui"
	if l.Role == RoleSoldier {
		allowRoots(p.worktree, p.gitDir, filepath.Join(p.commonDir, "objects"),
			filepath.Join(p.commonDir, "refs", "remotes"))
		allow(&b, "literal", gitBranchRefFiles(p.commonDir, branch)...)
		// The seat appends to its own reflogs but cannot swap one for a symlink
		// an unfenced push or fetch then writes through (fence-reflog-symlink-
		// deputy): its worktree HEAD reflog (created by the detached `git
		// worktree add`) and its common-dir branch reflog (pre-created by munsu
		// at spawn, ensureBranchReflog, then appended by the seat's first `git
		// checkout -b`) are append-only, with create, unlink and rename refused
		// under their logs/. The gitDir logs deny comes after the gitDir root
		// allow so it wins. The gitDir is itself a writable root, so its own name
		// is denied too: without it the seat could rename the gitDir away, swap
		// logs/HEAD inside the moved copy, and rename it back. <common>/
		// refs/remotes is a root; <common>/logs/refs/remotes is not. The seat
		// appends only to the remote reflogs munsu pre-created at spawn
		// (Launch.RemoteReflogs), so a link it plants there is never one an
		// unfenced fetch writes through (fence-reflog-remotes-residual).
		// Human words (domain.Words): grantor the
		// Human, channel supervisor-relay:dialog, words=selected, G1168: "B1
		// (Recommended): B on the seat branch reflog, worktree HEAD and gate
		// reflog; munsu pre-creates the gate branch reflog at spawn (probe
		// first); logs/refs/remotes residual becomes its own task", building on
		// G1167: "B: dò trước rồi làm (Recommended): munsu pre-creates the
		// soldier's own branch reflog files; fence allows data writes to exactly
		// those, refuses create/unlink/rename elsewhere in logs/".
		deny(&b, "subpath", filepath.Join(p.gitDir, "logs"))
		allowData(&b, filepath.Join(p.gitDir, "logs", "HEAD"),
			filepath.Join(p.commonDir, "logs", "refs", "heads", filepath.FromSlash(branch)))
		// Human words (domain.Words): grantor the Human, channel direct chat with
		// the Lead, words=selected, 2026-10-13: "Vá ngay: munsu tạo sẵn reflog
		// remote lúc spawn và khóa logs/refs/remotes; fetch remote mới fail cho tới
		// khi reflog được tạo sẵn".
		allowData(&b, p.remoteReflogs...)
		deny(&b, "literal", p.gitDir)
		// A worktree config can set hooks and commands that later run unfenced.
		deny(&b, "literal", filepath.Join(p.gitDir, "config.worktree"))
		// The worktree's own name and every directory above it cannot be
		// unlinked or renamed, and its .git file cannot be written: a seat
		// that moved its worktree away and planted a link at its name, or
		// rewrote the gitdir: line, would have the next launch's fence grant
		// another task's worktree and git dir, whose config and hooks unfenced
		// git runs. Human words (domain.Words), each grantor the Human, channel
		// supervisor-relay:dialog, words=selected: G1058: "A soldier's write
		// fence lets it write only its own task's status, report and request
		// files; it can never write another task's documents, any review
		// verdict, delivery authorization or gate record, or the fleet
		// registries (projects.json, captains.json)." G1085: "Quy tắc chung
		// (Recommended): Seat có fence không bao giờ ghi chương trình hay cấu
		// hình mà tiến trình ngoài fence sẽ chạy, và không gửi input vào
		// session ngoài session của nó."
		denyUnlink(&b, append(ancestors(p.worktree), p.worktree)...)
		deny(&b, "literal", filepath.Join(p.worktree, ".git"))
	}
	// No seat renames a directory above a protected path or the home away and
	// plants a tree of its own at its name, as a captain cannot (newCaptain).
	// Words: Human, typed, 2026-10-16: "các task cần lời của tôi thì hỏi concil rồi quyết" (council 3/3: denyUnlink ancestors of primary/commonDir/home for soldier+reviewer)
	denyUnlink(&b, ancestors(append(protected, p.home)...)...)
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
	denySocketRemoval(&b, keptSocketDirs())

	// The probe proves the home refused: a soldier's every home root (G1058,
	// quoted above), and a reviewer's whole home, which no reviewer writes but
	// its Files and harness state dir.
	refuse := []string{p.primary, p.commonDir}
	if l.Role == RoleReviewer {
		refuse = append(refuse, p.worktree, p.gitDir)
		if p.home != "" {
			refuse = append(refuse, p.home)
		}
	} else {
		refuse = append(refuse, homeRoots(p.home)...)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return &Fence{
		role: l.Role, harness: l.Harness,
		profile: b.String(), digest: hex.EncodeToString(sum[:]), refuse: dedupe(refuse), readRefuse: readRefuse, roots: p.roots,
	}, nil
}

// pathDirDenies returns the PATH directories (and the shim dir) as given and
// symlink-resolved, with every symlink resolving them follows, and the names
// of all their ancestors split into locked and removable. Every ancestor of
// the shim dir is locked. For any other PATH dir an ancestor that is itself a
// writable root is removable (outside every root the profile already refuses
// it too), and every other ancestor is locked. A relative or empty entry names
// the seat's working directory, which the fence cannot refuse, so it is an
// error: the caller keeps such entries off the seat's PATH. An entry with a .
// or .. name, or that does not resolve, is an error too, so the launch refuses
// rather than leave the directory a write lands in, or a link on the way to
// it, undenied.
func pathDirDenies(pathDirs []string, shim string, roots []string) (dirs, locked, removable []string, err error) {
	expand := func(in []string) (out []string, err error) {
		for _, d := range in {
			if !filepath.IsAbs(d) {
				return nil, fmt.Errorf("fence: PATH directory %q is not absolute", d)
			}
			d = strings.TrimRight(d, "/")
			if err := lexicalPath(d); err != nil {
				return nil, fmt.Errorf("fence: PATH directory: %w", err)
			}
			r, links, _, err := walkPath(d)
			if err != nil {
				return nil, fmt.Errorf("fence: PATH directory %s: %w", d, err)
			}
			out = append(append(out, d, r), links...)
		}
		return dedupe(out), nil
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
	shimAncestors := ancestors(shimDirs...)
	for _, a := range ancestors(dirs...) {
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
	if len(l.RemoteReflogs) > 0 && l.Role != RoleSoldier {
		return p, fmt.Errorf("fence: remote reflogs are a soldier's only")
	}
	remotes := filepath.Join(p.commonDir, "logs", "refs", "remotes")
	for _, f := range l.RemoteReflogs {
		if err := lexicalPath(f); err != nil {
			return p, fmt.Errorf("fence: remote reflogs: %w", err)
		}
		dir, err := resolvePath(filepath.Dir(f), false)
		if err != nil {
			return p, fmt.Errorf("fence: remote reflogs: %w", err)
		}
		path := filepath.Join(dir, filepath.Base(f))
		if path == remotes || !within(path, remotes) {
			return p, fmt.Errorf("fence: remote reflogs: %s is not under %s", f, remotes)
		}
		p.remoteReflogs = append(p.remoteReflogs, path)
	}
	if p.home != "" {
		if err := realHomeRoots(p.home, l.Role); err != nil {
			return p, err
		}
	}
	if err := resolveStateDir(l, &p); err != nil {
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
	checkouts := append([]string{p.primary, p.worktree, p.gitDir, p.commonDir}, l.Checkouts...)
	roots, err := ValidateWritableRoots(l.WritableRoots, RootFacts{Home: p.home, Checkouts: checkouts})
	if err != nil {
		return err
	}
	p.roots = roots
	return nil
}

// homeRoots are the munsu home directories no seat writes beyond its own
// files: its durable roots, and the config and project clones that unfenced
// munsu reads and runs.
func homeRoots(home string) []string {
	return append(durableHomeRoots(home), filepath.Join(home, "config"), filepath.Join(home, "projects"))
}

// durableHomeRoots are the home's state, data, journal and lock roots.
func durableHomeRoots(home string) []string {
	return []string{filepath.Join(home, "state"), filepath.Join(home, "data"), filepath.Join(home, ".journal"), filepath.Join(home, ".lock")}
}

// realHomeRoots refuses a home root that is a symlink or not a directory. The
// profile denies the home by its name, and the sandbox matches resolved
// paths, so a root linked elsewhere (into a temp root, say) would escape the
// deny while the seat's own files, resolved through the link, stay allowed. A
// missing root is fine: the seat cannot create it. It runs for every seat
// with a home. A soldier's every home root is checked. A reviewer's config/
// and projects/ are not: G1122 names only the four durable roots, so its
// config/ and projects/ are denied by name and not checked here. Human words
// (domain.Words): for a soldier, G1058, quoted in New; for a reviewer,
// grantor the Human, channel supervisor-relay:dialog, words=selected, G1122:
// "Áp cho cả reviewer (Recommended): reviewer launch refuses when home
// state/, data/, .journal/ or .lock/ is a symlink or not a directory; same
// code path as soldiers".
func realHomeRoots(home string, role Role) error {
	roots := homeRoots(home)
	if role == RoleReviewer {
		roots = durableHomeRoots(home)
	}
	for _, r := range roots {
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
	// [...]"
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
		if o != "" && overlaps(r, o) {
			return fmt.Errorf("fence: harness state dir %s overlaps %s", r, o)
		}
	}
	p.stateDir = r
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

// gitBranchRefFiles are the exact common-dir ref paths a commit on branch
// writes: the loose ref and its lock. The directories of a slashed name are
// pre-created by munsu at spawn (ensureBranchReflog) and stay refused, so the
// seat cannot rename one away, write another task's ref in the moved copy, and
// move it back. The branch's reflog under logs/ is append-only (allowData at
// the call site), not here. The primary's main, HEAD, packed-refs, config and
// hooks stay refused.
// Words: Human, typed, 2026-10-16: "các task cần lời của tôi thì hỏi concil rồi quyết" (council 3/3: pre-create refs/heads/mu at spawn; seat cannot create/rename it)
func gitBranchRefFiles(commonDir, branch string) []string {
	ref := filepath.Join(commonDir, "refs", "heads", filepath.FromSlash(branch))
	return []string{ref, ref + ".lock"}
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

// socketTempDir is the temp root claude and tmux make their socket dirs in when
// no environment variable moves them; a variable so tests point it into scratch.
var socketTempDir = "/private/tmp"

// keptSocketDirs returns claude's and tmux's default socket dirs, and tmux's
// under TMUX_TMPDIR when resolvePath accepts it (a relative one is left out
// without error).
func keptSocketDirs() []string {
	tmux := "tmux-" + strconv.Itoa(os.Getuid())
	dirs := []string{filepath.Join(socketTempDir, "cc-socks"), filepath.Join(socketTempDir, tmux)}
	if v := os.Getenv("TMUX_TMPDIR"); v != "" {
		if r, err := resolvePath(filepath.Join(v, tmux), false); err == nil {
			dirs = append(dirs, r)
		}
	}
	return dedupe(dirs)
}

// denySocketConnects refuses connecting to every unix socket but DNS's
// (mDNSResponder) and seatSocket: a captain's seat socket, through which munsu
// runs the captain's record and pane commands outside its fence; a soldier or
// reviewer passes none. So a seat cannot inject messages into another claude
// session, type into a tmux pane, drive any herdr pane (its own included), or
// reach any other daemon or listener, its own included. The allows are exact
// paths, and a symlink at one is matched at its target. Human words
// (domain.Words), each grantor the Human, channel supervisor-relay:dialog,
// words=selected: G1150: "Chặn hết trừ DNS (Recommended): deny every
// unix-socket connect except mDNSResponder"; G1148: "Chặn hẳn
// (Recommended): deny the herdr socket entirely for fenced seats". G1085: "Quy tắc chung (Recommended): Seat
// có fence không bao giờ ghi chương trình hay cấu hình mà tiến trình ngoài
// fence sẽ chạy, và không gửi input vào session ngoài session của nó." The
// captain's seat socket: grantor the Human, channel direct chat with the Lead,
// 2026-10-13, words=selected, 1A: "Xác nhận cả 7 refusal đã ghi theo tự quyết
// đi" (ratifies the "tự quyết đi" record, scope: the captain-fence design);
// 2A, words=selected: "Captain được gửi input vào session của chính soldier
// của nó qua munsu".
// Words: Human, typed, 2026-10-15: "xóa no-mistake đi, nó chỉ là 1 plugin tích hợp rời nếu user cần thui"
func denySocketConnects(b *strings.Builder, seatSocket string) {
	b.WriteString("(deny network-outbound (remote unix-socket))\n")
	for _, s := range []string{"/private/var/run/mDNSResponder", seatSocket} {
		if s != "" {
			b.WriteString("(allow network-outbound (remote unix-socket (path-literal " + quote(s) + ")))\n")
		}
	}
}

// denySocketRemoval refuses unlinking or renaming each dir, anything in it, and
// each ancestor's own name, so a seat cannot remove another session's socket
// and bind its own in its place, nor move the dir or a parent away and plant a
// tree of its own. Creating a socket stays allowed: the seat's own claude binds
// its socket in cc-socks. Its own socket stays behind at exit, which the words
// accept. Human words (domain.Words): grantor the Human, channel
// supervisor-relay:dialog, words=selected, G1149: "Chặn (Recommended): deny
// unlink/rename/replace of sockets in /tmp/cc-socks and the tmux dir".
func denySocketRemoval(b *strings.Builder, dirs []string) {
	rule(b, "deny", "file-write-unlink", "subpath", dirs)
	denyUnlink(b, ancestors(dirs...)...)
}

// ancestors returns every directory above each path, "/" excluded, once.
func ancestors(paths ...string) []string {
	var out []string
	for _, d := range paths {
		for a := filepath.Dir(d); a != filepath.Dir(a); a = filepath.Dir(a) {
			out = append(out, a)
		}
	}
	return dedupe(out)
}

// resolvePath returns the symlink-resolved absolute path. A path that does not
// exist is resolved through its nearest existing ancestor unless mustExist, and
// a dangling symlink on the way to its target, where a write through it lands.
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
	r, _, missing, err := walkPath(p)
	if err != nil {
		return "", err
	}
	if filepath.Dir(missing) == "/" {
		return "", fmt.Errorf("path %q resolves through the filesystem root", p)
	}
	return r, nil
}

// maxLinkHops bounds the symlinks walkPath follows, as the kernel bounds a
// lookup (MAXSYMLINKS).
const maxLinkHops = 32

// walkPath resolves the lexically valid p one name at a time as the kernel
// does, following every symlink, existing or dangling, on one hop budget. It
// returns the resolved path, every link it followed (each by its resolved
// name), and the first name found missing ("" when the whole path exists). A
// write through p lands at the resolved path, and replacing any of those links
// moves it, so a caller denying p denies them all. A link target may climb from
// the link's directory, whose ancestors the caller denies with the link, but a
// ".." after a name in the target is refused: the directory it climbs out of is
// neither a link nor an ancestor of the result, so a seat could replace it. A
// target with a control character, or a path resolving to "/", is refused.
// Words: Human, typed, 2026-10-16: "các task cần lời của tôi thì hỏi concil rồi quyết" (council 3/3: follow dangling PATH links, deny target, propagate resolve errors)
func walkPath(p string) (resolved string, links []string, missing string, err error) {
	cur, todo, hops := "/", strings.Split(p, "/"), 0
	for len(todo) > 0 {
		name := todo[0]
		todo = todo[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, name)
		fi, err := os.Lstat(next)
		if os.IsNotExist(err) {
			return filepath.Join(append([]string{next}, todo...)...), links, next, nil
		}
		if err != nil {
			return "", nil, "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if hops++; hops > maxLinkHops {
			return "", nil, "", fmt.Errorf("path %q: too many links", p)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", nil, "", err
		}
		if hasControl(target) {
			return "", nil, "", fmt.Errorf("path %q: link %s target %q holds a control character", p, next, target)
		}
		names := strings.Split(target, "/")
		lead := 0
		for lead < len(names) && (names[lead] == "" || names[lead] == "." || names[lead] == "..") {
			lead++
		}
		if slices.Contains(names[lead:], "..") {
			return "", nil, "", fmt.Errorf("path %q: link %s target %q has a .. past its first name", p, next, target)
		}
		links = append(links, next)
		if filepath.IsAbs(target) {
			cur = "/"
		}
		todo = append(names, todo...)
	}
	if cur == "/" {
		return "", nil, "", fmt.Errorf("path %q resolves to the filesystem root", p)
	}
	return cur, links, "", nil
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

// denyCredentialReads refuses reading the credential paths under userHome, each
// as given and symlink-resolved, and returns those that exist for the probe: a
// missing one reads ENOENT and proves nothing. It follows (deny file-write*)
// and no rule after it allows a read. ~/Library/Keychains, ~/.config/gh,
// ~/.gitconfig, ~/.claude* and ~/.pi stay readable.
// Words: Human, selected, 2026-10-17: "Làm, qua council" (council 3/3: seat credential read deny)
func denyCredentialReads(b *strings.Builder, userHome string) []string {
	dirs := withResolved(homePaths(userHome, credentialDirs))
	files := withResolved(homePaths(userHome, credentialFiles))
	rule(b, "deny", "file-read*", "subpath", dirs)
	rule(b, "deny", "file-read*", "literal", files)
	var exist []string
	for _, p := range append(dirs, files...) {
		if _, err := os.Stat(p); err == nil {
			exist = append(exist, p)
		}
	}
	return exist
}

func allow(b *strings.Builder, filter string, paths ...string) {
	rule(b, "allow", "file-write*", filter, paths)
}

// allowData allows appending to each exact existing file while the surrounding
// create, unlink and rename denies stand: the seat may write its own reflog's
// data, but cannot unlink it, rename over it, or create a new one, so it cannot
// swap the file for a symlink an unfenced push or fetch then writes through.
func allowData(b *strings.Builder, paths ...string) {
	rule(b, "allow", "file-write-data", "literal", paths)
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
