package testutil

import (
	"testing"
)

// MakePathUnreadable makes path unreadable to the current user for the duration
// of the test, failing the test if the host cannot establish the denial.
// It registers a test cleanup to restore access.
func MakePathUnreadable(t *testing.T, path string) {
	t.Helper()
	makePathUnreadable(t, path)
}

// MakeDirectoryReadOnly makes path (a directory) read-only to the current user,
// denying write and file removal inside it for the duration of the test.
// It registers a test cleanup to restore access.
func MakeDirectoryReadOnly(t *testing.T, path string) {
	t.Helper()
	makeDirectoryReadOnly(t, path)
}

// AssertOwnerPrivate verifies that path is accessible only to its owner:
// mode 0600 for files and 0700 for directories on POSIX, and a protected
// owner-only DACL granting full control on Windows.
func AssertOwnerPrivate(t *testing.T, path string) {
	t.Helper()
	assertOwnerPrivate(t, path)
}

// AssertOwnerReadOnly verifies that path is restricted to read-only access
// for its owner: mode 0500 for directories and 0400 for files on POSIX, and
// write denial on Windows.
func AssertOwnerReadOnly(t *testing.T, path string) {
	t.Helper()
	assertOwnerReadOnly(t, path)
}
