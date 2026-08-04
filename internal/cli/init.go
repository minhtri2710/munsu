package cli

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

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
Auto-detects and persists soldier harness and backlog backend.
Writes starter configuration files and the orchestrator operating manual (AGENTS.md).
Also installs the munsu skills so coding-agent harnesses can discover them.

Use --reconfigure to re-run auto-detection and overwrite existing config files and the orchestrator operating manual (AGENTS.md).`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if err := home.EnsureDirTree(ctx.Home); err != nil {
				return fmt.Errorf("creating home tree: %w", err)
			}

			// Auto-detect and persist config (only if absent or --reconfigure)
			if err := autoDetectConfig(ctx.Home); err != nil {
				fmt.Fprintf(os.Stderr, "warning: auto-detect config: %v\n", err)
			}

			// Write orchestrator AGENTS.md (always on fresh install, or with --reconfigure)
			agentsPath := filepath.Join(ctx.Home, "AGENTS.md")
			if reconfigure || func() bool { _, err := os.Stat(agentsPath); return os.IsNotExist(err) }() {
				if err := os.WriteFile(agentsPath, []byte(orchestratorManual), 0644); err != nil {
					return fmt.Errorf("writing orchestrator manual: %w", err)
				}
				fmt.Printf("Wrote orchestrator manual to %s\n", agentsPath)
			} else {
				fmt.Printf("AGENTS.md already exists at %s (skipped; use --reconfigure to overwrite)\n", agentsPath)
			}

			fmt.Printf("Initialized munsu home at %s\n", ctx.Home)

			// Install munsu skills
			if err := runSkillInstall(cmd, ctx.Home); err != nil {
				return err
			}

			// Run bootstrap diagnostics and print
			fmt.Println()
			fmt.Println("--- Diagnostics ---")
			result, err := bootstrap.Run(ctx.Home, false, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bootstrap diagnostics: %v\n", err)
			} else {
				for _, d := range result.Tools {
					fmt.Println(d.String())
				}
				if result.Auth != nil {
					fmt.Println(result.Auth.String())
				}
				for _, c := range result.Configs {
					fmt.Println(c.String())
				}
				if result.GC != nil {
					fmt.Println(result.GC.String())
				}
			}

			// Print next steps
			printNextSteps(ctx.Home)

			return nil
		}),
	}
	cmd.Flags().StringVar(&skillChoice, "skill", "", "install munsu skills: global (~/.agents/skills/), local (<home>/.agents/skills/), or skip")
	cmd.Flags().BoolVar(&reconfigure, "reconfigure", false, "Re-run auto-detection and overwrite existing config files")
	return cmd
}

// autoDetectConfig detects soldier harness and backlog backend,
// persisting them only if the config file is absent (or --reconfigure is set).
func autoDetectConfig(homeDir string) error {
	// Note: backend is runtime context and is NOT persisted at init time.

	// 1. Auto-detect soldier harness
	detectedHarness := ""
	if reconfigure || !configFileExists(homeDir, "soldier-harness") {
		harnessName, err := harness.Detect()
		if err == nil && harnessName != "" {
			detectedHarness = harnessName
			if err := config.Set(homeDir, "soldier-harness", harnessName); err != nil {
				return fmt.Errorf("setting soldier-harness: %w", err)
			}
			fmt.Printf("Detected and persisted soldier-harness: %s\n", harnessName)
		}
	} else {
		fmt.Println("config/soldier-harness already exists (skipped; use --reconfigure to overwrite)")
	}

	// 1b. Typed fleet base document (config/base.json). Init is an explicit
	// authoring boundary: a created base.json mirrors SoldierHarness and seeds
	// the fixed captain default so init'd homes resolve a captain harness
	// (fail-closed-able). This never reads or translates legacy flat pins, and
	// re-init on an existing authored base preserves its CaptainProfile.
	basePath := filepath.Join(homeDir, config.BaseDocumentPath)
	_, statErr := os.Stat(basePath)
	switch {
	case statErr != nil && !os.IsNotExist(statErr):
		return statErr
	case os.IsNotExist(statErr):
		baseDoc := config.FleetBaseDocument{SchemaVersion: config.FleetBaseSchemaVersion}
		if detectedHarness != "" {
			baseDoc.Config.SoldierHarness = detectedHarness
		}
		baseDoc.CaptainProfile = config.CaptainProfile{Harness: harness.Pi}
		if err := config.StoreFleetBase(homeDir, baseDoc); err != nil {
			return fmt.Errorf("writing fleet base document: %w", err)
		}
	case reconfigure:
		baseDoc, err := config.LoadFleetBase(homeDir)
		if err != nil {
			// Fail closed: never self-repair a malformed/invalid document.
			return fmt.Errorf("loading fleet base document: %w", err)
		}
		if detectedHarness != "" {
			baseDoc.Config.SoldierHarness = detectedHarness
		}
		if baseDoc.CaptainProfile.Harness == "" {
			baseDoc.CaptainProfile = config.CaptainProfile{Harness: harness.Pi}
		}
		if err := config.StoreFleetBase(homeDir, baseDoc); err != nil {
			return fmt.Errorf("writing fleet base document: %w", err)
		}
	}
	// Existing base.json without --reconfigure stays untouched (idempotent).

	// 2. Auto-detect backlog backend
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

// configFileExists returns true if the config/<key> file exists under homeDir.
func configFileExists(homeDir, key string) bool {
	p := filepath.Join(config.ConfigDir(homeDir), key)
	_, err := os.Stat(p)
	return err == nil
}

// runSkillInstall resolves the skill destination (flag, env, or interactive prompt)
// and writes the embedded skills there. Existing skills are confirmed before overwrite.
func runSkillInstall(cmd *cobra.Command, homeDir string) error {
	choice := skillChoice

	// 1. If no flag, check env override
	if choice == "" {
		choice = os.Getenv("MUNSU_INIT_SKILL")
	}

	// 2. If still unset and stdin is not a terminal, default to skip
	if choice == "" && !isStdinTerminal() {
		choice = skillSkip
	}

	// 3. Prompt interactively if still unset
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

// isStdinTerminal returns true when stdin is connected to a terminal (TTY).
func isStdinTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
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
		fmt.Println("Auxiliary skills (read on demand): bootstrap-diagnostics, decision-hold-lifecycle, harness-adapters, munsu-update, captain-provisioning, stuck-soldier-recovery")
		fmt.Println("  Run: munsu skill show <name>")
	}
	return nil
}
