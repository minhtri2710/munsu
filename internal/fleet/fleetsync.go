// Package fleet manages project clone synchronization.
package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SyncResult holds the result of a fleet-sync operation.
type SyncResult struct {
	Synced []string
	Stuck  []string
	Errors []string
}

// Sync fast-forwards all remote-backed project clones under the given projects dir.
// If projectName is non-empty, syncs only that
func Sync(home string, projectName string) (*SyncResult, error) {
	res := &SyncResult{}

	projectsDir := filepath.Join(home, "projects")
	projectsFile := filepath.Join(home, "data", "projects.md")

	// Read the project registry to find directories
	dirs, localDirs, err := readProjectDirs(projectsFile, projectsDir)
	if err != nil {
		return res, fmt.Errorf("reading projects: %w", err)
	}

	// Track which dirs are local-path registrations
	localSet := make(map[string]bool)
	for _, d := range localDirs {
		localSet[d] = true
	}

	for _, dir := range dirs {
		if projectName != "" && filepath.Base(dir) != projectName {
			continue
		}

		isLocal := localSet[dir]
		if err := syncOne(dir); err != nil {
			if isLocal && strings.Contains(err.Error(), "dirty working tree") {
				fmt.Fprintf(os.Stderr, "WARN: %s: %v (local path, skipping)\n", filepath.Base(dir), err)
			} else {
				res.Stuck = append(res.Stuck, fmt.Sprintf("%s: %v", filepath.Base(dir), err))
			}
		} else {
			res.Synced = append(res.Synced, filepath.Base(dir))
		}
	}

	return res, nil
}

// readProjectDirs reads the project registry and resolves project directories.
// Uses the project registry to find both cloned repos (in projects/<name>)
// and local-path registrations (where Description is an absolute existing path).
func readProjectDirs(projectsFile, projectsDir string) (dirs []string, localDirs []string, _ error) {
	projects, err := ListFromFile(projectsFile)
	if err != nil {
		return nil, nil, err
	}

	for _, p := range projects {
		// 1. Check if Description is an absolute existing path (local path registration)
		if filepath.IsAbs(p.Description) {
			if fi, statErr := os.Stat(p.Description); statErr == nil && fi.IsDir() {
				dirs = append(dirs, p.Description)
				localDirs = append(localDirs, p.Description)
				continue
			}
		}

		// 2. Check if projects/<name> exists (cloned repo)
		dir := filepath.Join(projectsDir, p.Name)
		if fi, statErr := os.Stat(dir); statErr == nil && fi.IsDir() {
			dirs = append(dirs, dir)
		}
	}

	return dirs, localDirs, nil
}

// syncOne fast-forwards a single project clone.
func syncOne(dir string) error {
	// Check it's a git repo
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not a git repository")
	}

	// Recover stale packed-refs.lock
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(filepath.Join(gitDir, "packed-refs.lock")); err == nil {
		os.Remove(filepath.Join(gitDir, "packed-refs.lock"))
	}

	// Check if dirty
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()
	if len(strings.TrimSpace(string(statusOut))) > 0 {
		return fmt.Errorf("dirty working tree, skipping")
	}

	// Fetch origin
	fetchCmd := exec.Command("git", "fetch", "origin")
	fetchCmd.Dir = dir
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch failed: %s", strings.TrimSpace(string(out)))
	}

	// Identify default branch
	var defaultBranch string
	for _, candidate := range []string{"main", "master"} {
		refCmd := exec.Command("git", "rev-parse", "--verify", "refs/remotes/origin/"+candidate)
		refCmd.Dir = dir
		if refCmd.Run() == nil {
			defaultBranch = candidate
			break
		}
	}
	if defaultBranch == "" {
		return fmt.Errorf("no default branch (main/master) found on origin")
	}

	// Fast-forward merge
	mergeCmd := exec.Command("git", "merge", "--ff-only", "origin/"+defaultBranch)
	mergeCmd.Dir = dir
	if out, err := mergeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ff-merge failed: %s", strings.TrimSpace(string(out)))
	}

	return nil
}
