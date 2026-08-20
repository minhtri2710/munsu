//go:build windows

package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsMetaStatusFilePathsResolveDurableKey(t *testing.T) {
	homeDir := t.TempDir()
	for _, id := range []string{"rel.1", "captain:sm-1", "plain"} {
		stem, err := DurableKey(id)
		if err != nil {
			t.Fatalf("DurableKey(%q): %v", id, err)
		}
		mp, err := MetaFilePath(homeDir, id)
		if err != nil {
			t.Fatalf("MetaFilePath(%q): %v", id, err)
		}
		if filepath.Base(mp) != stem+".meta" {
			t.Errorf("MetaFilePath(%q) = %q, want stem %q.meta", id, mp, stem)
		}
		sp, err := StatusFilePath(homeDir, id)
		if err != nil {
			t.Fatalf("StatusFilePath(%q): %v", id, err)
		}
		if filepath.Base(sp) != stem+".status" {
			t.Errorf("StatusFilePath(%q) = %q, want stem %q.status", id, sp, stem)
		}
	}
}

func TestWindowsWriteReadAgreeOnEncodedPath(t *testing.T) {
	homeDir := t.TempDir()
	if _, err := Init(homeDir); err != nil {
		t.Fatal(err)
	}
	id := "rel.1"
	if err := WriteMeta(homeDir, id, map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendStatus(homeDir, id, "working: seeded"); err != nil {
		t.Fatal(err)
	}
	sp, err := StatusFilePath(homeDir, id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sp)
	if err != nil {
		t.Fatalf("reading status at %q: %v", sp, err)
	}
	if got := string(data); got != "working: seeded\n" {
		t.Fatalf("status at %q = %q, want %q", sp, got, "working: seeded\n")
	}
}
