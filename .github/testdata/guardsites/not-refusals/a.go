// Branches that end an operation without refusing it, or do not end it at all.
// None of these is a guard, and none may appear in the derived set.
package fixture

func EarlyNil(cached bool) error {
	if cached {
		return nil
	}
	return nil
}

func EarlyValue(n int) int {
	if n == 0 {
		return 42
	}
	return n
}

func BareReturn(done bool) {
	if done {
		return
	}
}

func FallsThrough(n int) int {
	if n < 0 {
		n = 0
	}
	return n
}

func EmptyBody(n int) int {
	if n < 0 {
	}
	return n
}
