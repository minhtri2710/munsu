// Package secondmate manages persistent domain supervisors (secondmates).
package secondmate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
)

// Info holds the state of a secondmate.
type Info struct {
	ID      string
	Home    string
	Scope   string
	Project string
	Added   string
}

// Seed creates a new secondmate home with a charter brief.
// id is the secondmate identifier, homePath is the MUNSU_HOME for it,
// charter is the content of the AGENTS.md/operating manual.
func Seed(id, homePath, charter string) error {
	// Ensure home exists
	if err := os.MkdirAll(homePath, 0755); err != nil {
		return fmt.Errorf("creating secondmate home %s: %w", homePath, err)
	}

	// Create state/data/config/projects dirs
	for _, dir := range []string{"state", "data", "config", "projects"} {
		if err := os.MkdirAll(filepath.Join(homePath, dir), 0755); err != nil {
			return fmt.Errorf("creating %s/%s: %w", homePath, dir, err)
		}
	}

	// Write AGENTS.md (charter)
	agentsPath := filepath.Join(homePath, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte(charter), 0644); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}

	fmt.Printf("Seeded secondmate %s at %s\n", id, homePath)
	return nil
}

// Launch starts a secondmate process in its home.
// It runs the configured harness (from parent home's config/secondmate-harness)
// with the AGENTS.md as the launch prompt.
func Launch(secondmateHome, parentHome string) error {
	// Read secondmate harness config
	shPath := filepath.Join(parentHome, "config", "secondmate-harness")
	harness := "pi" // default
	if data, err := os.ReadFile(shPath); err == nil {
		if h := strings.TrimSpace(string(data)); h != "" && h != "default" {
			parts := strings.Fields(h)
			harness = parts[0]
		}
	}

	// Read model from crew-harness config if harness is pi
	model := "cline-pass/deepseek-v4-flash"
	if harness == "pi" {
		if m, err := config.Get(parentHome, "model"); err == nil && m != "" {
			model = m
		}
	}

	// Resolve the pi binary
	piPath, err := exec.LookPath("pi")
	if err != nil {
		return fmt.Errorf("pi harness not found on PATH: %w", err)
	}

	// Launch: cd to secondmate home and run pi with AGENTS.md as prompt
	cmd := exec.Command(piPath, "--model", model,
		"--", secondmateHome,
		"$(cat "+filepath.Join(secondmateHome, "AGENTS.md")+")")
	cmd.Dir = secondmateHome
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launching secondmate: %w", err)
	}

	fmt.Printf("Launched secondmate %s (pid %d) in %s\n",
		filepath.Base(secondmateHome), cmd.Process.Pid, secondmateHome)
	return nil
}

// Retire tears down a secondmate: notifies the process and optionally removes the home.
// If removeHome is true, the secondmate home directory is removed.
func Retire(secondmateHome string, removeHome bool) error {
	// Read PID from lock file if exists
	lockFile := filepath.Join(secondmateHome, "state", ".lock")
	if data, err := os.ReadFile(lockFile); err == nil {
		var pid int
		fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid)
		if pid > 0 {
			proc, err := os.FindProcess(pid)
			if err == nil {
				proc.Kill()
			}
		}
	}

	if removeHome {
		if err := os.RemoveAll(secondmateHome); err != nil {
			return fmt.Errorf("removing secondmate home %s: %w", secondmateHome, err)
		}
		fmt.Printf("Retired and removed secondmate home %s\n", secondmateHome)
	} else {
		fmt.Printf("Retired secondmate at %s (home retained)\n", secondmateHome)
	}

	return nil
}

// List returns all registered secondmates by scanning the registry.
func List(parentHome string) ([]Info, error) {
	// For now, scan home directories under the parent
	// The full implementation reads data/secondmates.md
	projectsDir := filepath.Join(parentHome, "secondmates")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var mates []Info
	for _, e := range entries {
		if e.IsDir() {
			mates = append(mates, Info{
				ID:   e.Name(),
				Home: filepath.Join(projectsDir, e.Name()),
			})
		}
	}
	return mates, nil
}

// Handoff moves backlog items from the parent home to a secondmate.
func Handoff(parentHome, secondmateHome string, itemKeys []string) error {
	for _, key := range itemKeys {
		// Copy the item metadata from parent backlog to secondmate
		srcMeta := filepath.Join(parentHome, "state", key+".meta")
		dstMeta := filepath.Join(secondmateHome, "state", key+".meta")
		dstDir := filepath.Dir(dstMeta)
		os.MkdirAll(dstDir, 0755)

		data, err := os.ReadFile(srcMeta)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: task %s has no meta at %s: %v\n", key, srcMeta, err)
			continue
		}
		if err := os.WriteFile(dstMeta, data, 0644); err != nil {
			return fmt.Errorf("writing meta for %s: %w", key, err)
		}

		// Copy status file if exists
		srcStatus := filepath.Join(parentHome, "state", key+".status")
		if _, err := os.Stat(srcStatus); err == nil {
			statusData, _ := os.ReadFile(srcStatus)
			os.WriteFile(filepath.Join(secondmateHome, "state", key+".status"), statusData, 0644)
		}

		fmt.Printf("Handed off task %s to secondmate at %s\n", key, secondmateHome)
	}
	return nil
}

// ConfigPush copies inheritable config from the parent home to the secondmate.
func ConfigPush(parentHome, secondmateHome string) error {
	inheritable := []string{"crew-harness", "crew-dispatch.json", "backlog-backend"}

	for _, name := range inheritable {
		src := filepath.Join(parentHome, "config", name)
		dstDir := filepath.Join(secondmateHome, "config")
		os.MkdirAll(dstDir, 0755)
		dst := filepath.Join(dstDir, name)

		data, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("reading %s: %w", src, err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", dst, err)
		}
		fmt.Printf("Pushed config %s to secondmate\n", name)
	}
	return nil
}
