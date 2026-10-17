//go:build darwin

package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/testutil"
)

// fencedCaptainOut names, for TestFencedCaptainChild, the file its results go
// to; fencedCaptainHome names the captain home it writes into.
const (
	fencedCaptainOut  = "MUNSU_TEST_FENCED_CAPTAIN_OUT"
	fencedCaptainHome = "MUNSU_TEST_FENCED_CAPTAIN_HOME"
)

func TestFencedCaptainChild(t *testing.T) {
	out := os.Getenv(fencedCaptainOut)
	if out == "" {
		t.Skip("runs only as the captain of TestFencedCaptainRunsItsSeatCommandsOutsideTheFence")
	}
	direct := os.WriteFile(filepath.Join(os.Getenv(fencedCaptainHome), "state", "direct"), nil, 0o600)
	code, err := ForwardCaptainSeat(os.Getenv(CaptainSeatEnv), []string{"spawn", "T-1", "proj"})
	os.WriteFile(out, []byte(fmt.Sprintf("%v|%d|%v", errors.Is(direct, syscall.EPERM), code, err)), 0o600)
	os.Exit(code)
}

// A captain under its fence cannot write its home's state/, but the spawn it
// sends through its seat runs outside the fence: it writes there, and its
// sandbox-exec does not nest (sandbox_apply refuses inside a sandbox).
func TestFencedCaptainRunsItsSeatCommandsOutsideTheFence(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "captains", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	captainHome, err := canonicalPath(filepath.Join(parent, "captains", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.Init(captainHome); err != nil {
		t.Fatal(err)
	}
	seat, err := prepareCaptainSeat(captainHome, parent, "c", "pi")
	if err != nil {
		t.Fatalf("prepareCaptainSeat: %v", err)
	}
	if seat.fence == nil || len(seat.digest) != 64 || seat.meta()["fence"] != "applied" {
		t.Fatalf("seat = %+v, want a probed fence", seat)
	}
	rec, out := filepath.Join(t.TempDir(), "rec"), filepath.Join(t.TempDir(), "out")
	fake := testutil.WriteFakeExecutable(t, filepath.Join(t.TempDir(), "munsu"), "#!/bin/sh\n"+
		"touch state/seat-ran && /usr/bin/sandbox-exec -p '(version 1)(allow default)' /usr/bin/true && echo \"$*\" > '"+rec+"'\n")
	orig := captainSeatMunsu
	t.Cleanup(func() { captainSeatMunsu = orig })
	captainSeatMunsu = func() (string, error) { return fake, nil }
	t.Setenv(fencedCaptainOut, out)
	t.Setenv(fencedCaptainHome, captainHome)
	argv, err := seat.fence.Wrap([]string{os.Args[0], "-test.run=^TestFencedCaptainChild$"})
	if err != nil {
		t.Fatal(err)
	}
	code, err := ServeCaptainSeat(captainHome, argv)
	if err != nil || code != 0 {
		t.Fatalf("ServeCaptainSeat = %d, %v", code, err)
	}
	if got, _ := os.ReadFile(out); string(got) != "true|0|<nil>" {
		t.Fatalf("captain saw %q, want its direct write refused and its spawn run", got)
	}
	if got, _ := os.ReadFile(rec); strings.TrimSpace(string(got)) != "spawn T-1 proj" {
		t.Fatalf("seat ran %q", got)
	}
	if _, err := os.Stat(filepath.Join(captainHome, "state", "seat-ran")); err != nil {
		t.Fatalf("the seat's spawn did not write outside the fence: %v", err)
	}
}

// A claude captain runs from its own claude config dir, opened in its fence,
// and never gets a pi agent dir.
func TestFencedClaudeCaptainRunsFromItsClaudeConfigDir(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "captains", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	captainHome, err := canonicalPath(filepath.Join(parent, "captains", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := home.Init(captainHome); err != nil {
		t.Fatal(err)
	}
	seat, err := prepareCaptainSeat(captainHome, parent, "c", "claude")
	if err != nil {
		t.Fatalf("prepareCaptainSeat: %v", err)
	}
	if _, err := buildLaunchScript("/test/bin/claude", nil, captainHome, parent, "c", seat); err != nil {
		t.Fatalf("buildLaunchScript: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(captainHome, ".captain-launch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	claudeDir, err := claudeConfigDirPath(captainHome, taskIDForCaptain("c"))
	if err != nil {
		t.Fatal(err)
	}
	piDir, err := piAgentDirPath(captainHome, taskIDForCaptain("c"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "export CLAUDE_CONFIG_DIR="+shQuote(claudeDir)+"\n") || strings.Contains(script, "PI_CODING_AGENT_DIR") {
		t.Fatalf("launch script:\n%s\nwant CLAUDE_CONFIG_DIR=%s and no pi agent dir", script, claudeDir)
	}
	if fi, err := os.Lstat(claudeDir); err != nil || !fi.IsDir() {
		t.Fatalf("claude config dir not built: %v", err)
	}
	if _, err := os.Lstat(piDir); !os.IsNotExist(err) {
		t.Fatalf("pi agent dir %s exists for a claude captain: %v", piDir, err)
	}
}
