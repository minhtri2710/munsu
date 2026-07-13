// Package session provides the session backend interface and resolution.
//
// The Backend interface abstracts session management operations (creating
// windows, sending keys, capturing output, health checks, and teardown).
// The default resolution (Backend()) returns a tmux-based implementation.
package session

import "fmt"

// Backend defines the operations for managing agent session windows.
type Backend interface {
	// NewWindow creates a new window with the given session and window name.
	// Returns the backend-specific window identifier and any error.
	NewWindow(session, name string) (string, error)

	// SendKeys sends literal text as keystrokes to the identified window/pane.
	SendKeys(windowID, text string) error

	// Capture captures the last N lines of output from the identified window/pane.
	Capture(windowID string, lines int) (string, error)

	// Alive checks whether the identified window/pane still exists.
	Alive(windowID string) bool

	// Teardown destroys the identified window/pane. Errors are suppressed
	// if the window is already gone.
	Teardown(windowID string) error
}

// Default returns the default session backend — currently the tmux adapter.
func Default() Backend {
	return &TmuxBackend{}
}

// ErrNotImplemented is returned when a backend operation is not yet available.
var ErrNotImplemented = fmt.Errorf("not yet implemented")
