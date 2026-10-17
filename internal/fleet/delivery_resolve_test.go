//go:build integration

package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// TestResolveTaskHome locates the task by its current canonical record, and a
// handed-off task resolves to the captain home that holds it.
func TestResolveTaskHome(t *testing.T) {
	t.Run("primary task", func(t *testing.T) {
		home := t.TempDir()
		seedTaskInHome(t, home, "t1", taskauthority.KindShip)
		gotHome, err := ResolveTaskHome(home, "t1")
		if err != nil || gotHome != home {
			t.Fatalf("ResolveTaskHome = %q, %v; want primary %q", gotHome, err, home)
		}
	})
	t.Run("captain home task", func(t *testing.T) {
		parent := t.TempDir()
		seedTaskInHome(t, parent, "other", taskauthority.KindShip)
		capHome := filepath.Join(parent, "captains", "munsu")
		seedTaskInHome(t, capHome, "handed-ship", taskauthority.KindShip)
		gotHome, err := ResolveTaskHome(parent, "handed-ship")
		if err != nil || gotHome != capHome {
			t.Fatalf("ResolveTaskHome = %q, %v; want captain %q", gotHome, err, capHome)
		}
	})
	t.Run("handed-off task resolves to the captain over its source home", func(t *testing.T) {
		parent := t.TempDir()
		if _, err := mhome.Init(parent); err != nil {
			t.Fatal(err)
		}
		capHome := filepath.Join(parent, "captains", "test-sm")
		if _, err := mhome.Init(capHome); err != nil {
			t.Fatal(err)
		}
		if err := mhome.SeedCaptainProvenance(capHome, "test-sm"); err != nil {
			t.Fatal(err)
		}
		seedCanonicalQueuedTask(t, mustAuthority(t, parent), "TASK-1", "general")
		if err := Handoff(parent, capHome, []string{"TASK-1"}); err != nil {
			t.Fatalf("Handoff: %v", err)
		}
		gotHome, err := ResolveTaskHome(parent, "TASK-1")
		if err != nil || gotHome != capHome {
			t.Fatalf("ResolveTaskHome = %q, %v; want captain %q", gotHome, err, capHome)
		}
	})
	t.Run("a task without a canonical record is not found", func(t *testing.T) {
		parent := t.TempDir()
		seedTaskInHome(t, parent, "other", taskauthority.KindShip)
		_, err := ResolveTaskHome(parent, "missing")
		if err == nil || !strings.Contains(err.Error(), "task missing not found") || errors.Is(err, taskauthority.ErrNotFound) {
			t.Fatalf("ResolveTaskHome err = %v, want the clean not-found error", err)
		}
	})
}

// TestRequireShipTask_ReadsCanonicalKind pins the kind refusal: a canonical
// scout or review is refused, and a canonical ship in a captain home passes.
func TestRequireShipTask_ReadsCanonicalKind(t *testing.T) {
	t.Run("canonical scout is refused", func(t *testing.T) {
		home := t.TempDir()
		seedTaskInHome(t, home, "s1", taskauthority.KindScout)
		_, err := RequireShipTask(home, "s1")
		if err == nil || !strings.Contains(err.Error(), `kind="scout"`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("canonical review is refused", func(t *testing.T) {
		c, home := newFleetCanonical(t)
		mustWorkingShipTask(t, c, "ship-1")
		review := mustBindReviewTask(t, c, "ship-1", deliveryTestHead)
		_, err := RequireShipTask(home, review.TaskID)
		if err == nil || !strings.Contains(err.Error(), `kind="review"`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("canonical ship in a captain home passes", func(t *testing.T) {
		parent := t.TempDir()
		os.MkdirAll(filepath.Join(parent, "data"), 0755)
		capHome := filepath.Join(parent, "captains", "x")
		seedTaskInHome(t, capHome, "ship-1", taskauthority.KindShip)
		os.WriteFile(filepath.Join(parent, "data", "captains.md"),
			[]byte("- x - (home: "+capHome+"; scope: ; projects: ; added: 2026-07-20)\n"), 0644)

		th, err := RequireShipTask(parent, "ship-1")
		if err != nil {
			t.Fatal(err)
		}
		if th != capHome {
			t.Fatalf("th=%s", th)
		}
	})
}

// seedTaskInHome creates a queued task of the given kind in homeDir, which is
// initialized when it is not yet a home. A scout gets the scope and budget its
// definition requires.
func seedTaskInHome(t *testing.T, homeDir, taskID, kind string) *taskauthority.Canonical {
	t.Helper()
	h, err := mhome.Open(homeDir)
	if err != nil {
		if h, err = mhome.Init(homeDir); err != nil {
			t.Fatalf("mhome.Init(%s): %v", homeDir, err)
		}
	}
	c, err := taskauthority.NewCanonical(h)
	if err != nil {
		t.Fatalf("NewCanonical: %v", err)
	}
	req := taskauthority.CanonicalCreateRequest{
		HomeID: c.HomeID(), TaskID: mustFleetTaskID(t, taskID), Owner: "owner", Description: "work",
		Kind: kind, Reason: "create",
	}
	if kind == taskauthority.KindScout {
		req.ScoutScope = "investigate the requested question"
	}
	if _, err := c.Create(mustFleetOperation(t, "op-create-"+taskID, req), req); err != nil {
		t.Fatalf("Create(%s): %v", taskID, err)
	}
	return c
}
