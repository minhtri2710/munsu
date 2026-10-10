package fleet

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// PreflightResult captures the result of a delivery mode preflight check.
type PreflightResult struct {
	Mode     string  `json:"mode"`
	Feasible bool    `json:"feasible"`
	Checks   []Check `json:"checks"`
}

// Check represents a single preflight check result.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Preflight verifies that the requested delivery mode is feasible in the
// current environment. repoPath is optional (empty string skips
// project-local checks such as has-remote). Returns a PreflightResult with
// individual check results; callers should inspect Feasible to decide
// whether to proceed.
func Preflight(mode, repoPath string, forge taskauthority.DeliveryStep) (*PreflightResult, error) {
	switch mode {
	case "no-mistakes":
		return &PreflightResult{Mode: mode, Feasible: true}, nil
	case "direct-PR":
		return preflightDirectPR(repoPath, forge)
	case "local-only":
		return preflightLocalOnly()
	default:
		return nil, fmt.Errorf("unknown delivery mode: %q", mode)
	}
}

func preflightDirectPR(repoPath string, forge taskauthority.DeliveryStep) (*PreflightResult, error) {
	state := probeConfiguredForge(toolEntryOf(forge))
	if state != backend.Ready {
		return nil, fmt.Errorf("forge adapter %s probe %s: configured forge is not Ready", forge.Adapter, state)
	}
	checks := []Check{{Name: "forge-tool", OK: true, Detail: fmt.Sprintf("%s is Ready", forge.Adapter)}}
	if repoPath != "" && forge.Adapter == "github" {
		checks = append(checks, checkHasRemote(repoPath))
	}
	result := &PreflightResult{Mode: "direct-PR", Checks: checks}
	result.Feasible = allOK(checks)
	return result, nil
}

func preflightLocalOnly() (*PreflightResult, error) {
	checks := []Check{
		{Name: "git-configured", OK: true, Detail: "local-only always feasible"},
	}
	return &PreflightResult{Mode: "local-only", Feasible: true, Checks: checks}, nil
}

func checkHasRemote(repoPath string) Check {
	cmd := exec.Command("git", "-C", repoPath, "remote", "-v")
	out, err := cmd.Output()
	if err != nil {
		return Check{
			Name:   "has-remote",
			OK:     false,
			Detail: fmt.Sprintf("git remote failed: %v", err),
		}
	}
	if strings.TrimSpace(string(out)) == "" {
		return Check{
			Name:   "has-remote",
			OK:     false,
			Detail: fmt.Sprintf("no git remotes configured in %s", repoPath),
		}
	}
	return Check{Name: "has-remote", OK: true, Detail: "remote configured"}
}

func allOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}
