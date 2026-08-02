package fleet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/configmigration"
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

// checkLegacyConfig returns an error if legacy config files exist and the
// typed documents are not yet installed. This is called at the top of
// registry-mutating functions to prevent writes to legacy files.
func checkLegacyConfig(homeDir string) error {
	needed, _ := configmigration.NeedsConfigMigration(homeDir)
	if needed {
		return configmigration.LegacyConfigCheckError(homeDir)
	}
	return nil
}

// Add registers a project. If pathOrURL is a URL, clones it first.
// If the name is already registered, updates the existing entry in-place.
func Add(homeDir, name, pathOrURL, mode string, yolo bool) error {
	// Check for legacy config before writing.
	if err := checkLegacyConfig(homeDir); err != nil {
		return err
	}

	// Ensure data directory exists
	if err := os.MkdirAll(filepath.Join(homeDir, "data"), 0755); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

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

	// Load or initialize typed project registry.
	registry, err := config.LoadProjectRegistry(homeDir)
	if err != nil {
		// If the file doesn't exist yet, start with an empty registry.
		if errors.Is(err, os.ErrNotExist) {
			registry = config.ProjectRegistryDocument{
				SchemaVersion: config.ProjectRegistrySchemaVersion,
			}
		} else {
			return fmt.Errorf("reading project registry: %w", err)
		}
	}

	record := config.ProjectRecord{
		Name: name,
		Path: pathOrURL,
		Mode: mode,
	}
	// Yolo (skip pre-flight gates) is preserved through the typed schema as
	// requireNoMistakes=false. When yolo is false the overlay is left unset so
	// an in-place update clears a previously stored +yolo flag.
	if yolo {
		falseVal := false
		record.Config.RequireNoMistakes = &falseVal
	}

	// Check if name already exists — update in-place to avoid duplicates.
	for i, p := range registry.Projects {
		if p.Name == name {
			registry.Projects[i] = record
			if err := config.StoreProjectRegistry(homeDir, registry); err != nil {
				return fmt.Errorf("writing project registry: %w", err)
			}
			fmt.Printf("Updated project %q (%s)\n", name, pathOrURL)
			return nil
		}
	}

	// Append new entry.
	registry.Projects = append(registry.Projects, record)
	if err := config.StoreProjectRegistry(homeDir, registry); err != nil {
		return fmt.Errorf("writing project registry: %w", err)
	}

	fmt.Printf("Registered project %q (%s)\n", name, pathOrURL)
	return nil
}

// List reads and returns all registered projects.
func List(homeDir string) ([]*Project, error) {
	if err := checkLegacyConfig(homeDir); err != nil {
		return nil, err
	}

	registry, err := config.LoadProjectRegistry(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading project registry: %w", err)
	}

	var projects []*Project
	for _, p := range registry.Projects {
		// The typed schema expresses the legacy +yolo flag as
		// requireNoMistakes=false; map it back so CLI list/mode surface it.
		yolo := p.Config.RequireNoMistakes != nil && !*p.Config.RequireNoMistakes
		projects = append(projects, &Project{
			Name:        p.Name,
			Mode:        p.Mode,
			Yolo:        yolo,
			Description: p.Path,
			Added:       today(),
		})
	}
	return projects, nil
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

// Rm removes a project from the registry (but does not delete cloned repos).
func Rm(homeDir, name string) error {
	if err := checkLegacyConfig(homeDir); err != nil {
		return err
	}

	registry, err := config.LoadProjectRegistry(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("project %q not found in registry", name)
		}
		return fmt.Errorf("reading project registry: %w", err)
	}

	found := false
	var updated []config.ProjectRecord
	for _, p := range registry.Projects {
		if p.Name == name {
			found = true
			continue
		}
		updated = append(updated, p)
	}
	if !found {
		return fmt.Errorf("project %q not found in registry", name)
	}

	registry.Projects = updated
	if err := config.StoreProjectRegistry(homeDir, registry); err != nil {
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
