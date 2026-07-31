// Package configmigration implements the hard-cut migration from legacy config
// files (captains.md, projects.md, soldier-dispatch.json) to typed config
// documents (config/base.json, data/captains.json, data/projects.json).
package configmigration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LegacyProject represents a parsed project entry from projects.md.
type LegacyProject struct {
	Name        string
	Mode        string
	Yolo        bool
	Description string
	Added       string
}

// LegacyCaptainInfo represents a parsed captain entry from captains.md.
type LegacyCaptainInfo struct {
	ID      string
	Home    string
	Scope   string
	Project string
	Added   string
}

// LegacyDispatchConfig is the parsed soldier-dispatch.json structure.
type LegacyDispatchConfig struct {
	DefaultHarness string              `json:"defaultHarness,omitempty"`
	DefaultModel   string              `json:"defaultModel,omitempty"`
	DefaultEffort  string              `json:"defaultEffort,omitempty"`
	Default        *DispatchCandidate  `json:"default,omitempty"`
	Profiles       []DispatchProfile   `json:"profiles,omitempty"`
	Rules          []DispatchProfile   `json:"rules,omitempty"`
}

// DispatchCandidate mirrors config.DispatchCandidate for the migration package.
type DispatchCandidate struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// DispatchProfile mirrors config.DispatchProfile for the migration package.
type DispatchProfile struct {
	Name           string              `json:"name,omitempty"`
	Match          []string            `json:"match,omitempty"`
	When           string              `json:"when,omitempty"`
	Harness        string              `json:"harness,omitempty"`
	Model          string              `json:"model,omitempty"`
	Effort         string              `json:"effort,omitempty"`
	MaxConcurrent  int                 `json:"maxConcurrent,omitempty"`
	SelectStrategy string              `json:"select,omitempty"`
	Why            string              `json:"why,omitempty"`
	Use            []DispatchCandidate `json:"use,omitempty"`
}

// registryPath returns the path to the projects.md registry file.
func registryPath(homeDir string) string {
	return filepath.Join(homeDir, "data", "projects.md")
}

// ProjectsDir returns the path where cloned project repos live.
func ProjectsDir(homeDir string) string {
	return filepath.Join(homeDir, "projects")
}

// parseEntry parses a single registry line into a LegacyProject.
// Format: - <name> [<mode>] [+yolo] - <description> (added <date>)
func parseEntry(line string) (*LegacyProject, error) {
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

	p := &LegacyProject{
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

// formatEntry formats a LegacyProject as a registry line.
func formatEntry(p *LegacyProject) string {
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

// listFromFile reads and returns all registered projects from a specific file.
func listFromFile(regPath string) ([]*LegacyProject, error) {
	data, err := os.ReadFile(regPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading registry: %w", err)
	}

	var projects []*LegacyProject
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := parseEntry(line)
		if err != nil {
			return nil, fmt.Errorf("parsing registry line %q: %w", line, err)
		}
		projects = append(projects, p)
	}
	return projects, nil
}

// captainRegistryPath returns the path to the captains.md registry file.
func captainRegistryPath(parentHome string) string {
	return filepath.Join(parentHome, "data", "captains.md")
}

// parseRegistry parses a captains.md file and returns entries.
func parseRegistry(registryPath string) ([]LegacyCaptainInfo, error) {
	f, err := os.Open(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening registry %s: %w", registryPath, err)
	}
	defer f.Close()

	var mates []LegacyCaptainInfo
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		parts := strings.SplitN(rest, " - ", 2)
		if len(parts) < 1 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		if id == "" {
			continue
		}
		entry := LegacyCaptainInfo{ID: id}

		if len(parts) >= 2 {
			metaPart := parts[1]
			if idx := strings.LastIndex(metaPart, "("); idx >= 0 {
				meta := metaPart[idx+1:]
				if endIdx := strings.LastIndex(meta, ")"); endIdx >= 0 {
					meta = meta[:endIdx]
				}
				entry.Home = extractMetaValue(meta, "home:")
				entry.Scope = extractMetaValue(meta, "scope:")
				entry.Project = extractMetaValue(meta, "projects:")
				entry.Added = extractMetaValue(meta, "added:")
			}
		}

		mates = append(mates, entry)
	}
	return mates, scanner.Err()
}

func extractMetaValue(meta, key string) string {
	parts := strings.Split(meta, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, key) {
			v := strings.TrimSpace(strings.TrimPrefix(p, key))
			return v
		}
	}
	return ""
}

// dispatchPath returns the path to soldier-dispatch.json.
func dispatchPath(homeDir string) string {
	return strings.TrimRight(homeDir, "/") + "/config/soldier-dispatch.json"
}

// dispatchActive reports whether a soldier-dispatch.json file exists.
func dispatchActive(homeDir string) bool {
	_, err := os.Stat(dispatchPath(homeDir))
	return err == nil
}

// loadDispatch reads and parses a soldier-dispatch.json file.
func loadDispatch(path string) (*LegacyDispatchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading dispatch config: %w", err)
	}
	var cfg LegacyDispatchConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing dispatch config: %w", err)
	}
	normalizeLegacyDispatch(&cfg)
	return &cfg, nil
}

func normalizeLegacyDispatch(cfg *LegacyDispatchConfig) {
	if cfg.Default != nil {
		if cfg.DefaultHarness == "" {
			cfg.DefaultHarness = cfg.Default.Harness
		}
		if cfg.DefaultModel == "" {
			cfg.DefaultModel = cfg.Default.Model
		}
		if cfg.DefaultEffort == "" {
			cfg.DefaultEffort = cfg.Default.Effort
		}
	}
	if len(cfg.Profiles) == 0 && len(cfg.Rules) > 0 {
		cfg.Profiles = append([]DispatchProfile(nil), cfg.Rules...)
	}
	for i := range cfg.Profiles {
		normalizeLegacyProfile(&cfg.Profiles[i])
	}
}

func normalizeLegacyProfile(p *DispatchProfile) {
	if len(p.Match) == 0 && p.When != "" {
		p.Match = []string{p.When}
	}
	if len(p.Use) == 0 {
		return
	}
	if p.Harness == "" {
		c := p.Use[0]
		p.Harness = c.Harness
		if p.Model == "" {
			p.Model = c.Model
		}
		if p.Effort == "" {
			p.Effort = c.Effort
		}
	}
}

// getInheritableList returns the list of inheritable config file names.
func getInheritableList() []string {
	env := os.Getenv("MUNSU_INHERITABLE_CONFIG")
	if env != "" {
		return strings.Split(env, ":")
	}
	return []string{"soldier-harness", "soldier-dispatch.json", "backlog-backend"}
}

// pushProjectsRegistry copies the parent's data/projects.md into the captain
// home. Entries keep absolute path descriptions so ResolveRepoPath works
// without cloning into the captain projects/ tree.
func pushProjectsRegistry(parentHome, captainHome string, logFn func(action, name string)) error {
	src := registryPath(parentHome)
	dst := registryPath(captainHome)

	if !isSafeConfigPath(dst, parentHome, captainHome) {
		return fmt.Errorf("projects.md path escapes captain container — refuse")
	}
	if isGitTracked(filepath.Dir(dst), filepath.Base(dst)) {
		return fmt.Errorf("projects.md is tracked in captain git — must be gitignored")
	}

	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			if _, stErr := os.Stat(dst); stErr == nil {
				if err := os.Remove(dst); err != nil {
					logFn("delete-failed", "projects.md — "+err.Error())
					return fmt.Errorf("mirror deletion: removing projects.md: %w", err)
				}
				logFn("deleted", "projects.md")
			}
			return nil
		}
		logFn("skipped", "projects.md — "+err.Error())
		return nil
	}

	if _, err := listFromFile(src); err != nil {
		return fmt.Errorf("reading parent projects.md: %w", err)
	}

	if err := atomicWriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("writing projects.md: %w", err)
	}
	logFn("pushed", "projects.md")
	return nil
}

func isSafeConfigPath(dst, parentHome, captainHome string) bool {
	smCanon, err := canonicalCaptainHome(captainHome)
	if err != nil {
		return false
	}
	canonDst, err := resolveDeepestAncestor(dst)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(smCanon, canonDst)
	if err != nil {
		return false
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	parentCanon, err := canonicalCaptainHome(parentHome)
	if err != nil {
		return false
	}
	parentRel, err := filepath.Rel(parentCanon, canonDst)
	if err == nil && !strings.HasPrefix(parentRel, "..") && !filepath.IsAbs(parentRel) {
		if !strings.HasPrefix(canonDst, smCanon+string(filepath.Separator)) && canonDst != smCanon {
			return false
		}
	}
	return true
}

func canonicalCaptainHome(homePath string) (string, error) {
	abs, err := filepath.Abs(homePath)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func resolveDeepestAncestor(path string) (string, error) {
	candidate := path
	for {
		_, err := os.Stat(candidate)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", err
			}
			abs, err := filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			suffix, _ := filepath.Rel(candidate, path)
			if suffix != "." {
				return filepath.Join(abs, suffix), nil
			}
			return abs, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return filepath.Abs(path)
		}
		candidate = parent
	}
}

func isGitTracked(dir, name string) bool {
	out, err := execCommand("git", "-C", dir, "ls-files", "--error-unmatch", name).CombinedOutput()
	return err == nil && len(out) > 0
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil {
		if info, statErr := os.Stat(path); statErr == nil && string(existing) == string(data) && info.Mode().Perm() == mode.Perm() {
			return nil
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".munsu-inherit-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("setting temp file mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// execCommand is overridable in tests.
var execCommand = func(name string, args ...string) *exec.Cmd {
	//nolint:gosec // Git commands are run with trusted arguments.
	return exec.Command(name, args...)
}