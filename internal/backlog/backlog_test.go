package backlog

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected [3]int
	}{
		{"0.1.1", [3]int{0, 1, 1}},
		{"v1.0.0", [3]int{1, 0, 0}},
		{"tasks-axi version 0.1.5", [3]int{0, 1, 5}},
		{"tasks-axi version 1.2.3-beta", [3]int{1, 2, 3}},
		{"invalid", [3]int{0, 0, 0}},
		{"1.2", [3]int{0, 0, 0}},
		{"some prefix 10.20.30 suffix", [3]int{10, 20, 30}},
	}

	for _, tc := range tests {
		got := parseVersion(tc.input)
		if got != tc.expected {
			t.Errorf("parseVersion(%q) = %v; want %v", tc.input, got, tc.expected)
		}
	}
}

func TestIsCompatibleVersion(t *testing.T) {
	tests := []struct {
		installed string
		minimum   string
		expected  bool
	}{
		{"0.1.1", "0.1.1", true},
		{"0.1.2", "0.1.1", true},
		{"0.2.0", "0.1.1", true},
		{"1.0.0", "0.1.1", true},
		{"0.1.0", "0.1.1", false},
		{"0.0.9", "0.1.1", false},
		{"v0.1.5", "0.1.1", true},
		{"tasks-axi version 0.1.5", "0.1.1", true},
		{"tasks-axi version 0.1.0", "0.1.1", false},
		{"v1.0.0", "0.1.1", true},
	}

	for _, tc := range tests {
		got := isCompatibleVersion(tc.installed, tc.minimum)
		if got != tc.expected {
			t.Errorf("isCompatibleVersion(%q, %q) = %v; want %v", tc.installed, tc.minimum, got, tc.expected)
		}
	}
}

// TestHelperProcess is used to mock actual exec.Cmd execution without external binaries.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}

	if len(args) == 0 {
		return
	}

	cmd := args[0]
	subArgs := args[1:]

	if strings.HasSuffix(cmd, "tasks-axi") {
		if len(subArgs) == 1 && subArgs[0] == "--version" {
			fmt.Println(os.Getenv("MOCK_TASKS_AXI_VERSION"))
			return
		}
		fmt.Printf("EXEC_CMD: %s\n", strings.Join(subArgs, " "))
		return
	}
}

// Helper to capture stdout during tests.
func captureStdout(fn func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

func TestRun_TasksAxiFallback(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	execCommand = func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}

	t.Run("TasksAxiUnavailable", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			return "", fmt.Errorf("file not found")
		}
		homeDir := t.TempDir()
		// Should not error — falls back to manual
		err := Run(homeDir, "list", []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("TasksAxiIncompatible", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.0")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")

		homeDir := t.TempDir()
		err := Run(homeDir, "list", []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("TasksAxiCompatibleAndExecutes", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")

		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		oldStdout := os.Stdout
		os.Stdout = w
		defer func() { os.Stdout = oldStdout }()

		err = Run("", "add", []string{"task-1", "priority:high"})
		w.Close()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var buf bytes.Buffer
		_, err = buf.ReadFrom(r)
		if err != nil {
			t.Fatal(err)
		}
		output := buf.String()

		expectedOutput := "EXEC_CMD: add task-1 priority:high\n"
		if output != expectedOutput {
			t.Errorf("expected output %q, got %q", expectedOutput, output)
		}
	})
}

func TestParseBacklog(t *testing.T) {
	t.Run("nonexistent file", func(t *testing.T) {
		items, err := parseBacklog("/nonexistent/path/backlog.md")
		if err != nil {
			t.Fatal(err)
		}
		if items != nil {
			t.Fatalf("expected nil, got %v", items)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		os.WriteFile(path, []byte("# Backlog\n\n## 2024-01-01\n"), 0644)

		items, err := parseBacklog(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(items))
		}
	})

	t.Run("multiple items", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		content := `# Backlog

## 2024-01-01
- [ ] TASK-1: First task
- [-] TASK-2: In progress
- [!] TASK-3: Blocked task
- [x] TASK-4: Done task
`
		os.WriteFile(path, []byte(content), 0644)

		items, err := parseBacklog(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 4 {
			t.Fatalf("expected 4 items, got %d", len(items))
		}

		expected := []struct {
			id    string
			desc  string
			state string
		}{
			{"TASK-1", "First task", " "},
			{"TASK-2", "In progress", "-"},
			{"TASK-3", "Blocked task", "!"},
			{"TASK-4", "Done task", "x"},
		}
		for i, exp := range expected {
			if items[i].id != exp.id {
				t.Errorf("item[%d].id = %q, want %q", i, items[i].id, exp.id)
			}
			if items[i].desc != exp.desc {
				t.Errorf("item[%d].desc = %q, want %q", i, items[i].desc, exp.desc)
			}
			if items[i].state != exp.state {
				t.Errorf("item[%d].state = %q, want %q", i, items[i].state, exp.state)
			}
		}
	})

	t.Run("indented items", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		content := "# Backlog\n\n## 2024-01-01\n  - [ ] TASK-1: indented\n\t- [x] TASK-2: tabbed\n"
		os.WriteFile(path, []byte(content), 0644)

		items, err := parseBacklog(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
	})
}

func TestRenderBacklog(t *testing.T) {
	t.Run("render and reparse", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")

		items := []backlogItem{
			{id: "TASK-2", desc: "Second", state: "-"},
			{id: "TASK-1", desc: "First", state: " "},
			{id: "TASK-4", desc: "Done", state: "x"},
			{id: "TASK-3", desc: "Blocked", state: "!"},
		}

		if err := renderBacklog(path, items); err != nil {
			t.Fatal(err)
		}

		// Re-read and verify
		reloaded, err := parseBacklog(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(reloaded) != 4 {
			t.Fatalf("expected 4 items, got %d", len(reloaded))
		}
		// Order: queued first, then in-flight, blocked, done
		if reloaded[0].id != "TASK-1" || reloaded[0].state != " " {
			t.Errorf("first item should be queued TASK-1")
		}
		if reloaded[1].id != "TASK-2" || reloaded[1].state != "-" {
			t.Errorf("second item should be in-flight TASK-2")
		}
		if reloaded[2].id != "TASK-3" || reloaded[2].state != "!" {
			t.Errorf("third item should be blocked TASK-3")
		}
		if reloaded[3].id != "TASK-4" || reloaded[3].state != "x" {
			t.Errorf("fourth item should be done TASK-4")
		}
	})
}

func TestAddItem(t *testing.T) {
	t.Run("add to empty backlog", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")

		output := captureStdout(func() {
			if err := addItem(path, []string{"TASK-1", "My first task"}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "added: TASK-1") {
			t.Errorf("expected add confirmation, got %q", output)
		}

		items, err := parseBacklog(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].id != "TASK-1" || items[0].desc != "My first task" || items[0].state != " " {
			t.Errorf("unexpected item: %+v", items[0])
		}
	})

	t.Run("duplicate id", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "first"})
		err := addItem(path, []string{"TASK-1", "second"})
		if err == nil {
			t.Fatal("expected error for duplicate id, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("expected 'already exists', got %v", err)
		}
	})

	t.Run("missing args", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		err := addItem(path, []string{"TASK-1"})
		if err == nil {
			t.Fatal("expected error for missing args, got nil")
		}
	})
}

func TestListItems(t *testing.T) {
	t.Run("empty backlog", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		output := captureStdout(func() {
			if err := listItems(path, []string{}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "backlog is empty") {
			t.Errorf("expected empty message, got %q", output)
		}
	})

	t.Run("all items", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "First"})
		addItem(path, []string{"TASK-2", "Second"})

		output := captureStdout(func() {
			if err := listItems(path, []string{}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "TASK-1") || !strings.Contains(output, "TASK-2") {
			t.Errorf("expected both items, got %q", output)
		}
	})

	t.Run("filter by state", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "First"})
		transitionItem(path, []string{"TASK-1"}, "x")

		output := captureStdout(func() {
			if err := listItems(path, []string{"done"}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "[x]") {
			t.Errorf("expected done item, got %q", output)
		}
	})

	t.Run("invalid state filter", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "First"})
		err := listItems(path, []string{"bogus"})
		if err == nil {
			t.Fatal("expected error for invalid filter, got nil")
		}
	})
}

func TestShowItem(t *testing.T) {
	t.Run("existing item", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "My task"})

		output := captureStdout(func() {
			if err := showItem(path, []string{"TASK-1"}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "TASK-1") || !strings.Contains(output, "My task") {
			t.Errorf("expected item details, got %q", output)
		}
	})

	t.Run("nonexistent item", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		err := showItem(path, []string{"NONEXISTENT"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected 'not found', got %v", err)
		}
	})

	t.Run("missing id arg", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		err := showItem(path, []string{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestTransitionItem(t *testing.T) {
	t.Run("start task", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "My task"})

		output := captureStdout(func() {
			if err := transitionItem(path, []string{"TASK-1"}, "-"); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "[-]") {
			t.Errorf("expected in-flight marker, got %q", output)
		}

		items, _ := parseBacklog(path)
		if items[0].state != "-" {
			t.Errorf("expected state '-', got %q", items[0].state)
		}
	})

	t.Run("done task", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "My task"})

		output := captureStdout(func() {
			if err := transitionItem(path, []string{"TASK-1"}, "x"); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "[x]") {
			t.Errorf("expected done marker, got %q", output)
		}

		items, _ := parseBacklog(path)
		if items[0].state != "x" {
			t.Errorf("expected state 'x', got %q", items[0].state)
		}
	})

	t.Run("block task", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "My task"})

		transitionItem(path, []string{"TASK-1"}, "!")
		items, _ := parseBacklog(path)
		if items[0].state != "!" {
			t.Errorf("expected state '!', got %q", items[0].state)
		}
	})

	t.Run("ready task (unblock)", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		addItem(path, []string{"TASK-1", "My task"})
		transitionItem(path, []string{"TASK-1"}, "!")

		transitionItem(path, []string{"TASK-1"}, " ")
		items, _ := parseBacklog(path)
		if items[0].state != " " {
			t.Errorf("expected state ' ' (queued), got %q", items[0].state)
		}
	})

	t.Run("nonexistent item", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		err := transitionItem(path, []string{"NONEXISTENT"}, "x")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected 'not found', got %v", err)
		}
	})

	t.Run("missing id arg", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		err := transitionItem(path, []string{}, "x")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestManualRun_Verbs(t *testing.T) {
	t.Run("unknown verb", func(t *testing.T) {
		homeDir := t.TempDir()
		err := manualRun(homeDir, "bogus", []string{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "unknown verb") {
			t.Errorf("expected 'unknown verb', got %v", err)
		}
	})

	t.Run("add and list", func(t *testing.T) {
		homeDir := t.TempDir()
		if err := manualRun(homeDir, "add", []string{"TASK-1", "First task"}); err != nil {
			t.Fatal(err)
		}
		if err := manualRun(homeDir, "add", []string{"TASK-2", "Second task"}); err != nil {
			t.Fatal(err)
		}

		output := captureStdout(func() {
			if err := manualRun(homeDir, "list", []string{}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "TASK-1") || !strings.Contains(output, "TASK-2") {
			t.Errorf("expected both tasks, got %q", output)
		}
	})

	t.Run("add, start, done cycle", func(t *testing.T) {
		homeDir := t.TempDir()
		manualRun(homeDir, "add", []string{"TASK-1", "My task"})

		// start
		output := captureStdout(func() {
			manualRun(homeDir, "start", []string{"TASK-1"})
		})
		if !strings.Contains(output, "[-]") {
			t.Errorf("expected in-flight, got %q", output)
		}

		// done
		output = captureStdout(func() {
			manualRun(homeDir, "done", []string{"TASK-1"})
		})
		if !strings.Contains(output, "[x]") {
			t.Errorf("expected done, got %q", output)
		}
	})

	t.Run("block and unblock", func(t *testing.T) {
		homeDir := t.TempDir()
		manualRun(homeDir, "add", []string{"TASK-1", "My task"})

		manualRun(homeDir, "block", []string{"TASK-1"})
		items, _ := parseBacklog(filepath.Join(homeDir, "data", "backlog.md"))
		if items[0].state != "!" {
			t.Errorf("expected blocked, got %q", items[0].state)
		}

		manualRun(homeDir, "ready", []string{"TASK-1"})
		items, _ = parseBacklog(filepath.Join(homeDir, "data", "backlog.md"))
		if items[0].state != " " {
			t.Errorf("expected queued after ready, got %q", items[0].state)
		}
	})
}

func TestEnsureBacklog(t *testing.T) {
	t.Run("creates file with header", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "data", "backlog.md")

		if err := ensureBacklog(path); err != nil {
			t.Fatal(err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "# Backlog") {
			t.Errorf("expected header, got %q", string(data))
		}
	})

	t.Run("existing file unchanged", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		os.WriteFile(path, []byte("existing content\n"), 0644)

		if err := ensureBacklog(path); err != nil {
			t.Fatal(err)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "existing content\n" {
			t.Errorf("expected unchanged content, got %q", string(data))
		}
	})
}

func TestStateDisplay(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{" ", "[ ]"},
		{"-", "[-]"},
		{"!", "[!]"},
		{"x", "[x]"},
		{"unknown", "[ ]"}, // default
	}

	for _, tc := range tests {
		got := stateDisplay(tc.input)
		if got != tc.expected {
			t.Errorf("stateDisplay(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
