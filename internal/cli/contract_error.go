package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
)

// WriteContractError writes a structured contract error and returns its exit code.
// Non-contract errors are automatically wrapped as generic operation errors.
func WriteContractError(writer io.Writer, err error, args []string) int {
	var contractErr *contractError
	if !errors.As(err, &contractErr) {
		// Wrap non-contract errors as generic operation errors
		contractErr = &contractError{
			status: exitOperation,
			value: contract.ErrorResponse{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "error",
				Status:        "error",
				Error: contract.ErrorEnvelope{
					ErrorCode: "error",
					Action:    "See the error message and retry",
					Message:   err.Error(),
				},
			},
		}
	}
	output := contract.OutputTOON
	for index, arg := range args {
		if arg == "--output=json" {
			output = contract.OutputJSON
		}
		if arg == "--output" && index+1 < len(args) && args[index+1] == contract.OutputJSON {
			output = contract.OutputJSON
		}
	}
	encoded, encodeErr := contract.Encode(contractErr.value, output)
	if encodeErr != nil {
		return 1
	}
	_, _ = fmt.Fprintln(writer, strings.TrimSpace(encoded))
	return contractErr.status
}
