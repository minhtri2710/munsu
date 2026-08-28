//go:build !windows

package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

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
