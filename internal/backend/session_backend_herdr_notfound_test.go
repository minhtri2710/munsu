package backend

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// TestIsNotFoundErr_ExecTransportFailsClosed proves Go exec/transport
// failures (herdr binary missing from PATH, spawn errors) are backend
// failures and never classify as pane absence. Classifying them as
// ErrPaneNotFound would let a generic backend failure authorize captain
// auto-relaunch.
func TestIsNotFoundErr_ExecTransportFailsClosed(t *testing.T) {
	missingOnPath := fmt.Errorf("herdr: not found on PATH: %w", exec.ErrNotFound)
	if isNotFoundErr(missingOnPath) {
		t.Error("isNotFoundErr matched exec.ErrNotFound transport error; must fail closed")
	}

	var spawnErr error = &exec.Error{Name: "herdr", Err: exec.ErrNotFound}
	if isNotFoundErr(fmt.Errorf("herdr pane get s:p: %w", spawnErr)) {
		t.Error("isNotFoundErr matched *exec.Error spawn failure; must fail closed")
	}

	// Only a structured not-found code is absence; unstructured text that says
	// "not found" is a backend failure.
	if !isNotFoundErr(fmt.Errorf("herdr pane get s:p: {\"error\":{\"code\":\"pane_not_found\",\"message\":\"pane gone\"}}")) {
		t.Error("structured pane_not_found must classify as pane absence")
	}
	if isNotFoundErr(errors.New("pane not found: something")) {
		t.Error("unstructured text must not classify as pane absence")
	}
}
