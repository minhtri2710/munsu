//go:build !windows

package harness

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether fi is owned by the current user.
func ownedByCurrentUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
