//go:build !windows

package fence

import (
	"os"
	"strings"
	"testing"
)

func TestValidateWritableRootsRefusesAnUnsafeDirectory(t *testing.T) {
	l := newLayout(t)
	rootsEnv(t, l.root)
	facts := RootFacts{Home: l.home}
	for _, tt := range []struct {
		name string
		mode os.FileMode
	}{{"group-writable", 0o770}, {"world-writable", 0o707}} {
		t.Run(tt.name, func(t *testing.T) {
			dir := mkRoot(t, t.TempDir()+"/d")
			if err := os.Chmod(dir, tt.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateWritableRoots([]string{dir}, facts); err == nil || !strings.Contains(err.Error(), dir+" is group- or world-writable") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("a symlink", func(t *testing.T) {
		link := l.root + "/link"
		if err := os.Symlink(mkRoot(t, l.root+"/target"), link); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateWritableRoots([]string{link}, facts); err == nil || !strings.Contains(err.Error(), link+" is a symlink") {
			t.Fatalf("err = %v", err)
		}
	})
	// A symlinked parent is not a symlink root: only the re-check after
	// resolution sees where it leads.
	checkout := mkRoot(t, l.root+"/checkout/sub")
	clearDir := mkRoot(t, l.root+"/clear/sub")
	for _, tt := range []struct{ name, target, want string }{
		{"a symlinked parent leading into a checkout", l.root + "/checkout", "overlaps protected path " + l.root + "/checkout"},
		{"a symlinked parent leading to a clear directory", l.root + "/clear", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir() + "/lp"
			if err := os.Symlink(tt.target, parent); err != nil {
				t.Fatal(err)
			}
			got, err := ValidateWritableRoots([]string{parent + "/sub"}, RootFacts{Home: l.home, Checkouts: []string{l.root + "/checkout"}})
			if tt.want == "" {
				if err != nil || len(got) != 1 || got[0] != clearDir {
					t.Fatalf("roots = %q, %v, want the resolved %s", got, err, clearDir)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q (checkout %s)", err, tt.want, checkout)
			}
		})
	}
	t.Run("not owned by the user", func(t *testing.T) {
		if _, err := ValidateWritableRoots([]string{"/usr/lib"}, facts); err == nil || !strings.Contains(err.Error(), "/usr/lib is not owned by the current user") {
			t.Fatalf("err = %v", err)
		}
	})
}
