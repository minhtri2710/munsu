package backlog

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	execCommand = exec.Command
	lookPath    = exec.LookPath
)

var semverRegex = regexp.MustCompile(`\d+\.\d+\.\d+`)

// Run dispatches to tasks-axi if compatible, or fallback.
func Run(verb string, args []string) error {
	// Probe for compatible tasks-axi
	if tasksAxiAvailable() {
		return runTasksAxi(verb, args)
	}

	return fmt.Errorf("backlog: tasks-axi not available and fallback backlog.md editing not yet implemented")
}

// tasksAxiAvailable checks if tasks-axi >= 0.1.1 is on PATH.
func tasksAxiAvailable() bool {
	path, err := lookPath("tasks-axi")
	if err != nil {
		return false
	}

	cmd := execCommand(path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	version := strings.TrimSpace(string(out))
	return isCompatibleVersion(version, "0.1.1")
}

// isCompatibleVersion checks if the installed version is >= minimum.
// Simple semver comparison (major.minor.patch).
func isCompatibleVersion(installed, minimum string) bool {
	installParts := parseVersion(installed)
	minParts := parseVersion(minimum)

	for i := 0; i < 3; i++ {
		if installParts[i] > minParts[i] {
			return true
		}
		if installParts[i] < minParts[i] {
			return false
		}
	}
	return true
}

// parseVersion extracts the first "x.y.z" match and splits it into [major, minor, patch] ints.
func parseVersion(v string) [3]int {
	match := semverRegex.FindString(v)
	if match == "" {
		return [3]int{0, 0, 0}
	}
	parts := strings.SplitN(match, ".", 3)
	var result [3]int
	for i, p := range parts {
		if i < 3 {
			result[i] = atoi(p)
		}
	}
	return result
}

// atoi parses an integer from a string, returning 0 on error.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}

// runTasksAxi runs tasks-axi with the given verb and args.
func runTasksAxi(verb string, args []string) error {
	path, err := lookPath("tasks-axi")
	if err != nil {
		return fmt.Errorf("tasks-axi not found: %w", err)
	}

	cliArgs := []string{verb}
	cliArgs = append(cliArgs, args...)

	cmd := execCommand(path, cliArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
