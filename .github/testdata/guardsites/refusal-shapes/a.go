// Every shape that counts as a refusal. One rule per function.
package fixture

import (
	"errors"
	"fmt"
	"os"
)

var ErrSentinel = errors.New("sentinel")

type badInputError struct{ why string }

func (e *badInputError) Error() string { return e.why }

func Wrapped(identity string) error {
	if identity != "worktree" {
		return fmt.Errorf("refusing %q", identity)
	}
	return nil
}

func Constructed(n int) error {
	if n < 0 {
		return errors.New("negative")
	}
	return nil
}

func Sentinel(ok bool) error {
	if !ok {
		return ErrSentinel
	}
	return nil
}

func Literal(name string) (string, error) {
	if name == "" {
		return "", &badInputError{why: "empty name"}
	}
	return name, nil
}

func Panics(fence uint64) {
	if fence == 0 {
		panic("unfenced write")
	}
}

func Exits(code int) {
	if code > 125 {
		os.Exit(2)
	}
}

func AfterLogging(rank string) error {
	if rank != "captain" {
		fmt.Println("refusing")
		return fmt.Errorf("rank %q may not dispatch", rank)
	}
	return nil
}
