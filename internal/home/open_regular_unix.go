//go:build !windows

package home

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// openRegular opens path only if it is a regular file, never through a
// symlink, and without blocking on a FIFO. A fenced soldier writes its own
// status, ack log and scout report, so it can put a symlink, a FIFO or a
// directory at those names; no unfenced reader or writer may follow one.
func openRegular(path string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, perm)
	switch {
	case errors.Is(err, syscall.ELOOP):
		return nil, fmt.Errorf("%s is a symlink", path)
	case errors.Is(err, syscall.ENXIO), errors.Is(err, syscall.EISDIR):
		return nil, fmt.Errorf("%s is not a regular file", path)
	case err != nil:
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = fmt.Errorf("%s is not a regular file", path)
		}
		return nil, err
	}
	return f, nil
}

// secureOpened makes an opened file owner-private through its descriptor, so
// a swap of its name after the open changes nothing. A file already at 0600
// is left alone, as secureFile does.
func secureOpened(f *os.File) error {
	fi, err := f.Stat()
	if err == nil && fi.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0600 {
		return nil
	}
	return f.Chmod(0600)
}
