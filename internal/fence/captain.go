package fence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/harness"
)

// CaptainDocsDir is the directory under a captain home's state/ where the
// captain writes the detailed-result docs its status lines point to.
const CaptainDocsDir = "captain-docs"

// newCaptain builds a captain's profile. The captain writes its harness state
// dir, the temp roots, data/<task>/brief.md in its home (the brief it fills;
// munsu makes data/<task>/, which the captain cannot create or replace) and
// .md files in state/captain-docs/. Nothing else in its home or its General's
// home, no git dir of its home's repository, no seat dir, no PATH dir, and no
// unix socket but DNS's and its seat socket, through which munsu runs the
// commands that write records and drive panes outside the fence. Human words
// (domain.Words): grantor the Human, channel supervisor-relay:dialog,
// words=selected, G1085: "Quy tắc chung (Recommended): Seat có fence không
// bao giờ ghi chương trình hay cấu hình mà tiến trình ngoài fence sẽ chạy, và
// không gửi input vào session ngoài session của nó." The captain-fence design
// was delegated: grantor the Human, channel direct chat with the Lead,
// 2026-10-13, words=selected, 1A: "Xác nhận cả 7 refusal đã ghi theo tự quyết
// đi" (ratifies the "tự quyết đi" record, scope: the captain-fence design).
// Seat socket: grantor the Human, channel direct chat with the Lead,
// 2026-10-13, words=selected, 2A: "Captain được gửi input vào session của
// chính soldier của nó qua munsu" (the seat relays send, spawn and teardown
// into its own soldiers' sessions, under G1085).
func newCaptain(l Launch) (*Fence, error) {
	c, err := resolveCaptain(l)
	if err != nil {
		return nil, err
	}
	p, parent, git, socket, docs := c.launchPaths, c.parent, c.git, c.socket, c.docs
	state := filepath.Join(p.home, "state")

	userHome, err := resolvePath(l.UserHome, true)
	if err != nil {
		return nil, fmt.Errorf("fence: user home: %w", err)
	}
	dirs, binDirs, err := harnessState(l.Harness, userHome, p.stateDir)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	readRefuse := denyCredentialReads(&b, userHome)
	denySocketConnects(&b, socket)
	var roots []string
	allowRoots := func(paths ...string) {
		roots = append(roots, paths...)
		allow(&b, "subpath", paths...)
	}
	allowRoots(append([]string{"/private/tmp", "/private/var/folders", "/dev"}, dirs...)...)
	deny(&b, "subpath", append([]string{p.home, parent}, git...)...)
	b.WriteString(`(allow file-write* (regex (string-append "^" (regex-quote ` + quote(filepath.Join(p.home, "data")+"/") +
		`) "[^/]+/brief\\.md(\\.tmp\\.[0-9]+\\.[0-9a-f]+)?$")))` + "\n")
	b.WriteString(`(allow file-write* (regex (string-append "^" (regex-quote ` + quote(docs+"/") +
		`) "[^/]+\\.md(\\.tmp\\.[0-9]+\\.[0-9a-f]+)?$")))` + "\n")
	deny(&b, "subpath", harness.SeatBuildDir(p.home))
	denySeatDirs(&b, state)
	if p.stateDir != "" {
		allowRoots(p.stateDir)
	}
	pathDirs, locked, removable, err := pathDirDenies(append(append([]string{}, l.PathDirs...), binDirs...), l.ShimDir, roots)
	if err != nil {
		return nil, err
	}
	deny(&b, "subpath", pathDirs...)
	deny(&b, "literal", locked...)
	denyUnlink(&b, removable...)
	denySocketRemoval(&b, keptSocketDirs())
	denyUnlink(&b, ancestors(append([]string{socket, parent}, git...)...)...)

	refuse := append(append([]string{p.home}, homeRoots(p.home)...), parent)
	refuse = append(append(refuse, durableHomeRoots(parent)...), git...)
	sum := sha256.Sum256([]byte(b.String()))
	return &Fence{role: RoleCaptain, harness: l.Harness, profile: b.String(), digest: hex.EncodeToString(sum[:]), refuse: dedupe(refuse), readRefuse: readRefuse}, nil
}

// captainPaths is a captain Launch with every path resolved and validated.
type captainPaths struct {
	launchPaths
	parent, socket, docs string
	git                  []string
}

func resolveCaptain(l Launch) (captainPaths, error) {
	var c captainPaths
	if l.Worktree != "" || l.Branch != "" || len(l.Files) > 0 || len(l.WritableRoots) > 0 || len(l.Checkouts) > 0 {
		return c, fmt.Errorf("fence: a captain launch has no worktree, branch, files or writable roots")
	}
	var err error
	if c.home, err = realDir("home", l.Home); err != nil {
		return c, err
	}
	if c.parent, err = realDir("General home", l.ParentHome); err != nil {
		return c, err
	}
	if c.home == c.parent {
		return c, fmt.Errorf("fence: captain home %s is its General home", c.home)
	}
	for _, h := range []string{c.home, c.parent} {
		if err := realHomeRoots(h, RoleSoldier); err != nil {
			return c, err
		}
	}
	for _, g := range []struct {
		name, in string
		out      *string
	}{{"primary checkout", l.Primary, &c.primary}, {"git dir", l.GitDir, &c.gitDir}, {"git common dir", l.CommonDir, &c.commonDir}} {
		if g.in == "" {
			continue
		}
		if *g.out, err = realDir(g.name, g.in); err != nil {
			return c, err
		}
		c.git = append(c.git, *g.out)
	}
	if err := resolveStateDir(l, &c.launchPaths); err != nil {
		return c, err
	}
	state := filepath.Join(c.home, "state")
	if err := lexicalPath(l.Socket); err != nil {
		return c, fmt.Errorf("fence: seat socket: %w", err)
	}
	if dir, err := resolvePath(filepath.Dir(l.Socket), true); err != nil || dir != state {
		return c, fmt.Errorf("fence: seat socket %s is not directly under %s", l.Socket, state)
	}
	c.socket = filepath.Join(state, filepath.Base(l.Socket))
	c.docs = filepath.Join(state, CaptainDocsDir)
	if fi, err := os.Lstat(c.docs); err != nil || !fi.IsDir() {
		return c, fmt.Errorf("fence: captain docs dir %s is missing, a symlink or not a directory", c.docs)
	}
	return c, nil
}

// realDir resolves an existing directory.
func realDir(name, in string) (string, error) {
	r, err := resolvePath(in, true)
	if err != nil {
		return "", fmt.Errorf("fence: %s: %w", name, err)
	}
	if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("fence: %s %s is not a directory", name, r)
	}
	return r, nil
}
