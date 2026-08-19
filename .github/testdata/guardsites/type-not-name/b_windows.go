//go:build windows

package fixture

import "errors"

// A single-GOOS load never type-checks this file, so every identifier in it
// falls back to the name heuristic: e reads as an error and this refusal goes
// unmeasured. That is the fail-open the guards lane was measuring itself with.
func WindowsEndpoint(e string) error {
	if e == "" {
		return errors.New("empty endpoint")
	}
	return nil
}

// Still not a refusal this file owns, under any GOOS.
func WindowsPropagate(e error) error {
	if e != nil {
		return e
	}
	return nil
}
