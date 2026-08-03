package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
)

// Project represents a registered or ad-hoc project entry.
type Project struct {
	Name        string
	Mode        string // feat, fix, refactor, etc.
	Yolo        bool
	Description string
	Added       string // date string
}

// ProjectsDir returns the path where cloned project repos live.
func ProjectsDir(homeDir string) string {
	return filepath.Join(homeDir, "projects")
}

// today returns today's date as YYYY-MM-DD.
func today() string {
	return time.Now().Format("2006-01-02")
}

// isURL reports whether s looks like a git remote URL.
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, "ssh://")
}

// Add registers a project in the canonical Fleet Registry. If pathOrURL is a
// URL, it is cloned first. If the name is already registered, the existing
// entry is updated in-place.
func Add(homeDir, name, pathOrURL, mode string, yolo bool) error {
	// Clone if URL
	if isURL(pathOrURL) {
		projDir := filepath.Join(ProjectsDir(homeDir), name)
		if err := os.MkdirAll(ProjectsDir(homeDir), 0755); err != nil {
			return fmt.Errorf("creating projects directory: %w", err)
		}
		cmd := exec.Command("git", "clone", pathOrURL, projDir)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cloning %s: %w", pathOrURL, err)
		}
	}

	r, err := openRegistry(homeDir)
	if err != nil {
		return err
	}
	projectID, err := domain.NewProjectID(name)
	if err != nil {
		return fmt.Errorf("register project %q: %w", name, err)
	}
	_, gerr := r.GetProject(projectID)
	if gerr == nil {
		// Update in-place: register the same ID with the new definition.
		rev, err := r.ProjectRevision()
		if err != nil {
			return fmt.Errorf("reading project registry: %w", err)
		}
		req := RegisterProjectRequest{
			HomeID:       r.HomeID(),
			ProjectID:    projectID,
			Name:         name,
			Path:         pathOrURL,
			Mode:         mode,
			Yolo:         yolo,
			Precondition: preconditionOf(rev),
			Reason:       "update",
		}
		if _, err := r.RegisterProject(opFor(req), req); err != nil {
			return fmt.Errorf("writing project registry: %w", err)
		}
		fmt.Printf("Updated project %q (%s)\n", name, pathOrURL)
		return nil
	}
	if !errors.Is(gerr, ErrNotFound) {
		return fmt.Errorf("reading project registry: %w", gerr)
	}
	rev, err := r.ProjectRevision()
	if err != nil {
		return fmt.Errorf("reading project registry: %w", err)
	}
	req := RegisterProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    projectID,
		Name:         name,
		Path:         pathOrURL,
		Mode:         mode,
		Yolo:         yolo,
		Precondition: preconditionOf(rev),
		Reason:       "register",
	}
	if _, err := r.RegisterProject(opFor(req), req); err != nil {
		return fmt.Errorf("writing project registry: %w", err)
	}
	fmt.Printf("Registered project %q (%s)\n", name, pathOrURL)
	return nil
}

// List reads and returns all registered projects from the canonical Fleet Registry.
func List(homeDir string) ([]*Project, error) {
	r, err := openRegistry(homeDir)
	if err != nil {
		return nil, err
	}
	projects, err := r.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("reading project registry: %w", err)
	}
	var result []*Project
	for _, p := range projects {
		result = append(result, &Project{
			Name:        p.Name,
			Mode:        p.Mode,
			Yolo:        p.Yolo,
			Description: p.Path,
			Added:       today(),
		})
	}
	return result, nil
}

// Find looks up a project by name in the registry.
func Find(homeDir, name string) (*Project, error) {
	projects, err := List(homeDir)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("project %q not found in registry", name)
}

// Rm removes a project from the canonical Fleet Registry (but does not delete
// cloned repos). A project that is still owned by a Captain cannot be retired.
func Rm(homeDir, name string) error {
	r, err := openRegistry(homeDir)
	if err != nil {
		return err
	}
	projectID, err := domain.NewProjectID(name)
	if err != nil {
		return fmt.Errorf("remove project %q: %w", name, err)
	}
	if _, err := r.GetProject(projectID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("project %q not found in registry", name)
		}
		return fmt.Errorf("reading project registry: %w", err)
	}
	rev, err := r.ProjectRevision()
	if err != nil {
		return fmt.Errorf("reading project registry: %w", err)
	}
	ret := RetireProjectRequest{
		HomeID:       r.HomeID(),
		ProjectID:    projectID,
		Precondition: preconditionOf(rev),
		Reason:       "remove",
	}
	if _, err := r.RetireProject(opFor(ret), ret); err != nil {
		return fmt.Errorf("writing project registry: %w", err)
	}
	fmt.Printf("Removed project %q from registry\n", name)
	return nil
}

// Mode returns the delivery mode for a project.
// If mode is empty, defaults to "no-mistakes" (the default delivery mode).
func Mode(homeDir, name string) (mode string, yolo bool, err error) {
	p, err := Find(homeDir, name)
	if err != nil {
		return "", false, err
	}
	mode = p.Mode
	if mode == "" {
		mode = "no-mistakes"
	}
	return mode, p.Yolo, nil
}

// ResolveRepoPath resolves a project name to an absolute repo path.
// Priority:
//  1. If the project Path is an existing absolute directory -> use it
//  2. If projects/<name> is an existing directory -> use it
//  3. Otherwise -> error
func ResolveRepoPath(homeDir, name string) (string, error) {
	p, err := Find(homeDir, name)
	if err != nil {
		return "", err
	}

	// 1. Check if Path is an absolute existing path
	if filepath.IsAbs(p.Description) {
		if fi, statErr := os.Stat(p.Description); statErr == nil && fi.IsDir() {
			return p.Description, nil
		}
	}

	// 2. Check if projects/<name> exists
	projDir := filepath.Join(ProjectsDir(homeDir), name)
	if fi, statErr := os.Stat(projDir); statErr == nil && fi.IsDir() {
		return projDir, nil
	}

	return "", fmt.Errorf("project %q not resolvable: no local path or cloned repo found for %q", name, p.Description)
}

// gitRoot returns the absolute path to the git repository root from cwd.
func gitRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveAdhoc detects a git repo from cwd and returns a transient Project.
// Uses git rev-parse --show-toplevel to find the repo root, then uses the
// directory basename as the project name. No registry write occurs.
func ResolveAdhoc() (*Project, error) {
	repoRoot, err := gitRoot()
	if err != nil {
		return nil, err
	}
	name := filepath.Base(repoRoot)
	return &Project{
		Name:        name,
		Description: repoRoot,
		Added:       today(),
	}, nil
}

// ResolveFromCwd detects the git repo from cwd and tries to match its root
// against registered project paths. If a registry project matches, the
// registered project is returned. Falls back to ResolveAdhoc.
func ResolveFromCwd(homeDir string) (*Project, error) {
	repoRoot, err := gitRoot()
	if err != nil {
		return nil, err
	}

	cleanRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		cleanRoot = filepath.Clean(repoRoot)
	}

	projects, err := List(homeDir)
	if err != nil {
		return ResolveAdhoc()
	}

	for _, p := range projects {
		if !filepath.IsAbs(p.Description) {
			continue
		}
		cleanDesc, err := filepath.EvalSymlinks(p.Description)
		if err != nil {
			cleanDesc = filepath.Clean(p.Description)
		}
		if cleanDesc == cleanRoot {
			return p, nil
		}
	}

	return ResolveAdhoc()
}