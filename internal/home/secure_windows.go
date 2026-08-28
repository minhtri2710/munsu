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

// restrictDir ensures path grants no access to other principals while
// preserving the owner's existing access bits. It is used for pre-existing
// directories whose owner access must not be increased.
func restrictDir(path string) error {
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

	sdPtr := unsafe.Pointer(sd)
	daclOff := *(*uint32)(unsafe.Pointer(uintptr(sdPtr) + 16))
	var ownerEntries []windows.EXPLICIT_ACCESS
	if daclOff != 0 {
		dacl := (*windows.ACL)(unsafe.Pointer(uintptr(sdPtr) + uintptr(daclOff)))
		for i := uint16(0); i < dacl.AceCount; i++ {
			var pAce *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, uint32(i), &pAce); err != nil {
				return fmt.Errorf("home: read ACE for %s: %w", path, err)
			}
			aceSid := (*windows.SID)(unsafe.Pointer(&pAce.SidStart))
			if !windows.EqualSid(aceSid, sid) {
				continue
			}
			var mode windows.ACCESS_MODE
			switch pAce.Header.AceType {
			case windows.ACCESS_ALLOWED_ACE_TYPE:
				mode = windows.GRANT_ACCESS
			case windows.ACCESS_DENIED_ACE_TYPE:
				mode = windows.DENY_ACCESS
			default:
				continue
			}
			ea := windows.EXPLICIT_ACCESS{
				AccessPermissions: pAce.Mask,
				AccessMode:        mode,
				Inheritance:       windows.NO_INHERITANCE,
			}
			ea.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
			ea.Trustee.TrusteeType = windows.TRUSTEE_IS_USER
			ea.Trustee.TrusteeValue = windows.TrusteeValueFromSID(sid)
			ownerEntries = append(ownerEntries, ea)
		}
	}

	if len(ownerEntries) == 0 {
		ea := windows.EXPLICIT_ACCESS{
			AccessPermissions: ownerAllAccess,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
		}
		ea.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
		ea.Trustee.TrusteeType = windows.TRUSTEE_IS_USER
		ea.Trustee.TrusteeValue = windows.TrusteeValueFromSID(sid)
		ownerEntries = append(ownerEntries, ea)
	}

	var pinner runtime.Pinner
	pinner.Pin(sid)
	newDacl, err := windows.ACLFromEntries(ownerEntries, nil)
	pinner.Unpin()
	if err != nil {
		return fmt.Errorf("home: build restricted ACL for %s: %w", path, err)
	}

	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, newDacl, nil,
	); err != nil {
		return fmt.Errorf("home: set restricted ACL for %s: %w", path, err)
	}

	return verifyRestrictedProtection(path, sid)
}

// verifyRestrictedProtection confirms that path's DACL contains only ACEs
// belonging to sid, with no inherited ACEs, ensuring no other principals have access.
func verifyRestrictedProtection(path string, sid *windows.SID) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("home: read DACL for %s: %w", path, err)
	}
	if sd == nil {
		return fmt.Errorf("home: %s has no security descriptor", path)
	}
	sdPtr := unsafe.Pointer(sd)
	daclOff := *(*uint32)(unsafe.Pointer(uintptr(sdPtr) + 16))
	if daclOff == 0 {
		return fmt.Errorf("home: %s has no DACL", path)
	}
	dacl := (*windows.ACL)(unsafe.Pointer(uintptr(sdPtr) + uintptr(daclOff)))
	if dacl.AceCount == 0 {
		return fmt.Errorf("home: %s DACL has 0 ACEs", path)
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var pAce *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &pAce); err != nil {
			return fmt.Errorf("home: read ACE for %s: %w", path, err)
		}
		if pAce.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return fmt.Errorf("home: %s ACE is inherited, protection is not owner-only", path)
		}
		aceSid := (*windows.SID)(unsafe.Pointer(&pAce.SidStart))
		if !windows.EqualSid(aceSid, sid) {
			return fmt.Errorf("home: %s ACE grants a non-owner principal", path)
		}
	}
	return nil
}

// securePath sets and verifies an owner-only DACL on path. The DACL contains a
// single ACCESS_ALLOWED ACE for the current user granting full control, with
// no inheritance, so the file or directory is accessible only by its owner.
func securePath(path string, isDir bool) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: ownerAllAccess,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
	}
	ea.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	ea.Trustee.TrusteeType = windows.TRUSTEE_IS_USER
	ea.Trustee.TrusteeValue = windows.TrusteeValueFromSID(sid)

	// TrusteeValueFromSID points the trustee at sid; the SID must stay alive
	// while ACLFromEntries copies it into the ACE.
	var pinner runtime.Pinner
	pinner.Pin(sid)
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, nil)
	pinner.Unpin()
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
