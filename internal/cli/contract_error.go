package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// WriteContractError writes a structured contract error and returns its exit code.
// Non-contract errors are automatically wrapped as generic operation errors.
func WriteContractError(writer io.Writer, err error, args []string) int {
	var partial *LifecyclePartialError
	if errors.As(err, &partial) {
		contractErr := &contractError{
			status: exitOperation,
			value: ErrorResponse{
				SchemaVersion: SchemaVersion,
				Kind:          "error",
				Status:        "error",
				Error: ErrorEnvelope{
					ErrorCode: "conflict",
					Retryable: false,
					Action:    "Inspect the authoritative task state before retrying the .status projection",
					Message:   err.Error(),
				},
			},
		}
		return writeContractError(writer, contractErr, args)
	}

	var contractErr *contractError
	if !errors.As(err, &contractErr) {
		// Wrap non-contract errors as generic operation errors
		contractErr = &contractError{
			status: exitOperation,
			value: ErrorResponse{
				SchemaVersion: SchemaVersion,
				Kind:          "error",
				Status:        "error",
				Error: ErrorEnvelope{
					ErrorCode: "error",
					Action:    "See the error message and retry",
					Message:   err.Error(),
				},
			},
		}
	}
	return writeContractError(writer, contractErr, args)
}

func writeContractError(writer io.Writer, contractErr *contractError, args []string) int {
	output := OutputTOON
	for index, arg := range args {
		if arg == "--output=json" {
			output = OutputJSON
		}
		if arg == "--output" && index+1 < len(args) && args[index+1] == OutputJSON {
			output = OutputJSON
		}
	}
	var value any = contractErr.value
	if contractErr.response != nil {
		value = contractErr.response
	}
	encoded, encodeErr := Encode(value, output)
	if encodeErr != nil {
		return 1
	}
	_, _ = fmt.Fprintln(writer, strings.TrimSpace(encoded))
	return contractErr.status
}
