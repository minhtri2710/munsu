//go:build windows

package fsaccess

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMakeReadOnlyPreservesNullDACLAccess(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, []byte("marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, nil, nil,
	); err != nil {
		t.Fatalf("set NULL DACL: %v", err)
	}
	assertNullDACL(t, dir)

	if !t.Run("read-only", func(t *testing.T) {
		MakeReadOnly(t, dir)
		if _, err := os.ReadDir(dir); err != nil {
			t.Fatalf("directory listing failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "probe"), []byte("probe"), 0600); err == nil {
			t.Fatal("read-only directory remained writable")
		}
	}) {
		t.Fatal("MakeReadOnly failed")
	}

	assertNullDACL(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "restored"), []byte("restored"), 0600); err != nil {
		t.Fatalf("NULL-DACL write access was not restored: %v", err)
	}
}

func assertNullDACL(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("DACL is not protected")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl != nil {
		t.Fatal("DACL is present, want NULL DACL")
	}
}
