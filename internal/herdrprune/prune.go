// Package herdrprune implements the operator prune command for munsu-created
// herdr workspaces that have zero live tabs.
package herdrprune

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/hometag"
)

// PruneOptions controls the prune operation.
type PruneOptions struct {
	// Session is the herdr session name. Default from HERDR_SESSION env or "default".
	Session string
	// Apply closes matching workspaces. When false, only dry-run listing.
	Apply bool
	// HomeDir is the resolved munsu home directory used for hometag and live-task scanning.
	HomeDir string
}

// PruneWorkspace describes one workspace and the action taken.
type PruneWorkspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	TabCount    int    `json:"tab_count"`
	AgentStatus string `json:"agent_status"`
	Action      string `json:"action"` // keep | would_close | closed | skip
	Reason      string `json:"reason,omitempty"`
}

// PruneResult is the aggregate result of a prune run.
type PruneResult struct {
	Total      int              `json:"total"`
	ToClose    int              `json:"to_close"`
	Closed     int              `json:"closed"`
	Workspaces []PruneWorkspace `json:"workspaces"`
}

// herdrWorkspaceListResponse matches the herdr CLI JSON output for workspace list.
type herdrWorkspaceListResponse struct {
	Result struct {
		Workspaces []herdrWorkspaceEntry `json:"workspaces"`
	} `json:"result"`
}

type herdrWorkspaceEntry struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	TabCount    int    `json:"tab_count"`
	AgentStatus string `json:"agent_status"`
}

// herdrCLI runs a herdr CLI command with --session and returns stdout.
func herdrCLI(session string, args ...string) (string, error) {
	bin, err := exec.LookPath("herdr")
	if err != nil {
		return "", fmt.Errorf("herdr: not found on PATH: %w", err)
	}
	fullArgs := append([]string{"--session", session}, args...)
	cmd := exec.Command(bin, fullArgs...)
	cmd.Env = append(os.Environ(), "HERDR_SESSION="+session)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("herdr %v: %s", fullArgs, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("herdr %v: %w", fullArgs, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// denyListedLabel returns true if the label must never be closed.
func denyListedLabel(label string) bool {
	if label == "firstmate" {
		return true
	}
	if strings.HasPrefix(label, "second-") {
		return true
	}
	return false
}

// isLiveAgent returns true if the agent_status indicates a live agent.
func isLiveAgent(status string) bool {
	if status == "" || status == "unknown" || status == "none" {
		return false
	}
	return true
}

// liveWorkspaceIDsFromTaskMeta scans all task meta files in the state directory
// and returns the set of herdr_workspace_id values found.
func liveWorkspaceIDsFromTaskMeta(homeDir string) (map[string]bool, error) {
	stateDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading state dir: %w", err)
	}
	ids := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".meta") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".meta")
		meta, err := readMetaFile(filepath.Join(stateDir, e.Name()))
		if err != nil {
			continue // skip unreadable meta
		}
		if wsID := meta["herdr_workspace_id"]; wsID != "" {
			ids[wsID] = true
		}
		_ = id // meta file read for workspace id extraction only
	}
	return ids, nil
}

// readMetaFile reads a key=value meta file.
func readMetaFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	meta := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return meta, nil
}

// RunPrune executes the prune algorithm and returns the result.
// It does NOT perform any destructive action in dry-run mode (Apply=false).
func RunPrune(opts PruneOptions) (*PruneResult, error) {
	session := opts.Session
	if session == "" {
		session = os.Getenv("HERDR_SESSION")
	}
	if session == "" {
		session = "default"
	}

	// Step 1: Compute hometag for the munsu home.
	tag := hometag.Tag(opts.HomeDir)

	// Step 2: List herdr workspaces.
	out, err := herdrCLI(session, "workspace", "list")
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	var resp herdrWorkspaceListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("parsing workspace list: %w", err)
	}

	// Step 3: Scan live task meta for referenced workspace IDs.
	liveWSIDs, err := liveWorkspaceIDsFromTaskMeta(opts.HomeDir)
	if err != nil {
		liveWSIDs = nil // best-effort: treat as empty
	}

	result := &PruneResult{
		Total: len(resp.Result.Workspaces),
	}
	for _, ws := range resp.Result.Workspaces {
		pw := PruneWorkspace{
			WorkspaceID: ws.WorkspaceID,
			Label:       ws.Label,
			TabCount:    ws.TabCount,
			AgentStatus: ws.AgentStatus,
		}

		// Safety: never close non-hometag-matching labels.
		if ws.Label != tag {
			pw.Action = "keep"
			pw.Reason = fmt.Sprintf("label %q does not match hometag %q", ws.Label, tag)
			result.Workspaces = append(result.Workspaces, pw)
			continue
		}

		// Safety: never close deny-listed labels.
		if denyListedLabel(ws.Label) {
			pw.Action = "skip"
			pw.Reason = "deny-listed label"
			result.Workspaces = append(result.Workspaces, pw)
			continue
		}

		// Safety: never close workspace with live agent.
		if isLiveAgent(ws.AgentStatus) {
			pw.Action = "skip"
			pw.Reason = fmt.Sprintf("live agent (status=%q)", ws.AgentStatus)
			result.Workspaces = append(result.Workspaces, pw)
			continue
		}

		// Safety: never close workspace referenced by live task meta.
		if liveWSIDs[ws.WorkspaceID] {
			pw.Action = "skip"
			pw.Reason = "referenced by live task meta herdr_workspace_id"
			result.Workspaces = append(result.Workspaces, pw)
			continue
		}

		// Only close empty workspaces (tab_count == 0).
		if ws.TabCount > 0 {
			pw.Action = "keep"
			pw.Reason = fmt.Sprintf("has %d tab(s)", ws.TabCount)
			result.Workspaces = append(result.Workspaces, pw)
			continue
		}

		// Prune candidate.
		if opts.Apply {
			_, closeErr := herdrCLI(session, "workspace", "close", ws.WorkspaceID)
			if closeErr != nil {
				pw.Action = "skip"
				pw.Reason = fmt.Sprintf("close failed: %v", closeErr)
			} else {
				pw.Action = "closed"
				result.Closed++
			}
		} else {
			pw.Action = "would_close"
			result.ToClose++
		}
		result.Workspaces = append(result.Workspaces, pw)
	}
	return result, nil
}
