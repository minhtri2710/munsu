//go:build darwin

package fixture

import "errors"

// Type-checked only in the GOOS=darwin pass, where `e` resolves to string, so
// this refusal is measured; a name-only heuristic would read `e` as an error.
func DarwinEndpoint(e string) error {
	if e == "" {
		return errors.New("empty endpoint")
	}
	return nil
}

// Still not a refusal this file owns, under any GOOS.
func DarwinPropagate(e error) error {
	if e != nil {
		return e
	}
	return nil
}
