//go:build windows

package fsaccess

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const unreadableAccess = windows.FILE_GENERIC_READ
const readOnlyAccess = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | windows.DELETE | 0x40

var getEffectiveRightsFromACL = windows.NewLazySystemDLL("advapi32.dll").NewProc("GetEffectiveRightsFromAclW")

// MakeUnreadable adds an explicit deny ACE for the current user while retaining
// WRITE_DAC, verifies that reading/listing is refused, and restores the exact
// original DACL at test cleanup.
func MakeUnreadable(t *testing.T, path string) {
	t.Helper()
	state := captureACL(t, path)
	applyDeniedAccess(t, path, unreadableAccess)
	restore := registerRestore(t, func() error { return state.restore(path) })
	if err := verifyUnreadable(path, state.isDir); err != nil {
		_ = restore()
		t.Fatalf("unreadable path %q was still readable: %v", path, err)
	}
}

// MakeReadOnly denies writes/deletes for the current user while retaining
// WRITE_DAC, verifies that a write operation is refused, and restores the exact
// original DACL at test cleanup.
func MakeReadOnly(t *testing.T, path string) {
	t.Helper()
	state := captureACL(t, path)
	applyDeniedAccess(t, path, readOnlyAccess)
	restore := registerRestore(t, func() error { return state.restore(path) })
	if err := verifyReadOnly(path, state.isDir); err != nil {
		_ = restore()
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

func applyDeniedAccess(t *testing.T, path string, access uint32) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read DACL for %q: %v", path, err)
	}
	if sd == nil {
		t.Fatalf("path %q has no security descriptor", path)
	}
	oldDACL, _, err := sd.DACL()
	if err != nil && !errorsIsObjectNotFound(err) {
		t.Fatalf("read DACL for %q: %v", path, err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatalf("current user SID: %v", err)
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(access),
		AccessMode:        windows.DENY_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
	}
	entry.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	entry.Trustee.TrusteeType = windows.TRUSTEE_IS_USER
	entry.Trustee.TrusteeValue = windows.TrusteeValueFromSID(sid)
	var pinner runtime.Pinner
	pinner.Pin(sid)
	newDACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, oldDACL)
	pinner.Unpin()
	if err != nil {
		t.Fatalf("build denied DACL for %q: %v", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, newDACL, nil); err != nil {
		t.Fatalf("set denied DACL for %q: %v", path, err)
	}
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
		probe := path + `\fsaccess-write-probe`
		f, err := os.Create(probe)
		if err == nil {
			f.Close()
			_ = os.Remove(probe)
			return fmt.Errorf("directory create succeeded")
		}
		return nil
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
