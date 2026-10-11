//go:build !windows

package fence

import (
	"os"
	"syscall"
)

// unsafeDir says why a configured writable root is unsafe, or "" when it is
// owned by the current user and writable by no one else.
func unsafeDir(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return "is not owned by the current user"
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return "is group- or world-writable"
	}
	return ""
}
