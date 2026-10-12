package cli

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/backend"
)

// boundBackend resolves the backend of a task's bound pane and refuses unless
// the task records that backend, the resolver agrees with the record, and a
// herdr handle sits in the session the task records as its owner.
func boundBackend(home string, meta map[string]string, resolve func(string, map[string]string) (backend.Backend, string, error)) (backend.Backend, string, error) {
	recorded := meta["backend"]
	if recorded == "" {
		return nil, "", fmt.Errorf("task does not record the pane's backend")
	}
	bk, name, err := resolve(home, meta)
	if err != nil {
		return nil, "", err
	}
	if name != recorded {
		return nil, "", fmt.Errorf("bound backend resolved as %q, task records %q", name, recorded)
	}
	if name == "herdr" && backend.SessionOwnerMismatch(meta["window"], meta[backend.MetaSessionOwner]) {
		return nil, "", fmt.Errorf("herdr session ownership mismatch")
	}
	return bk, name, nil
}
