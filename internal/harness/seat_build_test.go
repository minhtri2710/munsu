package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// seatBuildDirOf is the seat build dir of the home a <home>/state/<name> dst
// sits in.
func seatBuildDirOf(dst string) string { return SeatBuildDir(filepath.Dir(filepath.Dir(dst))) }

// A seat dir already at dst is kept only when it is a real directory, and
// munsu writes nothing into it. A symlink or another non-directory there would
// hand the seat's harness whatever it points at, so the launch refuses and
// nothing is written through it. Both builders share the rule.
func TestSeatDirIsKeptOnlyAsARealDir(t *testing.T) {
	builders := []struct {
		name  string
		build func(t *testing.T) (dst string, build func() error)
	}{
		{"claude", func(t *testing.T) (string, func() error) {
			c, dst := claudeConfigFixture(t)
			return dst, func() error { return BuildClaudeConfigDir(c, seatBuildDirOf(dst), dst) }
		}},
		{"pi", func(t *testing.T) (string, func() error) {
			_, src, dst := agentDirFixture(t)
			return dst, func() error { return BuildPiAgentDir(src, seatBuildDirOf(dst), dst) }
		}},
	}
	rows := []struct {
		name    string
		plant   func(t *testing.T, dst, victim string)
		refused bool
	}{
		{"a real dir is kept untouched", func(t *testing.T, dst, _ string) {
			mustDo(t, os.Mkdir(dst, 0o700))
			mustDo(t, os.WriteFile(filepath.Join(dst, "live"), nil, 0o600))
		}, false},
		{"a symlink to a dir refuses", func(t *testing.T, dst, victim string) {
			mustDo(t, os.Symlink(victim, dst))
		}, true},
		{"a regular file refuses", func(t *testing.T, dst, _ string) {
			mustDo(t, os.WriteFile(dst, []byte("x"), 0o600))
		}, true},
	}
	for _, b := range builders {
		for _, r := range rows {
			t.Run(b.name+"/"+r.name, func(t *testing.T) {
				dst, build := b.build(t)
				victim := filepath.Join(t.TempDir(), "victim")
				mustDo(t, os.Mkdir(victim, 0o700))
				r.plant(t, dst, victim)
				err := build()
				if !r.refused {
					if err != nil {
						t.Fatal(err)
					}
					if names := dirNames(t, dst); !reflect.DeepEqual(names, []string{"live"}) {
						t.Fatalf("existing dir was rebuilt: %v", names)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), dst+" is a symlink or not a directory; remove it, then respawn") {
					t.Fatalf("err = %v, want the refusal naming %s and the fix", err, dst)
				}
				if names := dirNames(t, victim); len(names) != 0 {
					t.Fatalf("the build wrote through the planted link: %v", names)
				}
			})
		}
	}
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
