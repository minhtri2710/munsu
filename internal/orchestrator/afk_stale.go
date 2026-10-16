package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
)

// ClearStaleArtifacts removes a prior session's state/.afk-digest that was
// never consumed, except one holding report refs: those entries are the only
// copy of the digested report wakes, so it is kept and the next `munsu afk
// return` drains it (the digester appends to it meanwhile) and settles them.
//
// Only operates within the given home directory.
func ClearStaleArtifacts(homeDir string) error {
	path := filepath.Join(homeDir, digestFile)
	if digestHoldsRefs(path) {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale artifact .afk-digest: %w", err)
	}
	return nil
}

// digestHoldsRefs reports whether the digest file at path carries a report ref.
// An unreadable or unparseable file holds none this code can use.
func digestHoldsRefs(path string) bool {
	be, err := readDigestFile(path)
	if err != nil || be == nil {
		return false
	}
	for _, entry := range be.Entries {
		if entry.Ref != "" {
			return true
		}
	}
	return false
}
