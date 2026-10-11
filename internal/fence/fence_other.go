//go:build !darwin

package fence

import "runtime"

// Supported returns *UnsupportedError: this GOOS has no write fence.
func Supported() error { return &UnsupportedError{GOOS: runtime.GOOS} }
