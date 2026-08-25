package fixture

import (
	"errors"
)

func validationError(message string) error { return errors.New(message) }

var Direct = func(ok bool) error {
	if !ok {
		return errors.New("direct refusal")
	}
	return nil
}

var Helper = func(ok bool) error {
	if !ok {
		return validationError("helper refusal")
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

var SwitchHelper = func(value int) error {
	switch value {
	default:
		return validationError("switch helper refusal")
	}
}

var TypeSwitchHelper = func(value any) error {
	switch value.(type) {
	default:
		return validationError("type switch helper refusal")
	}
}

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
