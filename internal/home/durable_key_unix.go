//go:build !windows

package home

// durableKey returns the persisted file stem (no extension) for a logical
// task key (id). On Unix every id that passes validateTaskID is already a
// safe filename, so the durable key is the identity: persisted names on Unix
// are unchanged.
func durableKey(id string) (string, error) {
	return id, nil
}

// reverseDurableKey returns the logical task key for a persisted file stem.
// On Unix the stem is already the logical key.
func reverseDurableKey(stem string) (string, error) {
	return stem, nil
}
