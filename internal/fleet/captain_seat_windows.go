//go:build windows

package fleet

// captainSeatUnsupportedError is the seat's refusal on a GOOS with no captain
// fence. It is a typed error, as fence.UnsupportedError is, so a refusal is
// a concrete value rather than a fresh fmt.Errorf at each stub.
type captainSeatUnsupportedError struct{}

func (*captainSeatUnsupportedError) Error() string {
	return "captain seat: this GOOS has no captain fence"
}

// ServeCaptainSeat refuses: this GOOS has no fence, so no captain launches
// through a seat.
func ServeCaptainSeat(string, []string) (int, error) {
	return 0, &captainSeatUnsupportedError{}
}

// ForwardCaptainSeat refuses: this GOOS has no fence, so no captain has a
// seat.
func ForwardCaptainSeat(string, []string) (int, error) {
	return 0, &captainSeatUnsupportedError{}
}
