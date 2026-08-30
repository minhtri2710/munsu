package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/fleet"
	mhome "github.com/minhtri2710/munsu/internal/home"
)

// deliveryLockScopeName derives the scope `fleet.RecoverDeliveryJournals` locks
// from the lock file it creates, rather than hard-coding the unexported
// fleet constant. A rename there renames the file, and this test keeps
// contending with the scope the gate actually takes instead of silently
// locking a scope nobody wants and passing for the wrong reason.
func deliveryLockScopeName(t *testing.T, homeDir string) string {
	t.Helper()
	if err := fleet.RecoverDeliveryJournals(homeDir); err != nil {
		t.Fatalf("priming the delivery recovery gate: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(homeDir, mhome.LockDirName))
	if err != nil {
		t.Fatal(err)
	}
	var scopes []string
	for _, e := range entries {
		// Scopes are the .lock files; the sibling .fence counter is not a scope.
		if !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		scopes = append(scopes, strings.TrimSuffix(e.Name(), ".lock"))
	}
	if len(scopes) != 1 {
		t.Fatalf("delivery recovery gate on a fresh home locked %v, want exactly one scope to contend with", scopes)
	}
	return scopes[0]
}

// watchCycleHome builds a home whose watcher cycle emits exactly one wake, so
// a test can tell "the cycle ran" from "the cycle never got there".
func watchCycleHome(t *testing.T) string {
	t.Helper()
	homeDir := t.TempDir()
	if _, err := mhome.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUNSU_HOME", homeDir)
	t.Setenv("MUNSU_GUARD_SKIP", "1")
	if err := os.MkdirAll(filepath.Join(homeDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	mhome.WriteMeta(homeDir, "task-1", map[string]string{"window": "@missing-watch-run"})
	return homeDir
}

// runWatchCycle runs one `watch run` and returns its error and its output.
func runWatchCycle(t *testing.T) (error, string) {
	t.Helper()
	root := NewRootCommand()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watch", "run", "--output", "toon"})
	return root.Execute(), out.String()
}

// TestWatchRun_ContinuesWhileDeliveryScopeIsHeld pins the cycle against the
// regression that non-blocking Home.Lock introduced: Deliver holds the
// delivery scope across a provider merge, which is two network round trips
// through a subprocess and routinely outlasts the 5s lock budget. Before this
// was classified, `watch run` returned that timeout as a hard error and the
// whole cycle -- every wake it would have emitted -- was lost for as long as
// somebody was merging.
func TestWatchRun_ContinuesWhileDeliveryScopeIsHeld(t *testing.T) {
	homeDir := watchCycleHome(t)
	scope := deliveryLockScopeName(t, homeDir)

	// Stand in for a Deliver that is mid-merge: the scope stays held for the
	// whole run, so the gate spends its entire budget and times out.
	h, err := mhome.Open(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	held, err := h.Lock(scope)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	err, out := runWatchCycle(t)
	if err != nil {
		t.Fatalf("watch run while the delivery scope is held: %v\n%s", err, out)
	}
	if countQueuedWakes(homeDir) != 1 {
		t.Fatalf("queue after the cycle = %d, want 1: the cycle ran but emitted nothing\n%s", countQueuedWakes(homeDir), out)
	}
	if !strings.Contains(out, "delivery recovery gate skipped") {
		t.Errorf("the skipped gate was not reported, so an operator reading logs cannot tell the cycle ran degraded:\n%s", out)
	}
}

// TestWatchRun_FailsWhenDeliveryGateFailsForAnyOtherReason holds the second
// direction of that classification. Only a held scope may be skipped: a
// delivery recovery gate that fails for any other reason must still stop the
// cycle, because the gate exists to converge an interrupted delivery before
// anything reads the state it was mid-way through changing.
//
// Without this, widening the classification -- folding in another sentinel, or
// slipping to a bare `err != nil` -- turns the gate fail-open and `watch run`
// reports success over a broken delivery journal. That mutation survives every
// other test in this package.
func TestWatchRun_FailsWhenDeliveryGateFailsForAnyOtherReason(t *testing.T) {
	homeDir := watchCycleHome(t)
	scope := deliveryLockScopeName(t, homeDir)

	// Break the gate itself, not the home: the scope's lock file becomes a
	// directory, so the gate cannot open it and fails before it ever contends.
	// Nothing else in the cycle reads this path, so a cycle that runs anyway
	// proves the classification let a real failure through.
	lockPath := filepath.Join(homeDir, mhome.LockDirName, scope+".lock")
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0700); err != nil {
		t.Fatal(err)
	}

	err, out := runWatchCycle(t)
	if err == nil {
		t.Fatalf("watch run returned success over a delivery recovery gate that failed:\n%s", out)
	}
	if errors.Is(err, mhome.ErrLockTimeout) {
		t.Fatalf("the fixture produced a lock timeout, so this case is testing the branch it means to exclude: %v", err)
	}
	if countQueuedWakes(homeDir) != 0 {
		t.Errorf("the cycle emitted %d wake(s) past a failed recovery gate, want 0", countQueuedWakes(homeDir))
	}
	if strings.Contains(out, "delivery recovery gate skipped") {
		t.Errorf("a real gate failure was reported as a skipped gate:\n%s", out)
	}
}
