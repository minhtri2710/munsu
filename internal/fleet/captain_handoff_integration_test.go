//go:build integration

package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// TestHandoffOrderedInvariantAcrossEveryStage proves the ADR-0008 ordered
// invariant through the production entry points: durable intent -> source
// Reserve -> destination Receive (NON-CURRENT) -> scoped verification ->
// source Commit/supersede -> destination Activate. A crash at every durable
// stage is recovered by RecoverTaskHandoffs, and no successful state ever
// exposes both homes as current owners.
func TestHandoffOrderedInvariantAcrossEveryStage(t *testing.T) {
	for _, boundary := range []string{"journal", "reserved", "received", "committed", "activated"} {
		t.Run(boundary, func(t *testing.T) {
			if os.Getenv("MUNSU_HANDOFF_CRASH_HELPER") == "1" {
				crashBoundary := os.Getenv("MUNSU_HANDOFF_CRASH_AFTER")
				handoffCrashHook = func(got string) {
					if got == crashBoundary {
						os.Exit(92)
					}
				}
				if err := Handoff(os.Getenv("MUNSU_HANDOFF_SOURCE"), os.Getenv("MUNSU_HANDOFF_DEST"), []string{"TASK-1", "TASK-2"}); err == nil {
					os.Exit(0)
				}
				os.Exit(91)
			}

			parent := t.TempDir()
			if _, err := home.Init(parent); err != nil {
				t.Fatal(err)
			}
			captain := filepath.Join(parent, "captains", "test-sm")
			if _, err := home.Init(captain); err != nil {
				t.Fatal(err)
			}
			if err := SeedProvenance(captain, "test-sm"); err != nil {
				t.Fatal(err)
			}
			parentAuth := mustAuthority(t, parent)
			seedCanonicalQueuedTask(t, parentAuth, "TASK-1", "general")
			seedCanonicalQueuedTask(t, parentAuth, "TASK-2", "general")

			cmd := exec.Command(os.Args[0], "-test.run", "^TestHandoffOrderedInvariantAcrossEveryStage$", "--")
			cmd.Env = append(os.Environ(),
				"MUNSU_HANDOFF_CRASH_HELPER=1",
				"MUNSU_HANDOFF_CRASH_AFTER="+boundary,
				"MUNSU_HANDOFF_SOURCE="+parent,
				"MUNSU_HANDOFF_DEST="+captain,
			)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("expected helper subprocess to crash at %s", boundary)
			} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 92 {
				t.Fatalf("helper exited at boundary %s: %v\n%s", boundary, err, output)
			}

			if err := RecoverTaskHandoffs(parent); err != nil {
				t.Fatalf("boundary %s: RecoverTaskHandoffs: %v", boundary, err)
			}

			for _, taskID := range []string{"TASK-1", "TASK-2"} {
				mustTransferNoOwner(t, parent, taskID)
				agg := mustTransferOwner(t, captain, taskID)
				if agg.Generation != 1 || agg.Definition.Owner != "captain:test-sm" {
					t.Fatalf("boundary %s: %s aggregate = %+v", boundary, taskID, agg)
				}
			}
			if pendingJournalCount(t, parent) != 0 {
				t.Fatalf("boundary %s: journal not removed after recovery", boundary)
			}
		})
	}
}
