package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
)

type sessionBoundCapture struct {
	resolve func(string, map[string]string) (backend.Backend, string, error)
}

func (c sessionBoundCapture) Capture(homeDir string, meta map[string]string, lines int) (string, error) {
	handle := meta["window"]
	if homeDir == "" || handle == "" {
		return "", fmt.Errorf("bound capture identity is incomplete")
	}
	resolve := c.resolve
	if resolve == nil {
		resolve = backend.BackendForTask
	}
	bk, name, err := boundBackend(homeDir, meta, resolve)
	if err != nil {
		return "", err
	}
	switch name {
	case "tmux", "herdr", "zellij", "cmux", "orca":
	default:
		return "", fmt.Errorf("unsupported bound backend %q", name)
	}
	return bk.Capture(handle, lines)
}
