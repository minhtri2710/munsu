//go:build windows

package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

func TestRetireClearsEncodedCaptainParentMeta(t *testing.T) {
	parent := t.TempDir()
	if _, err := home.Init(parent); err != nil {
		t.Fatal(err)
	}
	captainHome := filepath.Join(parent, "captains", "encoded")
	if err := os.MkdirAll(filepath.Join(captainHome, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := SeedProvenance(captainHome, "encoded"); err != nil {
		t.Fatal(err)
	}
	writeCaptainMeta(t, parent, "encoded", captainHome, "window")

	taskID := taskIDForCaptain("encoded")
	metaPath, err := home.MetaFilePath(parent, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("encoded captain meta %q missing before retire: %v", metaPath, err)
	}

	endpoint := &testRetireEndpoint{}
	if err := Retire(captainHome, parent, false, false, endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint.calls != 1 {
		t.Fatalf("endpoint calls=%d, want 1", endpoint.calls)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatalf("encoded captain meta %q still present after retire", metaPath)
	}
	if _, err := home.ReadMeta(parent, taskID); err == nil {
		t.Fatalf("captain %s still resolvable through the durable meta surface after retire", taskID)
	}
}
