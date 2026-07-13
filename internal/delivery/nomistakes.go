package delivery

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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
