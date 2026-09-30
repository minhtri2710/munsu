//go:build !darwin

package fence

import "runtime"

func platformCheck() error { return &UnsupportedError{GOOS: runtime.GOOS} }
