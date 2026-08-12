package home

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Tests for the Home.ReadDir enumeration seam (A-02). ReadDir must resolve its
// path through the same contained, no-follow seam as Read, so enumeration
// cannot bypass the home security boundary.

func TestReadDirEnumeration(t *testing.T) {
	h := newTestHome(t)

	// Create a subdirectory with two files under the state root.
	p, err := h.Path(RootState, "sub")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "a.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "b.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	entries, err := h.ReadDir(RootState, "sub")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Name()] = true
	}
	if !seen["a.json"] || !seen["b.txt"] {
		t.Errorf("ReadDir entries = %v, want both a.json and b.txt", seen)
	}
}

func TestReadDirMissingDirReportsNotExist(t *testing.T) {
	h := newTestHome(t)
	if _, err := h.ReadDir(RootState, "no/such/dir"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadDir missing dir err = %v, want os.ErrNotExist", err)
	}
}

func TestReadDirNonDirectoryTargetErrors(t *testing.T) {
	h := newTestHome(t)
	// A file at the target path is not a directory.
	p, err := h.Path(RootState, "file")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ReadDir(RootState, "file"); err == nil {
		t.Fatalf("ReadDir on a regular file = nil error, want failure")
	}
}

func TestReadDirSymlinkAncestorEscapesRootFailsClosed(t *testing.T) {
	h := newTestHome(t)
	outside := t.TempDir()

	// state/link -> outside. The task dir resolved under link escapes the home.
	linkPath, err := h.Path(RootState, "link")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatal(err)
	}

	if _, err := h.ReadDir(RootState, "link/taskdir"); !errors.Is(err, ErrSymlinkEscapes) {
		t.Fatalf("ReadDir through escaping symlink err = %v, want ErrSymlinkEscapes", err)
	}
}

func TestReadDirEscapingKeyRejected(t *testing.T) {
	h := newTestHome(t)
	if _, err := h.ReadDir(RootState, "../escape"); !errors.Is(err, ErrKeyEscapes) {
		t.Fatalf("ReadDir escaping key err = %v, want ErrKeyEscapes", err)
	}
	if _, err := h.ReadDir(RootState, ""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("ReadDir empty key err = %v, want ErrEmptyKey", err)
	}
}

func TestReadDirUnknownRootRejected(t *testing.T) {
	h := newTestHome(t)
	if _, err := h.ReadDir("not-a-root", "x"); !errors.Is(err, ErrUnknownRoot) {
		t.Fatalf("ReadDir unknown root err = %v, want ErrUnknownRoot", err)
	}
}
