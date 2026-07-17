// Package nostatus reads and parses structured no-mistakes run status at the CLI boundary.
package nostatus

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// Step represents one step in a no-mistakes pipeline run.
type Step struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Findings   int    `json:"findings"`
	DurationMs int    `json:"duration_ms"`
}

// RunStatus holds the parsed state from a no-mistakes axi status call.
type RunStatus struct {
	ID            string `json:"id"`
	Branch        string `json:"branch"`
	Status        string `json:"status"`                  // in_progress or completed
	Head          string `json:"head,omitempty"`          // commit SHA, present on completed runs
	PR            string `json:"pr,omitempty"`            // PR URL, present on completed runs that pushed
	Findings      string `json:"findings,omitempty"`      // findings count or "none"
	Outcome       string `json:"outcome,omitempty"`       // passed, failed, checks-passed, cancelled, etc.
	Error         string `json:"error,omitempty"`         // error message when the run errored
	Steps         []Step `json:"steps,omitempty"`
	AwaitingAgent string `json:"awaiting_agent,omitempty"` // non-empty when pipeline is parked at a gate
}

// ErrNoActiveRun is returned by Parse when the output contains no active or recent run.
var ErrNoActiveRun = errors.New("no active run in output")

// Read calls no-mistakes axi status from the worktree path and parses the
// TOON output into a structured RunStatus.
func Read(wtPath string) (*RunStatus, error) {
	cmd := exec.Command("no-mistakes", "axi", "status")
	if wtPath != "" {
		cmd.Dir = wtPath
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return Parse(string(out))
}

// Parse parses TOON-formatted no-mistakes axi status output into a RunStatus.
// The TOON format is a flat key-value format with indented blocks. Keys like
// "run:", "outcome:", and "error:" are top-level siblings — outcome is NOT
// nested inside run. Steps are an indented CSV block under "steps[N]{...}:".
func Parse(output string) (*RunStatus, error) {
	r := &RunStatus{}
	lines := strings.Split(output, "\n")

	inSteps := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Detect steps block header.
		if strings.HasPrefix(trimmed, "steps[") {
			inSteps = true
			continue
		}

		if inSteps {
			// Non-CSV lines break the steps block.
			if strings.HasPrefix(trimmed, "outcome:") {
				inSteps = false
				r.Outcome = extractValue(trimmed, "outcome:")
				continue
			}
			if strings.HasPrefix(trimmed, "run:") {
				inSteps = false
				continue
			}
			if !strings.Contains(trimmed, ",") {
				inSteps = false
				r.processKeyValue(trimmed)
				continue
			}
			// Step CSV: name,status,findings,duration_ms
			parts := strings.Split(trimmed, ",")
			step := Step{}
			if len(parts) >= 4 {
				step.Name = strings.TrimSpace(parts[0])
				step.Status = strings.TrimSpace(parts[1])
				step.Findings, _ = strconv.Atoi(strings.TrimSpace(parts[2]))
				step.DurationMs, _ = strconv.Atoi(strings.TrimSpace(parts[3]))
			} else if len(parts) >= 2 {
				step.Name = strings.TrimSpace(parts[0])
				step.Status = strings.TrimSpace(parts[1])
			} else {
				step.Name = strings.TrimSpace(parts[0])
			}
			r.Steps = append(r.Steps, step)
			continue
		}

		// Process key-value lines.
		switch {
		case trimmed == "run:":
			// Start of a run detail block; no data on this line.
		case strings.HasPrefix(trimmed, "awaiting_agent:"):
			r.AwaitingAgent = extractValue(trimmed, "awaiting_agent:")
		default:
			r.processKeyValue(trimmed)
		}
	}

	if r.Status == "" {
		return nil, ErrNoActiveRun
	}
	return r, nil
}

// processKeyValue attempts to parse a key: value line.
func (r *RunStatus) processKeyValue(trimmed string) {
	switch {
	case strings.HasPrefix(trimmed, "id:"):
		r.ID = extractValue(trimmed, "id:")
	case strings.HasPrefix(trimmed, "branch:"):
		r.Branch = extractValue(trimmed, "branch:")
	case strings.HasPrefix(trimmed, "status:"):
		r.Status = extractValue(trimmed, "status:")
	case strings.HasPrefix(trimmed, "head:"):
		r.Head = extractValue(trimmed, "head:")
	case strings.HasPrefix(trimmed, "pr:"):
		r.PR = extractValue(trimmed, "pr:")
	case strings.HasPrefix(trimmed, "findings:"):
		r.Findings = extractValue(trimmed, "findings:")
	case strings.HasPrefix(trimmed, "error:"):
		r.Error = extractValue(trimmed, "error:")
	case strings.HasPrefix(trimmed, "outcome:"):
		r.Outcome = extractValue(trimmed, "outcome:")
	}
}

// extractValue extracts the value after a key: prefix, stripping quotes and spaces.
func extractValue(line, prefix string) string {
	val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	val = strings.Trim(val, `"`)
	return val
}

// ConceptualStep resolves the run into a high-level step name and outcome,
// matching the crewstate domain contract.
func (r *RunStatus) ConceptualStep() (step, outcome string) {
	switch r.Status {
	case "in_progress":
		return r.resolveActiveStep()
	case "completed":
		switch r.Outcome {
		case "passed", "checks-passed":
			return r.Outcome, r.Outcome
		case "failed":
			return "failed", "failed"
		case "cancelled":
			return "cancelled", "cancelled"
		}
	}
	return "", ""
}

// resolveActiveStep finds the current step in an in-progress run.
func (r *RunStatus) resolveActiveStep() (step, outcome string) {
	if r.AwaitingAgent != "" {
		return "awaiting_approval", ""
	}
	for _, s := range r.Steps {
		if s.Status != "completed" && s.Status != "pending" {
			if s.Name == "ci" && s.Status == "running" {
				return "ci", ""
			}
			if s.Status == "fixing" {
				return "fixing", ""
			}
			return "running", ""
		}
	}
	return "running", ""
}
