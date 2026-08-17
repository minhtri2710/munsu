// Reacting to an error somebody else produced. Every branch here refuses in
// shape, and none of them is self-originating, so the set must stay empty.
package fixture

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
)

func Plain() error {
	if err := run(); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}

func Field(r result) error {
	if r.Err != nil {
		return fmt.Errorf("result: %w", r.Err)
	}
	return nil
}

func ByHelper(path string) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("no %s", path)
	}
	return nil
}

func ByErrors(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("gone: %w", err)
	}
	return nil
}

// The condition here says only `ok`. The init statement is what makes it error
// handling, which is why the init statement is read too.
func ByAssertion(err error) error {
	if ee, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("exit: %s", ee.Stderr)
	}
	return nil
}

type result struct{ Err error }

func run() error { return nil }
