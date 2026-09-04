package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/spf13/cobra"
)

// shimBinSuffix is the trailing path segment every fleet git-shim directory
// ends in (<home>/state/shim/bin). The strip matches this suffix rather than
// rederiving the absolute dir from MUNSU_HOME, so it removes the shim
// regardless of what MUNSU_HOME says — see stripShimDirFromPath.
var shimBinSuffix = string(os.PathSeparator) + filepath.FromSlash(fleet.GitShimBinRelPath)

// stripShimDirFromPath removes the fleet git-shim directory from this process's
// PATH so every git the munsu binary itself resolves — the guard's own
// `git rev-parse` probes and the real-git handoff, plus the repository mechanics
// munsu runs (worktree add/remove, delivery) — reaches the real git, never the
// shim. The shim fences agent-issued git; munsu is the authority and runs
// unfenced.
//
// The match is on the shim path suffix (…/state/shim/bin), NOT on a dir
// rederived from MUNSU_HOME. Keying off MUNSU_HOME would recurse: if the shim
// dir is on PATH but MUNSU_HOME is unset or points elsewhere, the strip would
// no-op, LookPath would find the shim again, and a read that the fence allows
// would re-invoke the shim without bound — an unbounded process chain, not an
// unfenced git. The suffix match strips the shim whenever it is present, so the
// real-git handoff always reaches the real git. Idempotent; a no-op when no
// shim dir is on PATH.
func stripShimDirFromPath() {
	path := os.Getenv("PATH")
	if path == "" {
		return
	}
	entries := filepath.SplitList(path)
	kept := make([]string, 0, len(entries))
	changed := false
	for _, e := range entries {
		if strings.HasSuffix(filepath.Clean(e), shimBinSuffix) {
			changed = true
			continue
		}
		kept = append(kept, e)
	}
	if changed {
		os.Setenv("PATH", strings.Join(kept, string(os.PathListSeparator)))
	}
}

// newGitGuardCmd is the git shim's guard. The fleet launch PATH places a `git`
// shim ahead of the real git, and that shim re-invokes `munsu git-guard` with
// the git arguments. The guard evaluates the same worktree-binding fence the
// PreToolUse hook enforces (evaluateGitArgvSafety) and, when the mutation is
// allowed, hands off to the real git. Because the shell resolves `git` through
// PATH, a shell wrapper such as `sh -c 'git push --force'` reaches this guard
// too — the residual the hook path leaves open.
//
// The command is hidden: it is a fleet-internal mechanism, not an operator
// surface. Flag parsing is disabled so every git flag (`--force`, `--`, ...)
// reaches the guard verbatim rather than being claimed by cobra.
func newGitGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "git-guard [git-args...]",
		Short:              "Internal git-fence shim guard (fleet use only)",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runGitGuard(args)
		},
	}
}

func runGitGuard(gitArgs []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return gitGuardRefuse("git mutation working directory unavailable: " + err.Error())
	}
	if blocked, reason := evaluateGitArgvSafety(cwd, gitArgs); blocked {
		return gitGuardRefuse(reason)
	}
	// The root PATH strip (stripShimDirFromPath) has already removed the shim
	// directory from this process's PATH, so LookPath resolves the real git,
	// not the shim that invoked us — this is also what keeps the fence's own
	// `git rev-parse` probes from recursing back into the shim. Fail closed if
	// the real git is gone (a reset PATH that dropped it).
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return gitGuardRefuse("real git not found on PATH after shim strip: " + err.Error())
	}
	real := exec.Command(gitPath, gitArgs...)
	real.Stdin = os.Stdin
	real.Stdout = os.Stdout
	real.Stderr = os.Stderr
	if err := real.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitWithCode(exitErr.ExitCode())
			return nil
		}
		return gitGuardRefuse("git execution failed: " + err.Error())
	}
	return nil
}

// gitGuardRefuse prints the fence reason to stderr and exits non-zero, the
// contract the shell (and any harness that spawned it) reads as a blocked git
// mutation.
func gitGuardRefuse(reason string) error {
	fmt.Fprintln(os.Stderr, "[git-fence] "+reason)
	exitWithCode(1)
	return nil
}
