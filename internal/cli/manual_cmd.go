package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newManualCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "manual",
		Short: "Print the orchestrator operating manual",
		Long: `Print the full orchestrator operating manual to stdout.

The manual describes the munsu rank hierarchy, session lifecycle, task dispatch,
soldier supervision, delivery, and escalation procedures. It is the same content
that 'munsu init' writes to <home>/AGENTS.md.

Use 'munsu skill show munsu-ops' for the operator skill card, or
'man munsu' for the CLI reference.`,
		Args: NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Print(orchestratorManual)
			return nil
		},
	}
}
