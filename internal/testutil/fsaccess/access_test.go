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
	if !t.Run("unreadable file", func(t *testing.T) {
		MakeUnreadable(t, file)
		if _, err := os.Open(file); err == nil {
			t.Fatal("file remained readable")
		}
	}) {
		t.Fatal("nested unreadable file test failed")
	}
	if f, err := os.Open(file); err != nil {
		t.Fatalf("file was not restored: %v", err)
	} else {
		f.Close()
	}

	dirPath := filepath.Join(dir, "unreadable-dir")
	if err := os.Mkdir(dirPath, 0700); err != nil {
		t.Fatal(err)
	}
	if !t.Run("unreadable directory", func(t *testing.T) {
		MakeUnreadable(t, dirPath)
		if _, err := os.ReadDir(dirPath); err == nil {
			t.Fatal("directory remained readable")
		}
	}) {
		t.Fatal("nested unreadable directory test failed")
	}
	if _, err := os.ReadDir(dirPath); err != nil {
		t.Fatalf("directory was not restored: %v", err)
	}

	if !t.Run("read-only file", func(t *testing.T) {
		MakeReadOnly(t, file)
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
		if err == nil {
			f.Close()
			t.Fatal("read-only file remained writable")
		}
	}) {
		t.Fatal("nested read-only file test failed")
	}
	if f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0); err != nil {
		t.Fatalf("file write access was not restored: %v", err)
	} else {
		f.Close()
	}

	if !t.Run("read-only directory", func(t *testing.T) {
		MakeReadOnly(t, dirPath)
		if err := os.Mkdir(filepath.Join(dirPath, "child"), 0700); err == nil {
			t.Fatal("read-only directory remained writable")
		}
	}) {
		t.Fatal("nested read-only directory test failed")
	}
	if err := os.Mkdir(filepath.Join(dirPath, "child"), 0700); err != nil {
		t.Fatalf("directory write access was not restored: %v", err)
	}
}
