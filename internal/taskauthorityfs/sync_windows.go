//go:build windows

package taskauthorityfs

// syncDir is a no-op on Windows: the OS does not expose directory fsync
// through file handles, and rename durability relies on the filesystem.
// This matches the platform handling in internal/home.
func syncDir(dir string) error {
	return nil
}
