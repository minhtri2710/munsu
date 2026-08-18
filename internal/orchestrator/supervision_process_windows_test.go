//go:build windows

package orchestrator

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureWatcherProcessCreatesIndependentGroup(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	configureWatcherProcess(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("SysProcAttr=%+v", cmd.SysProcAttr)
	}
}

// TestSignalWatcherProcessMatchesStopContract pins the windows half of the
// signal split at the only level this repo measures it: the goos-vet lane
// compiles this file, so the binding proves signalWatcherProcess exists on
// windows with the shape stopRunningWatcher calls. No lane runs it — the stop's
// runtime effect on a live watcher stays unproven here.
func TestSignalWatcherProcessMatchesStopContract(t *testing.T) {
	var stop func(*os.Process) error = signalWatcherProcess
	if stop == nil {
		t.Fatal("signalWatcherProcess is nil")
	}
}
