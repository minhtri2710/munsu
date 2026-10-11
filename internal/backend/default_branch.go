package backend

import (
	"errors"
	"fmt"
	"strings"
)

const originRemoteRefs = "refs/remotes/origin/"

var errOriginHeadUnset = errors.New("origin/HEAD is not set")

// OriginHeadBranch returns the branch refs/remotes/origin/HEAD points at. It
// has no fallback: a missing origin/HEAD and a malformed one are both errors.
func OriginHeadBranch(repoPath string) (string, error) {
	out, _, err := runWorktreeCommand("git", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errOriginHeadUnset, err)
	}
	ref := strings.TrimSpace(string(out))
	branch, ok := strings.CutPrefix(ref, originRemoteRefs)
	if !ok || branch == "" {
		return "", fmt.Errorf("unexpected origin/HEAD symbolic ref format: %q", ref)
	}
	return branch, nil
}

// DefaultBranch returns the default branch of repoPath: origin/HEAD when set,
// else the first of origin/main, origin/master, main, master that exists. A
// malformed origin/HEAD is an error, never a reason to fall back.
func DefaultBranch(repoPath string) (string, error) {
	branch, err := OriginHeadBranch(repoPath)
	if !errors.Is(err, errOriginHeadUnset) {
		return branch, err
	}
	for _, prefix := range []string{originRemoteRefs, "refs/heads/"} {
		for _, name := range []string{"main", "master"} {
			if _, _, verr := runWorktreeCommand("git", repoPath, "rev-parse", "--verify", "--quiet", prefix+name); verr == nil {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("cannot resolve default branch for %s: set origin/HEAD or create main or master: %w", repoPath, err)
}
