//go:build windows

package home

import (
	"fmt"
	"os"
)

// openRegular opens path only if it is a regular file and never through a
// symlink. Windows has no seat fence, so a check before the open is enough.
func openRegular(path string, flag int, perm os.FileMode) (*os.File, error) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is a symlink or not a regular file", path)
	}
	return os.OpenFile(path, flag, perm)
}

// secureOpened makes an opened file owner-private (see secureFile).
func secureOpened(f *os.File) error {
	return secureFile(f.Name())
}
