//go:build windows

package testutil

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ownerAllAccessWindows      = 0x001F01FF
	denyReadAccessWindows      = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE
	fileWriteDataWindows       = 0x00000002 // FILE_WRITE_DATA / FILE_ADD_FILE
	fileAppendDataWindows      = 0x00000004 // FILE_APPEND_DATA / FILE_ADD_SUBDIRECTORY
	fileWriteEAWindows         = 0x00000010 // FILE_WRITE_EA
	fileDeleteChildWindows     = 0x00000040 // FILE_DELETE_CHILD
	fileWriteAttributesWindows = 0x00000100 // FILE_WRITE_ATTRIBUTES
	deleteRightWindows         = 0x00010000 // DELETE
	genericWriteWindows        = 0x40000000 // GENERIC_WRITE
	genericAllWindows          = 0x10000000 // GENERIC_ALL

	allWriteRightsWindows = fileWriteDataWindows | fileAppendDataWindows | fileWriteEAWindows |
		fileDeleteChildWindows | fileWriteAttributesWindows | deleteRightWindows |
		genericWriteWindows | genericAllWindows

	denyWriteAccessWindows = windows.FILE_GENERIC_WRITE | fileDeleteChildWindows | windows.DELETE
)

func currentUserSID() (*windows.SID, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read current user SID: %w", err)
	}
	sid, err := tu.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("copy current user SID: %w", err)
	}
	return sid, nil
}

func restorePathAccessWindows(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: ownerAllAccessWindows,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
	}
	ea.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	ea.Trustee.TrusteeType = windows.TRUSTEE_IS_USER
	ea.Trustee.TrusteeValue = windows.TrusteeValueFromSID(sid)

	var pinner runtime.Pinner
	pinner.Pin(sid)
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, nil)
	pinner.Unpin()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}

func makePathUnreadable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("MakePathUnreadable: stat %s: %v", path, err)
	}

	sid, err := currentUserSID()
	if err != nil {
		t.Fatalf("MakePathUnreadable: %v", err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: denyReadAccessWindows,
			AccessMode:        windows.DENY_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
		{
			AccessPermissions: windows.WRITE_DAC | windows.WRITE_OWNER | windows.READ_CONTROL | windows.FILE_GENERIC_WRITE | windows.DELETE,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
	}

	var pinner runtime.Pinner
	pinner.Pin(sid)
	dacl, err := windows.ACLFromEntries(entries, nil)
	pinner.Unpin()
	if err != nil {
		t.Fatalf("MakePathUnreadable: build ACL for %s: %v", path, err)
	}

	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatalf("MakePathUnreadable: set ACL for %s: %v", path, err)
	}

	t.Cleanup(func() {
		_ = restorePathAccessWindows(path)
	})

	// Verify the path is genuinely unreadable.
	if info.IsDir() {
		if _, err := os.ReadDir(path); err == nil {
			t.Fatalf("MakePathUnreadable: directory %s remains readable after applying deny-read ACL", path)
		}
	} else {
		if f, err := os.Open(path); err == nil {
			f.Close()
			t.Fatalf("MakePathUnreadable: file %s remains readable after applying deny-read ACL", path)
		}
	}
}

func makeDirectoryReadOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("MakeDirectoryReadOnly: stat %s: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("MakeDirectoryReadOnly: %s is not a directory", path)
	}

	sid, err := currentUserSID()
	if err != nil {
		t.Fatalf("MakeDirectoryReadOnly: %v", err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: denyWriteAccessWindows,
			AccessMode:        windows.DENY_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
		{
			AccessPermissions: windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE | windows.WRITE_DAC | windows.READ_CONTROL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
	}

	var pinner runtime.Pinner
	pinner.Pin(sid)
	dacl, err := windows.ACLFromEntries(entries, nil)
	pinner.Unpin()
	if err != nil {
		t.Fatalf("MakeDirectoryReadOnly: build ACL for %s: %v", path, err)
	}

	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatalf("MakeDirectoryReadOnly: set ACL for %s: %v", path, err)
	}

	t.Cleanup(func() {
		_ = restorePathAccessWindows(path)
	})

	// Verify the read-only ACL was established.
	if err := verifyOwnerReadOnlyWindows(path, true); err != nil {
		t.Fatalf("MakeDirectoryReadOnly: %v", err)
	}
}

func verifyOwnerPrivateWindows(path string, isDir bool) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read DACL for %s: %w", path, err)
	}
	if sd == nil {
		return fmt.Errorf("%s has no security descriptor", path)
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	sdPtr := unsafe.Pointer(sd)
	control := *(*uint16)(unsafe.Pointer(uintptr(sdPtr) + 2))
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s DACL is not protected (control: 0x%04x)", path, control)
	}
	daclOff := *(*uint32)(unsafe.Pointer(uintptr(sdPtr) + 16))
	if daclOff == 0 {
		return fmt.Errorf("%s has no DACL", path)
	}
	dacl := (*windows.ACL)(unsafe.Pointer(uintptr(sdPtr) + uintptr(daclOff)))
	if dacl.AceCount != 1 {
		return fmt.Errorf("%s DACL has %d ACEs, want exactly one owner-only ACE", path, dacl.AceCount)
	}

	var pAce *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &pAce); err != nil {
		return fmt.Errorf("read ACE for %s: %w", path, err)
	}
	if pAce.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return fmt.Errorf("%s ACE type %d is not ACCESS_ALLOWED", path, pAce.Header.AceType)
	}
	if pAce.Header.AceFlags&windows.INHERITED_ACE != 0 {
		return fmt.Errorf("%s ACE is inherited, protection is not owner-only", path)
	}
	if pAce.Mask != ownerAllAccessWindows {
		return fmt.Errorf("%s ACE mask %#x is not full owner access %#x", path, pAce.Mask, ownerAllAccessWindows)
	}
	aceSid := (*windows.SID)(unsafe.Pointer(&pAce.SidStart))
	if !windows.EqualSid(aceSid, sid) {
		return fmt.Errorf("%s ACE grants a non-owner principal", path)
	}
	return nil
}

func assertOwnerPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("AssertOwnerPrivate: stat %s: %v", path, err)
	}
	if err := verifyOwnerPrivateWindows(path, info.IsDir()); err != nil {
		t.Fatalf("AssertOwnerPrivate: %v", err)
	}
}

func verifyOwnerReadOnlyWindows(path string, isDir bool) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read DACL for %s: %w", path, err)
	}
	if sd == nil {
		return fmt.Errorf("%s has no security descriptor", path)
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	sdPtr := unsafe.Pointer(sd)
	control := *(*uint16)(unsafe.Pointer(uintptr(sdPtr) + 2))
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s DACL is not protected (control: 0x%04x)", path, control)
	}
	daclOff := *(*uint32)(unsafe.Pointer(uintptr(sdPtr) + 16))
	if daclOff == 0 {
		return fmt.Errorf("%s has no DACL", path)
	}
	dacl := (*windows.ACL)(unsafe.Pointer(uintptr(sdPtr) + uintptr(daclOff)))
	if dacl.AceCount == 0 {
		// An empty protected DACL denies all access (including write), satisfying read-only / no-access.
		return nil
	}

	var deniedWriteRights windows.ACCESS_MASK
	for i := uint16(0); i < dacl.AceCount; i++ {
		var pAce *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &pAce); err != nil {
			return fmt.Errorf("read ACE for %s: %w", path, err)
		}
		if pAce.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return fmt.Errorf("%s ACE is inherited, protection is not owner-only", path)
		}
		aceSid := (*windows.SID)(unsafe.Pointer(&pAce.SidStart))
		if !windows.EqualSid(aceSid, sid) {
			return fmt.Errorf("%s ACE grants a non-owner principal", path)
		}
		switch pAce.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			deniedWriteRights |= (pAce.Mask & allWriteRightsWindows)
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			grantedWrite := pAce.Mask & allWriteRightsWindows
			if grantedWrite&^deniedWriteRights != 0 {
				return fmt.Errorf("%s DACL grants effective write access (granted: 0x%x, denied: 0x%x)", path, grantedWrite, deniedWriteRights)
			}
		}
	}
	return nil
}

func assertOwnerReadOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("AssertOwnerReadOnly: stat %s: %v", path, err)
	}
	if err := verifyOwnerReadOnlyWindows(path, info.IsDir()); err != nil {
		t.Fatalf("AssertOwnerReadOnly: %v", err)
	}
}
