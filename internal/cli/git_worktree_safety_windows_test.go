//go:build windows

package cli

import (
	"path/filepath"
	"testing"
)

// TestResolveSafetyPathWindowsHostModeIsolation is the regression test for
// issue #686. On a Windows host filepath.IsAbs is compiled to return true for
// drive (C:\) and UNC (\\server) paths, so the absoluteness decision used to
// depend on the host: resolveSafetyPathWithMode short-circuited on
// filepath.IsAbs before consulting the backslash mode. Under backslashEscapes
// (the POSIX shell reading) that wrongly returned a Windows-absolute path
// unchanged instead of joining it under the base, breaking the
// POSIX-vs-Windows mode isolation the guard asserts.
//
// The fix makes the decision mode-determined: under backslashEscapes a
// Windows-absolute path is joined under base on EVERY host, while under
// backslashLiteral (the Windows production reading) it stays absolute. This
// test pins both cells on a Windows host and therefore fails on the
// pre-fix code and passes after it.
func TestResolveSafetyPathWindowsHostModeIsolation(t *testing.T) {
	const base = `C:\repo\worktree`

	cases := []struct {
		name string
		path string
		mode backslashMode
		want string
	}{
		// Escape reading (POSIX shell): Windows paths are RELATIVE, joined
		// under base. This is the cell the bug broke on a Windows host —
		// pre-fix it returned the absolute path unchanged.
		{"drive-escape", `C:\Users\soldier\.git\worktrees\wt`, backslashEscapes, filepath.Join(base, `C:\Users\soldier\.git\worktrees\wt`)},
		{"unc-escape", `\\server\share\.git\worktrees\wt`, backslashEscapes, filepath.Join(base, `\\server\share\.git\worktrees\wt`)},
		// Literal reading (Windows production): Windows paths stay ABSOLUTE,
		// unchanged. This is the production Windows behavior the fix must not
		// alter.
		{"drive-literal", `C:\Users\soldier\.git\worktrees\wt`, backslashLiteral, `C:\Users\soldier\.git\worktrees\wt`},
		{"unc-literal", `\\server\share\.git\worktrees\wt`, backslashLiteral, `\\server\share\.git\worktrees\wt`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSafetyPathWithMode(base, tc.path, tc.mode)
			if got != tc.want {
				t.Fatalf("resolveSafetyPathWithMode(%q, %q, %v) = %q, want %q", base, tc.path, tc.mode, got, tc.want)
			}
		})
	}
}
