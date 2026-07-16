package cli

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Read-only diagnostics with fix commands",
		Long: `Run bootstrap diagnostics and print fix commands for each issue.

Hard-required tools (doctor exits non-zero if missing):
  git, session backend (tmux or herdr), and at least one coding harness
  (pi, claude, agy, etc.) if crew-harness detection fails.

Optional tools get warnings but do not fail the exit code:
  treehouse, no-mistakes, tasks-axi, gh-axi, gh, and GitHub auth.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve(homeOverride)
			if err != nil {
				return fmt.Errorf("resolving home: %w", err)
			}

			result, err := bootstrap.Run(homeDir, false, nil)
			if err != nil {
				return fmt.Errorf("bootstrap: %w", err)
			}

			exitCode := 0

			for _, d := range result.Diagnostics {
				fmt.Println(d)
				if fix := bootstrap.DoctorFix(d); fix != "" {
					fmt.Println(fix)
				}
			}

			for _, c := range result.ConfigDetails {
				fmt.Println(c)
			}

			// Determine hard-required tools for exit code
			// git, tmux (or herdr), at least one coding harness
			required := []string{"git", "tmux"}
			missingRequired := false
			for _, tool := range result.MissingTools {
				for _, req := range required {
					if tool == req {
						missingRequired = true
						break
					}
				}
			}

			// Also check herdr as alternative session backend
			if contains(result.MissingTools, "tmux") {
				// Check if herdr is available (look for herdr binary or env)
				herdrAvailable := os.Getenv("HERDR_ENV") != ""
				if !herdrAvailable {
					missingRequired = true
				}
			}

			if missingRequired {
				fmt.Fprintf(os.Stderr, "\nSome required tools are missing.\n")
				exitCode = 1
			}

			if contains(result.MissingTools, "herdr") {
				fmt.Println("herdr: optional (needed only if not using tmux)")
			}

			if exitCode != 0 {
				os.Exit(exitCode)
			}
			return nil
		},
	}
}

// contains returns true if the slice contains the given string.
func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
