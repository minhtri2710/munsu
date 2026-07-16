package cli

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

//go:embed seed_orchestrator_manual.md
var orchestratorManual string

var (
	skillChoice string
	reconfigure bool
)

const (
	skillGlobal = "global"
	skillLocal  = "local"
	skillSkip   = "skip"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create home and seed orchestrator operating manual",
		Long: `Initialize the munsu home directory tree.

Creates the directory structure: {state, data, config, projects}.
Auto-detects and persists session backend, crew harness, and backlog backend.
Writes starter configuration files and the orchestrator operating manual (AGENTS.md).
Also installs the munsu skills so coding-agent harnesses can discover them.

Use --reconfigure to re-run auto-detection and overwrite existing config files.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if err := home.EnsureDirTree(homeDir); err != nil {
				return fmt.Errorf("creating home tree: %w", err)
			}

			// Auto-detect and persist config (only if absent or --reconfigure)
			if err := autoDetectConfig(homeDir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: auto-detect config: %v\n", err)
			}

			// Write orchestrator AGENTS.md
			agentsPath := filepath.Join(homeDir, "AGENTS.md")
			if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
				if err := os.WriteFile(agentsPath, []byte(orchestratorManual), 0644); err != nil {
					return fmt.Errorf("writing orchestrator manual: %w", err)
				}
				fmt.Printf("Wrote orchestrator manual to %s\n", agentsPath)
			} else {
				fmt.Printf("AGENTS.md already exists at %s (skipped)\n", agentsPath)
			}

			fmt.Printf("Initialized munsu home at %s\n", homeDir)

			// Install munsu skills
			if err := runSkillInstall(cmd, homeDir); err != nil {
				return err
			}

			// Run bootstrap diagnostics and print
			fmt.Println()
			fmt.Println("--- Diagnostics ---")
			result, err := bootstrap.Run(homeDir, false, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bootstrap diagnostics: %v\n", err)
			} else {
				for _, d := range result.Diagnostics {
					fmt.Println(d)
				}
				for _, c := range result.ConfigDetails {
					fmt.Println(c)
				}
			}

			// Print next steps
			printNextSteps(homeDir)

			return nil
		},
	}
	cmd.Flags().StringVar(&skillChoice, "skill", "", "install munsu skills: global (~/.agents/skills/), local (<home>/.agents/skills/), or skip")
	cmd.Flags().BoolVar(&reconfigure, "reconfigure", false, "Re-run auto-detection and overwrite existing config files")
	return cmd
}

// autoDetectConfig detects session backend, crew harness, and backlog backend,
// persisting them only if the config file is absent (or --reconfigure is set).
func autoDetectConfig(homeDir string) error {
	// 1. Auto-detect backend
	if reconfigure || !configFileExists(homeDir, "backend") {
		backend := detectBackend()
		if backend != "" {
			if err := config.Set(homeDir, "backend", backend); err != nil {
				return fmt.Errorf("setting backend: %w", err)
			}
			fmt.Printf("Detected and persisted backend: %s\n", backend)
		}
	} else {
		fmt.Println("config/backend already exists (skipped; use --reconfigure to overwrite)")
	}

	// 2. Auto-detect crew harness
	if reconfigure || !configFileExists(homeDir, "crew-harness") {
		harnessName, err := harness.Detect()
		if err == nil && harnessName != "" {
			if err := config.Set(homeDir, "crew-harness", harnessName); err != nil {
				return fmt.Errorf("setting crew-harness: %w", err)
			}
			fmt.Printf("Detected and persisted crew-harness: %s\n", harnessName)
		}
	} else {
		fmt.Println("config/crew-harness already exists (skipped; use --reconfigure to overwrite)")
	}

	// 3. Auto-detect backlog backend
	if reconfigure || !configFileExists(homeDir, "backlog-backend") {
		if _, err := exec.LookPath("tasks-axi"); err == nil {
			if err := config.Set(homeDir, "backlog-backend", "tasks-axi"); err != nil {
				return fmt.Errorf("setting backlog-backend: %w", err)
			}
			fmt.Println("Detected and persisted backlog-backend: tasks-axi")
		}
	} else {
		fmt.Println("config/backlog-backend already exists (skipped; use --reconfigure to overwrite)")
	}

	return nil
}

// detectBackend returns the preferred session backend.
// Priority: HERDR_ENV env var > tmux availability.
func detectBackend() string {
	if os.Getenv("HERDR_ENV") != "" {
		return "herdr"
	}
	if _, err := exec.LookPath("tmux"); err == nil {
		return "tmux"
	}
	return "tmux" // default even if not found, will fail gracefully later
}

// configFileExists returns true if the config/<key> file exists under homeDir.
func configFileExists(homeDir, key string) bool {
	p := filepath.Join(config.ConfigDir(homeDir), key)
	_, err := os.Stat(p)
	return err == nil
}

// runSkillInstall resolves the skill destination (flag or interactive prompt)
// and writes the embedded skills there. Existing skills are confirmed before overwrite.
func runSkillInstall(cmd *cobra.Command, homeDir string) error {
	choice := skillChoice
	if choice == "" {
		choice = promptSkillChoice()
	}
	switch choice {
	case "", skillSkip:
		fmt.Println("Skipping skill install.")
		return nil
	case skillGlobal:
		userHome, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolving user home: %w", err)
		}
		return installSkillsTo(filepath.Join(userHome, ".agents", "skills"))
	case skillLocal:
		return installSkillsTo(filepath.Join(homeDir, ".agents", "skills"))
	default:
		return fmt.Errorf("invalid --skill value %q (want global|local|skip)", choice)
	}
}

// promptSkillChoice asks the user where to install skills.
func promptSkillChoice() string {
	fmt.Println("\nInstall munsu skills?")
	fmt.Println("  [1] global  (~/.agents/skills/)   — available in every project")
	fmt.Println("  [2] local   (<home>/.agents/skills/) — scoped to this munsu home")
	fmt.Println("  [3] skip")
	fmt.Print("Choose [1-3]: ")
	var resp string
	if _, err := fmt.Scanln(&resp); err != nil {
		return skillSkip
	}
	switch strings.TrimSpace(resp) {
	case "1", skillGlobal:
		return skillGlobal
	case "2", skillLocal:
		return skillLocal
	default:
		return skillSkip
	}
}

// installSkillsTo writes the munsu-ops skill (the entry-point skill) under dest,
// confirming overwrite if it already exists. Auxiliary skills stay embedded in
// the binary and are read on demand via 'munsu skill show <name>'.
func installSkillsTo(dest string) error {
	name := "munsu-ops"
	overwrite := skillExistsAt(dest, name) && confirmOverwrite(name)
	ok, err := installOneSkill(dest, name, overwrite)
	if err != nil {
		return fmt.Errorf("installing %s to %s: %w", name, dest, err)
	}
	if !ok {
		fmt.Printf("Skill %q kept as-is at %s\n", name, dest)
	} else {
		fmt.Printf("Installed skill %q to %s\n", name, dest)
		fmt.Println("Auxiliary skills (read on demand): bootstrap-diagnostics, harness-adapters, munsu-update, secondmate-provisioning, stuck-crewmate-recovery")
		fmt.Println("  Run: munsu skill show <name>")
	}
	return nil
}
