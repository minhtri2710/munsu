//go:build !windows

package fsaccess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateAssertionsUnix(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	AssertPrivateFile(t, file)
	AssertPrivateDir(t, dir)
}
