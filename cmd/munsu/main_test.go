package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainUnknownCommandExitsOne(t *testing.T) {
	if os.Getenv("MUNSU_MAIN_TEST_CHILD") == "1" {
		os.Args = []string{"munsu", "no-such-command"}
		main()
		return
	}

	home := t.TempDir()
	munsuHome := filepath.Join(home, "munsu-home")
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainUnknownCommandExitsOne$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") &&
			!strings.HasPrefix(entry, "MUNSU_HOME=") &&
			!strings.HasPrefix(entry, "MUNSU_MAIN_TEST_CHILD=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env,
		"MUNSU_MAIN_TEST_CHILD=1",
		"HOME="+home,
		"MUNSU_HOME="+munsuHome,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("child exit = %v, stdout=%q, stderr=%q; want exit 1", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "error_code: error") {
		t.Fatalf("stdout = %q, want contract error envelope", stdout.String())
	}
	if _, err := os.Stat(munsuHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown command touched MUNSU_HOME: stat error = %v", err)
	}
}
