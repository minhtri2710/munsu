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
	t.Run("not owned by the user", func(t *testing.T) {
		if _, err := ValidateWritableRoots([]string{"/usr/lib"}, facts); err == nil || !strings.Contains(err.Error(), "/usr/lib is not owned by the current user") {
			t.Fatalf("err = %v", err)
		}
	})
}
