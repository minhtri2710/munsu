//go:build !windows

package home

import (
	"fmt"
	"os"
)

// secureFile establishes owner-private protection on an already-created file,
// equivalent to Unix 0600. It fails closed if the mode cannot be set. A file
// already at 0600 is left alone: a fenced soldier's fence refuses even a
// same-mode chmod of the lock and state files its status append passes
// through.
func secureFile(path string) error {
	if fi, err := os.Stat(path); err == nil && fi.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0600 {
		return nil
	}
	return os.Chmod(path, 0600)
}

// secureDir establishes owner-private protection on an already-created
// directory, equivalent to Unix 0700. It fails closed if the mode cannot be
// set.
func secureDir(path string) error {
	return os.Chmod(path, 0700)
}

// restrictDir ensures path grants no access to other principals while
// preserving the owner's existing access bits. It is used for pre-existing
// directories whose owner access must not be increased.
func restrictDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Already owner-private: no chmod, which a fenced soldier's fence refuses
	// on state/ even at the same mode (see secureFile).
	if info.Mode()&(0o077|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 {
		return nil
	}
	return os.Chmod(path, info.Mode().Perm()&0o700)
}

// verifyProtection confirms that path is owner-private: no principal other
// than the owner may access it. It fails closed when the guarantee is not
// established or cannot be verified.
func verifyProtection(path string, isDir bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("home: stat for protection check %s: %w", path, err)
	}
	if isDir && !info.IsDir() {
		return fmt.Errorf("home: %s is not a directory", path)
	}
	if info.Mode().Perm()&0o77 != 0 {
		return fmt.Errorf("home: %s is not owner-private (mode %o)", path, info.Mode().Perm())
	}
	return nil
}
