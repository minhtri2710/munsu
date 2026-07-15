package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Project represents a registered or ad-hoc project entry.
type Project struct {
	Name        string
	Mode        string // feat, fix, refactor, etc.
	Yolo        bool
	Description string
	Added       string // date string
}

// RegistryPath returns the path to the projects.md registry file.
func RegistryPath(homeDir string) string {
	return filepath.Join(homeDir, "data", "projects.md")
}

// ProjectsDir returns the path where cloned project repos live.
func ProjectsDir(homeDir string) string {
	return filepath.Join(homeDir, "projects")
}

// ParseEntry parses a single registry line into a Project.
// Format: - <name> [<mode>] [+yolo] - <description> (added <date>)
func ParseEntry(line string) (*Project, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "- ") {
		return nil, fmt.Errorf("invalid project entry format: %q", line)
	}

	rest := line[2:] // skip "- "

	// Find the first " - " as the LHS/RHS separator
	sepIdx := strings.Index(rest, " - ")
	if sepIdx < 0 {
		return nil, fmt.Errorf("missing ' - ' separator in: %q", line)
	}

	lhs := rest[:sepIdx]
	rhs := strings.TrimSpace(rest[sepIdx+3:])

	// Parse RHS: "<description> (added <date>)"
	addedIdx := strings.LastIndex(rhs, "(added ")
	if addedIdx < 0 {
		return nil, fmt.Errorf("missing '(added ...)' in: %q", line)
	}

	desc := strings.TrimSpace(rhs[:addedIdx])
	datePart := rhs[addedIdx+7:] // skip "(added "
	date := strings.TrimSuffix(strings.TrimSpace(datePart), ")")

	// Parse LHS tokens: <name> [<mode>] [+yolo]
	tokens := strings.Fields(lhs)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("missing project name in: %q", line)
	}

	p := &Project{
		Name:        tokens[0],
		Description: desc,
		Added:       date,
	}

	for _, tok := range tokens[1:] {
		if tok == "+yolo" {
			p.Yolo = true
		} else {
			p.Mode = tok
		}
	}

	return p, nil
}

// FormatEntry formats a Project as a registry line.
func FormatEntry(p *Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s", p.Name)
	if p.Mode != "" {
		fmt.Fprintf(&b, " %s", p.Mode)
	}
	if p.Yolo {
		b.WriteString(" +yolo")
	}
	fmt.Fprintf(&b, " - %s (added %s)", p.Description, p.Added)
	return b.String()
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

// Add registers a project. If pathOrURL is a URL, clones it first.
func Add(homeDir, name, pathOrURL, mode string, yolo bool) error {
	regPath := RegistryPath(homeDir)

	// Ensure data directory exists
	if err := os.MkdirAll(filepath.Dir(regPath), 0755); err != nil {
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

	// Append registry entry
	p := &Project{
		Name:        name,
		Mode:        mode,
		Yolo:        yolo,
		Description: pathOrURL,
		Added:       today(),
	}

	f, err := os.OpenFile(regPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening registry: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintln(f, FormatEntry(p)); err != nil {
		return fmt.Errorf("writing registry: %w", err)
	}

	fmt.Printf("Registered project %q (%s)\n", name, pathOrURL)
	return nil
}

// List reads and returns all registered projects.
func List(homeDir string) ([]*Project, error) {
	regPath := RegistryPath(homeDir)
	data, err := os.ReadFile(regPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading registry: %w", err)
	}

	var projects []*Project
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := ParseEntry(line)
		if err != nil {
			return nil, fmt.Errorf("parsing registry line %q: %w", line, err)
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// ListFromFile reads and returns all registered projects from a specific registry file.
// This is used by consumers outside the project package that need to specify a path.
func ListFromFile(regPath string) ([]*Project, error) {
	data, err := os.ReadFile(regPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading registry: %w", err)
	}

	var projects []*Project
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := ParseEntry(line)
		if err != nil {
			return nil, fmt.Errorf("parsing registry line %q: %w", line, err)
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// Find looks up a project by name in the registry.

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
	projects, err := List(homeDir)
	if err != nil {
		return err
	}

	regPath := RegistryPath(homeDir)
	f, err := os.Create(regPath)
	if err != nil {
		return fmt.Errorf("rewriting registry: %w", err)
	}
	defer f.Close()

	found := false
	for _, p := range projects {
		if p.Name == name {
			found = true
			continue
		}
		if _, err := fmt.Fprintln(f, FormatEntry(p)); err != nil {
			return fmt.Errorf("writing registry: %w", err)
		}
	}

	if !found {
		return fmt.Errorf("project %q not found in registry", name)
	}
	fmt.Printf("Removed project %q from registry\n", name)
	return nil
}

// Mode returns the delivery mode and yolo flag for a project.
func Mode(homeDir, name string) (mode string, yolo bool, err error) {
	p, err := Find(homeDir, name)
	if err != nil {
		return "", false, err
	}
	return p.Mode, p.Yolo, nil
}

// ResolveRepoPath resolves a project name to an absolute repo path.
// Priority:
//  1. If the project Description is an existing absolute directory → use it
//  2. If projects/<name> is an existing directory → use it
//  3. Otherwise → error
func ResolveRepoPath(homeDir, name string) (string, error) {
	p, err := Find(homeDir, name)
	if err != nil {
		return "", err
	}

	// 1. Check if Description is an absolute existing path
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

// ResolveAdhoc detects a git repo from cwd and returns a transient Project.
// Uses git rev-parse --show-toplevel to find the repo root, then uses the
// directory basename as the project name. No registry write occurs.
func ResolveAdhoc() (*Project, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("not in a git repository: %w", err)
	}
	repoRoot := strings.TrimSpace(string(out))
	name := filepath.Base(repoRoot)
	return &Project{
		Name:        name,
		Description: repoRoot,
		Added:       today(),
	}, nil
}
