//go:build windows

package fsaccess

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestACLStateRestoresAbsentDACL(t *testing.T) {
	dir := t.TempDir()
	absolute, err := windows.NewSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if err := absolute.SetDACL(nil, false, false); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.WRITE_DAC, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetKernelObjectSecurity(h, windows.DACL_SECURITY_INFORMATION, absolute); err != nil {
		windows.CloseHandle(h)
		t.Fatalf("remove DACL: %v", err)
	}
	windows.CloseHandle(h)
	state := captureACL(t, dir)
	if !state.absentDACL {
		t.Fatal("captured DACL is not absent")
	}
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	entry := windows.EXPLICIT_ACCESS{AccessPermissions: windows.ACCESS_MASK(0x001F01FF), AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE}
	entry.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	entry.Trustee.TrusteeType = windows.TRUSTEE_IS_WELL_KNOWN_GROUP
	entry.Trustee.TrusteeValue = windows.TrusteeValueFromSID(everyone)
	allow, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, nil)
	if err != nil {
		t.Fatalf("build temporary DACL: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, allow, nil); err != nil {
		t.Fatalf("set temporary DACL: %v", err)
	}
	if err := state.restore(dir); err != nil {
		t.Fatalf("restore absent DACL: %v", err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sd.DACL(); !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		t.Fatalf("restored DACL error = %v, want ERROR_OBJECT_NOT_FOUND", err)
	}
}

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
		if err := MakeReadOnly(t, dir); err != nil {
			t.Fatal(err)
		}
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
