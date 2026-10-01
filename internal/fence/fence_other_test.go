//go:build !darwin

package fence

import (
	"errors"
	"runtime"
	"testing"
)

func TestNewRefusesOffDarwinWithTheGOOS(t *testing.T) {
	l := newLayout(t)
	for _, launch := range []Launch{l.soldier(), l.reviewer()} {
		f, err := New(launch)
		var unsupported *UnsupportedError
		if f != nil || !errors.As(err, &unsupported) || unsupported.GOOS != runtime.GOOS {
			t.Fatalf("New(%s) = %v, %v; want *UnsupportedError for %s", launch.Role, f, err, runtime.GOOS)
		}
	}
}
