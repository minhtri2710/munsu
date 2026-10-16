package fixture

import (
	"errors"
	"os"
)

func ElseRefuses(kind string) (string, error) {
	if kind == "captain" {
		return "captain", nil
	} else {
		return "", errors.New("managed soldier endpoints cannot spawn")
	}
}

func BothRefuse(kind string) error {
	if kind == "a" {
		return errors.New("a refused")
	} else {
		return errors.New("other refused")
	}
}

func ElseFallsThrough(kind string) string {
	role := ""
	if kind == "captain" {
		return "captain"
	} else {
		role = os.Getenv("MUNSU_ROLE")
	}
	return role
}

func ElseIfChain(kind string) error {
	if kind == "a" {
		return nil
	} else if kind == "b" {
		return nil
	} else {
		return errors.New("unknown kind")
	}
}

func ElsePropagates(kind string, err error) error {
	if kind == "captain" {
		return nil
	} else {
		return err
	}
}

func ElseAfterErrorCondition(err error) error {
	if err == nil {
		return nil
	} else {
		return errors.New("failed")
	}
}
