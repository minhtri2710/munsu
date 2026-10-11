package backend

import (
	"errors"
	"fmt"
	"strings"
)

const (
	originRemoteRefs = "refs/remotes/origin/"
	localRefs        = "refs/heads/"
)

var errOriginHeadUnset = errors.New("origin/HEAD is not set")

// Default is the default branch of a repository: its name, and the ref git
// resolves for its tip (the local branch when it exists, else origin/<Name>).
type Default struct {
	Name string
	Ref  string
}

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

// RemoteDefaultBranch returns the default branch as origin knows it:
// origin/HEAD when set, else the first of origin/main, origin/master. For
// callers that act on origin/<name>.
func RemoteDefaultBranch(repoPath string) (string, error) {
	branch, err := OriginHeadBranch(repoPath)
	if !errors.Is(err, errOriginHeadUnset) {
		return branch, err
	}
	if name, ok := firstExistingBranch(repoPath, originRemoteRefs); ok {
		return name, nil
	}
	return "", unresolvedDefault(repoPath, err)
}

// DefaultBranch returns the default branch of repoPath: origin/HEAD when set,
// else the first of local main, local master, origin/main, origin/master. A
// malformed origin/HEAD is an error, never a reason to fall back.
func DefaultBranch(repoPath string) (Default, error) {
	name, err := OriginHeadBranch(repoPath)
	if err != nil {
		if !errors.Is(err, errOriginHeadUnset) {
			return Default{}, err
		}
		var ok bool
		if name, ok = firstExistingBranch(repoPath, localRefs); !ok {
			if name, ok = firstExistingBranch(repoPath, originRemoteRefs); !ok {
				return Default{}, unresolvedDefault(repoPath, err)
			}
		}
	}
	if refExists(repoPath, localRefs+name) {
		return Default{Name: name, Ref: name}, nil
	}
	return Default{Name: name, Ref: "origin/" + name}, nil
}

func firstExistingBranch(repoPath, prefix string) (string, bool) {
	for _, name := range []string{"main", "master"} {
		if refExists(repoPath, prefix+name) {
			return name, true
		}
	}
	return "", false
}

func refExists(repoPath, ref string) bool {
	_, _, err := runWorktreeCommand("git", repoPath, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

func unresolvedDefault(repoPath string, unset error) error {
	return fmt.Errorf("cannot resolve default branch for %s: set origin/HEAD or create main or master: %w", repoPath, unset)
}
