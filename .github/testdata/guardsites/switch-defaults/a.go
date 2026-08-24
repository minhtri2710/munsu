package fixture

import (
	"errors"
	"fmt"
)

func Expression(n int) error {
	switch n {
	case 1:
		return nil
	default:
		return fmt.Errorf("unsupported value %d", n)
	}
}

func Type(v any) error {
	switch v.(type) {
	case string:
		return nil
	default:
		return errors.New("unsupported type")
	}
}

func Fallback(n int) int {
	switch n {
	default:
		return 0
	}
}

func Bare(n int) error {
	switch n {
	default:
		return nil
	}
}

func Propagate(err error) error {
	switch err {
	default:
		return err
	}
}

func TaglessError(err error) error {
	switch {
	case err == nil:
		return nil
	default:
		return fmt.Errorf("operation failed: %w", err)
	}
}
