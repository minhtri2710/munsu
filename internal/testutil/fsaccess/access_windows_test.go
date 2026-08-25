//go:build windows

package fsaccess

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

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
		assertEveryoneAllowACE(t, dir)
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

func assertEveryoneAllowACE(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("read applied DACL: %v", err)
	}
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), everyone) && ace.Mask == 0x001F01FF {
			found = true
		}
	}
	if !found {
		t.Fatal("applied DACL lacks Everyone full-access allow ACE")
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
