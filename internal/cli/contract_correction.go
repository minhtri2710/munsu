package cli

import "fmt"

// CommandCorrectionError is a typed usage error for a removed or renamed
// command form. Command is the exact replacement invocation.
type CommandCorrectionError struct {
	Code    string
	Command string
	Message string
}

func (e *CommandCorrectionError) Error() string {
	return e.Message
}

func exactCommandCorrection(code, command, message string) error {
	return &CommandCorrectionError{Code: code, Command: command, Message: message}
}

func correctionAction(command string) string {
	return fmt.Sprintf("Run `%s`", command)
}
