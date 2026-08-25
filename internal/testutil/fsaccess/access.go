package fsaccess

import (
	"fmt"
	"sync"
	"testing"
)

// registerRestore registers a restoration callback and returns an idempotent
// callback callers may invoke before test cleanup. Restoration errors are test
// failures rather than silently leaked access changes.
func registerRestore(t *testing.T, restore func() error) func() error {
	t.Helper()
	var once sync.Once
	var restoreErr error
	restoreOnce := func() error {
		once.Do(func() { restoreErr = restore() })
		return restoreErr
	}
	t.Cleanup(func() {
		if err := restoreOnce(); err != nil {
			t.Errorf("restore filesystem access: %v", err)
		}
	})
	return restoreOnce
}

func invalidPathKind(path string) error {
	return fmt.Errorf("unsupported filesystem path %q", path)
}
