//go:build integration

package cli

import (
	"bufio"
	"fmt"
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
	for name, src := range map[string]string{"claude": os.Args[0], "notagent": "/bin/cat"} {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, name), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	claude := "env " + unbufferedCaptain + "=1 " + filepath.Join(bin, "claude") + " -test.run=^TestUnbufferedCaptainChild$"
	notagent := filepath.Join(bin, "notagent")
	seat := "env " + unbufferedCaptain + "=1 " + munsu + " --home " + captainHome + " captain seat -- " + filepath.Join(bin, "claude") + " -test.run=^TestUnbufferedCaptainChild$"
	seatNotagent := munsu + " --home " + captainHome + " captain seat -- " + notagent

	tk := &backend.TmuxBackend{}
	for _, tc := range []struct {
		name, command, foreground string
		alive                     bool
	}{
		{"the seat's claude is the live captain", seat, "claude", true},
		{"claude in the pane is the live captain", claude, "claude", true},
		{"the seat running another program is not", seatNotagent, "notagent", false},
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

			got, err := probeCaptainBackend(tk, wid, "claude")
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

// unbufferedCaptain makes TestUnbufferedCaptainChild a stand-in captain: it
// reads its terminal unbuffered, as a harness does, and echoes each line.
const unbufferedCaptain = "MUNSU_TEST_UNBUFFERED_CAPTAIN"

func TestUnbufferedCaptainChild(t *testing.T) {
	if os.Getenv(unbufferedCaptain) == "" {
		t.Skip("runs only as the captain of TestCaptainProbeOnTmuxFindsTheCaptainUnderItsSeat")
	}
	stty := exec.Command("stty", "-icanon")
	stty.Stdin = os.Stdin
	if err := stty.Run(); err != nil {
		os.Exit(1)
	}
	lines := bufio.NewScanner(os.Stdin)
	for lines.Scan() {
		fmt.Println("got: " + lines.Text())
	}
	os.Exit(0)
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
