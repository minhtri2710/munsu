package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSafetyCheckDeniesNonJSONStdinPayload(t *testing.T) {
	code, stderr := runSafetyCheckWithStdin(t, []byte("not json"), false)
	if code != 2 {
		t.Fatalf("malformed stdin payload: exit=%d, want 2 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "safety-block") || !strings.Contains(stderr, "valid JSON") {
		t.Fatalf("malformed stdin payload was not denied for parse failure: %q", stderr)
	}
}

func TestSafetyCheckDeniesStdinReadError(t *testing.T) {
	code, stderr := runSafetyCheckWithStdin(t, nil, true)
	if code != 2 {
		t.Fatalf("stdin read error: exit=%d, want 2 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "safety-block") || !strings.Contains(stderr, "reading stdin") {
		t.Fatalf("stdin read error was not denied: %q", stderr)
	}
}

func runSafetyCheckWithStdin(t *testing.T, payload []byte, closeBeforeRead bool) (int, string) {
	t.Helper()
	home := t.TempDir()
	gitDir := filepath.Join(home, "repo")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, gitDir, "init")

	exitCode := 0
	oldExit := exitWithCode
	exitWithCode = func(code int) { exitCode = code }
	defer func() { exitWithCode = oldExit }()

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if closeBeforeRead {
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		if !closeBeforeRead {
			_ = r.Close()
		}
	}()

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	_, stderr := captureBoth(func() {
		if err := runSafetyCheck(cmd, gitDir, "", "", "codex"); err != nil {
			exitCode = 1
		}
	})
	return exitCode, stderr
}
