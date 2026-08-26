//go:build windows

package fsaccess

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestApplyDeniedAccessRejectsNullDACL(t *testing.T) {
	dir := t.TempDir()
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, nil, nil); err != nil {
		t.Fatalf("set NULL DACL: %v", err)
	}
	assertNullDACL(t, dir)
	if err := applyDeniedAccess(dir, windows.FILE_WRITE_DATA); err == nil {
		t.Fatal("applyDeniedAccess accepted a NULL DACL")
	}
	assertNullDACL(t, dir)
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, []byte("probe"), 0600); err != nil {
		t.Fatalf("NULL-DACL write access changed: %v", err)
	}
}

func TestMakeReadOnlyDeniesExistingChildDeletion(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	childDir := filepath.Join(dir, "child-dir")
	if err := os.WriteFile(file, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(childDir, 0700); err != nil {
		t.Fatal(err)
	}
	if !t.Run("read-only", func(t *testing.T) {
		MakeReadOnly(t, dir)
		if err := os.Remove(file); err == nil {
			t.Fatal("read-only directory allowed deleting child file")
		}
		if err := os.Remove(childDir); err == nil {
			t.Fatal("read-only directory allowed deleting child directory")
		}
	}) {
		t.Fatal("MakeReadOnly failed")
	}
	if err := os.Remove(file); err != nil {
		t.Fatalf("child file deletion was not restored: %v", err)
	}
	if err := os.Remove(childDir); err != nil {
		t.Fatalf("child directory deletion was not restored: %v", err)
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
