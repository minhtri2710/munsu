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
		return preflightNoMistakes(forge)
	case "direct-PR":
		return preflightDirectPR(repoPath, forge)
	case "local-only":
		return preflightLocalOnly()
	default:
		return nil, fmt.Errorf("unknown delivery mode: %q", mode)
	}
}

// preflightNoMistakes re-probes the captured forge: no-mistakes delivers
// through it, so a forge that is not Ready must refuse before any mutation.
func preflightNoMistakes(forge taskauthority.DeliveryStep) (*PreflightResult, error) {
	check, err := checkConfiguredForge(forge)
	if err != nil {
		return nil, err
	}
	return &PreflightResult{Mode: "no-mistakes", Feasible: true, Checks: []Check{check}}, nil
}

func preflightDirectPR(repoPath string, forge taskauthority.DeliveryStep) (*PreflightResult, error) {
	check, err := checkConfiguredForge(forge)
	if err != nil {
		return nil, err
	}
	checks := []Check{check}
	if repoPath != "" {
		checks = append(checks, checkHasRemote(repoPath))
	}
	result := &PreflightResult{Mode: "direct-PR", Checks: checks}
	result.Feasible = allOK(checks)
	return result, nil
}

// checkConfiguredForge refuses a captured forge whose probe is not Ready. Every
// mode that delivers through the forge shares it, so the refusal is one rule.
func checkConfiguredForge(forge taskauthority.DeliveryStep) (Check, error) {
	state := probeConfiguredForge(toolEntryOf(forge))
	if state != backend.Ready {
		return Check{}, fmt.Errorf("forge adapter %s probe %s: configured forge is not Ready", forge.Adapter, state)
	}
	return Check{Name: "forge-tool", OK: true, Detail: fmt.Sprintf("%s is Ready", forge.Adapter)}, nil
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
