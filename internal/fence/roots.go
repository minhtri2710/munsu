package fence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RootFacts are the paths a configured writable root must stay clear of, beyond
// the fixed user and system denylist.
type RootFacts struct {
	Home      string   // the whole munsu home
	Checkouts []string // every checkout, worktree and git path, and the gate paths
}

// ValidateWritableRoots checks a project's fence-writable-roots and returns
// them symlink-resolved. It is the one validator: `munsu project config set`
// runs it before storing and fence.New runs it before building a profile. A
// root must be absolute, clean, itself not a symlink, an existing directory
// owned by the user and not group- or world-writable, and clear (in either
// direction) of the user's home, the munsu home, every checkout, the user and
// system tool directories and every PATH entry. The first violation refuses.
func ValidateWritableRoots(roots []string, facts RootFacts) ([]string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("fence-writable-roots: resolving user home: %w", err)
	}
	userHome, err = resolvePath(userHome, true)
	if err != nil {
		return nil, fmt.Errorf("fence-writable-roots: user home: %w", err)
	}
	forbidden := forbiddenRootPaths(userHome, facts)
	var out []string
	for _, root := range roots {
		if err := lexicalPath(root); err != nil {
			return nil, fmt.Errorf("fence-writable-roots: %w", err)
		}
		if err := clearOfProtected(root, userHome, forbidden); err != nil {
			return nil, err
		}
		fi, err := os.Lstat(root)
		if err != nil {
			return nil, fmt.Errorf("fence-writable-roots: %s: %w", root, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("fence-writable-roots: %s is a symlink; name the directory it points to", root)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("fence-writable-roots: %s is not a directory", root)
		}
		if why := unsafeDir(fi); why != "" {
			return nil, fmt.Errorf("fence-writable-roots: %s %s", root, why)
		}
		resolved, err := resolvePath(root, true)
		if err != nil {
			return nil, fmt.Errorf("fence-writable-roots: %w", err)
		}
		out = append(out, resolved)
	}
	return out, nil
}

// clearOfProtected refuses root when it is the user's home or an ancestor of it
// (a root under the home is the point), or overlaps any forbidden path.
func clearOfProtected(root, userHome string, forbidden []string) error {
	if withinOnDisk(userHome, root) {
		return fmt.Errorf("fence-writable-roots: %s is the user home or an ancestor of it", root)
	}
	for _, f := range forbidden {
		if withinOnDisk(root, f) || withinOnDisk(f, root) {
			return fmt.Errorf("fence-writable-roots: %s overlaps protected path %s", root, f)
		}
	}
	return nil
}

// withinOnDisk is within, widened by identity on disk: when root exists, child
// is also within it if child or any of its ancestors is the same file. The
// lexical test misses a case variant (/users/me, ~/.SSH) on a case-insensitive
// filesystem and a symlinked parent (<d>/lp/sub with lp linking to a checkout);
// os.SameFile misses neither, so no separate check of the resolved path is needed.
func withinOnDisk(child, root string) bool {
	if within(child, root) {
		return true
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false
	}
	for p := child; ; p = filepath.Dir(p) {
		if info, err := os.Stat(p); err == nil && os.SameFile(info, rootInfo) {
			return true
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

// forbiddenRootPaths lists each protected path as given and, when it exists,
// symlink-resolved, so a root cannot reach one through a symlinked parent. The
// home-relative list is the known credential and tool set, not exhaustive.
func forbiddenRootPaths(userHome string, facts RootFacts) []string {
	paths := append([]string{facts.Home, "/opt/homebrew", "/usr/local"}, facts.Checkouts...)
	for _, rel := range []string{".ssh", ".gnupg", ".config", ".pi", ".docker", ".aws", ".kube", ".azure", ".password-store", ".local/bin", "go/bin", ".cargo/bin", "Library/LaunchAgents", "Library/Keychains"} {
		paths = append(paths, filepath.Join(userHome, filepath.FromSlash(rel)))
	}
	claude, _ := filepath.Glob(filepath.Join(userHome, ".claude*"))
	paths = append(paths, claude...)
	paths = append(paths, filepath.SplitList(os.Getenv("PATH"))...)
	var out []string
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		out = append(out, p)
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			out = append(out, r)
		}
	}
	return dedupe(out)
}

// lexicalPath refuses a path that is not absolute and clean, holds a control
// character, or is the filesystem root.
func lexicalPath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return fmt.Errorf("path %q is not absolute and clean", p)
	}
	if strings.ContainsFunc(p, func(c rune) bool { return c < 0x20 || c == 0x7f }) {
		return fmt.Errorf("path %q holds a control character", p)
	}
	if p == "/" {
		return fmt.Errorf("path is the filesystem root")
	}
	return nil
}
