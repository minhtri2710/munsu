//go:build windows

package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ownerAllAccessWindows      = 0x001F01FF
	fileReadDataWindows        = 0x00000001 // FILE_READ_DATA / FILE_LIST_DIRECTORY
	fileReadEAWindows          = 0x00000008 // FILE_READ_EA
	fileExecuteWindows         = 0x00000020 // FILE_EXECUTE / FILE_TRAVERSE
	fileReadAttributesWindows  = 0x00000080 // FILE_READ_ATTRIBUTES
	fileWriteDataWindows       = 0x00000002 // FILE_WRITE_DATA / FILE_ADD_FILE
	fileAppendDataWindows      = 0x00000004 // FILE_APPEND_DATA / FILE_ADD_SUBDIRECTORY
	fileWriteEAWindows         = 0x00000010 // FILE_WRITE_EA
	fileDeleteChildWindows     = 0x00000040 // FILE_DELETE_CHILD
	fileWriteAttributesWindows = 0x00000100 // FILE_WRITE_ATTRIBUTES
	deleteRightWindows         = 0x00010000 // DELETE
	genericWriteWindows        = 0x40000000 // GENERIC_WRITE
	genericAllWindows          = 0x10000000 // GENERIC_ALL

	// denyReadAccessWindows defines specific read and execute rights mapped for file objects.
	// Generic bits (GENERIC_READ, GENERIC_ALL) are explicitly excluded so that
	// WRITE_DAC, WRITE_OWNER, and DELETE are not inadvertently denied.
	denyReadAccessWindows = fileReadDataWindows | fileReadEAWindows | fileExecuteWindows | fileReadAttributesWindows

	// denyWriteAccessWindows defines specific write and delete rights mapped for file objects.
	// Generic bits (GENERIC_WRITE, GENERIC_ALL) are explicitly excluded so that
	// WRITE_DAC, READ_CONTROL, and non-write rights are not denied.
	denyWriteAccessWindows = fileWriteDataWindows | fileAppendDataWindows | fileWriteEAWindows |
		fileDeleteChildWindows | fileWriteAttributesWindows | deleteRightWindows

	allWriteRightsWindows = denyWriteAccessWindows | genericWriteWindows | genericAllWindows
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

func buildDenyReadDACLWindows(sid *windows.SID, isDir bool) (*windows.ACL, error) {
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		return nil, fmt.Errorf("buildDenyReadDACL: StringToSid(Everyone): %w", err)
	}

	everyoneLen := int(windows.GetLengthSid(everyone))
	everyoneBytes := unsafe.Slice((*byte)(unsafe.Pointer(everyone)), everyoneLen)

	sidLen := int(windows.GetLengthSid(sid))
	sidBytes := unsafe.Slice((*byte)(unsafe.Pointer(sid)), sidLen)

	denyMask := uint32(denyReadAccessWindows)

	grantMask := uint32(windows.WRITE_DAC | windows.WRITE_OWNER | windows.READ_CONTROL |
		windows.FILE_GENERIC_WRITE | windows.DELETE)

	aceFlags := byte(0)
	if isDir {
		aceFlags = byte(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
	}

	ace0Size := (8 + everyoneLen + 3) &^ 3
	ace1Size := (8 + sidLen + 3) &^ 3
	ace2Size := (8 + sidLen + 3) &^ 3

	totalSize := 8 + ace0Size + ace1Size + ace2Size
	buf := make([]byte, totalSize)

	// ACL Header: AclRevision=2, Sbz1=0, AclSize, AceCount=3, Sbz2=0
	buf[0] = 2
	buf[1] = 0
	*(*uint16)(unsafe.Pointer(&buf[2])) = uint16(totalSize)
	*(*uint16)(unsafe.Pointer(&buf[4])) = 3
	*(*uint16)(unsafe.Pointer(&buf[6])) = 0

	offset := 8

	// ACE 0: ACCESS_DENIED_ACE for Everyone (S-1-1-0)
	buf[offset] = 1 // ACCESS_DENIED_ACE_TYPE
	buf[offset+1] = aceFlags
	*(*uint16)(unsafe.Pointer(&buf[offset+2])) = uint16(ace0Size)
	*(*uint32)(unsafe.Pointer(&buf[offset+4])) = denyMask
	copy(buf[offset+8:], everyoneBytes)
	offset += ace0Size

	// ACE 1: ACCESS_DENIED_ACE for current user SID
	buf[offset] = 1 // ACCESS_DENIED_ACE_TYPE
	buf[offset+1] = aceFlags
	*(*uint16)(unsafe.Pointer(&buf[offset+2])) = uint16(ace1Size)
	*(*uint32)(unsafe.Pointer(&buf[offset+4])) = denyMask
	copy(buf[offset+8:], sidBytes)
	offset += ace1Size

	// ACE 2: ACCESS_ALLOWED_ACE for current user SID (preserves WRITE_DAC/WRITE_OWNER/DELETE for cleanup)
	buf[offset] = 0 // ACCESS_ALLOWED_ACE_TYPE
	buf[offset+1] = aceFlags
	*(*uint16)(unsafe.Pointer(&buf[offset+2])) = uint16(ace2Size)
	*(*uint32)(unsafe.Pointer(&buf[offset+4])) = grantMask
	copy(buf[offset+8:], sidBytes)

	return (*windows.ACL)(unsafe.Pointer(&buf[0])), nil
}

func buildReadOnlyDACLWindows(sid *windows.SID, isDir bool) (*windows.ACL, error) {
	sidLen := int(windows.GetLengthSid(sid))
	sidBytes := unsafe.Slice((*byte)(unsafe.Pointer(sid)), sidLen)

	denyMask := uint32(denyWriteAccessWindows)
	grantMask := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE | windows.WRITE_DAC | windows.READ_CONTROL)

	aceFlags := byte(0)
	if isDir {
		aceFlags = byte(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
	}

	ace0Size := (8 + sidLen + 3) &^ 3
	ace1Size := (8 + sidLen + 3) &^ 3

	totalSize := 8 + ace0Size + ace1Size
	buf := make([]byte, totalSize)

	// ACL Header: AclRevision=2, Sbz1=0, AclSize, AceCount=2, Sbz2=0
	buf[0] = 2
	buf[1] = 0
	*(*uint16)(unsafe.Pointer(&buf[2])) = uint16(totalSize)
	*(*uint16)(unsafe.Pointer(&buf[4])) = 2
	*(*uint16)(unsafe.Pointer(&buf[6])) = 0

	offset := 8

	// ACE 0: ACCESS_DENIED_ACE for current user SID
	buf[offset] = 1 // ACCESS_DENIED_ACE_TYPE
	buf[offset+1] = aceFlags
	*(*uint16)(unsafe.Pointer(&buf[offset+2])) = uint16(ace0Size)
	*(*uint32)(unsafe.Pointer(&buf[offset+4])) = denyMask
	copy(buf[offset+8:], sidBytes)
	offset += ace0Size

	// ACE 1: ACCESS_ALLOWED_ACE for current user SID
	buf[offset] = 0 // ACCESS_ALLOWED_ACE_TYPE
	buf[offset+1] = aceFlags
	*(*uint16)(unsafe.Pointer(&buf[offset+2])) = uint16(ace1Size)
	*(*uint32)(unsafe.Pointer(&buf[offset+4])) = grantMask
	copy(buf[offset+8:], sidBytes)

	return (*windows.ACL)(unsafe.Pointer(&buf[0])), nil
}

var (
	modadvapi32              = windows.NewLazySystemDLL("advapi32.dll")
	procLookupPrivilegeNameW = modadvapi32.NewProc("LookupPrivilegeNameW")
)

func lookupPrivilegeNameWindows(systemName *uint16, luid *windows.LUID, name *uint16, nameLen *uint32) error {
	r1, _, err := procLookupPrivilegeNameW.Call(
		uintptr(unsafe.Pointer(systemName)),
		uintptr(unsafe.Pointer(luid)),
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(nameLen)),
	)
	if r1 == 0 {
		return err
	}
	return nil
}

// bypassPrivilegeNames lists the token privileges that bypass file system DACL
// checks on Windows (e.g. for the built-in Administrator account RID 500).
// The bypass mechanism was empirically confirmed by GitHub Actions run 33144040477.
//
// SeChangeNotifyPrivilege (Bypass Traverse Checking) deserves its own note: it
// is enabled for every token by default, and while it is enabled a parent
// directory's FILE_TRAVERSE denial never binds — which silently voids the
// traversal bit the deny-read mask deliberately carries. It also decides
// os.Stat: on Windows Stat consults GetFileAttributesExW first
// (go/src/os/stat_windows.go), an attribute query that checks only parent
// traversal and never the object's own DACL, so without disabling this
// privilege a Stat of a denied directory's child always succeeds no matter
// what the DACL says.
var bypassPrivilegeNames = []string{
	"SeBackupPrivilege",
	"SeRestorePrivilege",
	"SeTakeOwnershipPrivilege",
	"SeDebugPrivilege",
	"SeChangeNotifyPrivilege",
}

// disableBypassPrivilegesWindows disables the filesystem DACL-bypass privileges listed in
// bypassPrivilegeNames (read/write/ownership and traversal, e.g. SeBackup/SeRestore/SeTakeOwnership
// and SeChangeNotifyPrivilege) in the current process primary token. This mutates the process primary token, so every goroutine
// in the test binary observes it. The restore is registered in t.Cleanup before any t.Fatalf
// so it runs on every exit path including Goexit. Nesting is safe because cleanups run in LIFO
// order (inner captured state restored first, original state last). Note: no caller may add
// t.Parallel without first solving the shared-token window, because the helpers deliberately
// have no lock.
func disableBypassPrivilegesWindows() (token windows.Token, prevPrivs []windows.LUIDAndAttributes, log string, err error) {
	var sb strings.Builder
	err = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token)
	if err != nil {
		return 0, nil, "", fmt.Errorf("OpenProcessToken(TOKEN_ADJUST_PRIVILEGES|TOKEN_QUERY): %w", err)
	}

	for _, name := range bypassPrivilegeNames {
		nameUTF16, err := windows.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, nameUTF16, &luid); err != nil {
			sb.WriteString(fmt.Sprintf("  LookupPrivilegeValue(%s): not found (%v)\n", name, err))
			continue
		}

		var tp windows.Tokenprivileges
		tp.PrivilegeCount = 1
		tp.Privileges[0].Luid = luid
		tp.Privileges[0].Attributes = 0 // Disable privilege

		var oldTp windows.Tokenprivileges
		var retLen uint32
		err = windows.AdjustTokenPrivileges(
			token,
			false,
			&tp,
			uint32(unsafe.Sizeof(oldTp)),
			&oldTp,
			&retLen,
		)
		if err != nil {
			token.Close()
			return 0, nil, "", fmt.Errorf("AdjustTokenPrivileges(%s, 0): %w", name, err)
		}
		if oldTp.PrivilegeCount > 0 {
			prevPrivs = append(prevPrivs, oldTp.Privileges[0])
			wasEnabled := oldTp.Privileges[0].Attributes&windows.SE_PRIVILEGE_ENABLED != 0
			sb.WriteString(fmt.Sprintf("  Privilege %s: was present (Attributes=0x%08x, Enabled=%v), disabled now\n",
				name, oldTp.Privileges[0].Attributes, wasEnabled))
		} else {
			sb.WriteString(fmt.Sprintf("  Privilege %s: was not held in token\n", name))
		}
	}
	return token, prevPrivs, sb.String(), nil
}

func restorePrivilegesWindows(token windows.Token, privs []windows.LUIDAndAttributes) {
	for _, priv := range privs {
		var tp windows.Tokenprivileges
		tp.PrivilegeCount = 1
		tp.Privileges[0] = priv
		_ = windows.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil)
	}
}

func formatSecurityDiagnosticWindows(path string) string {
	var sb strings.Builder
	sid, err := currentUserSID()
	if err != nil {
		sb.WriteString(fmt.Sprintf("  currentUserSID error: %v\n", err))
	} else {
		sb.WriteString(fmt.Sprintf("  currentUserSID: %s\n", sid.String()))
	}

	// 1. Process Token Inspection (Privileges, Elevation, Groups)
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		sb.WriteString(fmt.Sprintf("  OpenProcessToken error: %v\n", err))
	} else {
		defer token.Close()

		// Elevation & Elevation Type
		var elevType uint32
		var retLen uint32
		if err := windows.GetTokenInformation(token, windows.TokenElevationType, (*byte)(unsafe.Pointer(&elevType)), uint32(unsafe.Sizeof(elevType)), &retLen); err == nil {
			elevName := "Unknown"
			switch elevType {
			case 1:
				elevName = "TokenElevationTypeDefault (1)"
			case 2:
				elevName = "TokenElevationTypeFull (2)"
			case 3:
				elevName = "TokenElevationTypeLimited (3)"
			}
			sb.WriteString(fmt.Sprintf("  TokenElevationType: %s\n", elevName))
		} else {
			sb.WriteString(fmt.Sprintf("  GetTokenInformation(TokenElevationType) error: %v\n", err))
		}

		var isElevated uint32
		if err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&isElevated)), uint32(unsafe.Sizeof(isElevated)), &retLen); err == nil {
			sb.WriteString(fmt.Sprintf("  TokenElevation: %v\n", isElevated != 0))
		} else {
			sb.WriteString(fmt.Sprintf("  GetTokenInformation(TokenElevation) error: %v\n", err))
		}

		// Groups (BUILTIN\Administrators check)
		var groupBufLen uint32
		_ = windows.GetTokenInformation(token, windows.TokenGroups, nil, 0, &groupBufLen)
		if groupBufLen > 0 {
			groupBuf := make([]byte, groupBufLen)
			if err := windows.GetTokenInformation(token, windows.TokenGroups, &groupBuf[0], groupBufLen, &groupBufLen); err == nil {
				tg := (*windows.Tokengroups)(unsafe.Pointer(&groupBuf[0]))
				adminSid, _ := windows.StringToSid("S-1-5-32-544")
				foundAdmin := false
				groups := unsafe.Slice(&tg.Groups[0], tg.GroupCount)
				for _, g := range groups {
					if adminSid != nil && windows.EqualSid(g.Sid, adminSid) {
						foundAdmin = true
						enabled := g.Attributes&windows.SE_GROUP_ENABLED != 0
						denyOnly := g.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY != 0
						sb.WriteString(fmt.Sprintf("  TokenGroup BUILTIN\\Administrators (S-1-5-32-544): present, Attributes: 0x%08x (Enabled: %v, DenyOnly: %v)\n",
							g.Attributes, enabled, denyOnly))
					}
				}
				if !foundAdmin {
					sb.WriteString("  TokenGroup BUILTIN\\Administrators (S-1-5-32-544): not present\n")
				}
			} else {
				sb.WriteString(fmt.Sprintf("  GetTokenInformation(TokenGroups) error: %v\n", err))
			}
		}

		// Privileges
		var privBufLen uint32
		_ = windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &privBufLen)
		if privBufLen > 0 {
			privBuf := make([]byte, privBufLen)
			if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &privBuf[0], privBufLen, &privBufLen); err == nil {
				tp := (*windows.Tokenprivileges)(unsafe.Pointer(&privBuf[0]))
				sb.WriteString(fmt.Sprintf("  TokenPrivileges: count=%d\n", tp.PrivilegeCount))
				privs := unsafe.Slice(&tp.Privileges[0], tp.PrivilegeCount)
				for i, p := range privs {
					var nameBuf [256]uint16
					nameLen := uint32(len(nameBuf))
					var privName string
					if err := lookupPrivilegeNameWindows(nil, &p.Luid, &nameBuf[0], &nameLen); err == nil {
						privName = windows.UTF16ToString(nameBuf[:nameLen])
					} else {
						privName = fmt.Sprintf("LUID{Low:0x%x, High:%d}", p.Luid.LowPart, p.Luid.HighPart)
					}
					enabled := p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0
					defaultEnabled := p.Attributes&windows.SE_PRIVILEGE_ENABLED_BY_DEFAULT != 0
					sb.WriteString(fmt.Sprintf("    Privilege %d: %s, Attributes=0x%08x (Enabled: %v, DefaultEnabled: %v)\n",
						i, privName, p.Attributes, enabled, defaultEnabled))
				}
			} else {
				sb.WriteString(fmt.Sprintf("  GetTokenInformation(TokenPrivileges) error: %v\n", err))
			}
		}
	}

	// 2. Object Security Descriptor Inspection
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		sb.WriteString(fmt.Sprintf("  GetNamedSecurityInfo error: %v\n", err))
		return sb.String()
	}
	if sd == nil {
		sb.WriteString("  SecurityDescriptor: nil\n")
		return sb.String()
	}

	sdPtr := unsafe.Pointer(sd)
	control := *(*uint16)(unsafe.Pointer(uintptr(sdPtr) + 2))
	sb.WriteString(fmt.Sprintf("  Control: 0x%04x (SE_DACL_PROTECTED: %v)\n", control, control&windows.SE_DACL_PROTECTED != 0))

	ownerOff := *(*uint32)(unsafe.Pointer(uintptr(sdPtr) + 4))
	if ownerOff != 0 {
		ownerSid := (*windows.SID)(unsafe.Pointer(uintptr(sdPtr) + uintptr(ownerOff)))
		sb.WriteString(fmt.Sprintf("  Owner SID: %s\n", ownerSid.String()))
	}

	daclOff := *(*uint32)(unsafe.Pointer(uintptr(sdPtr) + 16))
	if daclOff == 0 {
		sb.WriteString("  DACL: absent (NULL DACL)\n")
		return sb.String()
	}
	dacl := (*windows.ACL)(unsafe.Pointer(uintptr(sdPtr) + uintptr(daclOff)))
	sb.WriteString(fmt.Sprintf("  DACL: AceCount=%d\n", dacl.AceCount))
	for i := uint16(0); i < dacl.AceCount; i++ {
		var pAce *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &pAce); err != nil {
			sb.WriteString(fmt.Sprintf("    ACE %d: GetAce error: %v\n", i, err))
			continue
		}
		typeName := "UNKNOWN"
		switch pAce.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			typeName = "ACCESS_ALLOWED"
		case windows.ACCESS_DENIED_ACE_TYPE:
			typeName = "ACCESS_DENIED"
		}
		aceSid := (*windows.SID)(unsafe.Pointer(&pAce.SidStart))
		sb.WriteString(fmt.Sprintf("    ACE %d: Type=%s(%d) Flags=0x%02x Mask=0x%08x SID=%s\n",
			i, typeName, pAce.Header.AceType, pAce.Header.AceFlags, pAce.Mask, aceSid.String()))
	}
	return sb.String()
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

	dacl, err := buildDenyReadDACLWindows(sid, info.IsDir())
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

	// Disable DACL-bypass privileges in token (read/ownership plus traversal, e.g. SeBackupPrivilege
	// on elevated Administrator tokens) so the deny-read ACL below binds.
	token, prevPrivs, privLog, err := disableBypassPrivilegesWindows()
	if err != nil {
		t.Fatalf("MakePathUnreadable: disable bypass privileges: %v\nPrivilege Log:\n%s", err, privLog)
	}
	t.Cleanup(func() {
		restorePrivilegesWindows(token, prevPrivs)
		token.Close()
	})

	// Verify the path is genuinely unreadable.
	if info.IsDir() {
		if _, err := os.ReadDir(path); err == nil {
			t.Fatalf("MakePathUnreadable: directory %s remains readable after applying deny-read ACL\nPrivilege adjustment log:\n%s\nDiagnostic:\n%s",
				path, privLog, formatSecurityDiagnosticWindows(path))
		}
	} else {
		if f, err := os.Open(path); err == nil {
			f.Close()
			t.Fatalf("MakePathUnreadable: file %s remains readable after applying deny-read ACL\nPrivilege adjustment log:\n%s\nDiagnostic:\n%s",
				path, privLog, formatSecurityDiagnosticWindows(path))
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

	dacl, err := buildReadOnlyDACLWindows(sid, true)
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

	// Disable DACL-bypass privileges in token (write/ownership plus traversal, e.g. SeRestorePrivilege
	// on elevated Administrator tokens) so the read-only ACL below binds.
	token, prevPrivs, privLog, err := disableBypassPrivilegesWindows()
	if err != nil {
		t.Fatalf("MakeDirectoryReadOnly: disable bypass privileges: %v\nPrivilege Log:\n%s", err, privLog)
	}
	t.Cleanup(func() {
		restorePrivilegesWindows(token, prevPrivs)
		token.Close()
	})

	// Verify the read-only ACL was established.
	if err := verifyOwnerReadOnlyWindows(path, true); err != nil {
		t.Fatalf("MakeDirectoryReadOnly: %v", err)
	}

	probe := filepath.Join(path, ".test_write_probe")
	if f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE, 0600); err == nil {
		f.Close()
		_ = os.Remove(probe)
		t.Fatalf("MakeDirectoryReadOnly: directory %s remains writable after applying deny-write ACL", path)
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
		if pAce.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			deniedWriteRights |= (pAce.Mask & allWriteRightsWindows)
		}
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var pAce *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &pAce); err != nil {
			return fmt.Errorf("read ACE for %s: %w", path, err)
		}
		if pAce.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE {
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
