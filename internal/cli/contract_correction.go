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

func correctionAction(command string) string {
	return fmt.Sprintf("Run `%s`", command)
}
