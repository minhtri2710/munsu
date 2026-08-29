package orchestrator

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	afkDaemonChildEnv       = "MUNSU_TEST_AFK_DAEMON_CHILD"
	afkDaemonChildHomeEnv   = "MUNSU_TEST_AFK_DAEMON_HOME"
	afkDaemonChildReadyEnv  = "MUNSU_TEST_AFK_DAEMON_READY"
	afkDaemonChildReadyFile = ".afk-test-ready"
)

func afkDaemonReadyPath(homeDir string) string {
	return filepath.Join(homeDir, afkDaemonChildReadyFile)
}

type afkDaemonChild struct {
	cmd     *exec.Cmd
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
	done    chan struct{}
	waitErr error
}

func startAFKDaemonChild(t *testing.T, homeDir string, signalReady bool) *afkDaemonChild {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locating test executable: %v", err)
	}

	args := []string{"-test.run=^TestAFKDaemonChildProcess$"}
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-test.gocoverdir=") {
			args = append(args, arg)
		}
	}
	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(),
		afkDaemonChildEnv+"=1",
		afkDaemonChildHomeEnv+"="+homeDir,
	)
	if signalReady {
		cmd.Env = append(cmd.Env, afkDaemonChildReadyEnv+"="+afkDaemonReadyPath(homeDir))
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting AFK daemon child: %v", err)
	}
	if cmd.Process.Pid == os.Getpid() {
		t.Fatal("AFK daemon child reused the test process PID")
	}

	child := &afkDaemonChild{
		cmd:    cmd,
		stdout: stdout,
		stderr: stderr,
		done:   make(chan struct{}),
	}
	go func() {
		child.waitErr = cmd.Wait()
		close(child.done)
	}()
	t.Cleanup(child.cleanup)
	return child
}

// TestAFKDaemonChildProcess is the re-exec entry point for the daemon lifecycle
// tests. It is skipped during the parent test run and selected only in the
// child test binary by startAFKDaemonChild.
func TestAFKDaemonChildProcess(t *testing.T) {
	if os.Getenv(afkDaemonChildEnv) != "1" {
		t.Skip("AFK daemon child process entry point")
	}

	homeDir := os.Getenv(afkDaemonChildHomeEnv)
	if homeDir == "" {
		t.Fatal("AFK daemon child home is empty")
	}
	readyPath := os.Getenv(afkDaemonChildReadyEnv)
	if readyPath == "" {
		if err := (&Daemon{}).Start(homeDir); err != nil {
			t.Fatalf("Daemon.Start: %v", err)
		}
		return
	}

	d := &Daemon{ready: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- d.Start(homeDir)
	}()
	<-d.ready
	if err := os.WriteFile(readyPath, []byte("ready\n"), 0644); err != nil {
		t.Fatalf("writing daemon readiness marker: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Daemon.Start: %v", err)
	}
}

func (c *afkDaemonChild) cleanup() {
	select {
	case <-c.done:
		return
	default:
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	<-c.done
}

func (c *afkDaemonChild) output() string {
	return fmt.Sprintf("stdout:\n%s\nstderr:\n%s", c.stdout.String(), c.stderr.String())
}

func (c *afkDaemonChild) wait(t *testing.T, timeout time.Duration) error {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.done:
		return c.waitErr
	case <-timer.C:
		killErr := error(nil)
		if c.cmd.Process != nil {
			killErr = c.cmd.Process.Kill()
		}
		<-c.done
		t.Fatalf("AFK daemon child did not exit within %s; cleanup kill: %v\n%s", timeout, killErr, c.output())
		return nil
	}
}

func stopAFKDaemonChild(t *testing.T, child *afkDaemonChild) {
	t.Helper()
	pid := child.cmd.Process.Pid
	if pid == os.Getpid() {
		t.Fatal("refusing to stop the test process")
	}
	if err := stopProcess(pid); err != nil {
		t.Fatalf("stopping AFK daemon child PID %d: %v", pid, err)
	}
	if err := child.wait(t, 5*time.Second); err != nil && !stopProcessIsLossy() {
		t.Fatalf("AFK daemon child exited after graceful stop with error: %v\n%s", err, child.output())
	}
}

func TestWaitForFileReportsExitedChild(t *testing.T) {
	done := make(chan struct{})
	close(done)
	child := &afkDaemonChild{
		stdout:  bytes.NewBufferString("child stdout"),
		stderr:  bytes.NewBufferString("child stderr"),
		done:    done,
		waitErr: fmt.Errorf("child failed"),
	}

	started := time.Now()
	err := waitForFile(child, filepath.Join(t.TempDir(), "missing"), time.Second)
	if err == nil {
		t.Fatal("waitForFile returned nil, want child-exit error")
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("waitForFile took %s after child exit, want prompt return", elapsed)
	}
	for _, want := range []string{"child failed", "child stdout", "child stderr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("waitForFile error = %q, want %q", err, want)
		}
	}
}
