package fixture

import (
	"errors"
)

var Direct = func(ok bool) error {
	if !ok {
		return errors.New("direct refusal")
	}
	return nil
}

var Switch = func(value int) error {
	switch value {
	default:
		return errors.New("switch refusal")
	}
}

var NonRefusal = func(ok bool) error {
	if !ok {
		return nil
	}
	return nil
}

const IgnoredConst = 1

type IgnoredType int

func pair() (int, int) { return 1, 2 }

var NotFunction = 1

var PairA, PairB = pair()

var Explicit func(bool) error = func(ok bool) error {
	if !ok {
		return errors.New("explicit refusal")
	}
	return nil
}

var Parenthesized = (func(ok bool) error {
	if !ok {
		return errors.New("parenthesized refusal")
	}
	return nil
})

var (
	Grouped = func(ok bool) error {
		if !ok {
			return errors.New("grouped refusal")
		}
		return nil
	}
	First, Second = func(ok bool) error {
		if !ok {
			return errors.New("first refusal")
		}
		return nil
	}, func(ok bool) error {
		if !ok {
			return errors.New("second refusal")
		}
		return nil
	}
	NoInitializer func(bool) error
	_             = func(ok bool) error {
		if !ok {
			return errors.New("blank refusal")
		}
		return nil
	}
)
