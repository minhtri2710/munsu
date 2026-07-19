package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/supervision"
)

func TestWaitForWatcherBeacon_TimeoutWithoutBeat(t *testing.T) {
	home := t.TempDir()
	status, ok := waitForWatcherBeacon(home, 1, 120*time.Millisecond)
	if ok {
		t.Fatal("expected validation failure without beat/identity")
	}
	if status.Exists {
		t.Fatal("expected no beat")
	}
}

func TestWaitForWatcherBeacon_SeesBeat(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	os.WriteFile(lifecycle.BeatPath(home), []byte(fmt.Sprintf("%d %d\n", time.Now().Unix(), pid)), 0644)
	id := supervision.NewIdentity(home)
	id.PID = pid
	_ = supervision.WriteIdentity(home, id)

	status, _ := waitForWatcherBeacon(home, pid, 200*time.Millisecond)
	if !status.Exists {
		t.Fatal("expected beat to exist")
	}
}

func TestStopWatcher_AlreadyStopped(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	resp := stopWatcher(home)
	if resp.Data.State != "already-stopped" {
		t.Fatalf("state=%q", resp.Data.State)
	}
}

func TestEnsureWatcher_ReturnsContract(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	resp := ensureWatcher(home, false)
	if resp.Kind != "watch.ensure" {
		t.Fatalf("kind=%q", resp.Kind)
	}
	if resp.Data.State == "" {
		t.Fatal("empty state")
	}
	_ = stopWatcher(home)
}
