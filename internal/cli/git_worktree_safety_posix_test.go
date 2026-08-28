//go:build !windows

package cli

import (
	"testing"
)

// TestResolveSafetyPathPosixAbsoluteUnchanged pins the POSIX-host half of
// issue #686: on a POSIX host a Unix-absolute path (/abs/unix/path) must be
// returned unchanged under BOTH backslash readings. It is kept in a
// POSIX-tagged file (not in the untagged TestResolveSafetyPathModeDetermined)
// because the expectation depends on the host's filepath.IsAbs being true,
// which is only the case off Windows. On Windows the same path is not
// host-absolute and is correctly joined under base — that divergence is why
// the cell cannot live in the untagged, host-agnostic test.
func TestResolveSafetyPathPosixAbsoluteUnchanged(t *testing.T) {
	const base = "/repo/worktree"
	const path = "/abs/unix/path"

	for _, mode := range []backslashMode{backslashLiteral, backslashEscapes} {
		got := resolveSafetyPathWithMode(base, path, mode)
		if got != path {
			t.Fatalf("resolveSafetyPathWithMode(%q, %q, %v) = %q, want %q", base, path, mode, got, path)
		}
	}
}
