package fleet

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

// mockExecCommand returns a function that creates exec.Cmd instances backed by
// the test helper process. When MOCK_TASKS_AXI_FAIL is set, tasks-axi commands
// exit with code 1 instead of printing.
func mockExecCommand() func(string, ...string) *exec.Cmd {
	return func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
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
		// Version check must succeed regardless of fail mode — tasksAxiAvailable
		// depends on it to distinguish ABSENT/UNSUPPORTED from FAILED.
		if len(subArgs) == 1 && subArgs[0] == "--version" {
			fmt.Println(os.Getenv("MOCK_TASKS_AXI_VERSION"))
			return
		}
		// Fail mode: return non-zero exit (fail-closed test).
		if os.Getenv("MOCK_TASKS_AXI_FAIL") == "1" {
			fmt.Fprintf(os.Stderr, "tasks-axi failed\n")
			os.Exit(1)
		}
		// Structured mock output for show/list commands.
		if mockShow := os.Getenv("MOCK_TASKS_AXI_SHOW"); mockShow != "" {
			fmt.Print(mockShow)
			return
		}
		if mockList := os.Getenv("MOCK_TASKS_AXI_LIST"); mockList != "" {
			fmt.Print(mockList)
			return
		}
		fmt.Printf("EXEC_CMD: %s\n", strings.Join(subArgs, " "))
		return
	}
}

// Helper to capture stdout during tests.
func backlogCaptureStdout(fn func()) string {
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

		expectedOutput := fmt.Sprintf("EXEC_CMD: add task-1 priority:high --file %s\n", filepath.Join(homeDir, "data", "md"))
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

		expectedOutput := fmt.Sprintf("EXEC_CMD: block task-a --by task-b --file %s\n", filepath.Join(homeDir, "data", "md"))
		if output != expectedOutput {
			t.Errorf("expected output %q, got %q", expectedOutput, output)
		}
	})
}

func TestResolveBackend(t *testing.T) {
	t.Run("non-default home forces manual", func(t *testing.T) {
		homeDir := t.TempDir()
		mode := resolveBackend(homeDir, false)
		if mode != ModeManual {
			t.Errorf("expected ModeManual, got %v", mode)
		}
	})

	t.Run("manual config returns manual", func(t *testing.T) {
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644)

		mode := resolveBackend(homeDir, true)
		if mode != ModeManual {
			t.Errorf("expected ModeManual, got %v", mode)
		}
	})

	t.Run("tasks-axi config returns tasks-axi", func(t *testing.T) {
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		mode := resolveBackend(homeDir, true)
		if mode != ModeTasksAxi {
			t.Errorf("expected ModeTasksAxi, got %v", mode)
		}
	})

	t.Run("absent config returns auto", func(t *testing.T) {
		homeDir := t.TempDir()
		mode := resolveBackend(homeDir, true)
		if mode != ModeAuto {
			t.Errorf("expected ModeAuto, got %v", mode)
		}
	})

	t.Run("unknown config value returns auto", func(t *testing.T) {
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("something-else\n"), 0644)

		mode := resolveBackend(homeDir, true)
		if mode != ModeAuto {
			t.Errorf("expected ModeAuto, got %v", mode)
		}
	})
}

func TestRun_BackendSelection(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	execCommand = mockExecCommand()

	t.Run("ModeManual explicitly configured", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			return "/mock/tasks-axi", nil
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644)

		// Even though tasks-axi is available, manual config forces manual.
		err := Run(homeDir, true, "add", []string{"TASK-1", "Manual mode"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify md was created by FileBackend (manual path).
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); os.IsNotExist(err) {
			t.Errorf("expected md at %s (manual mode should use FileBackend), but it does not exist", backlogPath)
		}
	})

	t.Run("ModeTasksAxi configured and available", func(t *testing.T) {
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
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		output := backlogCaptureStdout(func() {
			err := Run(homeDir, true, "list", []string{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})

		if !strings.Contains(output, "EXEC_CMD: list") {
			t.Errorf("expected tasks-axi execution, got %q", output)
		}

		// No md should exist — FileBackend was never invoked.
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); err == nil {
			t.Errorf("md should NOT exist under tasks-axi backend (no cross-store write), but it does")
		}
	})

	t.Run("ModeTasksAxi configured but CLI absent fails closed", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			return "", fmt.Errorf("not found")
		}

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		err := Run(homeDir, true, "list", []string{})
		if err == nil {
			t.Fatal("expected error for absent tasks-axi in ModeTasksAxi, got nil")
		}
		if !strings.Contains(err.Error(), "tasks-axi") {
			t.Errorf("expected error mentioning tasks-axi, got %v", err)
		}
	})
}

func TestRun_FailClosed(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	execCommand = mockExecCommand()
	lookPath = func(name string) (string, error) {
		if name == "tasks-axi" {
			return "/mock/tasks-axi", nil
		}
		return "", fmt.Errorf("file not found")
	}
	os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
	defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
	os.Setenv("MOCK_TASKS_AXI_FAIL", "1")
	defer os.Unsetenv("MOCK_TASKS_AXI_FAIL")

	t.Run("ModeTasksAxi - runtime failure propagates", func(t *testing.T) {
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		err := Run(homeDir, true, "list", []string{})
		if err == nil {
			t.Fatal("expected error from tasks-axi FAILED in ModeTasksAxi, got nil")
		}

		// Verify no md was created — no silent fallback to FileBackend.
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); err == nil {
			t.Errorf("md should NOT exist after tasks-axi FAILED (fail-closed, no fallback), but it does")
		}
	})

	t.Run("ModeAuto - runtime failure still propagates", func(t *testing.T) {
		homeDir := t.TempDir()
		// No config file → ModeAuto

		err := Run(homeDir, true, "list", []string{})
		if err == nil {
			t.Fatal("expected error from tasks-axi FAILED in ModeAuto, got nil")
		}

		// Error should mention tasks-axi, not a manual error.
		// Verify no md was created — no silent fallback to FileBackend.
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); err == nil {
			t.Errorf("md should NOT exist after tasks-axi FAILED (fail-closed, no fallback), but it does")
		}
	})
}

func TestRun_FallbackOnAbsent(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	execCommand = mockExecCommand()

	t.Run("ModeAuto absent tasks-axi falls back to manual", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			return "", fmt.Errorf("not found")
		}

		homeDir := t.TempDir()
		// No config → ModeAuto

		err := Run(homeDir, true, "add", []string{"TASK-1", "Fallback test"})
		if err != nil {
			t.Fatalf("unexpected error from fallback: %v", err)
		}

		// FileBackend should have created md.
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); os.IsNotExist(err) {
			t.Errorf("expected md from manual fallback, but it does not exist")
		}
	})

	t.Run("ModeAuto incompatible version falls back to manual", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.0")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")

		homeDir := t.TempDir()
		// No config → ModeAuto

		err := Run(homeDir, true, "add", []string{"TASK-1", "Compat fallback"})
		if err != nil {
			t.Fatalf("unexpected error from compat fallback: %v", err)
		}

		// FileBackend should have created md.
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); os.IsNotExist(err) {
			t.Errorf("expected md from compat fallback, but it does not exist")
		}
	})
}

func TestAddItemDispatch_NoCrossStoreMutation(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	execCommand = mockExecCommand()
	lookPath = func(name string) (string, error) {
		if name == "tasks-axi" {
			return "/mock/tasks-axi", nil
		}
		return "", fmt.Errorf("file not found")
	}
	os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
	defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")

	t.Run("tasks-axi active does not invoke FileBackend", func(t *testing.T) {
		homeDir := t.TempDir()
		// No config → ModeAuto with tasks-axi available → tasks-axi active

		err := AddItemDispatch(homeDir, "TASK-1", "No cross-store write", "scout", "munsu", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// md should NOT exist — tasks-axi was used, not FileBackend.
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); err == nil {
			t.Errorf("md should NOT exist under tasks-axi backend (no cross-store mutation), but it does")
		}
	})

	t.Run("tasks-axi FAILED does not touch FileBackend", func(t *testing.T) {
		os.Setenv("MOCK_TASKS_AXI_FAIL", "1")
		defer os.Unsetenv("MOCK_TASKS_AXI_FAIL")

		homeDir := t.TempDir()

		err := AddItemDispatch(homeDir, "TASK-1", "Fail closed", "", "", false)
		if err == nil {
			t.Fatal("expected error from tasks-axi FAILED, got nil")
		}

		// md should NOT exist — FileBackend was never called.
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); err == nil {
			t.Errorf("md should NOT exist after tasks-axi FAILED (fail-closed), but it does")
		}
	})
}

func TestGetItem_RouteThroughBackend(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	execCommand = mockExecCommand()

	// tasks-axi show mock output for a single item.
	const mockShowOutput = `task:
  id: TASK-1
  title: From tasks-axi
  state: in_flight
  kind: scout
  repo: munsu
`

	t.Run("ModeManual reads from FileBackend", func(t *testing.T) {
		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644)

		// Pre-populate md (FileBackend should read this).
		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [-] TASK-1: From native\n"), 0644)

		lookPath = func(name string) (string, error) {
			return "/mock/tasks-axi", nil
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
		os.Setenv("MOCK_TASKS_AXI_SHOW", mockShowOutput)
		defer os.Unsetenv("MOCK_TASKS_AXI_SHOW")

		item, found, err := GetItem(homeDir, "TASK-1")
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatal("expected item to be found")
		}
		// Must read from FileBackend (native), not tasks-axi mock.
		if item.Description != "From native" {
			t.Errorf("expected 'From native' (FileBackend), got %q", item.Description)
		}
	})

	t.Run("ModeTasksAxi reads from tasks-axi, not FileBackend", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
		os.Setenv("MOCK_TASKS_AXI_SHOW", mockShowOutput)
		defer os.Unsetenv("MOCK_TASKS_AXI_SHOW")

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		// Pre-populate md with DIVERGENT data (tasks-axi should NOT read this).
		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [x] TASK-1: Divergent native data\n"), 0644)

		item, found, err := GetItem(homeDir, "TASK-1")
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatal("expected item to be found")
		}
		// Must read from tasks-axi mock, not FileBackend.
		if item.Description != "From tasks-axi" {
			t.Errorf("expected 'From tasks-axi' (tasks-axi backend), got %q", item.Description)
		}
		if item.State != StateInFlight {
			t.Errorf("expected state InFlight from tasks-axi, got %v", item.State)
		}

		// Verify md was NOT consulted (should still have divergent data).
		data, _ := os.ReadFile(filepath.Join(dataDir, "md"))
		if !strings.Contains(string(data), "Divergent native data") {
			t.Errorf("md should remain unchanged (not consulted by GetItem)")
		}
	})
}

func TestHasDuplicate_RouteThroughBackend(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	execCommand = mockExecCommand()

	// tasks-axi list mock output with two items (one duplicate).
	const mockListWithDup = `count: 2
	tasks[2]{id,state,kind,repo,title}:
	  TASK-1,in_flight,scout,munsu,first
	  TASK-1,queued,scout,munsu,duplicate
`

	const mockListNoDup = `count: 1
	tasks[1]{id,state,kind,repo,title}:
	  TASK-1,in_flight,scout,munsu,only one
`

	t.Run("ModeTasksAxi detects duplicates via tasks-axi", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
		os.Setenv("MOCK_TASKS_AXI_LIST", mockListWithDup)
		defer os.Unsetenv("MOCK_TASKS_AXI_LIST")

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		// Pre-populate md with NO duplicates (tasks-axi should NOT read this).
		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [-] TASK-1: Only one in native\n"), 0644)

		dup, err := HasDuplicate(homeDir, "TASK-1")
		if err != nil {
			t.Fatal(err)
		}
		if !dup {
			t.Error("expected duplicate detected via tasks-axi")
		}
	})

	t.Run("ModeTasksAxi no duplicates via tasks-axi", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
		os.Setenv("MOCK_TASKS_AXI_LIST", mockListNoDup)
		defer os.Unsetenv("MOCK_TASKS_AXI_LIST")

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		dup, err := HasDuplicate(homeDir, "TASK-1")
		if err != nil {
			t.Fatal(err)
		}
		if dup {
			t.Error("expected no duplicate, got true")
		}
	})

	t.Run("ModeTasksAxi FAILED propagates error", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
		os.Setenv("MOCK_TASKS_AXI_FAIL", "1")
		defer os.Unsetenv("MOCK_TASKS_AXI_FAIL")

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		// FAILED should propagate — GetItem returns error, not silent not-found.
		_, found, err := GetItem(homeDir, "TASK-1")
		if err == nil {
			t.Fatal("expected error from tasks-axi FAILED in GetItem, got nil")
		}
		if !strings.Contains(err.Error(), "tasks-axi") {
			t.Errorf("error should reference tasks-axi failure, got: %v", err)
		}
		if found {
			t.Error("expected not-found from tasks-axi FAILED, got found")
		}

		// Pre-populate divergent native markdown to prove FAILED never consults it.
		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [-] TASK-1: Divergent native\n"), 0644)

		// FAILED should still propagate — does not fall back to FileBackend despite native file.
		_, _, err = GetItem(homeDir, "TASK-1")
		if err == nil {
			t.Fatal("expected error from tasks-axi FAILED even with native md present (fail-closed)")
		}

		// FAILED should NOT fall through to FileBackend — HasDuplicate returns error.
		_, err = HasDuplicate(homeDir, "TASK-1")
		if err == nil {
			t.Error("expected error from tasks-axi FAILED in HasDuplicate, got nil")
		}
	})
}

func TestParseTasksAxiShowOutput(t *testing.T) {
	t.Run("full item", func(t *testing.T) {
		out := `task:
  id: test-1
  title: my task
  state: in_flight
  kind: scout
  repo: munsu
`
		item, ok := parseTasksAxiShowOutput(out)
		if !ok {
			t.Fatal("expected parse success")
		}
		if item.ID != "test-1" {
			t.Errorf("id = %q, want 'test-1'", item.ID)
		}
		if item.Description != "my task" {
			t.Errorf("desc = %q, want 'my task'", item.Description)
		}
		if item.State != StateInFlight {
			t.Errorf("state = %v, want InFlight", item.State)
		}
		if item.Kind != "scout" {
			t.Errorf("kind = %q, want 'scout'", item.Kind)
		}
		if item.Repo != "munsu" {
			t.Errorf("repo = %q, want 'munsu'", item.Repo)
		}
	})

	t.Run("no metadata fields", func(t *testing.T) {
		out := `task:
  id: test-1
  title: simple
  state: queued
`
		item, ok := parseTasksAxiShowOutput(out)
		if !ok {
			t.Fatal("expected parse success")
		}
		if item.Description != "simple" {
			t.Errorf("desc = %q, want 'simple'", item.Description)
		}
		if item.State != StateQueued {
			t.Errorf("state = %v, want Queued", item.State)
		}
		if item.Kind != "" {
			t.Errorf("kind should be empty, got %q", item.Kind)
		}
		if item.Repo != "" {
			t.Errorf("repo should be empty, got %q", item.Repo)
		}
	})

	t.Run("empty output returns not found", func(t *testing.T) {
		_, ok := parseTasksAxiShowOutput("")
		if ok {
			t.Error("expected parse failure for empty output")
		}
	})

	t.Run("done item", func(t *testing.T) {
		out := `task:
  id: test-1
  title: done
  state: done
`
		item, ok := parseTasksAxiShowOutput(out)
		if !ok {
			t.Fatal("expected parse success")
		}
		if item.State != StateDone {
			t.Errorf("state = %v, want Done", item.State)
		}
	})

	t.Run("dash placeholder for kind/repo", func(t *testing.T) {
		out := `task:
  id: test-1
  title: no meta
  state: queued
  kind: -
  repo: -
`
		item, ok := parseTasksAxiShowOutput(out)
		if !ok {
			t.Fatal("expected parse success")
		}
		if item.Kind != "" {
			t.Errorf("kind should be empty for '-', got %q", item.Kind)
		}
		if item.Repo != "" {
			t.Errorf("repo should be empty for '-', got %q", item.Repo)
		}
	})
}

func TestParseTasksAxiListOutput(t *testing.T) {
	t.Run("single item", func(t *testing.T) {
		out := `count: 1
	tasks[1]{id,state,kind,repo,title}:
	  TASK-1,in_flight,scout,munsu,my task
`
		items, err := parseTasksAxiListOutput(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].ID != "TASK-1" {
			t.Errorf("id = %q, want 'TASK-1'", items[0].ID)
		}
		if items[0].State != StateInFlight {
			t.Errorf("state = %v, want InFlight", items[0].State)
		}
		if items[0].Kind != "scout" {
			t.Errorf("kind = %q, want 'scout'", items[0].Kind)
		}
		if items[0].Repo != "munsu" {
			t.Errorf("repo = %q, want 'munsu'", items[0].Repo)
		}
		if items[0].Description != "my task" {
			t.Errorf("desc = %q, want 'my task'", items[0].Description)
		}
	})

	t.Run("multiple items with duplicate", func(t *testing.T) {
		out := `count: 2
	tasks[2]{id,state,kind,repo,title}:
	  TASK-1,queued,scout,munsu,first
	  TASK-1,in_flight,ship,munsu,second
`
		items, err := parseTasksAxiListOutput(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
		if items[0].ID != "TASK-1" || items[1].ID != "TASK-1" {
			t.Errorf("expected both items to be TASK-1")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		out := `count: 0
`
		items, err := parseTasksAxiListOutput(out)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
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

	execCommand = mockExecCommand()

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

		// Verify md was created in homeDir/data/ (manual path)
		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); os.IsNotExist(err) {
			t.Errorf("expected md at %s, but it does not exist", backlogPath)
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
		expectedFile := filepath.Join(homeDir, "data", "md")
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

		backlogPath := filepath.Join(homeDir, "data", "md")
		if _, err := os.Stat(backlogPath); os.IsNotExist(err) {
			t.Errorf("expected md at %s, but it does not exist", backlogPath)
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
	output := backlogCaptureStdout(func() {
		if err := backend.Add("task-1", "scoped", "ship", "munsu", false); err != nil {
			t.Fatal(err)
		}
	})
	expectedFile := filepath.Join(homeDir, "data", "md")
	if !strings.Contains(output, "--file "+expectedFile) {
		t.Errorf("expected home-scoped tasks-axi call, got %q", output)
	}
}

func TestFileBackend_Parse(t *testing.T) {
	t.Run("nonexistent file", func(t *testing.T) {
		fb := NewFileBackend("/nonexistent/path/md")
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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

	t.Run("tasks-axi dash format", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "md")
		content := `# Backlog

## 2024-01-01
- [ ] test-item - test item (repo: munsu) (kind: scout) (since 2026-07-21)
- [-] in-flight-item - in progress (repo: munsu) (kind: ship) (since 2026-07-21)
- [!] blocked-item - blocked task (repo: other) (kind: scout) (since 2026-07-20)
- [x] done-item - completed
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

		tests := []struct {
			id         string
			desc       string
			state      TaskState
			kind, repo string
		}{
			{"test-item", "test item", StateQueued, "scout", "munsu"},
			{"in-flight-item", "in progress", StateInFlight, "ship", "munsu"},
			{"blocked-item", "blocked task", StateBlocked, "scout", "other"},
			{"done-item", "completed", StateDone, "", ""},
		}
		for i, tc := range tests {
			if items[i].ID != tc.id {
				t.Errorf("item[%d].id = %q, want %q", i, items[i].ID, tc.id)
			}
			if items[i].Description != tc.desc {
				t.Errorf("item[%d].desc = %q, want %q", i, items[i].Description, tc.desc)
			}
			if items[i].State != tc.state {
				t.Errorf("item[%d].state = %v, want %v", i, items[i].State, tc.state)
			}
			if items[i].Kind != tc.kind {
				t.Errorf("item[%d].kind = %q, want %q", i, items[i].Kind, tc.kind)
			}
			if items[i].Repo != tc.repo {
				t.Errorf("item[%d].repo = %q, want %q", i, items[i].Repo, tc.repo)
			}
		}
	})

	t.Run("tasks-axi dash format with description containing dashes", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "md")
		content := `# Backlog

## 2024-01-01
- [ ] my-task - my task - with dashes (repo: munsu) (kind: ship)
`
		os.WriteFile(path, []byte(content), 0644)
		fb := NewFileBackend(path)

		items, err := fb.parse()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].ID != "my-task" {
			t.Errorf("id = %q, want 'my-task'", items[0].ID)
		}
		if items[0].Description != "my task - with dashes" {
			t.Errorf("desc = %q, want 'my task - with dashes'", items[0].Description)
		}
		if items[0].Kind != "ship" {
			t.Errorf("kind = %q, want 'ship'", items[0].Kind)
		}
		if items[0].Repo != "munsu" {
			t.Errorf("repo = %q, want 'munsu'", items[0].Repo)
		}
	})
}

func TestFileBackend_Render(t *testing.T) {
	t.Run("render and reparse", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
		fb := NewFileBackend(path)

		output := backlogCaptureStdout(func() {
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		fb := NewFileBackend(filepath.Join(tmp, "md"))

		_, ok := fb.Show("NONEXISTENT")
		if ok {
			t.Fatal("expected false for nonexistent item")
		}
	})
}

func TestFileBackend_UpdateState(t *testing.T) {
	t.Run("start task", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		fb := NewFileBackend(filepath.Join(tmp, "md"))

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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "data", "md")
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
		path := filepath.Join(tmp, "md")
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

		output := backlogCaptureStdout(func() {
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
		output := backlogCaptureStdout(func() {
			manualRun(homeDir, "start", []string{"TASK-1"})
		})
		if !strings.Contains(output, "[-]") {
			t.Errorf("expected in-flight, got %q", output)
		}

		// done
		output = backlogCaptureStdout(func() {
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
		fb := NewFileBackend(filepath.Join(homeDir, "data", "md"))
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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
		path := filepath.Join(tmp, "md")
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

		fb := NewFileBackend(filepath.Join(homeDir, "data", "md"))
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

		fb := NewFileBackend(filepath.Join(homeDir, "data", "md"))
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

// --- ListItems routing tests ---

func TestListItems_RouteThroughBackend(t *testing.T) {
	oldLookPath := lookPath
	oldExecCommand := execCommand
	defer func() {
		lookPath = oldLookPath
		execCommand = oldExecCommand
	}()

	execCommand = mockExecCommand()

	// tasks-axi list mock output.
	const mockListOutput = `count: 2
	tasks[2]{id,state,kind,repo,title}:
	  TASK-1,in_flight,scout,munsu,From tasks-axi
	  TASK-2,queued,ship,other,queued item
`

	t.Run("ModeTasksAxi reads from tasks-axi, not FileBackend", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
		os.Setenv("MOCK_TASKS_AXI_LIST", mockListOutput)
		defer os.Unsetenv("MOCK_TASKS_AXI_LIST")

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		// Pre-populate md with DIVERGENT data (tasks-axi should NOT read this).
		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [x] TASK-1: Divergent native data\n"), 0644)

		items, err := ListItems(homeDir, StateQueued)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
		// Must read from tasks-axi mock, not FileBackend.
		if items[0].Description != "From tasks-axi" {
			t.Errorf("expected 'From tasks-axi' (tasks-axi), got %q", items[0].Description)
		}
	})

	t.Run("ModeTasksAxi FAILED fails closed on ListItems", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
		os.Setenv("MOCK_TASKS_AXI_FAIL", "1")
		defer os.Unsetenv("MOCK_TASKS_AXI_FAIL")

		homeDir := t.TempDir()
		configDir := filepath.Join(homeDir, "config")
		os.MkdirAll(configDir, 0755)
		os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("tasks-axi\n"), 0644)

		// Pre-populate divergent native backlog to prove FAILED never consults it.
		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [-] TASK-1: Divergent native\n"), 0644)

		_, err := ListItems(homeDir, StateQueued)
		if err == nil {
			t.Fatal("expected error from tasks-axi FAILED, got nil")
		}
		if !strings.Contains(err.Error(), "tasks-axi") {
			t.Errorf("error should reference tasks-axi, got: %v", err)
		}

		// Verify md was NOT consulted (still has divergent data).
		data, _ := os.ReadFile(filepath.Join(dataDir, "md"))
		if !strings.Contains(string(data), "Divergent native") {
			t.Errorf("md should remain unchanged (not consulted by ListItems)")
		}
	})

	t.Run("ModeManual still reads native", func(t *testing.T) {
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

		// Pre-populate md with native data.
		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [-] TASK-1: Native manual data\n"), 0644)

		items, err := ListItems(homeDir, StateQueued)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].Description != "Native manual data" {
			t.Errorf("expected 'Native manual data', got %q", items[0].Description)
		}
	})

	t.Run("ModeAuto with tasks-axi available reads from tasks-axi", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "tasks-axi" {
				return "/mock/tasks-axi", nil
			}
			return "", fmt.Errorf("file not found")
		}
		os.Setenv("MOCK_TASKS_AXI_VERSION", "tasks-axi version 0.1.5")
		defer os.Unsetenv("MOCK_TASKS_AXI_VERSION")
		os.Setenv("MOCK_TASKS_AXI_LIST", mockListOutput)
		defer os.Unsetenv("MOCK_TASKS_AXI_LIST")

		homeDir := t.TempDir()
		// No config/backlog-backend → ModeAuto

		// Pre-populate divergent native backlog to prove auto mode uses tasks-axi.
		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [x] TASK-1: Divergent native data\n"), 0644)

		items, err := ListItems(homeDir, StateQueued)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
		// Must read from tasks-axi mock, not native.
		if items[0].Description != "From tasks-axi" {
			t.Errorf("expected 'From tasks-axi' (auto mode), got %q", items[0].Description)
		}
	})

	t.Run("ModeAuto with tasks-axi ABSENT falls back to FileBackend", func(t *testing.T) {
		// No tasks-axi mock at all — simulate ABSENT.
		lookPath = func(name string) (string, error) {
			return "", fmt.Errorf("not found")
		}

		homeDir := t.TempDir()
		// No config/backlog-backend → ModeAuto, tasks-axi not available → FileBackend fallback

		dataDir := filepath.Join(homeDir, "data")
		os.MkdirAll(dataDir, 0755)
		os.WriteFile(filepath.Join(dataDir, "md"), []byte("# Backlog\n\n## 2026-01-01\n- [ ] TASK-1: Fallback native item\n"), 0644)

		items, err := ListItems(homeDir, StateQueued)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item via fallback, got %d", len(items))
		}
		if items[0].Description != "Fallback native item" {
			t.Errorf("expected 'Fallback native item', got %q", items[0].Description)
		}
	})
}
