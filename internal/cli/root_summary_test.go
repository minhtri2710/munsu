package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests are byte-for-byte characterization (golden) tests for the root
// "fleet summary" output produced when munsu runs with no subcommand (A-01).
// They lock the exact contract text so future presentation changes must be
// deliberate. The only dynamic substitution is the resolved home path, which
// is masked to {HOME} before comparison.
//
// Golden files live in internal/cli/testdata/*.golden. Regenerate intentionally
// (only after a deliberate, reviewed output change) with:
//
//	go test ./internal/cli -run 'TestRootSummaryGolden' -update

func buildRootSummary(t *testing.T, tasks [][2]string) string {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	// Initialize a real canonical home so fleet.Snapshot sees valid state.
	root.SetArgs([]string{"init"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: unexpected error: %v", err)
	}
	buf.Reset()

	for _, task := range tasks {
		id := task[0]
		meta := task[1]
		if err := os.WriteFile(filepath.Join(homeDir, "state", id+".meta"), []byte(meta), 0600); err != nil {
			t.Fatalf("write meta: %v", err)
		}
	}

	root.SetArgs([]string{})
	if err := root.Execute(); err != nil {
		t.Fatalf("root no-args: unexpected error: %v", err)
	}

	// Mask the resolved home path so golden files stay path-independent.
	resolved, err := filepath.Abs(homeDir)
	if err != nil {
		t.Fatalf("abs home: %v", err)
	}
	return strings.ReplaceAll(buf.String(), resolved, "{HOME}")
}

// TestRootSummaryGoldenEmpty locks the no-subcommand output for an empty home.
func TestRootSummaryGoldenEmpty(t *testing.T) {
	got := buildRootSummary(t, nil)
	golden, err := os.ReadFile(filepath.Join("testdata", "root_summary_empty.golden"))
	if err != nil {
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if werr := os.WriteFile(filepath.Join("testdata", "root_summary_empty.golden"), []byte(got), 0644); werr != nil {
				t.Fatalf("write golden: %v", werr)
			}
			t.Skip("golden updated")
		}
		t.Fatalf("read golden: %v", err)
	}
	if got != string(golden) {
		t.Errorf("empty summary mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, string(golden))
	}
}

// TestRootSummaryGoldenPopulated locks the no-subcommand output for a home with
// task meta files (ship + scout), covering project fallback, phase display,
// in-flight counting, and the no-status case.
func TestRootSummaryGoldenPopulated(t *testing.T) {
	tasks := [][2]string{
		{"task-one", "kind=ship\nproject=myproj\n"},
		{"task-two", "kind=scout\nproject=\n"},
	}
	got := buildRootSummary(t, tasks)
	golden, err := os.ReadFile(filepath.Join("testdata", "root_summary_populated.golden"))
	if err != nil {
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if werr := os.WriteFile(filepath.Join("testdata", "root_summary_populated.golden"), []byte(got), 0644); werr != nil {
				t.Fatalf("write golden: %v", werr)
			}
			t.Skip("golden updated")
		}
		t.Fatalf("read golden: %v", err)
	}
	if got != string(golden) {
		t.Errorf("populated summary mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, string(golden))
	}
}
