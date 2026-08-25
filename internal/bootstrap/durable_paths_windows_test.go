//go:build windows

package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestGCOrphanDataDirsKeepsEncodedLiveTask(t *testing.T) {
	homeDir := t.TempDir()
	id := "rel.1"
	dataDir := filepath.Join(homeDir, "data", id)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := home.Init(homeDir); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(homeDir, id, map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dataDir, old, old); err != nil {
		t.Fatalf("Chtimes on data dir: %v", err)
	}

	cleaned := gcOrphanDataDirs(homeDir, reclaimNone)
	for _, c := range cleaned {
		if c == id {
			t.Fatalf("live task data dir %q was GC'd", id)
		}
	}
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		t.Fatalf("live task data dir %q was removed", id)
	}
}
