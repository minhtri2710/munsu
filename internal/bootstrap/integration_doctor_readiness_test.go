package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

// TestCheckProjectReadiness pins the General doctor's per-project readiness:
// no configured project reports current, and a configured project that cannot
// resolve is reported stale rather than passing silently.
func TestCheckProjectReadiness(t *testing.T) {
	t.Run("no projects", func(t *testing.T) {
		entries := checkProjectReadiness(t.TempDir())
		if len(entries) != 1 || entries[0].Status != StatusCurrent || !strings.Contains(entries[0].Detail, "no projects configured") {
			t.Fatalf("entries = %+v, want one current no-projects entry", entries)
		}
	})

	t.Run("configured project that cannot resolve", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := config.StoreProjectOverlay(home, "ghost", config.ProjectOverlay{}); err != nil {
			t.Fatal(err)
		}
		entries := checkProjectReadiness(home)
		if len(entries) != 1 || entries[0].Subsystem != "project ghost" || entries[0].Status != StatusStale {
			t.Fatalf("entries = %+v, want one stale entry for project ghost", entries)
		}
	})
}
