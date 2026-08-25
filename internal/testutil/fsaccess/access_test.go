package fsaccess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakeUnreadableRestoresDirectoryAccess(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	MakeUnreadable(t, child)
	if _, err := os.ReadDir(child); err == nil {
		t.Fatal("unreadable directory remained readable")
	}
}

func TestMakeUnreadableRestoresFileAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	MakeUnreadable(t, path)
	if _, err := os.Open(path); err == nil {
		t.Fatal("unreadable file remained readable")
	}
}

func TestMakeReadOnlyRefusesWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	MakeReadOnly(t, path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		f.Close()
		t.Fatal("read-only file remained writable")
	}
}

func TestMakeReadOnlyRefusesDirectoryCreate(t *testing.T) {
	dir := t.TempDir()
	MakeReadOnly(t, dir)
	probe := filepath.Join(dir, "child")
	if err := os.Mkdir(probe, 0700); err == nil {
		t.Fatal("read-only directory remained writable")
	}
}

func TestAccessRestoresAfterNestedTest(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if !t.Run("unreadable", func(t *testing.T) {
		MakeUnreadable(t, file)
		if _, err := os.Open(file); err == nil {
			t.Fatal("file remained readable")
		}
	}) {
		t.Fatal("nested unreadable test failed")
	}
	if f, err := os.Open(file); err != nil {
		t.Fatalf("file was not restored: %v", err)
	} else {
		f.Close()
	}
}
