package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

// syncOrchestratorManual writes the embedded orchestrator manual to
// <home>/AGENTS.md when missing or when it differs from the binary seed.
// Returns the path and whether a write occurred.
func syncOrchestratorManual(homeDir string) (path string, wrote bool, err error) {
	if err := home.EnsureDirTree(homeDir); err != nil {
		return "", false, fmt.Errorf("ensuring home tree: %w", err)
	}
	path = filepath.Join(homeDir, "AGENTS.md")
	existing, readErr := os.ReadFile(path)
	if readErr == nil && string(existing) == orchestratorManual {
		return path, false, nil
	}
	if err := os.WriteFile(path, []byte(orchestratorManual), 0644); err != nil {
		return path, false, fmt.Errorf("writing orchestrator manual: %w", err)
	}
	return path, true, nil
}

func newContextCmd() *cobra.Command {
	var syncOnly bool
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Emit orchestrator doctrine into this session (sync home AGENTS.md + stdout)",
		Long: `Bootstrap general-session context without requiring cwd to be MUNSU_HOME.

1. Ensures munsu home exists.
2. Syncs <home>/AGENTS.md from the embedded orchestrator manual (same as munsu manual / init).
3. Unless --sync-only: prints the full manual, registered projects, and next-step contract to stdout
   so the harness session transcript holds the doctrine (firstmate-style load, pull-based).

General/orchestrator sessions only. Captains use charter AGENTS in their home; soldiers use briefs.
Do not run this to load product-repo AGENTS.md — that is the wrong role file.

Typical munsu-ops flow:
  munsu context
  # choose project target from the printed registry (ask user if ambiguous)
  munsu session-start`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			path, wrote, err := syncOrchestratorManual(ctx.Home)
			if err != nil {
				return err
			}
			if syncOnly {
				if wrote {
					fmt.Fprintf(cmd.OutOrStdout(), "synced orchestrator manual → %s\n", path)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "orchestrator manual already current → %s\n", path)
				}
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "=== munsu context: orchestrator doctrine (session inject) ===")
			fmt.Fprintf(out, "home: %s\n", ctx.Home)
			fmt.Fprintf(out, "agents: %s (%s)\n", path, map[bool]string{true: "synced from binary seed", false: "already current"}[wrote])
			fmt.Fprintln(out, "role: general/orchestrator — never do project work yourself; delegate to captains/soldiers")
			fmt.Fprintln(out, "source: embedded seed (munsu manual) — NOT the product-repo AGENTS.md")
			fmt.Fprintln(out, "")
			fmt.Fprint(out, orchestratorManual)
			if !strings.HasSuffix(orchestratorManual, "\n") {
				fmt.Fprintln(out)
			}
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "=== Registered projects (choose a fleet target; cwd may stay on a product folder) ===")
			projects, err := fleet.List(ctx.Home)
			if err != nil {
				return fmt.Errorf("listing projects: %w", err)
			}
			if len(projects) == 0 {
				fmt.Fprintln(out, "(none — munsu project add <name> --repo <path-or-url>)")
			} else {
				for _, p := range projects {
					line := "- " + p.Name
					if p.Description != "" {
						line += " — " + p.Description
					}
					if path, rerr := fleet.ResolveRepoPath(ctx.Home, p.Name); rerr == nil && path != "" {
						line += " (" + path + ")"
					}
					fmt.Fprintln(out, line)
				}
			}
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "=== Next steps (this session) ===")
			fmt.Fprintln(out, "1. If more than one project fits, ask the user which registry name is the work target.")
			fmt.Fprintln(out, "2. Run exactly once: munsu session-start")
			fmt.Fprintln(out, "3. Dispatch via backlog/brief/spawn or captain handoff — do not edit project trees yourself.")
			fmt.Fprintln(out, "4. Captains/soldiers must not re-run munsu context for orchestrator doctrine.")
			fmt.Fprintln(out, "=== end munsu context ===")
			return nil
		}),
	}
	cmd.Flags().BoolVar(&syncOnly, "sync-only", false, "Only refresh <home>/AGENTS.md from the binary seed; do not print the manual")
	return cmd
}
