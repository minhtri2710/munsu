package config

import "fmt"

type RemediationCode string

const (
	RemediateUnknownProject       RemediationCode = "unknown-project"
	RemediateInvalidProfile       RemediationCode = "invalid-profile"
	RemediateIncompatibleSnapshot RemediationCode = "incompatible-snapshot"
)

type RemediationError struct {
	Code   RemediationCode
	Repair string
	Err    error
}

func (e *RemediationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Repair)
	}
	return fmt.Sprintf("%s: %v; repair: %s", e.Code, e.Err, e.Repair)
}

func (e *RemediationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func Remediate(code RemediationCode, repair string, err error) error {
	return &RemediationError{Code: code, Repair: repair, Err: err}
}
