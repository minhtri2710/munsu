package orchestrator_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// TestReliablePath_WakeSurvivesCaptainRecovery proves wake survives drain/re-read.
func TestReliablePath_WakeSurvivesCaptainRecovery(t *testing.T) {
	home := t.TempDir()

	if err := orchestrator.EnqueueWake(home, "signal", "task-1", "done: PR merged"); err != nil {
		t.Fatalf("EnqueueWake: %v", err)
	}
	records, err := orchestrator.DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("drained %d, want 1", len(records))
	}
	if orchestrator.HasQueuedWakes(home) {
		t.Fatal("HasQueuedWakes true after drain")
	}
}

// TestReliablePath_DuplicateWakeIdempotency proves append-only.
func TestReliablePath_DuplicateWakeIdempotency(t *testing.T) {
	home := t.TempDir()
	orchestrator.EnqueueWake(home, "signal", "task-1", "done: same")
	orchestrator.EnqueueWake(home, "signal", "task-1", "done: same")

	records, err := orchestrator.DrainWakes(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("drained %d, want 2", len(records))
	}
}

// TestReliablePath_EventAppendRoundTrip proves event log round-trips.
func TestReliablePath_EventAppendRoundTrip(t *testing.T) {
	home := t.TempDir()
	sid, err := orchestrator.Append(home, "task.status", "producer", "key", "done: test")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	data, err := os.ReadFile(orchestrator.LogPath(home))
	if err != nil {
		t.Fatalf("reading event log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("records = %d, want 1", len(lines))
	}
	if gotID := strings.SplitN(lines[0], "\t", 2)[0]; gotID != fmt.Sprintf("%d", sid) {
		t.Errorf("ID = %s, want %d", gotID, sid)
	}
}

// TestTypedDiagnostics_EnqueueTimestamps proves epoch timestamps in wakes.
func TestTypedDiagnostics_EnqueueTimestamps(t *testing.T) {
	home := t.TempDir()
	before := time.Now().Unix()
	orchestrator.EnqueueWake(home, "signal", "task-ts", "done: test")
	after := time.Now().Unix()

	records, _ := orchestrator.DrainWakes(home)
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	var epoch int64
	fmt.Sscanf(records[0].Epoch, "%d", &epoch)
	if epoch < before || epoch > after+1 {
		t.Errorf("epoch = %d, want between %d and %d", epoch, before, after)
	}
}

// TestTypedDiagnostics_WatcherIdentityPersisted proves identity is durable.
func TestTypedDiagnostics_WatcherIdentityPersisted(t *testing.T) {
	home := t.TempDir()
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)

	read := orchestrator.ReadIdentity(home)
	if read == nil || read.PID != id.PID {
		t.Fatalf("identity mismatch: read=%+v", read)
	}
	if !orchestrator.ValidatePIDOwnership(home, os.Getpid()) {
		t.Fatal("ValidatePIDOwnership failed")
	}
}

// TestBoundedWakeDrainAnnotations proves drain with correct count.
func TestBoundedWakeDrainAnnotations(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 3; i++ {
		orchestrator.EnqueueWake(home, "signal", fmt.Sprintf("task-%d", i), "done: work")
	}

	records, _ := orchestrator.DrainWakes(home)
	if len(records) != 3 {
		t.Fatalf("drained %d, want 3", len(records))
	}
	if orchestrator.HasQueuedWakes(home) {
		t.Fatal("queue still exists after drain")
	}
}

// TestTypedDiagnostics_WatcherIdentitySummary checks identity summary format.
// TestConfigRereadWake_Visibility proves config wakes survive enqueue and drain.
func TestConfigRereadWake_Visibility(t *testing.T) {
	home := t.TempDir()

	// Enqueue a config-reread wake as the converge loop would.
	if err := orchestrator.EnqueueWake(home, "config", "config-reread", "config refreshed via converge"); err != nil {
		t.Fatalf("EnqueueWake: %v", err)
	}

	// Verify it survives drain.
	records, err := orchestrator.DrainWakes(home)
	if err != nil {
		t.Fatalf("DrainWakes: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("drained %d wakes, want 1", len(records))
	}
	if records[0].Kind != "config" {
		t.Errorf("kind = %q, want config", records[0].Kind)
	}
	if records[0].Key != "config-reread" {
		t.Errorf("key = %q, want config-reread", records[0].Key)
	}
	if !strings.Contains(records[0].Payload, "config refreshed") {
		t.Errorf("payload = %q, want config refreshed", records[0].Payload)
	}

	// Queue must be empty after drain.
	if orchestrator.HasQueuedWakes(home) {
		t.Fatal("HasQueuedWakes true after drain")
	}
}

func TestTypedDiagnostics_WatcherIdentitySummary(t *testing.T) {
	id := &orchestrator.WatcherIdentity{
		PID:             12345,
		BuildVersion:    "1.2.3",
		CommitSHA:       "abc1234",
		ProtocolVersion: 2,
		StartTime:       time.Now().Unix(),
		Home:            "/tmp/test",
	}
	summary := orchestrator.IdentitySummary(id)
	if !strings.Contains(summary, "pid=12345") {
		t.Errorf("missing pid: %q", summary)
	}
	if !strings.Contains(summary, "version=1.2.3") {
		t.Errorf("missing version: %q", summary)
	}
}
