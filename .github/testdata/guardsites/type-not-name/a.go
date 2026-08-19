// Whether a guard is a refusal is decided by the type of the expression it
// tests, not by the spelling of an identifier in it. Both directions are
// pinned here: a non-error named like an error is measured, and an
// error-typed expression is not, whatever it is called.
package fixture

import (
	"context"
	"errors"
	"fmt"
)

// e is a string. The name heuristic dropped every guard in a function whose
// parameter was called e; this one is a real refusal.
func Endpoint(e string) error {
	if e == "" {
		return errors.New("empty endpoint")
	}
	return nil
}

// senderRank and errs both contain "err" when lowercased and neither is an
// error. The substring clause dropped far more sites than the e clause did.
func Transfer(senderRank int, errs []string) error {
	if senderRank < 0 {
		return fmt.Errorf("rank %d", senderRank)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d problems", len(errs))
	}
	return nil
}

// Error-typed values propagate whatever they are named: neither guard is a
// refusal this file owns.
func Propagate(e error, boom error) error {
	if e != nil {
		return e
	}
	if boom != nil {
		return boom
	}
	return nil
}

// No identifier in this guard is error-typed -- the call expression is. A
// resolver keyed by identifier readmits it; one keyed by expression span
// does not.
func Cancelled(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}
