package fsaccess

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type UnsupportedFixtureError struct {
	Operation string
}

func (e *UnsupportedFixtureError) Error() string {
	return fmt.Sprintf("access-control fixture unsupported for %s", e.Operation)
}

func IsUnsupportedFixture(err error) bool {
	var unsupported *UnsupportedFixtureError
	return errors.As(err, &unsupported)
}

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
