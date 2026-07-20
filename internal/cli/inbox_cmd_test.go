package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInboxCmd_Wakes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create a wake queue with one entry
	stateDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(stateDir, 0755)
	wakePath := filepath.Join(tmpDir, "state", ".wake-queue")
	os.WriteFile(wakePath, []byte("1742000000\t1001\tsignal\ttest\tsome status: hello\n"), 0644)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"inbox"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Pending wakes: 1") {
		t.Errorf("inbox should show pending wakes count, got:\n%s", output)
	}
}

func TestInboxCmd_NoWakes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, "state"), 0755)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"inbox"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Wakes: none pending") {
		t.Errorf("inbox should show no wakes, got:\n%s", output)
	}
}

func TestInboxCmd_CaptainStatus(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)

	// Create captain status files
	stateDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(stateDir, 0755)

	// Actionable captain status (done = general-relevant)
	os.WriteFile(filepath.Join(stateDir, "captain:domain.status"),
		[]byte("working: processing\n"+
			"done: phase-1 complete\n"), 0644)

	// Non-actionable captain status (working)
	os.WriteFile(filepath.Join(stateDir, "captain:infra.status"),
		[]byte("working: healthy\n"), 0644)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"inbox"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "domain") {
		t.Errorf("inbox should show captain:domain status, got:\n%s", output)
	}
	if !strings.Contains(output, "infra") {
		t.Errorf("inbox should show captain:infra status, got:\n%s", output)
	}
	if !strings.Contains(output, "done: phase-1 complete") {
		t.Errorf("inbox should show done status line, got:\n%s", output)
	}
	if !strings.Contains(output, "!") {
		t.Errorf("inbox should mark actionable captain with !, got:\n%s", output)
	}
}

func TestInboxCmd_EmptyState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MUNSU_HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, "state"), 0755)

	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs([]string{"inbox"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("inbox: unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No captains registered.") {
		t.Errorf("inbox should show 'No captains registered.', got:\n%s", output)
	}
}
