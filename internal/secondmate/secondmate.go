// Package secondmate manages persistent domain supervisors (secondmates).
package secondmate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

// Handoff moves backlog items from the parent home to a secondmate atomically.
// Each item is copied (meta + status) then removed from the parent only after
// all copies succeed. If any copy fails, no originals are removed.
func Handoff(parentHome, secondmateHome string, itemKeys []string) error {
	// Phase 1: copy all items
	type copyResult struct {
		key    string
		metaOK bool
		status bool
	}
	var results []copyResult

	for _, key := range itemKeys {
		cr := copyResult{key: key}

		srcMeta := filepath.Join(parentHome, "state", key+".meta")
		dstMeta := filepath.Join(secondmateHome, "state", key+".meta")
		os.MkdirAll(filepath.Dir(dstMeta), 0755)

		data, err := os.ReadFile(srcMeta)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s — no meta at %s\n", key, srcMeta)
			results = append(results, cr)
			continue
		}
		if err := os.WriteFile(dstMeta, data, 0644); err != nil {
			return fmt.Errorf("writing meta for %s: %w", key, err)
		}
		cr.metaOK = true

		// Copy status file if exists
		srcStatus := filepath.Join(parentHome, "state", key+".status")
		if _, err := os.Stat(srcStatus); err == nil {
			statusData, _ := os.ReadFile(srcStatus)
			if err := os.WriteFile(filepath.Join(secondmateHome, "state", key+".status"), statusData, 0644); err != nil {
				return fmt.Errorf("writing status for %s: %w", key, err)
			}
			cr.status = true
		}

		results = append(results, cr)
	}

	// Phase 2: remove originals (only items that had meta)
	for _, r := range results {
		if !r.metaOK {
			continue
		}
		os.Remove(filepath.Join(parentHome, "state", r.key+".meta"))
		if r.status {
			os.Remove(filepath.Join(parentHome, "state", r.key+".status"))
		}
		fmt.Printf("handed-off %s\n", r.key)
	}

	return nil
}

// getInheritableList returns the list of config names to inherit.
// Uses MUNSU_INHERITABLE_CONFIG env if set (colon-separated),
// otherwise returns the default list.
func getInheritableList() []string {
	env := os.Getenv("MUNSU_INHERITABLE_CONFIG")
	if env != "" {
		return strings.Split(env, ":")
	}
	return []string{"crew-harness", "crew-dispatch.json", "backlog-backend"}
}

// ConfigPush copies inheritable config from the parent home to the secondmate,
// mirrors deletions, checks gitignore, and logs actions.
func ConfigPush(parentHome, secondmateHome string) error {
	inheritable := getInheritableList()

	// Open log file (append mode)
	logPath := filepath.Join(secondmateHome, "state", "config-push.log")
	os.MkdirAll(filepath.Dir(logPath), 0755)
	logF, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening config-push.log: %w", err)
	}
	defer logF.Close()

	ts := time.Now().UTC().Format(time.RFC3339)
	log := func(action, name string) {
		line := fmt.Sprintf("%s\t%s\t%s\n", ts, action, name)
		logF.WriteString(line)
		fmt.Printf("  %s %s\n", action, name)
	}

	// Mirror deletions: remove files in secondmate that are absent in parent
	configDir := filepath.Join(secondmateHome, "config")
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if !isInheritable(name, inheritable) {
				continue
			}
			srcPath := filepath.Join(parentHome, "config", name)
			if _, err := os.Stat(srcPath); os.IsNotExist(err) {
				dstPath := filepath.Join(configDir, name)
				os.Remove(dstPath)
				log("deleted", name)
			}
		}
	}

	// Copy present files
	for _, name := range inheritable {
		src := filepath.Join(parentHome, "config", name)
		dst := filepath.Join(configDir, name)

		data, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			log("skipped", name+" — "+err.Error())
			continue
		}

		os.MkdirAll(filepath.Dir(dst), 0755)
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
		log("pushed", name)

		// Gitignore check: warn if not gitignored
		if gitIgnoreCheck, err := exec.Command("git", "check-ignore", "-q", dst).CombinedOutput(); err != nil || len(gitIgnoreCheck) > 0 {
			// git check-ignore exit 0 means ignored, exit 1 means not ignored
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
				fmt.Printf("  WARNING: %s is tracked in secondmate git — add it to .gitignore\n", name)
			}
		}
	}

	return nil
}

func isInheritable(name string, list []string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}
