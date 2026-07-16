package cli

import (
	"fmt"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/spf13/cobra"
)

const (
	exitOperation = 1
	exitUsage     = 2
)

type contractError struct {
	status int
	value  contract.ErrorResponse
}

func (err *contractError) Error() string {
	return err.value.Error.Message
}

func usageError(code, action, message string) error {
	return &contractError{
		status: exitUsage,
		value: contract.ErrorResponse{
			SchemaVersion: contract.SchemaVersion,
			Kind:          "error",
			Status:        "error",
			Error: contract.ErrorEnvelope{
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
		value: contract.ErrorResponse{
			SchemaVersion: contract.SchemaVersion,
			Kind:          "error",
			Status:        "error",
			Error: contract.ErrorEnvelope{
				ErrorCode: code,
				Action:    action,
				Message:   message,
			},
		},
	}
}

func configureContractCommand(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError("unknown_flag", fmt.Sprintf("Run `%s --help`", commandPath(cmd)), strings.ReplaceAll(err.Error(), "error: ", ""))
	})
	cmd.Flags().String("output", contract.OutputTOON, "Output format (toon|json)")
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
	if output != contract.OutputTOON && output != contract.OutputJSON {
		return "", usageError("unsupported_input", fmt.Sprintf("Run `%s --output toon` or `%s --output json`", commandPath(cmd), commandPath(cmd)), fmt.Sprintf("Unsupported output format %q", output))
	}
	return output, nil
}

func writeContract(cmd *cobra.Command, value any) error {
	output, err := contractOutput(cmd)
	if err != nil {
		return err
	}
	encoded, err := contract.Encode(value, output)
	if err != nil {
		return operationError("internal", "Run the command again", "Unable to format the command result")
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), encoded)
	return err
}

func commandPath(cmd *cobra.Command) string {
	return strings.ReplaceAll(cmd.CommandPath(), "munsu munsu", "munsu")
}
