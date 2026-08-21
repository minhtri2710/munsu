//go:build !windows

package fleet

import (
	"fmt"
	"os"
)

// checkExecutable validates that a check plugin is executable. On Unix the
// owner-execute mode bit must be set. Windows has no exec mode bits (regular
// files always report 0666/0444) and is handled in check_exec_windows.go, so
// the two platforms keep distinct semantics without weakening the shared
// fail-closed rejections (symlink, non-regular, empty, missing shebang).
func checkExecutable(path string, fi os.FileInfo) error {
	if fi.Mode()&0100 == 0 {
		return fmt.Errorf("check is not executable: %s", path)
	}
	return nil
}
