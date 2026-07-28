package delivery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mhome "github.com/minhtri2710/munsu/internal/home"
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
		"pr":      "https://github.com/o/r/pull/1",
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
	if meta["kind"] != "ship" || meta["pr"] == "" {
		t.Fatalf("meta = %#v", meta)
	}
}

func TestResolveTaskHome_NotFound(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "data"), 0755)
	os.WriteFile(filepath.Join(parent, "data", "captains.md"), []byte("# Captains\n"), 0644)

	_, _, err := ResolveTaskHome(parent, "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestRequireShipMeta_RejectsScout(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "state"), 0755)
	mhome.WriteMeta(home, "s1", map[string]string{"kind": "scout"})
	_, _, err := RequireShipMeta(home, "s1")
	if err == nil || !strings.Contains(err.Error(), `kind="scout"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRequireShipMeta_CaptainShip(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "data"), 0755)
	capHome := filepath.Join(parent, "captains", "x")
	os.MkdirAll(filepath.Join(capHome, "state"), 0755)
	mhome.WriteMeta(capHome, "ship-1", map[string]string{"kind": "ship"})
	os.WriteFile(filepath.Join(parent, "data", "captains.md"),
		[]byte("- x - (home: "+capHome+"; scope: ; projects: ; added: 2026-07-20)\n"), 0644)

	th, meta, err := RequireShipMeta(parent, "ship-1")
	if err != nil {
		t.Fatal(err)
	}
	if th != capHome || meta["kind"] != "ship" {
		t.Fatalf("th=%s meta=%v", th, meta)
	}
}
