package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Read-only diagnostics with fix commands",
		Long: `Run bootstrap diagnostics and print fix commands for each issue.

Doctor exits non-zero when any of these holds:
  git is not on PATH;
  a session backend binary (herdr or tmux) set in the fleet base config or a
    registered project's overlay is not on PATH;
  the home resolves no session backend (fleet base config, or a captain
    home's published snapshot);
  config/base.json is unreadable or invalid.

Everything else (gh-axi, gh, fuser, GitHub auth) warns without failing.

Use --role for role-specific integration matrix:
  --role general   Harness adapter, session backend, gh auth, go toolchain, per-project launch readiness
  --role captain   Watcher integration, config, captain homes, converge readiness
  --role soldier   Worktree state, soldier brief`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			checkInstructions, _ := cmd.Flags().GetBool("check-instructions")
			if checkInstructions {
				return runCheckInstructions(ctx.Home)
			}

			if orphans, _ := cmd.Flags().GetBool("orphans"); orphans {
				return runOrphanScan(cmd.OutOrStdout(), ctx.Home)
			}

			// Role-specific doctor scan
			if role != "" {
				cwd, err := os.Getwd()
				if err != nil {
					cwd = ctx.Home
				}
				runtimeIdentity := bootstrap.CollectRuntimeIdentity(ctx.Home, cwd, Version)
				fmt.Println("Runtime Identity:")
				for _, line := range bootstrap.RuntimeIdentityLines(&runtimeIdentity) {
					fmt.Println("  " + line)
				}
				m := outputMarks(os.Stdout)
				r, err := bootstrap.Doctor(ctx.Home, bootstrap.Role(role))
				if err != nil {
					return fmt.Errorf("doctor: %w", err)
				}
				fmt.Printf("Integration matrix for role: %s\n", r.Role)
				for _, e := range r.Entries {
					fmt.Println(m.markAfter(e.String(), ": ", string(e.Status)))
					if e.RepairCmd != "" {
						fmt.Println("    Fix: " + e.RepairCmd)
					}
				}
				return nil
			}

			result := bootstrap.Run(ctx.Home, false, nil)

			exitCode := 0
			m := outputMarks(os.Stdout)

			if result.RuntimeIdentity != nil {
				result.RuntimeIdentity.Build.CLIVersion = Version
				fmt.Println("Runtime Identity:")
				for _, line := range bootstrap.RuntimeIdentityLines(result.RuntimeIdentity) {
					fmt.Println("  " + line)
				}
			}

			for _, d := range result.Tools {
				fmt.Println(m.markLead(d.String()))
				if fix := d.Fix(); fix != "" {
					fmt.Println("    Fix: " + fix)
				}
			}
			if result.Auth != nil {
				fmt.Println(m.markLead(result.Auth.String()))
				if fix := result.Auth.Fix(); fix != "" {
					fmt.Println("    Fix: " + fix)
				}
			}
			for _, c := range result.Configs {
				fmt.Println(c.String())
			}
			if result.Backend == "" {
				fmt.Println("    Fix: munsu config set backend herdr|tmux")
				exitCode = 1
			}
			if result.WriteFence != nil {
				fmt.Println(result.WriteFence.String())
			}
			if result.BaseConfigErr != nil {
				fmt.Println(m.markLead(fmt.Sprintf("BASE_CONFIG_INVALID: %v", result.BaseConfigErr)))
				exitCode = 1
			}
			if result.GC != nil {
				fmt.Println(result.GC.String())
			}

			// --- Extended capability diagnostics ---
			cwd, err := os.Getwd()
			if err != nil {
				cwd = "<unknown>"
			}
			capResult := CollectCapabilities(ctx.Home, cwd, Version)
			if len(capResult.Integrations) > 0 {
				fmt.Println("\nIntegration status:")
				for _, d := range capResult.Integrations {
					fmt.Println(m.markAfter(d.String(), "integration: ", d.State))
					if fix := d.Fix(); fix != "" {
						fmt.Println("    Fix: " + fix)
					}
				}
			}

			if capResult.Watcher != nil {
				fmt.Println("\nWatcher identity:")
				fmt.Println("  " + capResult.Watcher.String())
				if fix := capResult.Watcher.Fix(); fix != "" {
					fmt.Println("    Fix: " + fix)
				}
			}

			if capResult.General != nil {
				fmt.Println("\nGeneral target:")
				fmt.Println("  " + capResult.General.String())
				if fix := capResult.General.Fix(); fix != "" {
					fmt.Println("    Fix: " + fix)
				}
			}

			if capResult.ScopeResult != nil {
				fmt.Println("\nScope identity:")
				fmt.Println("  " + capResult.ScopeResult.String())
			}

			missingRequired := false
			for _, d := range result.Tools {
				if d.Status == bootstrap.ToolMissing && d.Required {
					missingRequired = true
				}
			}
			if missingRequired {
				fmt.Fprintf(os.Stderr, "\nSome required tools are missing.\n")
				exitCode = 1
			}

			if exitCode != 0 {
				exitWithCode(exitCode)
			}
			return nil
		}),
	}
	cmd.Flags().Bool("check-instructions", false, "Verify AGENTS.md/orchestrator manual references against real commands")
	cmd.Flags().Bool("orphans", false, "Report processes whose owning run has ended (never terminates anything; exit 1 leftovers found — the report may also list unresolved ones, 2 nothing conclusive but a member should look)")
	cmd.Flags().StringVar(&role, "role", "", "Role-specific scan: general, captain, or soldier")
	return cmd
}

// runCheckInstructions reads AGENTS.md / manual.md and verifies that every
// `munsu <command>` reference corresponds to a real cobra command/flag.
func runCheckInstructions(homeDir string) error {
	// Build the authoritative command tree
	rootCmd := NewRootCommand()

	// Build a lookup table of all real commands
	realCommands := buildCommandIndex(rootCmd)

	// Collect manual files to check
	var files []string
	for _, name := range []string{"AGENTS.md", "manual.md", "orchestrator-manual.md"} {
		p := filepath.Join(homeDir, name)
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}

	// Also check project AGENTS.md files
	projectsDir := filepath.Join(homeDir, "projects")
	if entries, err := os.ReadDir(projectsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				agentsPath := filepath.Join(projectsDir, e.Name(), "AGENTS.md")
				if _, err := os.Stat(agentsPath); err == nil {
					files = append(files, agentsPath)
				}
			}
		}
	}

	if len(files) == 0 {
		fmt.Println("No AGENTS.md or manual.md found to check.")
		return nil
	}

	mismatches := 0
	for _, f := range files {
		fileMismatches, err := checkFileInstructions(f, realCommands)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: error reading %s: %v\n", f, err)
			continue
		}
		mismatches += fileMismatches
	}

	if mismatches > 0 {
		fmt.Fprintf(os.Stderr, "\nFound %d doc-code mismatches. AGENTS.md references commands or flags that do not exist.\n", mismatches)
		exitWithCode(1)
	} else {
		fmt.Println("All command references in AGENTS.md match real commands.")
	}
	return nil
}

// buildCommandIndex walks the cobra command tree and returns a set of known
// command paths excluding the root (e.g., "doctor", "fleet snapshot", "fleet view").
func buildCommandIndex(cmd *cobra.Command) map[string]bool {
	index := make(map[string]bool)
	index["help"] = true // implicit cobra command
	for _, sub := range cmd.Commands() {
		// Include hidden commands for doctor validation
		collectCommand(sub, strings.Fields(sub.Use)[0], index)
	}
	return index
}

func collectCommand(cmd *cobra.Command, name string, index map[string]bool) {
	index[name] = true
	for _, sub := range cmd.Commands() {
		// Include hidden commands for doctor validation
		subName := name + " " + strings.Fields(sub.Use)[0]
		collectCommand(sub, subName, index)
	}
}

// checkFileInstructions scans a file for `munsu ` references and verifies them.
func checkFileInstructions(path string, realCommands map[string]bool) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	mismatches := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Find `munsu ` command references
		checkDocLine(line, path, realCommands, &mismatches)
	}

	if err := scanner.Err(); err != nil {
		return mismatches, err
	}
	return mismatches, nil
}

// checkDocLine scans one line for `munsu ` references and validates them.
func checkDocLine(line string, path string, realCommands map[string]bool, mismatches *int) {
	idx := strings.Index(line, "`munsu ")
	if idx < 0 {
		// Also check without backticks
		idx = strings.Index(line, "munsu ")
		if idx < 0 {
			return
		}
		// Skip if it's not a command reference
		before := ""
		if idx > 0 {
			before = line[idx-1 : idx]
		}
		if before != "" && before != " " && before != "\t" && before != "(" && before != "\"" {
			return
		}
	}

	rest := line[idx:]
	// Remove backtick prefix if present
	rest = strings.TrimPrefix(rest, "`")
	// Skip past "munsu "
	rest = strings.TrimPrefix(rest, "munsu ")
	if rest == "" {
		return
	}

	// Extract the command reference: up to backtick, space, or punctuation
	var ref string
	// Collect the full command path: balance backticks
	if strings.HasPrefix(line[idx:], "`") {
		// Backtick-delimited
		cmdPart := strings.TrimPrefix(line[idx:], "`")
		end := strings.Index(cmdPart, "`")
		if end < 0 {
			ref = strings.Fields(cmdPart)[0]
		} else {
			ref = strings.TrimSpace(cmdPart[:end])
		}
		// Backtick ref still has "munsu " prefix — strip it
		ref = strings.TrimPrefix(ref, "munsu ")
	} else {
		// Not backticked, take the next word or flag sequence
		parts := strings.Fields(rest)
		if len(parts) > 0 {
			ref = parts[0]
		}
	}

	if ref == "" {
		return
	}

	// Normalize: strip flags from the ref
	parts := strings.Fields(ref)
	if len(parts) == 0 {
		return
	}

	// Build command path (skip initial flags, their values, and argument placeholders)
	cmdParts := make([]string, 0, len(parts))
	skipNext := false
	for _, p := range parts {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(p, "--") || (strings.HasPrefix(p, "-") && len(p) == 2) {
			skipNext = true
			continue
		}
		// Skip angle-bracket argument placeholders like <id>, <project>
		if strings.HasPrefix(p, "<") && strings.HasSuffix(p, ">") {
			continue
		}
		// Skip quoted placeholders like "<desc>" or '<desc>'
		cleaned := strings.Trim(p, "\"'")
		if strings.HasPrefix(cleaned, "<") && strings.HasSuffix(cleaned, ">") {
			continue
		}
		cmdParts = append(cmdParts, p)
	}
	cmdPath := strings.Join(cmdParts, " ")
	if cmdPath == "" {
		cmdPath = parts[0]
	}

	// Validate against the real command tree
	if !realCommands[cmdPath] {
		fmt.Fprintf(os.Stderr, "MISMATCH: %s references command %q which does not exist\n", path, cmdPath)
		*mismatches++
	}
}
