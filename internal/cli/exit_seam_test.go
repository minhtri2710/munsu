package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureExit swaps the package exit seam for a recorder, runs f, and returns
// the codes the handler asked for. Without the seam these handlers call
// os.Exit inside RunE, which terminates the test binary itself and cuts off
// every test queued behind them (#698).
func captureExit(t *testing.T, f func()) []int {
	t.Helper()
	var codes []int
	oldExit := exitWithCode
	exitWithCode = func(code int) { codes = append(codes, code) }
	defer func() { exitWithCode = oldExit }()
	f()
	return codes
}

// captureProcessStderr collects os.Stderr (not cmd.ErrOrStderr) for the
// duration of f. doctor writes its refusal lines straight to os.Stderr.
func captureProcessStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("draining stderr: %v", err)
	}
	r.Close()
	return buf.String()
}

// TestDoctorReturnsWhenHardRequiredToolMissing proves `munsu doctor` reports
// exit code 1 through the seam and RETURNS, instead of terminating the
// process from inside RunE. An empty PATH makes every hard-required tool --
// tmux included -- missing, and HERDR_ENV is cleared so no alternative
// session backend rescues the check.
func TestDoctorReturnsWhenHardRequiredToolMissing(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HERDR_ENV", "")

	var out string
	codes := captureExit(t, func() {
		out = captureProcessStderr(t, func() {
			if got, err := runRoot(t, "doctor"); err != nil {
				t.Errorf("doctor returned an error: %v\n%s", err, got)
			}
		})
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("doctor exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(out, "Some required tools are missing.") {
		t.Fatalf("expected the missing-tools line on stderr, got: %s", out)
	}
}

// TestDoctorReturnsZeroExitWhenNothingHardRequiredIsMissing pins the other
// half of the external contract: a healthy run asks for no exit code at all,
// so `munsu doctor` still exits 0 from cmd/munsu/main.go.
func TestDoctorReturnsZeroExitWhenNothingHardRequiredIsMissing(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("HERDR_ENV", "1")

	codes := captureExit(t, func() {
		captureProcessStderr(t, func() {
			if got, err := runRoot(t, "doctor"); err != nil {
				t.Errorf("doctor returned an error: %v\n%s", err, got)
			}
		})
	})

	for _, code := range codes {
		if code != 0 {
			t.Fatalf("healthy doctor asked for exit %d, want none non-zero (all codes: %v)", code, codes)
		}
	}
}

// TestDoctorCheckInstructionsReturnsOnMismatch proves the doc-code mismatch
// path reports exit 1 through the seam and returns.
func TestDoctorCheckInstructionsReturnsOnMismatch(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)
	agents := filepath.Join(homeDir, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("Run `munsu definitely-not-a-command` to proceed.\n"), 0644); err != nil {
		t.Fatalf("writing AGENTS.md: %v", err)
	}

	var out string
	codes := captureExit(t, func() {
		out = captureProcessStderr(t, func() {
			if got, err := runRoot(t, "doctor", "--check-instructions"); err != nil {
				t.Errorf("check-instructions returned an error: %v\n%s", err, got)
			}
		})
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("check-instructions exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(out, "doc-code mismatches") {
		t.Fatalf("expected the mismatch summary on stderr, got: %s", out)
	}
}

// TestDoctorCheckInstructionsNoExitWhenDocsMatch pins the clean side: no exit
// code is requested, so the command still exits 0.
func TestDoctorCheckInstructionsNoExitWhenDocsMatch(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)
	agents := filepath.Join(homeDir, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("Run `munsu doctor` to proceed.\n"), 0644); err != nil {
		t.Fatalf("writing AGENTS.md: %v", err)
	}

	codes := captureExit(t, func() {
		captureProcessStderr(t, func() {
			if got, err := runRoot(t, "doctor", "--check-instructions"); err != nil {
				t.Errorf("check-instructions returned an error: %v\n%s", err, got)
			}
		})
	})

	if len(codes) != 0 {
		t.Fatalf("clean check-instructions exit codes = %v, want none", codes)
	}
}

// TestDecisionHoldVerifyReturnsOnUnresolvedHolds proves the unresolved branch
// of `decision-hold verify` reports exit 1 through the seam and returns.
func TestDecisionHoldVerifyReturnsOnUnresolvedHolds(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)
	if out, err := runRoot(t, "decision-hold", "hold", "approach",
		"--reason", "Pick the UI framework", "--from", "scout-r2",
		"--output", "json"); err != nil {
		t.Fatalf("hold: %v\n%s", err, out)
	}

	var out string
	codes := captureExit(t, func() {
		var err error
		out, err = runRoot(t, "decision-hold", "verify", "scout-r2")
		if err != nil {
			t.Errorf("verify returned an error: %v\n%s", err, out)
		}
	})

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("verify exit codes = %v, want [1]", codes)
	}
	if !strings.Contains(out, "unresolved decisions remain") {
		t.Fatalf("verify output = %s", out)
	}
}

// TestDecisionHoldVerifyReturnsWhenAuthorityCannotOpen proves the
// authority-composition branch reports exit 2 through the seam and returns.
// An uninitialized home fails closed in Ctx.TaskAuthority.
func TestDecisionHoldVerifyReturnsWhenAuthorityCannotOpen(t *testing.T) {
	homeDir := t.TempDir() // deliberately not initialized
	t.Setenv("MUNSU_HOME", homeDir)

	var out string
	codes := captureExit(t, func() {
		var err error
		out, err = runRoot(t, "decision-hold", "verify", "scout-r2")
		if err != nil {
			t.Errorf("verify returned an error: %v\n%s", err, out)
		}
	})

	if len(codes) != 1 || codes[0] != 2 {
		t.Fatalf("verify exit codes = %v, want [2]", codes)
	}
	if !strings.Contains(out, "error: verifying holds:") {
		t.Fatalf("verify output = %s", out)
	}
}

// TestDecisionHoldVerifyReturnsWhenHoldScanFails proves the scan-failure
// branch reports exit 2 through the seam and returns. An origin id the
// durable-key rules reject makes the status read fail after the Authority has
// already been composed.
func TestDecisionHoldVerifyReturnsWhenHoldScanFails(t *testing.T) {
	homeDir := t.TempDir()
	initCLITestHome(t, homeDir)
	t.Setenv("MUNSU_HOME", homeDir)

	var out string
	codes := captureExit(t, func() {
		var err error
		out, err = runRoot(t, "decision-hold", "verify", "scout/r2")
		if err != nil {
			t.Errorf("verify returned an error: %v\n%s", err, out)
		}
	})

	if len(codes) != 1 || codes[0] != 2 {
		t.Fatalf("verify exit codes = %v, want [2]", codes)
	}
	if !strings.Contains(out, "error: verifying holds:") {
		t.Fatalf("verify output = %s", out)
	}
}
