//go:build windows

package fsaccess

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateAssertionsWindows(t *testing.T) {
	dir := t.TempDir()
	if err := secureTestPath(dir, true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := secureTestPath(file, false); err != nil {
		t.Fatal(err)
	}
	AssertPrivateFile(t, file)
	AssertPrivateDir(t, dir)
}

func secureTestPath(path string, isDir bool) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: 0x001F01FF,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
	}
	ea.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	ea.Trustee.TrusteeType = windows.TRUSTEE_IS_USER
	ea.Trustee.TrusteeValue = windows.TrusteeValueFromSID(sid)
	var pinner runtime.Pinner
	pinner.Pin(sid)
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, nil)
	pinner.Unpin()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
}
