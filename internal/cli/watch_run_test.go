package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

func TestCountQueuedWakes_UsesLifecycleQueuePath(t *testing.T) {
	home := t.TempDir()
	if countQueuedWakes(home) != 0 {
		t.Fatal("empty home should report 0 queued wakes")
	}
	if err := orchestrator.EnqueueWake(home, "status", "task-1", "done: ready"); err != nil {
		t.Fatal(err)
	}
	if got := countQueuedWakes(home); got != 1 {
		t.Fatalf("countQueuedWakes = %d, want 1 via orchestrator.QueuePath", got)
	}
}

func TestWatchRun_UsesRunCycleDedup(t *testing.T) {
	home := t.TempDir()
	if _, err := mhome.Init(home); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", home)
	t.Setenv("MUNSU_GUARD_SKIP", "1")
	if err := os.MkdirAll(filepath.Join(home, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	mhome.WriteMeta(home, "task-1", map[string]string{"window": "@missing-watch-run"})

	run := func() string {
		root := NewRootCommand()
		var out strings.Builder
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"watch", "run", "--output", "toon"})
		if err := root.Execute(); err != nil {
			t.Fatalf("watch run: %v\n%s", err, out.String())
		}
		return out.String()
	}

	first := run()
	if !strings.Contains(first, "wakes_emitted: 1") && !strings.Contains(first, "WakesEmitted") {
		// Accept either TOON snake_case or presence of success + non-zero emitted semantics via queue.
		if countQueuedWakes(home) != 1 {
			t.Fatalf("first run output=%q queue=%d, want one wake emitted", first, countQueuedWakes(home))
		}
	}
	if got := countQueuedWakes(home); got != 1 {
		t.Fatalf("queue after first run = %d, want 1", got)
	}

	captain := run()
	_ = captain
	if got := countQueuedWakes(home); got != 1 {
		t.Fatalf("queue after captain run = %d, want 1 (deduped unchanged condition)", got)
	}
}
