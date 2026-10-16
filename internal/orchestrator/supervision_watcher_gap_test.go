package orchestrator

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

type identityProbePort struct {
	home string
	seen chan *WatcherIdentity
}

func (p identityProbePort) ActiveDeliveries(string) ([]ActiveDelivery, error) {
	select {
	case p.seen <- ReadIdentity(p.home):
	default:
	}
	return nil, nil
}

// A running watcher has written its identity: a cycle reads it back with this
// process's pid, and it is cleared when the watcher exits.
func TestRunWritesItsIdentityWhileRunning(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/state", 0o755); err != nil {
		t.Fatal(err)
	}
	sigCh := make(chan os.Signal, 1)
	port := identityProbePort{home: dir, seen: make(chan *WatcherIdentity, 1)}
	newTicker := func(time.Duration) *time.Ticker { return time.NewTicker(10 * time.Millisecond) }
	done := make(chan error, 1)
	go func() {
		_, err := run(dir, newTicker, sigCh, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, nil, nil, port)
		done <- err
	}()
	var id *WatcherIdentity
	select {
	case id = <-port.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("no cycle ran")
	}
	sigCh <- syscall.SIGTERM
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	if id == nil || id.PID != os.Getpid() {
		t.Fatalf("identity during the run = %+v, want this process's", id)
	}
	if ReadIdentity(dir) != nil {
		t.Fatal("identity survived the watcher's exit")
	}
}

// A restart stops the running watcher first: a beat whose owner cannot be
// proven refuses the restart before a second watcher starts.
func TestArmBackgroundRestartStopsTheOldWatcherFirst(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/state", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteBeat(dir); err != nil {
		t.Fatal(err)
	}
	saved := startWatcherProcess
	started := false
	startWatcherProcess = func(string) (int, error) { started = true; return 0, nil }
	t.Cleanup(func() { startWatcherProcess = saved })

	if err := ArmBackground(dir, true); err == nil {
		t.Fatal("restart over an unproven watcher succeeded")
	}
	if started {
		t.Fatal("restart started a watcher before the old one was stopped")
	}
}

// waitForWatcherExit reports an exit only for a process that is gone.
func TestWaitForWatcherExit(t *testing.T) {
	cmd := exec.Command("sleep", "0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !waitForWatcherExit(cmd.Process.Pid) {
		t.Fatal("an exited process was not seen as gone")
	}
	if waitForWatcherExit(os.Getpid()) {
		t.Fatal("this live process was seen as gone")
	}
}

// A Human-needed status line that is not a report wakes the General with a
// signal wake.
func TestRunCycleQueuesASignalWake(t *testing.T) {
	homeDir := t.TempDir()
	if err := home.AppendStatus(homeDir, "needs", "blocked: waiting on a key"); err != nil {
		t.Fatal(err)
	}
	if _, err := testRunCycle(homeDir); err != nil {
		t.Fatal(err)
	}
	for _, w := range mustReadWakeQueue(t, homeDir) {
		if w.Kind == "signal" && w.Key == "needs" {
			return
		}
	}
	t.Fatalf("wake queue = %+v, want a signal wake for needs", mustReadWakeQueue(t, homeDir))
}

// In a captain home the captain's own Human-needed line is not a signal for
// that home's watcher; a soldier's still is.
func TestScanFleetSkipsTheCaptainsOwnSignalInACaptainHome(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.WriteFile(homeDir+"/"+home.CaptainProvenanceMarkerName, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, "captain:x", map[string]string{"kind": "captain"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"captain:x", "soldier"} {
		if err := home.AppendStatus(homeDir, id, "blocked: waiting on a key"); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, r := range scanFleetWithProbe(homeDir, false, testEndpointProbe{}, testSeatLiveness, nil) {
		if r.Kind == "signal" {
			got = append(got, r.TaskIDs[0])
		}
	}
	if len(got) != 1 || got[0] != "soldier" {
		t.Fatalf("signal reasons for %v, want only soldier", got)
	}
}
