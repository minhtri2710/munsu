package cli

import (
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
		scopes = append(scopes, strings.TrimSuffix(e.Name(), ".lock"))
	}
	if len(scopes) != 1 {
		t.Fatalf("delivery recovery gate on a fresh home locked %v, want exactly one scope to contend with", scopes)
	}
	return scopes[0]
}

// TestWatchRun_ContinuesWhileDeliveryScopeIsHeld pins the cycle against the
// regression that non-blocking Home.Lock introduced: Deliver holds the
// delivery scope across a provider merge, which is two network round trips
// through a subprocess and routinely outlasts the 5s lock budget. Before this
// was classified, `watch run` returned that timeout as a hard error and the
// whole cycle -- every wake it would have emitted -- was lost for as long as
// somebody was merging.
func TestWatchRun_ContinuesWhileDeliveryScopeIsHeld(t *testing.T) {
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

	root := NewRootCommand()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"watch", "run", "--output", "toon"})
	if err := root.Execute(); err != nil {
		t.Fatalf("watch run while the delivery scope is held: %v\n%s", err, out.String())
	}
	if countQueuedWakes(homeDir) != 1 {
		t.Fatalf("queue after the cycle = %d, want 1: the cycle ran but emitted nothing\n%s", countQueuedWakes(homeDir), out.String())
	}
	if !strings.Contains(out.String(), "delivery recovery gate skipped") {
		t.Errorf("the skipped gate was not reported, so an operator reading logs cannot tell the cycle ran degraded:\n%s", out.String())
	}
}
