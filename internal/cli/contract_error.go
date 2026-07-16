package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
)

// WriteContractError writes a structured contract error and returns its exit code.
// It returns zero when err is not a contract error.
func WriteContractError(writer io.Writer, err error, args []string) int {
	var contractErr *contractError
	if !errors.As(err, &contractErr) {
		return 0
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
		return 0
	}
	_, _ = fmt.Fprintln(writer, strings.TrimSpace(encoded))
	return contractErr.status
}
