package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/minhtri2710/munsu/internal/fleet"
)

// RunCaptainSeatRequest runs args through the captain's seat when this munsu
// runs inside a fenced captain and args name a seat command, and reports the
// exit code; ok is false when the command runs here.
func RunCaptainSeatRequest(args []string, stderr io.Writer) (code int, ok bool) {
	socket := os.Getenv(fleet.CaptainSeatEnv)
	if socket == "" || !fleet.CaptainSeatRuns(args) {
		return 0, false
	}
	code, err := fleet.ForwardCaptainSeat(socket, args)
	if err != nil {
		fmt.Fprintf(stderr, "munsu: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code, true
}
