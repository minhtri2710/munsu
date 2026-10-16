package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type sessionBoundCapture struct {
	resolve boundResolver
}

func (c sessionBoundCapture) Capture(homeDir string, ep taskauthority.EndpointBinding, lines int) (string, error) {
	if homeDir == "" || ep.Handle == "" {
		return "", fmt.Errorf("bound capture identity is incomplete")
	}
	resolve := c.resolve
	if resolve == nil {
		resolve = backend.BackendForTask
	}
	bk, name, err := boundBackend(homeDir, bindingRef(ep), resolve)
	if err != nil {
		return "", err
	}
	switch name {
	case "tmux", "herdr":
	default:
		return "", fmt.Errorf("unsupported bound backend %q", name)
	}
	return bk.Capture(ep.Handle, lines)
}
