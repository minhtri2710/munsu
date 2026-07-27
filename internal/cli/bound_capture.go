package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/session"
)

type sessionBoundCapture struct {
	resolve func(string, map[string]string) (session.Backend, string, error)
}

func (c sessionBoundCapture) Capture(homeDir string, meta map[string]string, lines int) (string, error) {
	backendName, handle := meta["backend"], meta["window"]
	if homeDir == "" || backendName == "" || handle == "" {
		return "", fmt.Errorf("bound capture identity is incomplete")
	}
	switch backendName {
	case "tmux", "herdr", "zellij", "cmux", "orca":
	default:
		return "", fmt.Errorf("unsupported bound backend %q", backendName)
	}
	if backendName == "herdr" && meta["herdr_session"] != "" {
		handleSession, _ := session.ParseWindow(handle)
		if handleSession != "" && handleSession != meta["herdr_session"] {
			return "", fmt.Errorf("herdr session ownership mismatch")
		}
	}
	resolve := c.resolve
	if resolve == nil {
		resolve = session.BackendForTask
	}
	bk, resolved, err := resolve(homeDir, meta)
	if err != nil {
		return "", err
	}
	if resolved != backendName {
		return "", fmt.Errorf("bound backend resolved as %q", resolved)
	}
	return bk.Capture(handle, lines)
}
