package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/minhtri2710/munsu/internal/fleet"
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
		// Extract kind from the legacy meta text and seed a canonical record
		// (clean break: the root summary reads authoritative Task Authority).
		kind := "ship"
		if strings.Contains(meta, "kind=scout") {
			kind = "scout"
		}
		cliSeedCanonicalTask(t, homeDir, id, kind)
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

func TestStyledOutputStripsToPlain(t *testing.T) {
	view := rootSummaryView{homeDir: "/h", totalTasks: 3, inFlight: 2, watcher: "stale", tasks: []rootSummaryTask{
		{id: "t1", phase: "working", project: "p", status: "working"},
		{id: "t2", phase: "blocked", project: "p", status: "waiting on t1"},
		{id: "t3", phase: "mystery", project: "-", status: "mystery"},
	}}
	snap := &fleet.FleetSnapshot{Time: "now", Tasks: []fleet.TaskSnapshot{
		{ID: "t1", Project: "p", Kind: "ship", Mode: "pr", Yolo: "false", Window: "@1", CurrentState: "done"},
		{ID: "t2", Project: "p", Kind: "ship", Mode: "pr", Yolo: "false", Window: "@2"},
	}}
	render := func(mode markMode) string {
		old := outputMarks
		defer func() { outputMarks = old }()
		outputMarks = func(io.Writer) markMode { return mode }
		var b bytes.Buffer
		renderRootSummary(&b, view)
		renderFleetView(&b, snap)
		b.WriteString(mode.markLead("MISSING: tmux (install instructions vary)") + "\n")
		b.WriteString(mode.markLead("SOLDIER_HARNESS: pi") + "\n")
		b.WriteString(mode.markAfter("  hooks: absent (no file)", ": ", "absent") + "\n")
		return b.String()
	}
	color, icons, plain := render(markColor), render(markIcons), render(markPlain)
	if !strings.Contains(color, "\x1b[") {
		t.Fatalf("styled output carries no ANSI:\n%s", color)
	}
	if got := ansi.Strip(color); got != icons {
		t.Errorf("stripped styled output differs from NO_COLOR output:\n--- stripped ---\n%s\n--- no color ---\n%s", got, icons)
	}
	want := `munsu @ /h

fleet: 3 tasks (2 in-flight) | watcher: ! stale | holds: --

  t1                   [● working   ] p
  t2                   [! blocked   ] p
                        waiting on t1
  t3                   [? mystery   ] -

Next: munsu fleet view | munsu peek <id> | munsu --help
# Fleet View — now

Tasks: 2

- **t1** (repo: p) [primary]
  kind: ship | mode: pr | yolo: false
  pane: @1 (✓ done)

- **t2** (repo: p) [primary]
  kind: ship | mode: pr | yolo: false
  pane: @2 (✗ dead)

✗ MISSING: tmux (install instructions vary)
SOLDIER_HARNESS: pi
  hooks: ✗ absent (no file)
`
	if icons != want {
		t.Errorf("NO_COLOR output:\n%s\nwant:\n%s", icons, want)
	}
	if got := strings.NewReplacer("● ", "", "! ", "", "? ", "", "✓ ", "", "✗ ", "").Replace(icons); got != plain {
		t.Errorf("plain output is not the icon-free text:\n%s\nwant:\n%s", plain, got)
	}
}

func TestFleetViewShowsCanonicalQueuedPhase(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("MUNSU_HOME", homeDir)
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"init"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	cliSeedCanonicalTask(t, homeDir, "pre-spawn-task", "ship")
	buf.Reset()
	root.SetArgs([]string{"fleet", "view"})
	if err := root.Execute(); err != nil {
		t.Fatalf("fleet view: %v", err)
	}
	if !strings.Contains(buf.String(), "pane:  (queued)") {
		t.Errorf("fleet view lacks the canonical queued phase:\n%s", buf.String())
	}
}
