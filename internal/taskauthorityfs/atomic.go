package taskauthorityfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeFileAtomic writes data to path with a same-directory hidden temp file,
// fsync, and rename so readers never observe a partial document. Parent
// directories beneath the authority root are created one component at a time
// with private modes (never following a symlinked component), and the
// resulting file is secured to FilePerm. The rename is followed by a
// directory fsync where the platform supports it.
func writeFileAtomic(homeDir, path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := ensureDirSafe(homeDir, dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".txn-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(FilePerm); err != nil {
		tmp.Close()
		return fmt.Errorf("securing temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s into place: %w", path, err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("syncing directory %s: %w", dir, err)
	}
	return nil
}

// ensureDirSafe creates dir and every missing parent beneath the authority
// root one component at a time with DirPerm, verifying each existing
// component is a real directory and never a symlink. The trusted prefix
// (home/state/.task-authority/v2) is created with MkdirAll like every other
// authority path; components beneath the root are link-checked so a hostile
// or corrupt link can never redirect an authority write outside the home.
// The final component is re-secured to DirPerm.
func ensureDirSafe(homeDir, dir string) error {
	root, err := filepath.Abs(filepath.Join(homeDir, filepath.FromSlash(authorityRoot)))
	if err != nil {
		return fmt.Errorf("resolving authority root: %w", err)
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, DirPerm); err != nil {
		return fmt.Errorf("creating authority root %s: %w", root, err)
	}
	if err := os.Chmod(root, DirPerm); err != nil {
		return fmt.Errorf("securing authority root %s: %w", root, err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving authority directory %s: %w", dir, err)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("authority write path %s escapes the authority root", abs)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("inspecting authority directory %s: %w", current, err)
			}
			if err := os.Mkdir(current, DirPerm); err != nil {
				return fmt.Errorf("creating authority directory %s: %w", current, err)
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("authority path component %s is a symlink or not a directory", current)
		}
	}
	return os.Chmod(abs, DirPerm)
}

// readDocument reads one authority document, rejecting symlinks and
// non-regular files so before-digest computation and transaction recovery
// never read through a link.
func readDocument(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, corruptDocument("transaction_manifest", "", "entry %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
