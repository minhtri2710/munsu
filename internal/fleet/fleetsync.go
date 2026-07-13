// Package fleet manages project clone synchronization.
package fleet

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SyncResult holds the result of a fleet-sync operation.
type SyncResult struct {
	Synced   []string
	Stuck    []string
	Errors   []string
}

// Sync fast-forwards all remote-backed project clones under the given projects dir.
// If projectName is non-empty, syncs only that project.
func Sync(home string, projectName string) (*SyncResult, error) {
	res := &SyncResult{}

	projectsDir := filepath.Join(home, "projects")
	projectsFile := filepath.Join(home, "data", "projects.md")

	// Read the project registry to find directories
	dirs, err := readProjectDirs(projectsFile, projectsDir)
	if err != nil {
		return res, fmt.Errorf("reading projects: %w", err)
	}

	for _, dir := range dirs {
		if projectName != "" && filepath.Base(dir) != projectName {
			continue
		}

		if err := syncOne(dir); err != nil {
			res.Stuck = append(res.Stuck, fmt.Sprintf("%s: %v", filepath.Base(dir), err))
		} else {
			res.Synced = append(res.Synced, filepath.Base(dir))
		}
	}

	return res, nil
}

// readProjectDirs reads the project registry and resolves project directories.
func readProjectDirs(projectsFile, projectsDir string) ([]string, error) {
	f, err := os.Open(projectsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var dirs []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "##") {
			continue
		}

		// Parse: - <name> [<mode>] [+yolo] - <desc> (added <date>)
		raw = strings.TrimPrefix(raw, "- ")
		parts := strings.SplitN(raw, " ", 2)
		if len(parts) < 1 {
			continue
		}
		name := parts[0]

		// Skip synthetic entries (mode markers like [no-mistakes])
		if strings.HasPrefix(name, "[") {
			continue
		}

		dir := filepath.Join(projectsDir, name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}

	return dirs, scanner.Err()
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
