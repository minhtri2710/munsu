//go:build windows

package fsaccess

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const unreadableAccess = windows.FILE_GENERIC_READ
const readOnlyAccess = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | windows.DELETE | 0x40

// MakeUnreadable adds an explicit deny ACE for the current user while retaining
// WRITE_DAC, verifies that reading/listing is refused, and restores the exact
// original DACL at test cleanup.
func MakeUnreadable(t *testing.T, path string) {
	t.Helper()
	state := captureACL(t, path)
	if err := applyDeniedAccess(path, unreadableAccess); err != nil {
		t.Fatalf("make path unreadable %q: %v", path, err)
	}
	restore := registerRestore(t, func() error { return state.restore(path) })
	if err := verifyUnreadable(path, state.isDir); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			t.Fatalf("unreadable path %q was still readable: %v; restore failed: %v", path, err, restoreErr)
		}
		t.Fatalf("unreadable path %q was still readable: %v", path, err)
	}
}

// MakeReadOnly denies writes/deletes for the current user while retaining
// WRITE_DAC, verifies that a write operation is refused, and restores the exact
// original DACL at test cleanup.
func MakeReadOnly(t *testing.T, path string) {
	t.Helper()
	state := captureACL(t, path)
	if err := applyDeniedAccess(path, readOnlyAccess); err != nil {
		t.Fatalf("make path read-only %q: %v", path, err)
	}
	restore := registerRestore(t, func() error { return state.restore(path) })
	if state.isDir {
		entries, err := os.ReadDir(path)
		if err != nil {
			if restoreErr := restore(); restoreErr != nil {
				t.Fatalf("read children of read-only path %q: %v; restore failed: %v", path, err, restoreErr)
			}
			t.Fatalf("read children of read-only path %q: %v", path, err)
		}
		for _, entry := range entries {
			child := path + `\\` + entry.Name()
			childState := captureACL(t, child)
			if err := applyDeniedAccess(child, windows.DELETE); err != nil {
				t.Fatalf("deny child deletion %q: %v", child, err)
			}
			childStateCopy := childState
			childRestore := registerRestore(t, func() error { return childStateCopy.restore(child) })
			if err := verifyDeleteDenied(child); err != nil {
				restoreErr := childRestore()
				if restoreErr != nil {
					t.Fatalf("read-only child %q remained deletable: %v; restore failed: %v", child, err, restoreErr)
				}
				t.Fatalf("read-only child %q remained deletable: %v", child, err)
			}
		}
	}
	if err := verifyReadOnly(path, state.isDir); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			t.Fatalf("read-only path %q was still writable: %v; restore failed: %v", path, err, restoreErr)
		}
		t.Fatalf("read-only path %q was still writable: %v", path, err)
	}
}

func AssertPrivateFile(t *testing.T, path string) {
	t.Helper()
	assertOwnerOnly(t, path, false)
}

func AssertPrivateDir(t *testing.T, path string) {
	t.Helper()
	assertOwnerOnly(t, path, true)
}

type aclState struct {
	sddl      string
	protected bool
	isDir     bool
}

func captureACL(t *testing.T, path string) aclState {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat access-controlled path %q: %v", path, err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read DACL for %q: %v", path, err)
	}
	if sd == nil {
		t.Fatalf("path %q has no security descriptor", path)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("read DACL control for %q: %v", path, err)
	}
	return aclState{sddl: sd.String(), protected: control&windows.SE_DACL_PROTECTED != 0, isDir: info.IsDir()}
}

func (s aclState) restore(path string) error {
	sd, err := windows.SecurityDescriptorFromString(s.sddl)
	if err != nil {
		return fmt.Errorf("parse original DACL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil && !errorsIsObjectNotFound(err) {
		return fmt.Errorf("read original DACL: %w", err)
	}
	securityInfo := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if s.protected {
		securityInfo |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		securityInfo |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInfo, nil, nil, dacl, nil)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

func applyDeniedAccess(path string, access uint32) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read DACL for %q: %w", path, err)
	}
	if sd == nil {
		return fmt.Errorf("path %q has no security descriptor", path)
	}
	oldDACL, _, err := sd.DACL()
	if errorsIsObjectNotFound(err) {
		return fmt.Errorf("path %q has no DACL to restrict", path)
	}
	if err != nil {
		return fmt.Errorf("read DACL for %q: %w", path, err)
	}
	if oldDACL == nil {
		return fmt.Errorf("path %q has a present NULL DACL; selective access denial is unsupported", path)
	}
	sid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("current user SID: %w", err)
	}
	entry := windows.EXPLICIT_ACCESS{AccessPermissions: windows.ACCESS_MASK(access), AccessMode: windows.DENY_ACCESS, Inheritance: windows.NO_INHERITANCE}
	entry.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	entry.Trustee.TrusteeType = windows.TRUSTEE_IS_USER
	entry.Trustee.TrusteeValue = windows.TrusteeValueFromSID(sid)
	var pinner runtime.Pinner
	pinner.Pin(sid)
	newDACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, oldDACL)
	pinner.Unpin()
	if err != nil {
		return fmt.Errorf("build denied DACL for %q: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, newDACL, nil); err != nil {
		return fmt.Errorf("set denied DACL for %q: %w", path, err)
	}
	return nil
}

func verifyDeleteDenied(path string) error {
	access := uint32(windows.DELETE)
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err == nil {
		windows.CloseHandle(h)
		return fmt.Errorf("delete handle opened")
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("delete open failed without access denial: %w", err)
	}
	return nil
}

func currentUserSID() (*windows.SID, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return tu.User.Sid.Copy()
}

func verifyUnreadable(path string, isDir bool) error {
	if isDir {
		_, err := os.ReadDir(path)
		if err == nil {
			return fmt.Errorf("directory listing succeeded")
		}
		return nil
	}
	f, err := os.Open(path)
	if err == nil {
		f.Close()
		return fmt.Errorf("file open succeeded")
	}
	return nil
}

func verifyReadOnly(path string, isDir bool) error {
	if isDir {
		if _, err := os.ReadDir(path); err != nil {
			return fmt.Errorf("directory listing failed: %w", err)
		}
		probe := path + `\fsaccess-write-probe`
		f, err := os.Create(probe)
		if err == nil {
			f.Close()
			_ = os.Remove(probe)
			return fmt.Errorf("directory create succeeded")
		}
		return nil
	}
	if f, err := os.Open(path); err != nil {
		return fmt.Errorf("file open for read failed: %w", err)
	} else {
		f.Close()
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		f.Close()
		return fmt.Errorf("file open for write succeeded")
	}
	return nil
}

func assertOwnerOnly(t *testing.T, path string, wantDir bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat private path %q: %v", path, err)
	}
	if info.IsDir() != wantDir {
		t.Fatalf("private path %q directory=%v, want %v", path, info.IsDir(), wantDir)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read private DACL for %q: %v", path, err)
	}
	if sd == nil {
		t.Fatalf("private path %q has no security descriptor", path)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("read private DACL control for %q: %v", path, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("private path %q DACL is inheritable", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("read private DACL for %q: %v", path, err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		count := uint16(0)
		if dacl != nil {
			count = dacl.AceCount
		}
		t.Fatalf("private path %q DACL ACE count = %d, want 1", path, count)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("read private ACE for %q: %v", path, err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
		t.Fatalf("private path %q does not have one non-inherited allow ACE", path)
	}
	if ace.Mask != 0x001F01FF {
		t.Fatalf("private path %q owner mask = %#x, want %#x", path, ace.Mask, uint32(0x001F01FF))
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatalf("current user SID: %v", err)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !windows.EqualSid(aceSID, sid) {
		t.Fatalf("private path %q ACE is not for current user", path)
	}
}

func errorsIsObjectNotFound(err error) bool {
	return err == windows.ERROR_OBJECT_NOT_FOUND
}
