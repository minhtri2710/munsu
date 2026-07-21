package backlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBacklog creates a test backlog file with the given items
// and sets the backend to manual mode so native backlog.md is used.
func writeBacklog(t *testing.T, homeDir string, items []string) {
	t.Helper()
	dataDir := filepath.Join(homeDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("creating data dir: %v", err)
	}
	// Force manual mode so tests that write native backlog.md are read via FileBackend.
	configDir := filepath.Join(homeDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "backlog-backend"), []byte("manual\n"), 0644); err != nil {
		t.Fatalf("writing backlog-backend config: %v", err)
	}
	content := "# Backlog\n\n## 2026-07-19\n"
	for _, item := range items {
		content += item + "\n"
	}
	if err := os.WriteFile(filepath.Join(dataDir, "backlog.md"), []byte(content), 0644); err != nil {
		t.Fatalf("writing backlog: %v", err)
	}
}

func TestBuildDigest_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	d := BuildDigest(tmpDir)
	if d.Total != 0 {
		t.Errorf("expected total 0 for empty home, got %d", d.Total)
	}
	if d.String() != "  (backlog empty or absent)" {
		t.Errorf("unexpected empty string: %s", d.String())
	}
}

func TestBuildDigest_AbsentFile(t *testing.T) {
	tmpDir := t.TempDir()
	// No backlog file at all
	d := BuildDigest(tmpDir)
	if d.Total != 0 {
		t.Errorf("expected total 0 for absent backlog, got %d", d.Total)
	}
}

func TestBuildDigest_MixedStates(t *testing.T) {
	tmpDir := t.TempDir()
	writeBacklog(t, tmpDir, []string{
		"- [ ] task-queued-1: first queued item",
		"- [ ] task-queued-2: captain queued item",
		"- [-] task-flight-1: in flight item",
		"- [!] task-blocked-1: blocked by something",
		"- [x] task-done-1: done item",
	})
	d := BuildDigest(tmpDir)
	if d.Total != 5 {
		t.Errorf("expected total 5, got %d", d.Total)
	}
	if d.Queued != 2 {
		t.Errorf("expected 2 queued, got %d", d.Queued)
	}
	if d.InFlight != 1 {
		t.Errorf("expected 1 in-flight, got %d", d.InFlight)
	}
	if d.Blocked != 1 {
		t.Errorf("expected 1 blocked, got %d", d.Blocked)
	}
	if d.Done != 1 {
		t.Errorf("expected 1 done, got %d", d.Done)
	}
	if !d.HasUnfinished() {
		t.Errorf("expected HasUnfinished()=true with queued+in-flight+blocked items")
	}
}

func TestBuildDigest_NoTaskBodies(t *testing.T) {
	tmpDir := t.TempDir()
	writeBacklog(t, tmpDir, []string{
		"- [ ] task-1: this is a long description that should NEVER appear in digest",
		"- [-] task-2: another body with sensitive details",
	})
	d := BuildDigest(tmpDir)
	if d.Total != 2 {
		t.Errorf("expected total 2, got %d", d.Total)
	}
	output := d.String()
	if strings.Contains(output, "long description") {
		t.Errorf("digest contains task body: %s", output)
	}
	if strings.Contains(output, "sensitive details") {
		t.Errorf("digest contains task body: %s", output)
	}
	// Only IDs should appear
	if !strings.Contains(output, "task-1") {
		t.Errorf("expected ID task-1 in digest, got: %s", output)
	}
	if !strings.Contains(output, "task-2") {
		t.Errorf("expected ID task-2 in digest, got: %s", output)
	}
}

func TestBuildDigest_BoundedMaxItems(t *testing.T) {
	tmpDir := t.TempDir()
	var items []string
	for i := 0; i < 100; i++ {
		items = append(items, "- [ ] task-"+fmt.Sprintf("%03d", i)+": item body")
	}
	writeBacklog(t, tmpDir, items)
	d := BuildDigest(tmpDir)
	if d.Total != 100 {
		t.Errorf("expected total 100, got %d", d.Total)
	}
	if d.Queued != 100 {
		t.Errorf("expected 100 queued, got %d", d.Queued)
	}
	// Items slice should be bounded at 80
	if len(d.Items) > 80 {
		t.Errorf("expected at most 80 items in digest, got %d", len(d.Items))
	}
	// QueuedIDs should also be bounded
	if len(d.QueuedIDs) > 80 {
		t.Errorf("expected at most 80 queued IDs, got %d", len(d.QueuedIDs))
	}
}

func TestHasUnfinished_NilDigest(t *testing.T) {
	var d *BacklogDigest
	if d.HasUnfinished() {
		t.Error("expected false for nil digest")
	}
}

func TestBuildDigest_OnlyDone(t *testing.T) {
	tmpDir := t.TempDir()
	writeBacklog(t, tmpDir, []string{
		"- [x] done-1: completed item",
		"- [x] done-2: another completed item",
	})
	d := BuildDigest(tmpDir)
	if d.Total != 2 {
		t.Errorf("expected total 2, got %d", d.Total)
	}
	if d.Done != 2 {
		t.Errorf("expected 2 done, got %d", d.Done)
	}
	if d.HasUnfinished() {
		t.Errorf("expected HasUnfinished()=false with only done items")
	}
	output := d.String()
	if !strings.Contains(output, "done: 2") {
		t.Errorf("expected 'done: 2' in output, got: %s", output)
	}
}

func TestBuildDigest_StringFormat(t *testing.T) {
	tmpDir := t.TempDir()
	writeBacklog(t, tmpDir, []string{
		"- [-] flight-1: in flight item",
		"- [!] blocked-1: blocked item",
	})
	d := BuildDigest(tmpDir)
	output := d.String()
	if !strings.Contains(output, "total: 2 items") {
		t.Errorf("expected 'total: 2 items' in output, got: %s", output)
	}
	if !strings.Contains(output, "in-flight: flight-1") {
		t.Errorf("expected 'in-flight: flight-1' in output, got: %s", output)
	}
	if !strings.Contains(output, "blocked: blocked-1") {
		t.Errorf("expected 'blocked: blocked-1' in output, got: %s", output)
	}
}
