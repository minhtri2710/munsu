package fixture

import (
	"errors"
	"fmt"
	"os"
)

type LifecyclePartialError struct {
	Cause error
}

func (e *LifecyclePartialError) Error() string { return e.Cause.Error() }

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

func NamedType(v any) error {
	switch got := v.(type) {
	case string:
		return nil
	default:
		return fmt.Errorf("unsupported type %T", got)
	}
}

func ErrorCase(v any) error {
	switch v.(type) {
	case error:
		return nil
	default:
		return fmt.Errorf("unsupported")
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

func WrappedError(value int, err error) error {
	switch value {
	default:
		return fmt.Errorf("operation failed: %w", err)
	}
}

func PanicPropagate(value int, err error) error {
	switch value {
	default:
		panic(err)
	}
}

func MixedPropagate(value int, err error) (error, error) {
	switch value {
	default:
		return errors.New("unsupported"), err
	}
}

func KeyedPropagate(value int, err error) error {
	switch value {
	default:
		return &LifecyclePartialError{Cause: err}
	}
}

func KeyedConstruct(value int) error {
	switch value {
	default:
		return &LifecyclePartialError{Cause: errors.New("unsupported")}
	}
}

func ClosurePropagate(value int, err error) error {
	switch value {
	default:
		return fmt.Errorf("operation failed: %s", func() string { return err.Error() }())
	}
}

func EarlierDeclaration(value int, err error) error {
	switch value {
	default:
		reason := err.Error()
		return fmt.Errorf("operation failed: %s", reason)
	}
}

func EarlierAssignment(value int, err error) error {
	reason := ""
	switch value {
	default:
		reason = err.Error()
		return fmt.Errorf("operation failed: %s", reason)
	}
}

func EarlierCall(value int, err error) error {
	switch value {
	default:
		fmt.Sprintf("%v", err)
		return errors.New("unsupported")
	}
}

func EarlierNested(value int, err error) error {
	switch value {
	default:
		if err != nil {
			_ = err
		}
		return errors.New("unsupported")
	}
}

func EmptyBefore(value int) error {
	switch value {
	default:

		return errors.New("unsupported")
	}
}

func IsResult(value int, err error) bool {
	switch value {
	default:
		return errors.Is(errors.New("left"), errors.New("right"))
	}
}

func JoinEmpty(value int) error {
	switch value {
	default:
		return errors.Join()
	}
}

func JoinNil(value int) error {
	switch value {
	default:
		return errors.Join(nil)
	}
}

func JoinConstruct(value int) error {
	switch value {
	default:
		return errors.Join(errors.New("unsupported"))
	}
}

func Sentinel(value int) error {
	switch value {
	default:
		return ErrUnsupported
	}
}

var ErrUnsupported = errors.New("unsupported")

func PanicConstruct(value int) error {
	switch value {
	default:
		panic("unsupported")
	}
}

func ExitDefault(value int) error {
	switch value {
	default:
		os.Exit(1)
	}
	return nil
}
