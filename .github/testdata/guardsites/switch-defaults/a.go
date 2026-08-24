package fixture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

type LifecyclePartialError struct {
	Cause error
}

func (e *LifecyclePartialError) Error() string { return e.Cause.Error() }

type Refusal struct{}

func (Refusal) Error() string { return "unsupported" }

func ValueComposite(n int) error {
	switch n {
	default:
		return Refusal{}
	}
}

func PointerComposite(n int) error {
	switch n {
	default:
		return &Refusal{}
	}
}

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

func WrappedMethod(value int, ctx context.Context) error {
	switch value {
	default:
		return fmt.Errorf("operation failed: %w", ctx.Err())
	}
}

func UnknownConstructor(value int) error {
	switch value {
	default:
		return errorFactory()
	}
}

func ParenthesizedConstructor(value int) error {
	switch value {
	default:
		return (errors.New("unsupported"))
	}
}

func ParenthesizedSentinel(value int) error {
	switch value {
	default:
		return (ErrUnsupported)
	}
}

func ParenthesizedImportedSentinel(value int) error {
	switch value {
	default:
		return (io.EOF)
	}
}

func ParenthesizedValueComposite(value int) error {
	switch value {
	default:
		return (Refusal{})
	}
}

func ParenthesizedComposite(value int) error {
	switch value {
	default:
		return (&Refusal{})
	}
}

func ParenthesizedPanic(value int) error {
	switch value {
	default:
		panic((errors.New("unsupported")))
	}
}

func errorFactory() error { return nil }

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

func JoinDirectMixed(value int) error {
	switch value {
	default:
		return errors.Join(nil, errors.New("unsupported"))
	}
}

func JoinDirectPropagate(value int, err error) error {
	switch value {
	default:
		return errors.Join(errors.New("unsupported"), err)
	}
}

func JoinExpandedConstruct(value int) error {
	switch value {
	default:
		return errors.Join([]error{nil, errors.New("unsupported")}...)
	}
}

func JoinExpandedEmpty(value int) error {
	switch value {
	default:
		return errors.Join([]error{}...)
	}
}

func JoinExpandedAllNil(value int) error {
	switch value {
	default:
		return errors.Join([]error{nil, nil}...)
	}
}

func JoinExpandedUnknown(value int, reasons []error) error {
	switch value {
	default:
		return errors.Join(reasons...)
	}
}

func JoinExpandedPropagate(value int, err error) error {
	switch value {
	default:
		return errors.Join([]error{errors.New("unsupported"), err}...)
	}
}

func Sentinel(value int) error {
	switch value {
	default:
		return ErrUnsupported
	}
}

func ImportedSentinel(value int) error {
	switch value {
	default:
		return io.EOF
	}
}

func NestedNoError(value int) error {
	switch value {
	default:
		if value > 0 {
			logNested(value)
		}
		for value > 1 {
			value--
			break
		}
		if value == 0 {
			logNested(value)
		}
		return errors.New("unsupported")
	}
}

func ParameterSentinel(ErrCause error, value int) error {
	switch value {
	default:
		return ErrCause
	}
}

func LocalSentinel(value int) error {
	ErrCause := errors.New("existing")
	switch value {
	default:
		return ErrCause
	}
}

type sentinelHolder struct{ ErrCause error }

func FieldSentinel(holder sentinelHolder, value int) error {
	switch value {
	default:
		return holder.ErrCause
	}
}

func logNested(value int) {}

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
