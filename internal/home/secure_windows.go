//go:build windows

package home

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownerAllAccess is the Windows access mask granting a principal full control
// over a file or directory: every specific file/directory right plus the
// standard and synchronize rights. Granting it to exactly the owner SID, with
// no inherited ACEs, is the Windows equivalent of Unix 0600 (files) and 0700
// (directories): the owner has all access and every other principal is
// implicitly denied because the DACL contains no ACE that could grant them.
const ownerAllAccess = 0x001F01FF

var getEffectiveRightsFromACL = windows.NewLazySystemDLL("advapi32.dll").NewProc("GetEffectiveRightsFromAclW")

// secureFile establishes owner-private protection on an already-created file.
// On Windows this replaces Unix mode-bit enforcement with an owner-only DACL
// that is verified after being set. It fails closed if the ACL cannot be
// established or verified.
func secureFile(path string) error {
	return securePath(path, false)
}

// secureDir establishes owner-private protection on an already-created
// directory. See secureFile.
func secureDir(path string) error {
	return securePath(path, true)
}

// restrictDir removes access for other principals while preserving the
// current user's effective directory rights. Unlike secureDir, it must not
// upgrade a pre-existing read-only directory to full control.
func restrictDir(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("home: read DACL for %s: %w", path, err)
	}
	if sd == nil {
		return fmt.Errorf("home: %s has no security descriptor", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil && err != windows.ERROR_OBJECT_NOT_FOUND {
		return fmt.Errorf("home: read DACL for %s: %w", path, err)
	}
	rights, err := effectiveRights(dacl, sid)
	if err != nil {
		return fmt.Errorf("home: read effective rights for %s: %w", path, err)
	}
	dacl, err = ownerACL(sid, rights)
	if err != nil {
		return fmt.Errorf("home: build restricted ACL for %s: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("home: set restricted ACL for %s: %w", path, err)
	}
	return verifyRestrictedProtection(path, rights)
}

// securePath sets and verifies an owner-only DACL on path. The DACL contains a
// single ACCESS_ALLOWED ACE for the current user granting full control, with
// no inheritance, so the file or directory is accessible only by its owner.
func securePath(path string, isDir bool) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	dacl, err := ownerACL(sid, ownerAllAccess)
	if err != nil {
		return fmt.Errorf("home: build owner-only ACL for %s: %w", path, err)
	}

	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		return fmt.Errorf("home: set owner-only ACL for %s: %w", path, err)
	}

	return verifyProtection(path, isDir)
}

func ownerACL(sid *windows.SID, rights uint32) (*windows.ACL, error) {
	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(rights),
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
	}
	ea.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	ea.Trustee.TrusteeType = windows.TRUSTEE_IS_USER
	ea.Trustee.TrusteeValue = windows.TrusteeValueFromSID(sid)
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, nil)
}

func effectiveRights(dacl *windows.ACL, sid *windows.SID) (uint32, error) {
	if dacl == nil {
		return 0, nil
	}
	trustee := windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_SID,
		TrusteeType:  windows.TRUSTEE_IS_USER,
		TrusteeValue: windows.TrusteeValueFromSID(sid),
	}
	var rights uint32
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	ret, _, _ := getEffectiveRightsFromACL.Call(
		uintptr(unsafe.Pointer(dacl)), uintptr(unsafe.Pointer(&trustee)), uintptr(unsafe.Pointer(&rights)))
	if ret != 0 {
		return 0, windows.Errno(ret)
	}
	return rights, nil
}

func verifyRestrictedProtection(path string, rights uint32) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("home: read restricted DACL for %s: %w", path, err)
	}
	if sd == nil {
		return fmt.Errorf("home: %s has no security descriptor", path)
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("home: %s restricted DACL is inheritable", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil || dacl.AceCount != 1 {
		return fmt.Errorf("home: %s restricted DACL does not contain exactly one ACE", path)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return err
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 || uint32(ace.Mask) != rights {
		return fmt.Errorf("home: %s restricted DACL does not preserve effective rights %#x", path, rights)
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	if !windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), sid) {
		return fmt.Errorf("home: %s restricted DACL grants another principal", path)
	}
	return nil
}

// currentUserSID returns a copy of the SID of the user running this process.
func currentUserSID() (*windows.SID, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("home: read current user SID: %w", err)
	}
	sid, err := tu.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("home: copy current user SID: %w", err)
	}
	return sid, nil
}

// verifyProtection confirms that path is owner-private by reading its DACL and
// requiring exactly one ACCESS_ALLOWED ACE for the owner SID granting full
// control, with no inherited ACE. It fails closed when the guarantee is not
// established or cannot be verified.
func verifyProtection(path string, isDir bool) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("home: read DACL for %s: %w", path, err)
	}
	if sd == nil {
		return fmt.Errorf("home: %s has no security descriptor", path)
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	// A self-relative SECURITY_DESCRIPTOR has a fixed 20-byte header; the DACL
	// offset is a uint32 at byte offset 16 from the start of the descriptor.
	sdPtr := unsafe.Pointer(sd)
	daclOff := *(*uint32)(unsafe.Pointer(uintptr(sdPtr) + 16))
	if daclOff == 0 {
		return fmt.Errorf("home: %s has no DACL", path)
	}
	dacl := (*windows.ACL)(unsafe.Pointer(uintptr(sdPtr) + uintptr(daclOff)))
	if dacl.AceCount != 1 {
		return fmt.Errorf("home: %s DACL has %d ACEs, want exactly one owner-only ACE", path, dacl.AceCount)
	}

	var pAce *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &pAce); err != nil {
		return fmt.Errorf("home: read ACE for %s: %w", path, err)
	}
	if pAce.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return fmt.Errorf("home: %s ACE type %d is not ACCESS_ALLOWED", path, pAce.Header.AceType)
	}
	if pAce.Header.AceFlags&windows.INHERITED_ACE != 0 {
		return fmt.Errorf("home: %s ACE is inherited, protection is not owner-only", path)
	}
	if pAce.Mask != ownerAllAccess {
		return fmt.Errorf("home: %s ACE mask %#x is not full owner access %#x", path, pAce.Mask, ownerAllAccess)
	}
	aceSid := (*windows.SID)(unsafe.Pointer(&pAce.SidStart))
	if !windows.EqualSid(aceSid, sid) {
		return fmt.Errorf("home: %s ACE grants a non-owner principal", path)
	}
	return nil
}
