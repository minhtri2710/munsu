package cli

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

//go:embed seed_orchestrator_manual.md
var orchestratorManual string

// skillChoice resolves to one of: global, local, skip.
var skillChoice string

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
Writes starter configuration files and the orchestrator operating manual (AGENTS.md).
Also installs the munsu skills so coding-agent harnesses can discover them.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}
			if err := home.EnsureDirTree(homeDir); err != nil {
				return fmt.Errorf("creating home tree: %w", err)
			}

			// Write starter config
			if err := config.Set(homeDir, "backend", "tmux"); err != nil {
				return fmt.Errorf("writing starter config: %w", err)
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
			return nil
		},
	}
	cmd.Flags().StringVar(&skillChoice, "skill", "", "install munsu skills: global (~/.agents/skills/), local (<home>/.agents/skills/), or skip")
	return cmd
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
