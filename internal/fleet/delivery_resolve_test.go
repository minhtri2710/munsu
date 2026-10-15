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

func TestResolveTaskHome_Primary(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	if err := mhome.WriteMeta(home, "t1", map[string]string{"kind": "ship", "project": "munsu"}); err != nil {
		t.Fatal(err)
	}
	gotHome, meta, err := ResolveTaskHome(home, "t1")
	if err != nil {
		t.Fatalf("ResolveTaskHome: %v", err)
	}
	if gotHome != home {
		t.Fatalf("home = %q, want primary %q", gotHome, home)
	}
	if meta["kind"] != "ship" {
		t.Fatalf("kind = %q", meta["kind"])
	}
}

func TestResolveTaskHome_CaptainHome(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "data"), 0755)
	os.MkdirAll(filepath.Join(parent, "state"), 0755)

	capHome := filepath.Join(parent, "captains", "munsu")
	os.MkdirAll(filepath.Join(capHome, "state"), 0755)
	if err := mhome.WriteMeta(capHome, "handed-ship", map[string]string{
		"kind":    "ship",
		"project": "munsu",
	}); err != nil {
		t.Fatal(err)
	}

	reg := "# Captains\n\n- munsu - (home: " + capHome + "; scope: ; projects: ; added: 2026-07-20)\n"
	if err := os.WriteFile(filepath.Join(parent, "data", "captains.md"), []byte(reg), 0644); err != nil {
		t.Fatal(err)
	}

	// No meta on parent — must find captain
	if _, err := os.Stat(filepath.Join(parent, "state", "handed-ship.meta")); !os.IsNotExist(err) {
		t.Fatalf("parent should not have meta: %v", err)
	}

	gotHome, meta, err := ResolveTaskHome(parent, "handed-ship")
	if err != nil {
		t.Fatalf("ResolveTaskHome: %v", err)
	}
	if gotHome != capHome {
		t.Fatalf("home = %q, want captain %q", gotHome, capHome)
	}
	if meta["kind"] != "ship" || meta["project"] != "munsu" {
		t.Fatalf("meta = %#v", meta)
	}
}

func TestResolveTaskHome_NotFound(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "data"), 0755)
	os.WriteFile(filepath.Join(parent, "data", "captains.md"), []byte("# Captains\n"), 0644)

	// ReadMeta wraps the open's *os.PathError; errors.Is must still see the
	// not-exist cause so the clean not-found branch is taken, not the
	// wrapping one.
	_, readErr := mhome.ReadMeta(parent, "missing")
	var pe *os.PathError
	if !errors.As(readErr, &pe) || pe == readErr || !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("precondition: ReadMeta error = %#v, want a wrapped not-exist *os.PathError", readErr)
	}

	_, _, err := ResolveTaskHome(parent, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v", err)
	}
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "reading task meta") {
		t.Fatalf("not-exist took the wrapping branch: %v", err)
	}
}

// TestRequireShipMeta_ReadsCanonicalKind pins the kind refusal: the kind is the
// canonical task's, never the .meta's. The blocking row holds a .meta that
// claims ship over a canonical scout; the allowing row holds a .meta without a
// kind over a canonical ship in a captain home.
func TestRequireShipMeta_ReadsCanonicalKind(t *testing.T) {
	t.Run("canonical scout is refused although meta says ship", func(t *testing.T) {
		home := t.TempDir()
		seedTaskInHome(t, home, "s1", taskauthority.KindScout)
		mhome.WriteMeta(home, "s1", map[string]string{"kind": "ship"})
		_, _, err := RequireShipMeta(home, "s1")
		if err == nil || !strings.Contains(err.Error(), `kind="scout"`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("canonical ship in a captain home passes without a meta kind", func(t *testing.T) {
		parent := t.TempDir()
		os.MkdirAll(filepath.Join(parent, "data"), 0755)
		capHome := filepath.Join(parent, "captains", "x")
		seedTaskInHome(t, capHome, "ship-1", taskauthority.KindShip)
		mhome.WriteMeta(capHome, "ship-1", map[string]string{"window": "w"})
		os.WriteFile(filepath.Join(parent, "data", "captains.md"),
			[]byte("- x - (home: "+capHome+"; scope: ; projects: ; added: 2026-07-20)\n"), 0644)

		th, _, err := RequireShipMeta(parent, "ship-1")
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
