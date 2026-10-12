package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

// The seat build dir must be a real directory the current user owns; a
// symlink or a file there refuses the build and nothing is written through it.
// The allow row is the nearest valid case: the dir exists already.
func TestSeatBuildDirMustBeARealDir(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plant   func(t *testing.T, build, victim string)
		refused bool
	}{
		{"an existing real dir builds", func(t *testing.T, build, _ string) { mustDo(t, os.Mkdir(build, 0o700)) }, false},
		{"a symlink refuses", func(t *testing.T, build, victim string) { mustDo(t, os.Symlink(victim, build)) }, true},
		{"a regular file refuses", func(t *testing.T, build, _ string) { mustDo(t, os.WriteFile(build, nil, 0o600)) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, dst := claudeConfigFixture(t)
			build := seatBuildDirOf(dst)
			victim := filepath.Join(t.TempDir(), "victim")
			mustDo(t, os.Mkdir(victim, 0o700))
			tc.plant(t, build, victim)
			err := BuildClaudeConfigDir(c, build, dst)
			if !tc.refused {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "seat build dir "+build+" is not a directory the current user owns; remove it, then respawn") {
				t.Fatalf("err = %v, want the refusal naming %s", err, build)
			}
			if names := dirNames(t, victim); len(names) != 0 {
				t.Fatalf("the build wrote through the planted link: %v", names)
			}
			if _, err := os.Lstat(dst); !os.IsNotExist(err) {
				t.Fatalf("%s exists after a refused build: %v", dst, err)
			}
		})
	}
}

// A build removes the seat build dir's stale entries, which only a crashed
// build or preflight leaves, and keeps a fresh one, which may be another build
// in progress, of the same task included: each build has a name of its own.
func TestSeatBuildRemovesOnlyStaleEntries(t *testing.T) {
	_, src, dst := agentDirFixture(t)
	build := seatBuildDirOf(dst)
	stale, fresh := filepath.Join(build, "T-9.pi-agent-1"), filepath.Join(build, filepath.Base(dst))
	mustDo(t, os.MkdirAll(stale, 0o700))
	mustDo(t, os.WriteFile(filepath.Join(stale, "settings.json"), nil, 0o600))
	mustDo(t, os.Mkdir(fresh, 0o700))
	old := time.Now().Add(-seatBuildStaleAge - time.Minute)
	mustDo(t, os.Chtimes(stale, old, old))
	if err := BuildPiAgentDir(src, build, dst); err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, build); !reflect.DeepEqual(names, []string{filepath.Base(fresh)}) {
		t.Fatalf("seat build dir = %v, want only the fresh entry %s", names, filepath.Base(fresh))
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
