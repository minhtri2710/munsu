// Two refusals on the same predicate in one function. Without the ordinal they
// would share a key, so waiving one would waive both.
package fixture

import "fmt"

func Twice(m map[string]string, a, b string) error {
	if _, ok := m[a]; !ok {
		return fmt.Errorf("unknown %q", a)
	}
	if _, ok := m[b]; !ok {
		return fmt.Errorf("unknown %q", b)
	}
	return nil
}

func Once(m map[string]string, a string) error {
	if _, ok := m[a]; !ok {
		return fmt.Errorf("unknown %q", a)
	}
	return nil
}
