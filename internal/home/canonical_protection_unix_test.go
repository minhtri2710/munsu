//go:build !windows

package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The home-paths table: a path is trusted with durable records only while it is
// owner-private and the shape it was asked about. Rows with an empty wantSub are
// the nearest valid case of the row above them and must be accepted.
func TestHomePathsRefuseWhatIsNotOwnerPrivate(t *testing.T) {
	openWidened := func(name string) func(t *testing.T) error {
		return func(t *testing.T) error {
			h := newTestHome(t)
			if err := os.Chmod(filepath.Join(h.Root(), name), 0755); err != nil {
				t.Fatal(err)
			}
			_, err := Open(h.Root())
			return err
		}
	}
	verifyEntry := func(mode os.FileMode, isDir, wantDir bool) func(t *testing.T) error {
		return func(t *testing.T) error {
			path := filepath.Join(t.TempDir(), "entry")
			if isDir {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			return verifyProtection(path, wantDir)
		}
	}
	for _, tc := range []struct {
		name    string
		run     func(t *testing.T) error
		wantSub string
	}{
		{"Open on a fresh home", func(t *testing.T) error {
			_, err := Open(newTestHome(t).Root())
			return err
		}, ""},
		{"Open with a widened journal directory", openWidened(JournalDirName), JournalDirName + " is not owner-private (mode 755)"},
		{"Open with a widened lock directory", openWidened(LockDirName), LockDirName + " is not owner-private (mode 755)"},
		{"an owner-private file", verifyEntry(0600, false, false), ""},
		{"a file granting group and other read", verifyEntry(0644, false, false), "is not owner-private (mode 644)"},
		{"an owner-private directory", verifyEntry(0700, true, true), ""},
		{"a file where a directory is required", verifyEntry(0600, false, true), "is not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("refused the valid case: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted the refused case")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error = %v, want the %q refusal", err, tc.wantSub)
			}
		})
	}
}
