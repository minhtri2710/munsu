//go:build windows

package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func grantEveryoneWindows(t *testing.T, path string) {
	t.Helper()
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatalf("StringToSid: %v", err)
	}
	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.FILE_GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
	}
	ea.Trustee.TrusteeForm = windows.TRUSTEE_IS_SID
	ea.Trustee.TrusteeType = windows.TRUSTEE_IS_WELL_KNOWN_GROUP
	ea.Trustee.TrusteeValue = windows.TrusteeValueFromSID(everyone)
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, nil)
	if err != nil {
		t.Fatalf("ACLFromEntries: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatalf("SetNamedSecurityInfo: %v", err)
	}
}

func TestVerifyOwnerPrivate_Windows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private.txt")
	if err := os.WriteFile(path, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restorePathAccessWindows(path); err != nil {
		t.Fatal(err)
	}
	AssertOwnerPrivate(t, path)

	// Tamper to grant Everyone
	grantEveryoneWindows(t, path)
	if err := verifyOwnerPrivateWindows(path, false); err == nil {
		t.Fatal("verifyOwnerPrivateWindows accepted DACL granting Everyone")
	}
}

func TestVerifyOwnerReadOnly_Windows(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	MakeDirectoryReadOnly(t, sub)
	AssertOwnerReadOnly(t, sub)

	// Tamper: restore owner all access
	if err := restorePathAccessWindows(sub); err != nil {
		t.Fatal(err)
	}
	if err := verifyOwnerReadOnlyWindows(sub, true); err == nil {
		t.Fatal("verifyOwnerReadOnlyWindows accepted DACL granting full write access")
	}
}

// TestVerifyOwnerReadOnly_RejectsNarrowDenyWithBroadAllowWindows confirms that
// a DACL with a narrow deny (e.g. READ_CONTROL only) followed by a broad allow
// (e.g. FILE_GENERIC_WRITE | DELETE) is rejected by verifyOwnerReadOnlyWindows.
func TestVerifyOwnerReadOnly_RejectsNarrowDenyWithBroadAllowWindows(t *testing.T) {
	dir := t.TempDir()
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.READ_CONTROL,
			AccessMode:        windows.DENY_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
		{
			AccessPermissions: windows.FILE_GENERIC_WRITE | windows.DELETE | windows.FILE_GENERIC_READ,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatal(err)
	}

	if err := verifyOwnerReadOnlyWindows(dir, true); err == nil {
		t.Fatal("verifyOwnerReadOnlyWindows accepted narrow-deny + broad-allow DACL, want error")
	}
}
