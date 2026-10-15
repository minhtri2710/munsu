package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakePathUnreadable_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("secret content"), 0600); err != nil {
		t.Fatal(err)
	}

	MakePathUnreadable(t, path)

	if _, err := os.ReadFile(path); err == nil {
		t.Fatal("ReadFile succeeded on unreadable file")
	}
}

func TestMakePathUnreadable_Directory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "item.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	MakePathUnreadable(t, sub)

	if _, err := os.ReadDir(sub); err == nil {
		t.Fatal("ReadDir succeeded on unreadable directory")
	}
}

func TestMakeDirectoryReadOnly(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	MakeDirectoryReadOnly(t, sub)

	probe := filepath.Join(sub, "file.txt")
	if err := os.WriteFile(probe, []byte("data"), 0644); err == nil {
		t.Fatal("WriteFile succeeded in read-only directory")
	}
}

func TestAssertOwnerPrivate_Unix(t *testing.T) {
	dir := t.TempDir()

	// 0700 dir passes
	privateDir := filepath.Join(dir, "private-dir")
	if err := os.Mkdir(privateDir, 0700); err != nil {
		t.Fatal(err)
	}
	AssertOwnerPrivate(t, privateDir)

	// 0600 file passes
	privateFile := filepath.Join(privateDir, "private.txt")
	if err := os.WriteFile(privateFile, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	AssertOwnerPrivate(t, privateFile)
}

func TestAssertOwnerReadOnly_Unix(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "ro-dir")
	if err := os.Mkdir(roDir, 0500); err != nil {
		t.Fatal(err)
	}
	AssertOwnerReadOnly(t, roDir)
}
