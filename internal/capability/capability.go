// Package capability defines typed states for capability expressions.
// Each state models the lifecycle of an external capability that the
// orchestrator may depend on.
package capability

import "fmt"

// State represents the availability of an external capability.
type State int

const (
	// Absent means the capability is not present but could be installed.
	Absent State = iota
	// Unsupported means the capability is not available and cannot be made so.
	Unsupported
	// Ready means the capability is present and functional.
	Ready
	// Failed means the capability is present but encountered an error.
	Failed
)

var stateNames = map[State]string{
	Absent:      "absent",
	Unsupported: "unsupported",
	Ready:       "ready",
	Failed:      "failed",
}

// String returns the lowercase string representation of the state.
func (s State) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// MarshalText implements encoding.TextMarshaler.
func (s State) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *State) UnmarshalText(text []byte) error {
	switch string(text) {
	case "absent":
		*s = Absent
	case "unsupported":
		*s = Unsupported
	case "ready":
		*s = Ready
	case "failed":
		*s = Failed
	default:
		return fmt.Errorf("capability: unknown state %q", string(text))
	}
	return nil
}

// GoString implements fmt.GoStringer for readable debug output.
func (s State) GoString() string {
	return s.String()
}
