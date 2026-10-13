//go:build windows

package fleet

import "fmt"

// ServeCaptainSeat refuses: this GOOS has no fence, so no captain launches
// through a seat.
func ServeCaptainSeat(string, []string) (int, error) {
	return 0, fmt.Errorf("captain seat: this GOOS has no captain fence")
}

// ForwardCaptainSeat refuses: this GOOS has no fence, so no captain has a
// seat.
func ForwardCaptainSeat(string, []string) (int, error) {
	return 0, fmt.Errorf("captain seat: this GOOS has no captain fence")
}
