package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SeatBuildDirName is the hidden dir directly under a munsu home where an
// unfenced munsu builds what a claude or pi process will load: a seat's config
// dir before it is renamed into state/, and the auth preflight's empty config
// dir. fence.New denies every write under it to both the soldier and the
// reviewer, after all of its allows, so the dir stays munsu's wherever the home
// lives, a temp root included (pinned by TestSeatDirRefusals).
const SeatBuildDirName = ".seat-build"

// SeatBuildDir returns SeatBuildDirName under homeDir. It sits on the same
// filesystem as the home's state/, so a build renames into place atomically.
func SeatBuildDir(homeDir string) string {
	return filepath.Join(homeDir, SeatBuildDirName)
}

// seatBuildStaleAge is how old an entry of the seat build dir must be before a
// later build removes it. Every entry is a build in progress or a preflight's
// empty config dir; a build links a few entries and writes one small file, and
// a preflight is bounded by claudeAuthProbeTimeout, so an entry this old was
// left by a process that crashed or was killed.
const seatBuildStaleAge = time.Hour

// prepareSeatBuildDir creates buildDir if it is absent, requires it to be a
// real directory the current user owns, and removes its stale entries. The
// fence refuses every seat the name itself, so nothing a seat planted can be
// there; a symlink or another owner refuses the build anyway. No lane runs the
// other-owner branch: making a dir another user owns needs root.
//
// An entry is stale when its modification time is seatBuildStaleAge old. It is
// removed by its literal path from the listing, never through a link
// (os.RemoveAll unlinks a symlink and does not follow it), and a failed removal
// only leaves it for the next build.
func prepareSeatBuildDir(buildDir string) error {
	if err := os.Mkdir(buildDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	fi, err := os.Lstat(buildDir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || !ownedByCurrentUser(fi) {
		return fmt.Errorf("seat build dir %s is not a directory the current user owns; remove it, then respawn", buildDir)
	}
	entries, err := os.ReadDir(buildDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) >= seatBuildStaleAge {
			os.RemoveAll(filepath.Join(buildDir, e.Name()))
		}
	}
	return nil
}

// installSeatDir builds a seat's dir dst (what names it in errors) with fill,
// in a fresh dir under buildDir where no seat writes, then renames it into
// place, so munsu never creates, writes or follows a path inside the
// seat-writable state/. An existing real dir at dst is kept and never written:
// it is the seat's own, from an earlier launch of the same task, because the
// fence refuses every seat any other task's seat dir (TestSeatDirRefusals).
// Rebuilding it would drop the session state a pi agent dir holds.
//
// A symlink or non-directory at dst refuses the launch, fenced or not (an
// unfenced pi loads its dir too): the seat's harness would load whatever it
// points at. Human words (domain.Words): grantor the Human, channel
// supervisor-relay:dialog, words=selected, G1085: "Quy tắc chung
// (Recommended): Seat có fence không bao giờ ghi chương trình hay cấu hình mà
// tiến trình ngoài fence sẽ chạy, và không gửi input vào session ngoài session
// của nó." Supervisor decision G1099 keeps it for unfenced launches.
func installSeatDir(what, buildDir, dst string, fill func(dir string) error) error {
	if fi, err := os.Lstat(dst); err == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("%s %s is a symlink or not a directory; remove it, then respawn", what, dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking %s %s: %w", what, dst, err)
	}
	if err := prepareSeatBuildDir(buildDir); err != nil {
		return fmt.Errorf("building %s: %w", what, err)
	}
	tmp, err := os.MkdirTemp(buildDir, filepath.Base(dst)+"-")
	if err != nil {
		return fmt.Errorf("building %s: %w", what, err)
	}
	if err := fill(tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("installing %s %s: %w", what, dst, err)
	}
	return nil
}

// writeNewFile writes a private file that must not exist yet. O_EXCL fails on
// any existing name, a symlink included, so the write never follows one.
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
