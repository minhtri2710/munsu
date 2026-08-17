// The func column names a method the way .github/deadcode.allow does, so one
// name means one thing in both files. A guard inside a closure is attributed to
// the enclosing top-level function -- a closure has no stable name.
package fixture

import "fmt"

type Runner struct{}

type Store[T any] struct{}

func (r Runner) Value(n int) error {
	if n < 0 {
		return fmt.Errorf("negative")
	}
	return nil
}

func (r *Runner) Pointer(n int) error {
	if n < 0 {
		return fmt.Errorf("negative")
	}
	return nil
}

func (s *Store[T]) Generic(n int) error {
	if n < 0 {
		return fmt.Errorf("negative")
	}
	return nil
}

func Outer(ns []int) func() error {
	return func() error {
		for _, n := range ns {
			if n < 0 {
				return fmt.Errorf("negative")
			}
		}
		return nil
	}
}
