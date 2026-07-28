package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/minhtri2710/munsu/internal/capability"
)

// MinNoMistakesVersion is the minimum compatible no-mistakes version.
// Versions below this threshold may not support required axi subcommands.
const MinNoMistakesVersion = "1.20.0"

// ProbeResult captures the result of a no-mistakes capability probe.
type ProbeResult struct {
	State   capability.State `json:"state"`
	Path    string           `json:"path,omitempty"`
	Version string           `json:"version,omitempty"`
	Detail  string           `json:"detail,omitempty"`
}

// String returns a human-readable summary of the probe result.
func (p ProbeResult) String() string {
	switch p.State {
	case capability.Absent:
		return "no-mistakes: absent"
	case capability.Unsupported:
		return fmt.Sprintf("no-mistakes: unsupported (version %s)", p.Version)
	case capability.Ready:
		return fmt.Sprintf("no-mistakes: ready (%s at %s)", p.Version, p.Path)
	case capability.Failed:
		return fmt.Sprintf("no-mistakes: failed (%s)", p.Detail)
	default:
		return fmt.Sprintf("no-mistakes: unknown state")
	}
}

// NoMistakesProbe probes no-mistakes availability end-to-end:
// binary presence, version parsing, version compatibility, and axi command surface.
// It does not modify any state and is safe to call repeatedly.
func NoMistakesProbe() ProbeResult {
	path, err := exec.LookPath("no-mistakes")
	if err != nil {
		return ProbeResult{
			State:  capability.Absent,
			Detail: "no-mistakes not found on PATH",
		}
	}

	out, err := exec.Command("no-mistakes", "--version").Output()
	if err != nil {
		return ProbeResult{
			State:  capability.Failed,
			Path:   path,
			Detail: fmt.Sprintf("cannot check version: %v", err),
		}
	}
	ver := strings.TrimSpace(string(out))
	if ver == "" {
		return ProbeResult{
			State:  capability.Failed,
			Path:   path,
			Detail: "no-mistakes --version returned empty output",
		}
	}

	cleanVer := strings.TrimPrefix(ver, "v")
	// Handle format: "no-mistakes version v1.40.0 (87a5477) ..."
	// Strip leading "no-mistakes version " if present
	if strings.HasPrefix(cleanVer, "no-mistakes version ") {
		cleanVer = cleanVer[len("no-mistakes version "):]
		cleanVer = strings.TrimPrefix(cleanVer, "v")
	}
	// Extract first version component (e.g. "1.40.0" from "1.40.0 (87a5477)")
	if idx := strings.IndexAny(cleanVer, " ("); idx > 0 {
		cleanVer = cleanVer[:idx]
	}

	parsed, err := semver.NewVersion(cleanVer)
	if err != nil {
		return ProbeResult{
			State:   capability.Failed,
			Path:    path,
			Version: ver,
			Detail:  fmt.Sprintf("cannot parse version %q: %v", ver, err),
		}
	}

	minVer, err := semver.NewVersion(MinNoMistakesVersion)
	if err != nil {
		return ProbeResult{
			State:  capability.Failed,
			Path:   path,
			Detail: fmt.Sprintf("invalid minimum version %q: %v", MinNoMistakesVersion, err),
		}
	}

	if parsed.LessThan(minVer) {
		return ProbeResult{
			State:   capability.Unsupported,
			Path:    path,
			Version: parsed.String(),
			Detail:  fmt.Sprintf("no-mistakes version %s < minimum %s", parsed.String(), MinNoMistakesVersion),
		}
	}

	// Verify the axi command surface is available by probing axi status help.
	axiOut, err := exec.Command("no-mistakes", "axi", "status", "--help").Output()
	if err != nil {
		return ProbeResult{
			State:   capability.Failed,
			Path:    path,
			Version: parsed.String(),
			Detail:  fmt.Sprintf("axi command surface not available: %v", err),
		}
	}
	if !strings.Contains(string(axiOut), "status") {
		return ProbeResult{
			State:   capability.Failed,
			Path:    path,
			Version: parsed.String(),
			Detail:  "axi status subcommand not recognized",
		}
	}

	return ProbeResult{
		State:   capability.Ready,
		Path:    path,
		Version: parsed.String(),
		Detail:  "found on PATH, version compatible, axi surface available",
	}
}

// NoMistakesStatus runs `no-mistakes axi status` and parses the JSON output.
// It returns a map of status fields.
func NoMistakesStatus(branch string) (map[string]interface{}, error) {
	args := []string{"axi", "status"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}

	cmd := exec.Command("no-mistakes", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("no-mistakes axi status: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("no-mistakes axi status: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing no-mistakes status JSON: %w (output: %s)", err, string(out))
	}

	return result, nil
}

// NoMistakesRun runs `no-mistakes axi run --intent "..."` with optional --skip flags.
func NoMistakesRun(intent string, skip []string) error {
	args := []string{"axi", "run", "--intent", intent}
	for _, s := range skip {
		args = append(args, "--skip", s)
	}

	cmd := exec.Command("no-mistakes", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("no-mistakes axi run: %w", err)
	}

	return nil
}

// NoMistakesRespond runs `no-mistakes axi respond` with the given findings.
func NoMistakesRespond(findings []string) error {
	if len(findings) == 0 {
		return fmt.Errorf("no findings to respond to")
	}

	args := []string{"axi", "respond"}
	for _, f := range findings {
		args = append(args, f)
	}

	cmd := exec.Command("no-mistakes", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("no-mistakes axi respond: %w", err)
	}

	return nil
}
