//go:build integration

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
)

// The captain probe on a real tmux pane finds a claude captain run by its
// `munsu captain seat` as well as one run in the pane directly, and only the
// seat's own child counts: the seat running another program, and claude under
// a parent that is not a seat, are not a live captain. A nudge then reaches
// the seated captain.
func TestCaptainProbeOnTmuxFindsTheCaptainUnderItsSeat(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	munsu := buildMunsuBinary(t)
	// Under /tmp: a unix socket path is capped near 104 bytes.
	dir, err := os.MkdirTemp("/tmp", "munsu-cli-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	os.Unsetenv("TMUX")
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", "probe").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-server").Run() })

	bin := filepath.Join(dir, "bin")
	captainHome := filepath.Join(dir, "home")
	for _, d := range []string{bin, filepath.Join(captainHome, "state")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := os.ReadFile("/bin/cat")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "notagent"} {
		if err := os.WriteFile(filepath.Join(bin, name), cat, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	claude, notagent := filepath.Join(bin, "claude"), filepath.Join(bin, "notagent")
	seat := munsu + " --home " + captainHome + " captain seat -- "

	tk := &backend.TmuxBackend{}
	for _, tc := range []struct {
		name, command, foreground string
		alive                     bool
	}{
		{"the seat's claude is the live captain", seat + claude, "claude", true},
		{"claude in the pane is the live captain", claude, "claude", true},
		{"the seat running another program is not", seat + notagent, "notagent", false},
		{"claude under a parent that is not a seat is not", "perl -e 'system @ARGV' " + claude, "perl", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wid, err := tk.NewWindow("probe", "captain")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tk.Teardown(wid) })
			if err := tk.SendKeys(wid, tc.command); err != nil {
				t.Fatal(err)
			}
			waitTmux(t, "the foreground process "+tc.foreground, func() bool {
				got, _ := tk.ForegroundProcess(wid)
				return got == tc.foreground
			})
			// The program reading the pane echoes a line back once it runs.
			if err := tk.SendKeys(wid, "ping"); err != nil {
				t.Fatal(err)
			}
			waitTmux(t, "the program to echo", func() bool {
				screen, _ := tk.Capture(wid, 30)
				return strings.Count(screen, "ping") == 2
			})

			meta := map[string]string{"window": wid, "harness": "claude"}
			got, err := probeCaptainBackend(tk, meta)
			if err != nil || !got.PaneAlive || got.AgentAlive != tc.alive || got.ReadyForPrompt != tc.alive {
				t.Fatalf("probeCaptainBackend = %+v, %v; want a present pane with AgentAlive %v", got, err, tc.alive)
			}
			if !tc.alive {
				return
			}
			if res := backend.SubmitPrompt(tk, wid, "nudge-payload"); res.Status != backend.PromptSubmitted {
				t.Fatalf("SubmitPrompt = %+v, want submitted", res)
			}
			waitTmux(t, "the captain to echo the nudge", func() bool {
				screen, _ := tk.Capture(wid, 30)
				return strings.Count(screen, "nudge-payload") == 2
			})
		})
	}
}

func waitTmux(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
