//go:build windows

package home

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// grantEveryoneWindows replaces path's DACL with a single ACE granting the
// Everyone principal read access. It is used to simulate a tampered (non
// owner-only) ACL so the verification path can be exercised.
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

// TestSecureFileOwnerOnlyWindows confirms that secureFile establishes an
// owner-only ACL that verifyProtection accepts.
func TestSecureFileOwnerOnlyWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := secureFile(path); err != nil {
		t.Fatalf("secureFile: %v", err)
	}
	if err := verifyProtection(path, false); err != nil {
		t.Fatalf("verifyProtection after secureFile: %v", err)
	}
}

// TestSecureDirOwnerOnlyWindows confirms that secureDir establishes an
// owner-only ACL that verifyProtection accepts.
func TestSecureDirOwnerOnlyWindows(t *testing.T) {
	dir := t.TempDir()
	if err := secureDir(dir); err != nil {
		t.Fatalf("secureDir: %v", err)
	}
	if err := verifyProtection(dir, true); err != nil {
		t.Fatalf("verifyProtection after secureDir: %v", err)
	}
}

// TestSecureRefusesMissingPath confirms that the contract fails closed when
// the protection cannot be established for a path that does not exist.
func TestSecureRefusesMissingPath(t *testing.T) {
	dir := t.TempDir()
	missingFile := filepath.Join(dir, "nope.txt")
	if err := secureFile(missingFile); err == nil {
		t.Fatal("secureFile on a missing file succeeded")
	}
	missingDir := filepath.Join(dir, "nope")
	if err := secureDir(missingDir); err == nil {
		t.Fatal("secureDir on a missing dir succeeded")
	}
}

// TestVerifyProtectionRefusesTamperedWindows confirms that verifyProtection
// refuses a file whose ACL was weakened to grant another principal.
func TestVerifyProtectionRefusesTamperedWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := secureFile(path); err != nil {
		t.Fatalf("secureFile: %v", err)
	}
	// Tamper: grant Everyone read access, removing the owner-only guarantee.
	grantEveryoneWindows(t, path)
	if err := verifyProtection(path, false); err == nil {
		t.Fatal("verifyProtection accepted a tampered (non-owner-only) ACL")
	}
}

// TestVerifyHomeProtectionRefusesTamperedRootWindows confirms that the home
// owner boundary fails closed when a logical root's protection is tampered.
func TestVerifyHomeProtectionRefusesTamperedRootWindows(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Control: an untouched home opens.
	if _, err := Open(root); err != nil {
		t.Fatalf("Open on an untouched home: %v", err)
	}
	// Tamper one logical root so it is no longer owner-only.
	grantEveryoneWindows(t, filepath.Join(root, CanonicalLayout.State))
	if _, err := Open(root); err == nil {
		t.Fatal("Open succeeded on a home with a tampered logical root")
	}
}

// TestAtomicWriterPathsOwnerOnlyWindows confirms that the real temp/atomic
// writer paths produce owner-only files on Windows.
func TestAtomicWriterPathsOwnerOnlyWindows(t *testing.T) {
	dir := t.TempDir()

	atomicPath := filepath.Join(dir, "atomic.dat")
	if err := canonicalAtomicWrite(atomicPath, []byte("payload")); err != nil {
		t.Fatalf("canonicalAtomicWrite: %v", err)
	}
	if err := verifyProtection(atomicPath, false); err != nil {
		t.Fatalf("canonicalAtomicWrite result not owner-only: %v", err)
	}
}

// TestRestrictDirPreservesOwnerDenyWindows confirms that restrictDir strips
// non-owner principals while preserving the owner's existing restrictions.
func TestRestrictDirPreservesOwnerDenyWindows(t *testing.T) {
	dir := t.TempDir()
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}

	// DACL: owner has deny-write and grant-read; Everyone has grant-read.
	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.FILE_GENERIC_WRITE,
			AccessMode:        windows.DENY_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
		{
			AccessPermissions: windows.FILE_GENERIC_READ | windows.WRITE_DAC | windows.READ_CONTROL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		},
		{
			AccessPermissions: windows.FILE_GENERIC_READ,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(everyone),
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

	// restrictDir should succeed, strip Everyone, and preserve owner's deny-write.
	if err := restrictDir(dir); err != nil {
		t.Fatalf("restrictDir: %v", err)
	}

	// Verify Everyone is gone and only owner ACEs remain.
	if err := verifyRestrictedProtection(dir, sid); err != nil {
		t.Fatalf("verifyRestrictedProtection: %v", err)
	}
}
