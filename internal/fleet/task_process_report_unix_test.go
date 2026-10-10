//go:build darwin || linux

package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeLsof puts an lsof(8) on PATH that runs body, a POSIX sh script, and
// leaves no other tool reachable.
func fakeLsof(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestLsofProcessDetails(t *testing.T) {
	const script = `case "$*" in
*cwd*) printf 'p7\nn/private/tmp/dev\n'; exit 0;;
*) printf 'p7\nn*:8080\nn127.0.0.1:3000\nn[::1]:3000\n'; exit 0;;
esac`
	t.Run("cwd and sorted de-duplicated listening ports", func(t *testing.T) {
		fakeLsof(t, script)
		got := lsofProcessDetails([]int{7, 8})
		if want := (processDetail{cwd: "/private/tmp/dev", ports: "3000,8080"}); got[7] != want {
			t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
		}
		if want := (processDetail{cwd: unknownProcessField, ports: "none"}); got[8] != want {
			t.Fatalf("pid 8 = %+v, want %+v", got[8], want)
		}
	})
	t.Run("lsof finding nothing for a pid means no listeners, cwd unknown", func(t *testing.T) {
		fakeLsof(t, "exit 1")
		got := lsofProcessDetails([]int{7})
		if want := (processDetail{cwd: unknownProcessField, ports: "none"}); got[7] != want {
			t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
		}
	})
	t.Run("a failing lsof leaves every field unknown", func(t *testing.T) {
		fakeLsof(t, "exit 2")
		got := lsofProcessDetails([]int{7})
		if want := (processDetail{cwd: unknownProcessField, ports: unknownProcessField}); got[7] != want {
			t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
		}
	})
	t.Run("an absent lsof leaves every field unknown", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		got := lsofProcessDetails([]int{7})
		if want := (processDetail{cwd: unknownProcessField, ports: unknownProcessField}); got[7] != want {
			t.Fatalf("pid 7 = %+v, want %+v", got[7], want)
		}
	})
}
