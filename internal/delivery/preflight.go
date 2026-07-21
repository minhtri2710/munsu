package delivery

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/minhtri2710/munsu/internal/capability"
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
func Preflight(mode, repoPath string) (*PreflightResult, error) {
	switch mode {
	case "no-mistakes":
		return preflightNoMistakes()
	case "direct-PR":
		return preflightDirectPR(repoPath)
	case "local-only":
		return preflightLocalOnly()
	default:
		return nil, fmt.Errorf("unknown delivery mode: %q", mode)
	}
}

func preflightNoMistakes() (*PreflightResult, error) {
	checks := []Check{
		checkNoMistakesBinary(),
	}
	// Only check version/compatibility if binary is found.
	if checks[0].OK {
		probe := NoMistakesProbe()
		if probe.State != capability.Ready {
			checks = append(checks, Check{
				Name:   "no-mistakes-compat",
				OK:     false,
				Detail: probe.Detail,
			})
		}
	}
	result := &PreflightResult{Mode: "no-mistakes", Checks: checks}
	result.Feasible = allOK(checks)
	return result, nil
}

func preflightDirectPR(repoPath string) (*PreflightResult, error) {
	checks := []Check{
		checkGhAuth(),
	}
	if repoPath != "" {
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

func checkNoMistakesBinary() Check {
	_, err := exec.LookPath("no-mistakes")
	if err != nil {
		return Check{
			Name:   "no-mistakes-binary",
			OK:     false,
			Detail: "no-mistakes not on PATH; run 'go install github.com/kunchenguid/no-mistakes@latest'",
		}
	}
	return Check{Name: "no-mistakes-binary", OK: true, Detail: "found on PATH"}
}

func checkGhAuth() Check {
	// Check gh-axi availability first (consolidated authority path)
	if _, err := exec.LookPath("gh-axi"); err == nil {
		return Check{Name: "gh-auth", OK: true, Detail: "gh-axi available on PATH"}
	}
	// Fall back to gh auth status check
	cmd := exec.Command("gh", "auth", "status")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Check{
			Name:   "gh-auth",
			OK:     false,
			Detail: fmt.Sprintf("gh auth failed: %s", strings.TrimSpace(string(out))),
		}
	}
	return Check{Name: "gh-auth", OK: true, Detail: "authenticated"}
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
