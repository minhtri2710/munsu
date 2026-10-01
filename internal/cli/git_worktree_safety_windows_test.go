//go:build windows

package cli

import (
	"os"
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

// TestSafetyCheckGitQuotedWindowsPathVerdicts runs the quoted-path rows of
// TestSafetyCheckGitVerdictsOnSharedTokenizer under the Windows host reading
// (backslashLiteral), with the drive-letter, backslash-separated worktree path
// a Windows harness hands the hook. A quoted path with a space stays one word
// under this reading too: at 8765440e the -C row was allowed and the cd row
// refused.
func TestSafetyCheckGitQuotedWindowsPathVerdicts(t *testing.T) {
	primary := initGitRepoForSafety(t, t.TempDir())
	worktree := filepath.Join(t.TempDir(), "wt")
	runGitForSafety(t, primary, "worktree", "add", "--detach", worktree)
	homeDir := bindSafetyWorktree(t, "ship-vdw", primary, worktree)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_TASK_ID", "ship-vdw")
	runGitForSafety(t, worktree, "checkout", "-b", "mu/ship-vdw")
	sub := filepath.Join(worktree, "my dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		command string
		want    bool
	}{
		{`git -C "` + sub + `" push --force origin mu/ship-vdw`, true},
		{`cd "` + sub + `" && git push origin mu/ship-vdw`, false},
	} {
		block, reason := runPiSafetyForGit(t, worktree, tc.command)
		if block != tc.want {
			t.Errorf("%q block=%v reason=%q, want block=%v", tc.command, block, reason, tc.want)
		}
	}
}
