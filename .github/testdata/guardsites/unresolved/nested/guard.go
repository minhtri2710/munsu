package nested

import "errors"

// e is a string, so recognizing this refusal needs type info. The fixture's
// point is that the walk finds this file while nothing under the root loads
// it: the resolver type-checks nothing, and the derived set would silently
// fall back to the name heuristic -- the state the zero-spans guard exists to
// make fatal.
func Endpoint(e string) error {
	if e == "" {
		return errors.New("empty endpoint")
	}
	return nil
}
