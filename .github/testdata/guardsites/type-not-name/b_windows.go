//go:build windows

package fixture

import "errors"

// This file is type-checked in the GOOS=windows pass of the three-GOOS union
// (loadTypes unions linux, darwin and windows), so `e` resolves to string and
// WindowsEndpoint's refusal IS measured. The comment survives here to record
// the old failure mode: under a single-host-GOOS load, or a name-only
// heuristic, `e` read as an error and every branch of this function silently
// vanished from the lane -- the fail-open the fix removes.
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
