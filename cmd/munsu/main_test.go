package main

import (
	"bytes"
	"encoding/json"
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

// TestMainCaptainSeatRouting runs main in a subprocess with the captain seat
// env pointing at a socket nothing listens on. A charter verb must exit
// through the seat with its failure and run no root command; a non-charter
// verb must skip the seat and run the root command.
func TestMainCaptainSeatRouting(t *testing.T) {
	if args := os.Getenv("MUNSU_MAIN_SEAT_TEST_ARGS"); args != "" {
		var argv []string
		if err := json.Unmarshal([]byte(args), &argv); err != nil {
			t.Fatal(err)
		}
		os.Args = append([]string{"munsu"}, argv...)
		main()
		return
	}

	for _, tc := range []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "charter verb exits through the seat and runs no root command",
			args:       []string{"task", "list"},
			wantExit:   1,
			wantStdout: "",
			wantStderr: "munsu: captain seat: dial unix seat.sock: connect: no such file or directory\n",
		},
		{
			name:       "non-charter verb skips the seat and runs the root command",
			args:       []string{"--help"},
			wantExit:   0,
			wantStdout: "Usage:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			cmd := exec.Command(os.Args[0], "-test.run=^TestMainCaptainSeatRouting$")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "HOME=") &&
					!strings.HasPrefix(entry, "MUNSU_HOME=") &&
					!strings.HasPrefix(entry, "MUNSU_CAPTAIN_SEAT=") &&
					!strings.HasPrefix(entry, "MUNSU_MAIN_SEAT_TEST_ARGS=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			argv, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(cmd.Env,
				"MUNSU_MAIN_SEAT_TEST_ARGS="+string(argv),
				"MUNSU_CAPTAIN_SEAT="+filepath.Join(dir, "seat.sock"),
				"HOME="+dir,
				"MUNSU_HOME="+home,
			)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			runErr := cmd.Run()
			exit := 0
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				exit = exitErr.ExitCode()
			} else if runErr != nil {
				t.Fatalf("running child: %v", runErr)
			}
			if exit != tc.wantExit {
				t.Fatalf("exit = %d, want %d; stdout=%q stderr=%q", exit, tc.wantExit, stdout.String(), stderr.String())
			}
			if tc.wantStderr != "" && stderr.String() != tc.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.wantStderr)
			}
			if tc.wantStderr == "" && strings.Contains(stderr.String(), "captain seat") {
				t.Fatalf("non-charter verb went through the seat: stderr=%q", stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantStdout) || (tc.wantStdout == "" && stdout.Len() != 0) {
				t.Fatalf("stdout = %q, want it to contain %q", stdout.String(), tc.wantStdout)
			}
		})
	}
}
