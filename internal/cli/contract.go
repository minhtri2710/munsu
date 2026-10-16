package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const (
	exitOperation = 1
	exitUsage     = 2
)

type contractError struct {
	status int
	value  ErrorResponse
	// response, when set, is written instead of value: a command result
	// whose own Status is "error".
	response any
}

func (err *contractError) Error() string {
	return err.value.Error.Message
}

func usageError(code, action, message string) error {
	return &contractError{
		status: exitUsage,
		value: ErrorResponse{
			SchemaVersion: SchemaVersion,
			Kind:          "error",
			Status:        "error",
			Error: ErrorEnvelope{
				ErrorCode: code,
				Action:    action,
				Message:   message,
			},
		},
	}
}

func operationError(code, action, message string) error {
	return &contractError{
		status: exitOperation,
		value: ErrorResponse{
			SchemaVersion: SchemaVersion,
			Kind:          "error",
			Status:        "error",
			Error: ErrorEnvelope{
				ErrorCode: code,
				Action:    action,
				Message:   message,
			},
		},
	}
}

// configureContractCommand gives a contract command its --output toon|json flag
// (toon is the default); an unknown flag or an unsupported output format is a
// usage error.
func configureContractCommand(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError("unknown_flag", fmt.Sprintf("Run `%s --help` (Expected syntax: %s %s)", commandPath(cmd), commandPath(cmd), cmd.Use), strings.ReplaceAll(err.Error(), "error: ", ""))
	})
	cmd.Flags().String("output", OutputTOON, "Output format (toon|json)")
}

func contractArgs(count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != count {
			return usageError("invalid_argument", fmt.Sprintf("Run `%s --help`", commandPath(cmd)), fmt.Sprintf("%s requires %d argument(s)", commandPath(cmd), count))
		}
		return nil
	}
}

func contractNoArgs(cmd *cobra.Command, args []string) error {
	return contractArgs(0)(cmd, args)
}

func contractOutput(cmd *cobra.Command) (string, error) {
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return "", usageError("invalid_argument", fmt.Sprintf("Run `%s --help`", commandPath(cmd)), "Unable to read --output")
	}
	if output != OutputTOON && output != OutputJSON {
		return "", usageError("unsupported_input", fmt.Sprintf("Run `%s --output toon` or `%s --output json`", commandPath(cmd), commandPath(cmd)), fmt.Sprintf("Unsupported output format %q", output))
	}
	return output, nil
}

// writeContractResult writes resp, or returns it as an operation error when
// its Status is "error" so the command exits nonzero.
func writeContractResult[T any](cmd *cobra.Command, resp Response[T]) error {
	if resp.Status != "error" {
		return writeContract(cmd, resp)
	}
	if _, err := contractOutput(cmd); err != nil {
		return err
	}
	return &contractError{
		status:   exitOperation,
		response: resp,
		value:    ErrorResponse{Error: ErrorEnvelope{Message: strings.TrimSpace(fmt.Sprintf("%s failed: %s", resp.Kind, strings.Join(resp.Help, "; ")))}},
	}
}

func writeContract(cmd *cobra.Command, value any) error {
	output, err := contractOutput(cmd)
	if err != nil {
		return err
	}
	encoded, err := Encode(value, output)
	if err != nil {
		return operationError("internal", "Run the command again", "Unable to format the command result")
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), encoded)
	return err
}

func commandPath(cmd *cobra.Command) string {
	return strings.ReplaceAll(cmd.CommandPath(), "munsu munsu", "munsu")
}
