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
	if err := MakeUnreadable(t, child); err != nil {
		if IsUnsupportedFixture(err) {
			t.Errorf("access-control observation unavailable: %v", err)
			return
		}
		t.Fatal(err)
	}
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
	if err := MakeUnreadable(t, path); err != nil {
		if IsUnsupportedFixture(err) {
			t.Errorf("access-control observation unavailable: %v", err)
			return
		}
		t.Fatal(err)
	}
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
	if err := MakeReadOnly(t, path); err != nil {
		if IsUnsupportedFixture(err) {
			t.Errorf("access-control observation unavailable: %v", err)
			return
		}
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		f.Close()
		t.Fatal("read-only file remained writable")
	}
}

func TestMakeReadOnlyRefusesDirectoryCreate(t *testing.T) {
	dir := t.TempDir()
	if err := MakeReadOnly(t, dir); err != nil {
		if IsUnsupportedFixture(err) {
			t.Errorf("access-control observation unavailable: %v", err)
			return
		}
		t.Fatal(err)
	}
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
		if err := MakeUnreadable(t, file); err != nil {
			if IsUnsupportedFixture(err) {
				t.Errorf("access-control observation unavailable: %v", err)
				return
			}
			t.Fatal(err)
		}
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
		if err := MakeUnreadable(t, dirPath); err != nil {
			if IsUnsupportedFixture(err) {
				t.Errorf("access-control observation unavailable: %v", err)
				return
			}
			t.Fatal(err)
		}
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
		if err := MakeReadOnly(t, file); err != nil {
			if IsUnsupportedFixture(err) {
				t.Errorf("access-control observation unavailable: %v", err)
				return
			}
			t.Fatal(err)
		}
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
		if err := MakeReadOnly(t, dirPath); err != nil {
			if IsUnsupportedFixture(err) {
				t.Errorf("access-control observation unavailable: %v", err)
				return
			}
			t.Fatal(err)
		}
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
