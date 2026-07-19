package stow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTempHome creates a temporary munsu home with data/learnings.md and/or
// data/captain.md containing the given lines. Returns the home path and a
// cleanup function.
func setupTempHome(t *testing.T, learnings, captain []string) string {
	t.Helper()
	homeDir := t.TempDir()
	dataDir := filepath.Join(homeDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if len(learnings) > 0 {
		if err := os.WriteFile(filepath.Join(dataDir, "learnings.md"),
			[]byte(strings.Join(learnings, "\n")+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if len(captain) > 0 {
		if err := os.WriteFile(filepath.Join(dataDir, "captain.md"),
			[]byte(strings.Join(captain, "\n")+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return homeDir
}

// readFileLines reads a file and returns non-empty trimmed lines.
func readFileLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestRunKinded_EmptyItems(t *testing.T) {
	homeDir := setupTempHome(t, nil, nil)
	res, err := RunKinded(homeDir, KindLearning, nil)
	if err != nil {
		t.Fatalf("RunKinded with nil items: %v", err)
	}
	if res.DataLearnings != "" {
		t.Errorf("expected empty DataLearnings, got %q", res.DataLearnings)
	}
	if res.DataCaptain != "" {
		t.Errorf("expected empty DataCaptain, got %q", res.DataCaptain)
	}

	res, err = RunKinded(homeDir, KindLearning, []string{})
	if err != nil {
		t.Fatalf("RunKinded with empty items: %v", err)
	}
	if res.DataLearnings != "" {
		t.Errorf("expected empty DataLearnings, got %q", res.DataLearnings)
	}
}

func TestRunKinded_InvalidKind(t *testing.T) {
	homeDir := setupTempHome(t, nil, nil)
	_, err := RunKinded(homeDir, "invalid", []string{"test"})
	if err == nil {
		t.Fatal("expected error for invalid kind")
	}
}

func TestRunKinded_Learning_LazyCreate(t *testing.T) {
	homeDir := setupTempHome(t, nil, nil)
	res, err := RunKinded(homeDir, KindLearning, []string{"Go 1.26 uses range-over-func"})
	if err != nil {
		t.Fatal(err)
	}
	if res.DataLearnings == "" {
		t.Fatal("expected DataLearnings path")
	}
	lines := readFileLines(t, res.DataLearnings)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "Go 1.26 uses range-over-func") {
		t.Errorf("line %q missing text", lines[0])
	}
}

func TestRunKinded_Captain_LazyCreate(t *testing.T) {
	homeDir := setupTempHome(t, nil, nil)
	res, err := RunKinded(homeDir, KindCaptain, []string{"Prefer simple layouts"})
	if err != nil {
		t.Fatal(err)
	}
	if res.DataCaptain == "" {
		t.Fatal("expected DataCaptain path")
	}
	lines := readFileLines(t, res.DataCaptain)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "Prefer simple layouts") {
		t.Errorf("line %q missing text", lines[0])
	}
}

func TestRunKinded_Learning_AppendNoMatch(t *testing.T) {
	existing := []string{"- 2025-01-01: Project uses Go modules"}
	homeDir := setupTempHome(t, existing, nil)
	res, err := RunKinded(homeDir, KindLearning, []string{"Prefer table-driven tests"})
	if err != nil {
		t.Fatal(err)
	}
	lines := readFileLines(t, res.DataLearnings)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (existing + appended), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "Prefer table-driven tests") {
		t.Errorf("appended line %q missing text", lines[1])
	}
}

func TestRunKinded_Learning_ReplaceOnMatch(t *testing.T) {
	existing := []string{"- 2025-01-01: Project uses Go modules for dependency management"}
	homeDir := setupTempHome(t, existing, nil)
	res, err := RunKinded(homeDir, KindLearning, []string{"Go modules"})
	if err != nil {
		t.Fatal(err)
	}
	lines := readFileLines(t, res.DataLearnings)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (replaced), got %d: %v", len(lines), lines)
	}
	if strings.Contains(lines[0], "2025-01-01") {
		t.Errorf("expected updated date, got old date in %q", lines[0])
	}
	if !strings.Contains(lines[0], "Go modules") {
		t.Errorf("replaced line %q missing text", lines[0])
	}
}

func TestRunKinded_Learning_ReplaceIsCaseInsensitive(t *testing.T) {
	existing := []string{"- 2025-01-01: Project uses GO MODULES"}
	homeDir := setupTempHome(t, existing, nil)
	res, err := RunKinded(homeDir, KindLearning, []string{"go modules"})
	if err != nil {
		t.Fatal(err)
	}
	lines := readFileLines(t, res.DataLearnings)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (replaced), got %d", len(lines))
	}
}

func TestRunKinded_Captain_ReplaceOnMatch(t *testing.T) {
	existing := []string{"- 2025-01-01: Prefer simple project layouts"}
	homeDir := setupTempHome(t, nil, existing)
	res, err := RunKinded(homeDir, KindCaptain, []string{"simple project layouts"})
	if err != nil {
		t.Fatal(err)
	}
	lines := readFileLines(t, res.DataCaptain)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (replaced), got %d: %v", len(lines), lines)
	}
}

func TestRunKinded_Captain_AppendNoMatch(t *testing.T) {
	existing := []string{"- 2025-01-01: Prefer simple project layouts"}
	homeDir := setupTempHome(t, nil, existing)
	res, err := RunKinded(homeDir, KindCaptain, []string{"Always write tests first"})
	if err != nil {
		t.Fatal(err)
	}
	lines := readFileLines(t, res.DataCaptain)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
}

func TestRunKinded_MultipleItems_SomeMatch(t *testing.T) {
	existing := []string{
		"- 2025-01-01: Project uses Go modules",
		"- 2025-01-01: Prefer simple layouts",
	}
	homeDir := setupTempHome(t, existing, nil)
	res, err := RunKinded(homeDir, KindLearning, []string{
		"Go modules",               // matches first, replaces
		"Always write tests first", // no match, appends
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := readFileLines(t, res.DataLearnings)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (1 replaced + 1 unchanged + 1 appended), got %d: %v", len(lines), lines)
	}
}

func TestRunKinded_EmptyTextItems(t *testing.T) {
	homeDir := setupTempHome(t, nil, nil)
	res, err := RunKinded(homeDir, KindLearning, []string{"", "  ", "first"})
	if err != nil {
		t.Fatal(err)
	}
	lines := readFileLines(t, res.DataLearnings)
	if len(lines) != 1 {
		t.Fatalf("expected only 1 non-empty item, got %d: %v", len(lines), lines)
	}
}

func TestRun(t *testing.T) {
	homeDir := setupTempHome(t, nil, nil)
	res, err := Run(homeDir, []string{"test learning"})
	if err != nil {
		t.Fatal(err)
	}
	if res.DataLearnings == "" {
		t.Fatal("expected learnings path")
	}
	if res.DataCaptain != "" {
		t.Error("expected no captain path from Run()")
	}
}

func TestReplaceMatching(t *testing.T) {
	entries := []string{
		"- 2025-01-01: Go modules",
		"- 2025-01-01: Prefer simple",
	}

	// Match - case insensitive
	replaced := replaceMatching(entries, "GO MODULES", "- 2026-01-01: Go modules updated")
	if !replaced {
		t.Fatal("expected match")
	}
	if entries[0] != "- 2026-01-01: Go modules updated" {
		t.Errorf("expected replaced entry, got %q", entries[0])
	}

	// No match
	replaced = replaceMatching(entries, "something entirely different", "- 2026: new")
	if replaced {
		t.Fatal("expected no match")
	}
}

func TestReadEntries_MissingFile(t *testing.T) {
	homeDir := t.TempDir()
	path := filepath.Join(homeDir, "nonexistent.md")
	entries, err := readEntries(path)
	if !errors.Is(err, errMissingFile) {
		t.Fatalf("expected errMissingFile, got %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries, got %v", entries)
	}
}

func TestReadEntries_NonENOENTError(t *testing.T) {
	// Use a directory path, which will fail with EISDIR on Open.
	homeDir := t.TempDir()
	_, err := readEntries(homeDir)
	if err == nil {
		t.Fatal("expected error for directory path")
	}
	if errors.Is(err, errMissingFile) {
		t.Fatal("expected non-ENOENT error, got errMissingFile")
	}
}
