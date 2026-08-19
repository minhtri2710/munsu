//go:build windows

package fleet

import (
	"os"
)

// checkExecutable validates that a check plugin is executable. On Windows, Go
// never reports the Unix exec mode bits — a regular file reports 0666 or 0444
// depending only on FILE_ATTRIBUTE_READONLY — so there is no exec bit to test.
// A check plugin is therefore executable iff it is a regular file (checked by
// the caller) whose content is a runnable script; the caller's follow-on
// shebang check is the actual runnability gate. The fail-closed rejections
// (symlink, non-regular, empty/unreadable, missing shebang) are unchanged.
func checkExecutable(path string, fi os.FileInfo) error {
	_, _ = path, fi
	return nil
}
