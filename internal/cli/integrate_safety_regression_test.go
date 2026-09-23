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

func TestSafetyCheckDeniesEmptyStdinPayload(t *testing.T) {
	code, stderr := runSafetyCheckWithStdin(t, []byte(" \n\t "), false)
	if code != 2 {
		t.Fatalf("empty stdin payload: exit=%d, want 2 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "safety-block") || !strings.Contains(stderr, "stdin payload is empty") {
		t.Fatalf("empty stdin payload was not denied with an empty-payload reason: %q", stderr)
	}
}

func TestSafetyCheckAllowsEmptyJSONStdinPayload(t *testing.T) {
	code, stderr := runSafetyCheckWithStdin(t, []byte(`{}`), false)
	if code != 0 {
		t.Fatalf("empty JSON stdin payload: exit=%d, want 0 (stderr=%q)", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("empty JSON stdin payload produced a deny: %q", stderr)
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

// TestSafetyCheckWatchAndNoMistakesVerdictsParseTokens pins the command-token
// verdicts: bounded watcher operations and help pass, bare `munsu watch` and
// unknown subcommands are refused, and the guard/doctor exemption for
// no-mistakes directories needs a real `munsu guard|doctor` command.
func TestSafetyCheckWatchAndNoMistakesVerdictsParseTokens(t *testing.T) {
	t.Setenv("MUNSU_HOME", t.TempDir())
	gitDir := initGitRepoForSafety(t, t.TempDir())
	oldExit := exitWithCode
	defer func() { exitWithCode = oldExit }()
	for _, tc := range []struct {
		command string
		block   bool
	}{
		{"munsu watch status", false},
		{"munsu watch ensure", false},
		{"munsu watch stop", false},
		{"munsu watch run", false},
		{"munsu watch --help", false},
		{"munsu watch -h", false},
		{"munsu watch ensure --help", false},
		{"munsu --home /tmp/h watch run", false},
		{"munsu watch", true},
		{"munsu --home /tmp/h watch", true},
		{"cd /tmp && munsu watch", true},
		{`bash -c "munsu watch"`, true},
		{"munsu watch bogus", true},
		{"munsu watch bogus run", true},
		{"munsu watch --running", true},
		{"cd ~/.no-mistakes && munsu doctor", false},
		{"rm -rf ~/.no-mistakes/repos/x # doctor", true},
		{`rm -rf ~/.no-mistakes/repos/x "doctor"`, true},
		{"rm -rf ~/.no-mistakes/repos/x guard", true},
		{"munsu doctor; rm -rf ~/.no-mistakes/x", true},
		{"munsu doctor && rm -rf ~/.no-mistakes/x", true},
		{`bash -c "munsu guard; rm -rf ~/.no-mistakes/x"`, true},
		{"munsu guard ~/.no-mistakes/repos/x", false},
		{"rm -rf ~/.no-mistakes", true},
		{"rm -rf $HOME/.no-mistakes", true},
		{"rm -rf ${HOME}/.no-mistakes/", true},
		{"rm -rf /Users/x/.no-mistakes", true},
		{"cd .no-mistakes", true},
		{"munsu doctor", false},
		{"ls .no-mistakes-notes", false},
	} {
		var exitCode int
		exitWithCode = func(code int) { exitCode = code }
		cmd := &cobra.Command{}
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		_, stderr := captureBoth(func() {
			runSafetyCheck(cmd, gitDir, tc.command, "", "codex")
		})
		if blocked := exitCode == 2; blocked != tc.block {
			t.Errorf("%q: blocked=%v, want %v (stderr=%q)", tc.command, blocked, tc.block, stderr)
		}
		if tc.block && strings.Contains(tc.command, "munsu watch") &&
			(!strings.Contains(stderr, "'munsu watch ensure' for a persistent watcher") || !strings.Contains(stderr, "'munsu watch run' for one cycle")) {
			t.Errorf("%q: refusal must recommend watch ensure and watch run, got %q", tc.command, stderr)
		}
	}
}

// TestSafetyCheckRefusesWatchInCommandSubstitution pins a cross-rule
// dependency: the watch rule does not parse $(...) or backtick substitution
// into words, so only hasGitCommandSubstitution refuses these shapes.
func TestSafetyCheckRefusesWatchInCommandSubstitution(t *testing.T) {
	t.Setenv("MUNSU_HOME", t.TempDir())
	gitDir := initGitRepoForSafety(t, t.TempDir())
	oldExit := exitWithCode
	defer func() { exitWithCode = oldExit }()
	for _, command := range []string{"x=$(munsu watch)", "x=`munsu watch`", "echo $(munsu watch)"} {
		var exitCode int
		exitWithCode = func(code int) { exitCode = code }
		cmd := &cobra.Command{}
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		_, stderr := captureBoth(func() {
			runSafetyCheck(cmd, gitDir, command, "", "codex")
		})
		if exitCode != 2 {
			t.Errorf("%q: exit=%d, want 2 (refused) (stderr=%q)", command, exitCode, stderr)
		}
	}
}
