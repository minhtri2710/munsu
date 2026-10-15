package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/testutil"
)

func TestPreflight_AdapterKnown(t *testing.T) {
	testutil.SetPath(t, t.TempDir())
	result, err := Preflight(Claude, nil)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Claude, err)
	}
	if result.AdapterKnown != PreflightOK {
		t.Errorf("AdapterKnown = %q, want %q", result.AdapterKnown, PreflightOK)
	}
}

func TestPreflight_AdapterUnknown(t *testing.T) {
	_, err := Preflight("nonexistent-harness", nil)
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if _, ok := err.(*PreflightError); !ok {
		t.Fatalf("error type = %T, want *PreflightError", err)
	}
}

func TestPreflight_BinaryOnPath_Claude(t *testing.T) {
	testutil.FakeOnPath(t, "claude", "#!/bin/sh\nexit 0\n")
	result, err := Preflight(Claude, nil)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Claude, err)
	}
	if result.BinaryOnPath != PreflightOK {
		t.Errorf("BinaryOnPath = %q, want %q", result.BinaryOnPath, PreflightOK)
	}
}

// Readiness is decided by the harness's own readiness command alone. Each fake
// answers only that exact command; any other argument exits 2.
func TestPreflight_Ready(t *testing.T) {
	const claudeStatus = "#!/bin/sh\n[ \"$1 $2 $3\" = \"auth status --text\" ] || exit 2\n"
	const piList = "#!/bin/sh\n[ \"$1\" = \"--list-models\" ] || exit 2\n"
	// The seat's config dir must be a fresh dir directly in the seat build dir
	// the row names, never under a temp root.
	const seatAsks = "[ \"${CLAUDE_SECURESTORAGE_CONFIG_DIR-unset}\" = /h/claude ] && [ -d \"$CLAUDE_CONFIG_DIR\" ] && [ \"${CLAUDE_CONFIG_DIR%/*}\" = \"$WANT_BUILD_DIR\" ] || exit 1\n"
	tests := []struct {
		name       string
		harness    string
		script     string // fake harness script; empty leaves it off PATH
		timeout    time.Duration
		seat       *ClaudeConfig
		noBuild    bool // the seat's empty config dir cannot be made
		want       PreflightLevel
		wantDetail string
	}{
		{name: "claude logged in", harness: Claude, script: claudeStatus + "echo 'Login method: Claude Max account'\nexit 0\n", want: PreflightOK},
		{name: "claude not logged in", harness: Claude, script: claudeStatus + "echo 'Not logged in. Run claude auth login to authenticate.'\nexit 1\n", want: PreflightAbsent, wantDetail: "`claude auth status --text` failed: Not logged in. Run claude auth login to authenticate."},
		{name: "claude probe does not finish in time", harness: Claude, script: claudeStatus + "exec sleep 30\n", timeout: 100 * time.Millisecond, want: PreflightAbsent, wantDetail: "did not finish within 100ms"},
		{name: "claude not on PATH", harness: Claude, want: PreflightUnknown},
		{name: "pi lists models", harness: Pi, script: piList + "echo 'provider model'\necho 'anthropic claude-fable-5'\n", want: PreflightOK},
		{name: "pi has no models", harness: Pi, script: piList + "echo 'No models available. Use /login to log into a provider via OAuth or API key.'\n", want: PreflightAbsent, wantDetail: "`pi --list-models` reported: No models available."},
		{name: "codex has no readiness command", harness: Codex, script: "#!/bin/sh\nexit 1\n", want: PreflightUnknown},
		// A fenced seat logs in with a config dir of its own and the Human's
		// keychain item; the fake answers only when asked that way.
		{name: "seat asks with its own config dir and the Human's keychain item", harness: Claude, script: claudeStatus + seatAsks + "exit 0\n", seat: &ClaudeConfig{SecureStorage: "/h/claude"}, want: PreflightOK},
		{name: "Human's config asked where a seat is meant", harness: Claude, script: claudeStatus + seatAsks + "exit 0\n", want: PreflightAbsent},
		{name: "seat config dir cannot be made", harness: Claude, script: claudeStatus + "exit 0\n", seat: &ClaudeConfig{}, noBuild: true, want: PreflightAbsent, wantDetail: "preparing the seat build dir"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.script == "" {
				testutil.SetPath(t, t.TempDir())
			} else {
				testutil.FakeOnPath(t, preflightBinaryNames[tc.harness], tc.script)
			}
			var seat *ClaudeSeat
			if tc.seat != nil {
				home := t.TempDir()
				seat = &ClaudeSeat{Config: *tc.seat, BuildDir: SeatBuildDir(home)}
				if tc.noBuild {
					blocker := filepath.Join(home, "file")
					if err := os.WriteFile(blocker, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					seat.BuildDir = filepath.Join(blocker, SeatBuildDirName)
				}
				t.Setenv("WANT_BUILD_DIR", seat.BuildDir)
			}
			if tc.timeout > 0 {
				orig := readinessProbeTimeout
				readinessProbeTimeout = tc.timeout
				t.Cleanup(func() { readinessProbeTimeout = orig })
			}
			result, err := Preflight(tc.harness, seat)
			if err != nil {
				t.Fatalf("Preflight(%q) error = %v", tc.harness, err)
			}
			if result.Ready != tc.want {
				t.Errorf("Ready = %q, want %q (detail %q)", result.Ready, tc.want, result.ReadyDetail)
			}
			if !strings.Contains(result.ReadyDetail, tc.wantDetail) || (tc.want == PreflightOK && result.ReadyDetail != "") {
				t.Errorf("ReadyDetail = %q, want it to contain %q", result.ReadyDetail, tc.wantDetail)
			}
		})
	}
}

func TestPreflight_ModelValid_Unknown(t *testing.T) {
	result, err := Preflight(Codex, nil)
	if err != nil {
		t.Fatalf("Preflight(%q) error = %v", Codex, err)
	}
	if result.ModelValid != PreflightUnknown {
		t.Errorf("ModelValid = %q, want %q (can't validate without API)", result.ModelValid, PreflightUnknown)
	}
}

func TestPreflightError_ErrorMessages(t *testing.T) {
	err := &PreflightError{Harness: "codex", Reason: "adapter-unknown"}
	msg := err.Error()
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}

	err = &PreflightError{Harness: "pi", Reason: "binary-absent"}
	msg = err.Error()
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}

	err = &PreflightError{Harness: "claude", Reason: "not-ready", Detail: "`claude auth status --text` failed: Not logged in."}
	if msg = err.Error(); msg != "harness \"claude\" cannot run: `claude auth status --text` failed: Not logged in." {
		t.Fatalf("not-ready message = %q", msg)
	}
}

func TestPreflight_AllKnownHarnesses(t *testing.T) {
	testutil.SetPath(t, t.TempDir())
	for _, h := range KnownHarnesses {
		result, err := Preflight(h, nil)
		if err != nil {
			if _, ok := err.(*PreflightError); !ok {
				t.Errorf("Preflight(%q) error type = %T, want *PreflightError", h, err)
			}
		}
		if result != nil && result.AdapterKnown != PreflightOK {
			t.Errorf("Preflight(%q).AdapterKnown = %q, want %q", h, result.AdapterKnown, PreflightOK)
		}
	}
}
