package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SeatBuildDirName is the hidden dir directly under a munsu home where an
// unfenced munsu builds what a claude or pi process will load: a seat's config
// dir before it is renamed into state/, and the auth preflight's empty config
// dir. No fence root covers it: a soldier writes only the home's state, data,
// .journal and .lock roots, a reviewer none of the home, and the temp roots
// never hold a home. It is wrong the moment a seat can write the home root.
const SeatBuildDirName = ".seat-build"

// SeatBuildDir returns SeatBuildDirName under homeDir. It sits on the same
// filesystem as the home's state/, so a build renames into place atomically.
func SeatBuildDir(homeDir string) string {
	return filepath.Join(homeDir, SeatBuildDirName)
}

// makeSeatBuildDir creates buildDir if it is absent. Its parent is the home
// root, which no seat writes, so nothing a seat planted can be at buildDir.
func makeSeatBuildDir(buildDir string) error {
	if err := os.Mkdir(buildDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}

// installSeatDir builds a seat's dir dst (what names it in errors) with fill,
// in buildDir where no seat writes, then renames it into place, so munsu never
// creates, writes or follows a path inside the seat-writable state/. An
// existing real dir at dst is kept and never written: it may belong to a live
// seat, which can replace its entries. A crash leftover of dst's last build is
// cleared here, before the next build of the same dst.
//
// A symlink or non-directory at dst refuses the launch: the seat's harness
// would load whatever it points at. Human words (domain.Words): grantor the
// Human, channel supervisor-relay:dialog, words=selected, G1085: "Quy tắc
// chung (Recommended): Seat có fence không bao giờ ghi chương trình hay cấu
// hình mà tiến trình ngoài fence sẽ chạy, và không gửi input vào session ngoài
// session của nó."
func installSeatDir(what, buildDir, dst string, fill func(dir string) error) error {
	if fi, err := os.Lstat(dst); err == nil {
		if fi.IsDir() {
			return nil
		}
		return fmt.Errorf("%s %s is a symlink or not a directory; remove it, then respawn", what, dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking %s %s: %w", what, dst, err)
	}
	if err := makeSeatBuildDir(buildDir); err != nil {
		return fmt.Errorf("building %s: %w", what, err)
	}
	tmp := filepath.Join(buildDir, filepath.Base(dst))
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf("clearing %s: %w", tmp, err)
	}
	if err := os.Mkdir(tmp, 0o700); err != nil {
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
