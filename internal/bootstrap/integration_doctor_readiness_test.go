package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
)

// TestCheckProjectReadiness pins the General doctor's per-project readiness:
// projects come from the Fleet registry, not from overlays.
func TestCheckProjectReadiness(t *testing.T) {
	t.Run("no projects", func(t *testing.T) {
		entries := checkProjectReadiness(t.TempDir())
		if len(entries) != 1 || entries[0].Status != StatusCurrent || !strings.Contains(entries[0].Detail, "no projects configured") {
			t.Fatalf("entries = %+v, want one current no-projects entry", entries)
		}
	})

	t.Run("overlay without a registered project is not probed", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := config.StoreProjectOverlay(home, "ghost", config.ProjectOverlay{}); err != nil {
			t.Fatal(err)
		}
		entries := checkProjectReadiness(home)
		if len(entries) != 1 || entries[0].Status != StatusCurrent || !strings.Contains(entries[0].Detail, "no projects configured") {
			t.Fatalf("entries = %+v, want one current no-projects entry", entries)
		}
	})

	t.Run("registered project without an overlay is probed", func(t *testing.T) {
		home := t.TempDir()
		if err := fleet.Add(home, "beta", t.TempDir(), true); err != nil {
			t.Fatal(err)
		}
		if err := config.StoreProjectOverlay(home, "ghost", config.ProjectOverlay{}); err != nil {
			t.Fatal(err)
		}
		var beta, ghost bool
		for _, e := range checkProjectReadiness(home) {
			beta = beta || e.Subsystem == "project beta"
			ghost = ghost || e.Subsystem == "project ghost"
		}
		if !beta || ghost {
			t.Fatalf("beta probed = %v, ghost probed = %v; want beta probed and ghost not", beta, ghost)
		}
	})
}
