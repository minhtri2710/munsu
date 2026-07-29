package fleet

import "fmt"

// TaskState represents the state of a backlog item.
type TaskState int

const (
	// StateQueued is the default state for new items (" ").
	StateQueued TaskState = iota
	// StateInFlight means the item is being worked on ("-").
	StateInFlight
	// StateBlocked means the item is blocked ("!").
	StateBlocked
	// StateDone means the item is completed ("x").
	StateDone
)

// stateMarkers maps TaskState to file markers.
var stateMarkers = map[TaskState]string{
	StateQueued:   " ",
	StateInFlight: "-",
	StateBlocked:  "!",
	StateDone:     "x",
}

// stateDisplayMarkers maps TaskState to display markers.
var stateDisplayMarkers = map[TaskState]string{
	StateQueued:   "[ ]",
	StateInFlight: "[-]",
	StateBlocked:  "[!]",
	StateDone:     "[x]",
}

// stateNames maps TaskState to human-readable names.
var stateNames = map[TaskState]string{
	StateQueued:   "queued",
	StateInFlight: "in-flight",
	StateBlocked:  "blocked",
	StateDone:     "done",
}

// markerToState maps single-char markers to TaskState.
var markerToState = map[string]TaskState{
	" ": StateQueued,
	"-": StateInFlight,
	"!": StateBlocked,
	"x": StateDone,
}

// nameToState maps lowercase state names to TaskState.
var nameToState = map[string]TaskState{
	"queued":    StateQueued,
	"in-flight": StateInFlight,
	"in_flight": StateInFlight,
	"blocked":   StateBlocked,
	"done":      StateDone,
}

// Marker returns the single-char file marker for the state.
func (s TaskState) Marker() string {
	if m, ok := stateMarkers[s]; ok {
		return m
	}
	return " "
}

// Display returns the display marker (e.g. "[ ]", "[-]").
func (s TaskState) Display() string {
	if m, ok := stateDisplayMarkers[s]; ok {
		return m
	}
	return "[ ]"
}

// String returns the human-readable state name.
func (s TaskState) String() string {
	if n, ok := stateNames[s]; ok {
		return n
	}
	return "queued"
}

// ParseState parses a TaskState from a single-char marker or name string.
func ParseState(s string) (TaskState, error) {
	if st, ok := markerToState[s]; ok {
		return st, nil
	}
	if st, ok := nameToState[s]; ok {
		return st, nil
	}
	return StateQueued, fmt.Errorf("backlog: unknown state %q", s)
}

// CanTransitionTo checks whether a transition from s to to is valid.
// StateDone is terminal (no outgoing transitions).
// All other transitions are allowed, including back to queued (unblock/ready).
func (s TaskState) CanTransitionTo(to TaskState) bool {
	if s == to {
		return true
	}
	if s == StateDone {
		return false
	}
	return true
}

// Item represents a single backlog item.
type Item struct {
	ID          string
	Description string
	State       TaskState
	Kind        string
	Repo        string
}
