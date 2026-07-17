package afk

import (
	"os"
	"path/filepath"
	"strings"
)

// captainPaneConfig is the config file path that optionally specifies a hardcoded
// captain pane handle for inject target resolution, relative to homeDir.
const captainPaneConfig = "config/captain-pane"

// ResolveTarget resolves the captain pane handle from the home directory.
// Resolution order:
//  1. config/captain-pane file (first line, trimmed)
//  2. Returns empty strings when nothing is configured (caller may fall through
//     to runtime detection, which is not yet implemented — phase 2.3).
//
// Returns (paneHandle, session, error). paneHandle is in "session:pane_id" format
// for herdr, or a bare pane ID for tmux. Returns empty strings with nil error
// when no target is configured.
func ResolveTarget(homeDir string) (string, string, error) {
	// 1. Try config/captain-pane file.
	path := filepath.Join(homeDir, captainPaneConfig)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil // not configured — not an error
		}
		return "", "", err
	}

	paneHandle := strings.TrimSpace(string(data))
	if paneHandle == "" {
		return "", "", nil
	}

	// Extract session from the handle ("session:pane_id").
	session, _ := splitTargetHandle(paneHandle)
	return paneHandle, session, nil
}

// splitTargetHandle splits a pane handle on the first colon.
// Returns (session, paneID). If no colon is found, returns ("", handle).
func splitTargetHandle(handle string) (string, string) {
	idx := strings.Index(handle, ":")
	if idx < 0 {
		return "", handle
	}
	return handle[:idx], handle[idx+1:]
}
