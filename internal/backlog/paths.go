package backlog

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Paths identifies the separate development and fleet-runtime backlog files.
type Paths struct {
	Development string
	Runtime     string
	Config      string
}

// ResolvePaths resolves the development backlog from the nearest .tasks.toml
// and the runtime backlog from the selected munsu home.
func ResolvePaths(cwd, homeDir string) (Paths, error) {
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return Paths{}, fmt.Errorf("resolving working directory: %w", err)
	}
	homeAbs, err := filepath.Abs(homeDir)
	if err != nil {
		return Paths{}, fmt.Errorf("resolving munsu home: %w", err)
	}

	paths := Paths{
		Development: filepath.Join(cwdAbs, "backlog.md"),
		Runtime:     filepath.Join(homeAbs, "data", "backlog.md"),
	}

	configPath := findTasksConfig(cwdAbs)
	if configPath == "" {
		return paths, nil
	}
	developmentPath, err := markdownBacklogPath(configPath)
	if err != nil {
		return Paths{}, err
	}
	paths.Config = configPath
	paths.Development = developmentPath
	return paths, nil
}

func findTasksConfig(start string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, ".tasks.toml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func markdownBacklogPath(configPath string) (string, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return "", fmt.Errorf("opening tasks config: %w", err)
	}
	defer file.Close()

	inMarkdown := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inMarkdown = line == "[markdown]"
			continue
		}
		if !inMarkdown || !strings.HasPrefix(line, "path") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "path" {
			continue
		}
		pathValue, err := strconv.Unquote(strings.TrimSpace(value))
		if err != nil || pathValue == "" {
			return "", fmt.Errorf("invalid markdown.path in %s", configPath)
		}
		if filepath.IsAbs(pathValue) {
			return filepath.Clean(pathValue), nil
		}
		return filepath.Join(filepath.Dir(configPath), filepath.Clean(pathValue)), nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading tasks config: %w", err)
	}
	return filepath.Join(filepath.Dir(configPath), "backlog.md"), nil
}
