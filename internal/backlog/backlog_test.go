package backlog

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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
		// Write simulated run output to stdout
		fmt.Printf("EXEC_CMD: %s\n", strings.Join(subArgs, " "))
		return
	}
}

func TestRun(t *testing.T) {
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
		err := Run("list", []string{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "fallback backlog.md editing not yet implemented") {
			t.Errorf("unexpected error message: %v", err)
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

		err := Run("list", []string{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "fallback backlog.md editing not yet implemented") {
			t.Errorf("unexpected error message: %v", err)
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
		defer func() {
			os.Stdout = oldStdout
		}()

		err = Run("add", []string{"task-1", "priority:high"})
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
