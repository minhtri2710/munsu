// The root is given RELATIVE to the directory the tool runs from, the way a
// hand caller would give it. `want` pins that the derived set is exactly what
// an absolute-root invocation produces: the instrument must fail closed no
// matter how the caller names the tree. The first guard here is type-dependent
// (e is a string) -- it is the shape that silently vanished when resolver keys
// and walk keys resolved against different bases, so this fixture also pins
// that a relative root does not lose it.
package fixture

import "errors"

func Endpoint(e string) error {
	if e == "" {
		return errors.New("empty endpoint")
	}
	return nil
}

func Retry(attempts int) error {
	if attempts > 3 {
		return errors.New("too many attempts")
	}
	return nil
}
