package home

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// joinContained returns the native path for a logical key within root. Logical
// keys use slash separators and reject traversal, empty/current components,
// native backslashes, absolute aliases, and volume-qualified forms before
// native-path conversion. The comparison is purely lexical and does not follow
// symlinks.
func joinContained(root, key string) (string, error) {
	if key == "" {
		return "", ErrEmptyKey
	}
	if strings.HasPrefix(key, "/") || strings.HasPrefix(key, "\\") || path.IsAbs(key) || filepath.IsAbs(key) || (len(key) >= 2 && key[1] == ':') {
		return "", ErrAbsoluteKey
	}
	if strings.Contains(key, "\\") {
		return "", ErrKeyEscapes
	}
	// Reject escape/current/empty components in the raw key before any
	// cleaning, so a key cannot smuggle ".." or absolute escapes.
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrKeyEscapes
		}
	}
	clean := filepath.Clean(filepath.FromSlash(key))
	joined := filepath.Join(root, clean)
	if !withinRoot(root, joined) {
		return "", ErrKeyEscapes
	}
	return joined, nil
}

// verifyNoFollow ensures that the deepest existing ancestor of target stays
// within root. A symlink anywhere along the path that resolves outside root is
// rejected, so a root cannot be escaped through symlinks. Since the caller
// supplies a lexical key, the target itself is never followed.
func verifyNoFollow(root, target string) error {
	cur := target
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ErrKeyEscapes
		}
		cur = parent
	}
	real, err := filepath.EvalSymlinks(cur)
	if err != nil {
		return err
	}
	if !withinRoot(root, real) {
		return ErrSymlinkEscapes
	}
	return nil
}

// withinRoot reports whether p is strictly inside root, comparing canonical
// paths. root must already be canonical.
func withinRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// privateDir creates dir with owner-private permissions, failing if an
// existing path is not a directory.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrNotDirectory
	}
	return secureDir(dir)
}
