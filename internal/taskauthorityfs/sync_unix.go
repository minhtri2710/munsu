//go:build !windows

package taskauthorityfs

import (
	"fmt"
	"os"
)

// syncDir fsyncs a directory so a rename or removal inside it is durable.
// On Unix the directory handle itself is synced; Windows has no equivalent
// and syncDir is a no-op there.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening directory %s for sync: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("syncing directory %s: %w", dir, err)
	}
	return nil
}
