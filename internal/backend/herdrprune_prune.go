package backend

import (
	"encoding/json"
	"fmt"
	"os"
)

// PruneOptions controls the prune operation.
type PruneOptions struct {
	// Session is the herdr session name. Default from HERDR_SESSION env or "default".
	Session string
	// Apply closes matching workspaces. When false, only dry-run listing.
	Apply bool
	// HomeDir is the resolved munsu home directory whose hometag labels the
	// workspaces it owns.
	HomeDir string
	// LiveWorkspaceIDs are the workspaces the home's tasks and captains hold;
	// prune never closes one. The caller reads them from canonical state.
	LiveWorkspaceIDs map[string]bool
	// CaptainHomes are candidate captain home paths whose workspace labels are
	// also owned (and therefore prunable) by this home. The caller supplies and
	// validates these from the canonical Fleet Registry; Backend only derives
	// workspace labels from them and never reads registry state itself.
	CaptainHomes []string
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

// denyListedLabel returns true if the label must never be closed by prune.
// Foreign orchestrators and non-munsu labels are out of scope. Live munsu
// captain/primary workspaces are protected by live-agent and held-workspace checks.
func denyListedLabel(label string) bool {
	switch label {
	case "default":
		return true
	}
	return false
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

	// Step 1: Labels owned by this home (primary tag and WorkspaceTag for captains).
	primaryTag := Hometag(opts.HomeDir)
	ownedLabels := map[string]bool{primaryTag: true, WorkspaceTag(opts.HomeDir): true}
	// Also own the workspace labels of the caller-supplied candidate captain homes.
	for _, captainHome := range opts.CaptainHomes {
		ownedLabels[WorkspaceTag(captainHome)] = true
	}

	// Step 2: List herdr workspaces.
	out, err := runHerdr(backendCommandWorktree, session, []string{"workspace", "list"}, false)
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	var resp herdrWorkspaceListResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("parsing workspace list: %w", err)
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

		// Safety: never close labels not owned by this munsu home topology.
		if !ownedLabels[ws.Label] {
			pw.Action = "keep"
			pw.Reason = fmt.Sprintf("label %q not owned by this home", ws.Label)
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
		if isAgentStatusAlive(ws.AgentStatus) {
			pw.Action = "skip"
			pw.Reason = fmt.Sprintf("live agent (status=%q)", ws.AgentStatus)
			result.Workspaces = append(result.Workspaces, pw)
			continue
		}

		// Safety: never close a workspace a task or captain holds.
		if opts.LiveWorkspaceIDs[ws.WorkspaceID] {
			pw.Action = "skip"
			pw.Reason = "held by a live task or captain endpoint"
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
			_, closeErr := runHerdr(backendCommandWorktree, session, []string{"workspace", "close", ws.WorkspaceID}, false)
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
