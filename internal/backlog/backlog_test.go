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
		err := Run(homeDir, true, "list", []string{})
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
		err := Run(homeDir, true, "list", []string{})
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

		homeDir := t.TempDir()
		err = Run(homeDir, true, "add", []string{"task-1", "priority:high"})
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

		expectedOutput := fmt.Sprintf("EXEC_CMD: add task-1 priority:high --file %s\n", filepath.Join(homeDir, "data", "backlog.md"))
		if output != expectedOutput {
			t.Errorf("expected output %q, got %q", expectedOutput, output)
		}
	})
	t.Run("TasksAxiBlockWithBy", func(t *testing.T) {
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

		homeDir := t.TempDir()
		err = Run(homeDir, true, "block", []string{"task-a", "--by", "task-b"})
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

		expectedOutput := fmt.Sprintf("EXEC_CMD: block task-a --by task-b --file %s\n", filepath.Join(homeDir, "data", "backlog.md"))
		if output != expectedOutput {
			t.Errorf("expected output %q, got %q", expectedOutput, output)
		}
	})
}

func TestRun_ConfigBackendGate(t *testing.T) {
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

	t.Run("manual config forces manual with tasks-axi available", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644)

		// Should use manual path, not tasks-axi
		err := Run(homeDir, true, "add", []string{"TASK-1", "Forced manual"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify backlog.md was created in homeDir/data/ (manual path)
		backlogPath := filepath.Join(homeDir, "data", "backlog.md")
		if _, err := os.Stat(backlogPath); os.IsNotExist(err) {
			t.Errorf("expected backlog.md at %s, but it does not exist", backlogPath)
		}
	})

	t.Run("absent config uses tasks-axi when available", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")

		homeDir := t.TempDir()
		// No config/backlog-backend file

		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		oldStdout := os.Stdout
		os.Stdout = w
		defer func() { os.Stdout = oldStdout }()

		err = Run(homeDir, true, "list", []string{})
		w.Close()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()
		expectedFile := filepath.Join(homeDir, "data", "backlog.md")
		if !strings.Contains(output, "EXEC_CMD: list --file "+expectedFile) {
			t.Errorf("expected tasks-axi execution scoped to %s, got %q", expectedFile, output)
		}
	})

	t.Run("no tasks-axi uses manual", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			return "", fmt.Errorf("file not found")
		}
		homeDir := t.TempDir()
		// No config/backlog-backend and no tasks-axi

		err := Run(homeDir, true, "add", []string{"TASK-1", "Manual fallback"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		backlogPath := filepath.Join(homeDir, "data", "backlog.md")
		if _, err := os.Stat(backlogPath); os.IsNotExist(err) {
			t.Errorf("expected backlog.md at %s, but it does not exist", backlogPath)
		}
	})
}

func TestTasksAxiBackendScopesOperationsToHome(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	lookPath = func(name string) (string, error) {
		if name == "tasks-axi" {
			return "/mock/tasks-axi", nil
		}
		return "", fmt.Errorf("file not found")
	}
	execCommand = func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}

	homeDir := t.TempDir()
	backend := &TasksAxiBackend{HomeDir: homeDir}
	output := captureStdout(func() {
		if err := backend.Add("task-1", "scoped", "ship", "munsu", false); err != nil {
			t.Fatal(err)
		}
	})
	expectedFile := filepath.Join(homeDir, "data", "backlog.md")
	if !strings.Contains(output, "--file "+expectedFile) {
		t.Errorf("expected home-scoped tasks-axi call, got %q", output)
	}
}

func TestFileBackend_Parse(t *testing.T) {
	t.Run("nonexistent file", func(t *testing.T) {
		fb := NewFileBackend("/nonexistent/path/backlog.md")
		items, err := fb.parse()
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
		fb := NewFileBackend(path)

		items, err := fb.parse()
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
		fb := NewFileBackend(path)

		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 4 {
			t.Fatalf("expected 4 items, got %d", len(items))
		}

		expected := []struct {
			id    string
			desc  string
			state TaskState
		}{
			{"TASK-1", "First task", StateQueued},
			{"TASK-2", "In progress", StateInFlight},
			{"TASK-3", "Blocked task", StateBlocked},
			{"TASK-4", "Done task", StateDone},
		}
		for i, exp := range expected {
			if items[i].ID != exp.id {
				t.Errorf("item[%d].id = %q, want %q", i, items[i].ID, exp.id)
			}
			if items[i].Description != exp.desc {
				t.Errorf("item[%d].desc = %q, want %q", i, items[i].Description, exp.desc)
			}
			if items[i].State != exp.state {
				t.Errorf("item[%d].state = %v, want %v", i, items[i].State, exp.state)
			}
		}
	})

	t.Run("indented items", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		content := "# Backlog\n\n## 2024-01-01\n  - [ ] TASK-1: indented\n\t- [x] TASK-2: tabbed\n"
		os.WriteFile(path, []byte(content), 0644)
		fb := NewFileBackend(path)

		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
	})
}

func TestFileBackend_Render(t *testing.T) {
	t.Run("render and reparse", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		items := []Item{
			{ID: "TASK-2", Description: "Captain", State: StateInFlight},
			{ID: "TASK-1", Description: "First", State: StateQueued},
			{ID: "TASK-4", Description: "Done", State: StateDone},
			{ID: "TASK-3", Description: "Blocked", State: StateBlocked},
		}

		if err := fb.render(items); err != nil {
			t.Fatal(err)
		}

		// Re-read and verify
		fb2 := NewFileBackend(path)
		reloaded, err := fb2.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(reloaded) != 4 {
			t.Fatalf("expected 4 items, got %d", len(reloaded))
		}
		// Order: queued first, then in-flight, blocked, done
		if reloaded[0].ID != "TASK-1" || reloaded[0].State != StateQueued {
			t.Errorf("first item should be queued TASK-1")
		}
		if reloaded[1].ID != "TASK-2" || reloaded[1].State != StateInFlight {
			t.Errorf("captain item should be in-flight TASK-2")
		}
		if reloaded[2].ID != "TASK-3" || reloaded[2].State != StateBlocked {
			t.Errorf("third item should be blocked TASK-3")
		}
		if reloaded[3].ID != "TASK-4" || reloaded[3].State != StateDone {
			t.Errorf("fourth item should be done TASK-4")
		}
	})
}

func TestFileBackend_Add(t *testing.T) {
	t.Run("add to empty backlog", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		output := captureStdout(func() {
			if err := fb.Add("TASK-1", "My first task", "", "", false); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(output, "added: TASK-1") {
			t.Errorf("expected add confirmation, got %q", output)
		}

		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].ID != "TASK-1" || items[0].Description != "My first task" || items[0].State != StateQueued {
			t.Errorf("unexpected item: %+v", items[0])
		}
	})

	t.Run("duplicate id", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		fb.Add("TASK-1", "first", "", "", false)
		err := fb.Add("TASK-1", "general", "", "", false)
		if err == nil {
			t.Fatal("expected error for duplicate id, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("expected 'already exists', got %v", err)
		}
	})
}

func TestFileBackend_List(t *testing.T) {
	t.Run("empty backlog", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		items, err := fb.List(StateQueued)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(items))
		}
	})
}

func TestFileBackend_Show(t *testing.T) {
	t.Run("existing item", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		fb.Add("TASK-1", "My task", "", "", false)

		item, ok := fb.Show("TASK-1")
		if !ok {
			t.Fatal("expected item to be found")
		}
		if item.ID != "TASK-1" || item.Description != "My task" {
			t.Errorf("unexpected item: %+v", item)
		}
	})

	t.Run("nonexistent item", func(t *testing.T) {
		tmp := t.TempDir()
		fb := NewFileBackend(filepath.Join(tmp, "backlog.md"))

		_, ok := fb.Show("NONEXISTENT")
		if ok {
			t.Fatal("expected false for nonexistent item")
		}
	})
}

func TestFileBackend_UpdateState(t *testing.T) {
	t.Run("start task", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		fb.Add("TASK-1", "My task", "", "", false)
		if err := fb.UpdateState("TASK-1", StateInFlight); err != nil {
			t.Fatal(err)
		}

		items, _ := fb.parse()
		if items[0].State != StateInFlight {
			t.Errorf("expected state InFlight, got %v", items[0].State)
		}
	})

	t.Run("done task", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		fb.Add("TASK-1", "My task", "", "", false)
		if err := fb.UpdateState("TASK-1", StateDone); err != nil {
			t.Fatal(err)
		}

		items, _ := fb.parse()
		if items[0].State != StateDone {
			t.Errorf("expected state Done, got %v", items[0].State)
		}
	})

	t.Run("block task", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		fb.Add("TASK-1", "My task", "", "", false)
		if err := fb.UpdateState("TASK-1", StateBlocked); err != nil {
			t.Fatal(err)
		}

		items, _ := fb.parse()
		if items[0].State != StateBlocked {
			t.Errorf("expected state Blocked, got %v", items[0].State)
		}
	})

	t.Run("ready task (unblock)", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		fb.Add("TASK-1", "My task", "", "", false)
		fb.UpdateState("TASK-1", StateBlocked)
		fb.UpdateState("TASK-1", StateQueued)

		items, _ := fb.parse()
		if items[0].State != StateQueued {
			t.Errorf("expected state Queued after ready, got %v", items[0].State)
		}
	})

	t.Run("nonexistent item", func(t *testing.T) {
		tmp := t.TempDir()
		fb := NewFileBackend(filepath.Join(tmp, "backlog.md"))

		err := fb.UpdateState("NONEXISTENT", StateDone)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected 'not found', got %v", err)
		}
	})

	t.Run("transition from done is rejected", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		fb.Add("TASK-1", "My task", "", "", false)
		fb.UpdateState("TASK-1", StateDone)

		err := fb.UpdateState("TASK-1", StateQueued)
		if err == nil {
			t.Fatal("expected error for terminal state transition, got nil")
		}
		if !strings.Contains(err.Error(), "cannot transition") {
			t.Errorf("expected 'cannot transition', got %v", err)
		}
	})
}

func TestFileBackend_Ensure(t *testing.T) {
	t.Run("creates file with header", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "data", "backlog.md")
		fb := NewFileBackend(path)

		if err := fb.ensureBacklog(); err != nil {
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
		fb := NewFileBackend(path)

		if err := fb.ensureBacklog(); err != nil {
			t.Fatal(err)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "existing content\n" {
			t.Errorf("expected unchanged content, got %q", string(data))
		}
	})
}

func TestTaskStateDisplay(t *testing.T) {
	tests := []struct {
		state    TaskState
		expected string
	}{
		{StateQueued, "[ ]"},
		{StateInFlight, "[-]"},
		{StateBlocked, "[!]"},
		{StateDone, "[x]"},
	}
	// Also test that an invalid TaskState doesn't panic
	invalid := TaskState(99)
	if invalid.Display() != "[ ]" {
		t.Errorf("expected default display for invalid state, got %q", invalid.Display())
	}

	for _, tc := range tests {
		got := tc.state.Display()
		if got != tc.expected {
			t.Errorf("state.Display() for %v = %q, want %q", tc.state, got, tc.expected)
		}
	}
}

func TestTaskStateTransitions(t *testing.T) {
	t.Run("valid transitions", func(t *testing.T) {
		if !StateQueued.CanTransitionTo(StateInFlight) {
			t.Error("queued -> in-flight should be valid")
		}
		if !StateQueued.CanTransitionTo(StateBlocked) {
			t.Error("queued -> blocked should be valid")
		}
		if !StateQueued.CanTransitionTo(StateDone) {
			t.Error("queued -> done should be valid")
		}
		if !StateInFlight.CanTransitionTo(StateQueued) {
			t.Error("in-flight -> queued should be valid (ready/unblock)")
		}
		if !StateInFlight.CanTransitionTo(StateDone) {
			t.Error("in-flight -> done should be valid")
		}
		if !StateInFlight.CanTransitionTo(StateBlocked) {
			t.Error("in-flight -> blocked should be valid")
		}
		if !StateBlocked.CanTransitionTo(StateQueued) {
			t.Error("blocked -> queued should be valid (ready)")
		}
		if !StateBlocked.CanTransitionTo(StateDone) {
			t.Error("blocked -> done should be valid")
		}
	})

	t.Run("terminal state rejects transitions", func(t *testing.T) {
		if StateDone.CanTransitionTo(StateQueued) {
			t.Error("done -> queued should be invalid (terminal)")
		}
		if StateDone.CanTransitionTo(StateInFlight) {
			t.Error("done -> in-flight should be invalid (terminal)")
		}
		if StateDone.CanTransitionTo(StateBlocked) {
			t.Error("done -> blocked should be invalid (terminal)")
		}
	})

	t.Run("identity transitions", func(t *testing.T) {
		if !StateQueued.CanTransitionTo(StateQueued) {
			t.Error("queued -> queued should be valid (identity)")
		}
		if !StateDone.CanTransitionTo(StateDone) {
			t.Error("done -> done should be valid (identity)")
		}
	})
}

func TestParseState(t *testing.T) {
	t.Run("from markers", func(t *testing.T) {
		tests := []struct {
			marker string
			state  TaskState
		}{
			{" ", StateQueued},
			{"-", StateInFlight},
			{"!", StateBlocked},
			{"x", StateDone},
		}
		for _, tc := range tests {
			s, err := ParseState(tc.marker)
			if err != nil {
				t.Errorf("ParseState(%q): %v", tc.marker, err)
			}
			if s != tc.state {
				t.Errorf("ParseState(%q) = %v, want %v", tc.marker, s, tc.state)
			}
		}
	})

	t.Run("from names", func(t *testing.T) {
		tests := []struct {
			name  string
			state TaskState
		}{
			{"queued", StateQueued},
			{"in-flight", StateInFlight},
			{"blocked", StateBlocked},
			{"done", StateDone},
		}
		for _, tc := range tests {
			s, err := ParseState(tc.name)
			if err != nil {
				t.Errorf("ParseState(%q): %v", tc.name, err)
			}
			if s != tc.state {
				t.Errorf("ParseState(%q) = %v, want %v", tc.name, s, tc.state)
			}
		}
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := ParseState("bogus")
		if err == nil {
			t.Error("expected error for invalid state")
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
		if err := manualRun(homeDir, "add", []string{"TASK-2", "Captain task"}); err != nil {
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
		fb := NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
		items, _ := fb.parse()
		if items[0].State != StateBlocked {
			t.Errorf("expected blocked, got %v", items[0].State)
		}

		manualRun(homeDir, "ready", []string{"TASK-1"})
		items, _ = fb.parse()
		if items[0].State != StateQueued {
			t.Errorf("expected queued after ready, got %v", items[0].State)
		}
	})
}

func TestFileBackend_AddWithMetadata(t *testing.T) {
	t.Run("add with kind and repo", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		if err := fb.Add("TASK-1", "My task", "scout", "munsu", false); err != nil {
			t.Fatal(err)
		}

		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].Kind != "scout" {
			t.Errorf("expected kind 'scout', got %q", items[0].Kind)
		}
		if items[0].Repo != "munsu" {
			t.Errorf("expected repo 'munsu', got %q", items[0].Repo)
		}
		if items[0].State != StateQueued {
			t.Errorf("expected state Queued, got %v", items[0].State)
		}
	})

	t.Run("add with start flag sets in-flight", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		if err := fb.Add("TASK-1", "My task", "", "", true); err != nil {
			t.Fatal(err)
		}

		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if items[0].State != StateInFlight {
			t.Errorf("expected state InFlight, got %v", items[0].State)
		}
	})

	t.Run("add with all flags", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		if err := fb.Add("TASK-1", "My task", "scout", "munsu", true); err != nil {
			t.Fatal(err)
		}

		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if items[0].Kind != "scout" {
			t.Errorf("expected kind 'scout', got %q", items[0].Kind)
		}
		if items[0].Repo != "munsu" {
			t.Errorf("expected repo 'munsu', got %q", items[0].Repo)
		}
		if items[0].State != StateInFlight {
			t.Errorf("expected state InFlight, got %v", items[0].State)
		}
	})
}

func TestFileBackend_RenderWithMetadata(t *testing.T) {
	t.Run("metadata round-trip", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		items := []Item{
			{ID: "TASK-1", Description: "My task", State: StateQueued, Kind: "scout", Repo: "munsu"},
		}

		if err := fb.render(items); err != nil {
			t.Fatal(err)
		}

		// Verify the metadata suffix is in the file
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		if !strings.Contains(content, "[kind=scout repo=munsu]") {
			t.Errorf("expected metadata suffix, got %q", content)
		}

		// Reparse and verify
		fb2 := NewFileBackend(path)
		reloaded, err := fb2.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(reloaded) != 1 {
			t.Fatalf("expected 1 item, got %d", len(reloaded))
		}
		if reloaded[0].Kind != "scout" {
			t.Errorf("expected kind 'scout', got %q", reloaded[0].Kind)
		}
		if reloaded[0].Repo != "munsu" {
			t.Errorf("expected repo 'munsu', got %q", reloaded[0].Repo)
		}
	})

	t.Run("metadata round-trip both fields", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "backlog.md")
		fb := NewFileBackend(path)

		items := []Item{
			{ID: "TASK-1", Description: "My task", State: StateInFlight, Kind: "ship", Repo: "munsu"},
			{ID: "TASK-2", Description: "Other task", State: StateQueued, Kind: "scout", Repo: "munsu"},
			{ID: "TASK-3", Description: "No meta", State: StateDone},
		}

		if err := fb.render(items); err != nil {
			t.Fatal(err)
		}

		fb2 := NewFileBackend(path)
		reloaded, err := fb2.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(reloaded) != 3 {
			t.Fatalf("expected 3 items, got %d", len(reloaded))
		}

		// Expected order: TASK-2 (queued), TASK-1 (in-flight), TASK-3 (done)
		if reloaded[0].Kind != "scout" || reloaded[0].Repo != "munsu" {
			t.Errorf("TASK-2: expected kind=scout repo=munsu, got kind=%q repo=%q", reloaded[0].Kind, reloaded[0].Repo)
		}
		if reloaded[1].Kind != "ship" || reloaded[1].Repo != "munsu" {
			t.Errorf("TASK-1: expected kind=ship repo=munsu, got kind=%q repo=%q", reloaded[1].Kind, reloaded[1].Repo)
		}
		if reloaded[2].Kind != "" || reloaded[2].Repo != "" {
			t.Errorf("TASK-3: expected no metadata, got kind=%q repo=%q", reloaded[2].Kind, reloaded[2].Repo)
		}
	})
}

func TestAddItemPublic(t *testing.T) {
	t.Run("AddItem with kind and repo", func(t *testing.T) {
		homeDir := t.TempDir()

		if err := AddItem(homeDir, "TASK-1", "My task", "scout", "munsu", false); err != nil {
			t.Fatal(err)
		}

		fb := NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].Kind != "scout" {
			t.Errorf("expected kind 'scout', got %q", items[0].Kind)
		}
		if items[0].Repo != "munsu" {
			t.Errorf("expected repo 'munsu', got %q", items[0].Repo)
		}
	})

	t.Run("AddItem with start flag", func(t *testing.T) {
		homeDir := t.TempDir()

		if err := AddItem(homeDir, "TASK-1", "My task", "", "", true); err != nil {
			t.Fatal(err)
		}

		fb := NewFileBackend(filepath.Join(homeDir, "data", "backlog.md"))
		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if items[0].State != StateInFlight {
			t.Errorf("expected state InFlight, got %v", items[0].State)
		}
	})
}

func TestFormatItem(t *testing.T) {
	t.Run("basic item", func(t *testing.T) {
		item := Item{ID: "TASK-1", Description: "First task", State: StateQueued}
		got := formatItem(item)
		expected := "- [ ] TASK-1: First task"
		if got != expected {
			t.Errorf("formatItem = %q, want %q", got, expected)
		}
	})

	t.Run("with metadata", func(t *testing.T) {
		item := Item{ID: "TASK-1", Description: "My task", State: StateInFlight, Kind: "scout", Repo: "munsu"}
		got := formatItem(item)
		expected := "- [-] TASK-1: My task [kind=scout repo=munsu]"
		if got != expected {
			t.Errorf("formatItem = %q, want %q", got, expected)
		}
	})

	t.Run("blocked item", func(t *testing.T) {
		item := Item{ID: "TASK-2", Description: "Blocked", State: StateBlocked}
		got := formatItem(item)
		expected := "- [!] TASK-2: Blocked"
		if got != expected {
			t.Errorf("formatItem = %q, want %q", got, expected)
		}
	})

	t.Run("done item", func(t *testing.T) {
		item := Item{ID: "TASK-3", Description: "Finished", State: StateDone}
		got := formatItem(item)
		expected := "- [x] TASK-3: Finished"
		if got != expected {
			t.Errorf("formatItem = %q, want %q", got, expected)
		}
	})
}
