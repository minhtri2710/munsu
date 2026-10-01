package cli

import (
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// dashboardTTY reports whether stdin and stdout are terminals. Tests replace it.
var dashboardTTY = func() bool {
	return isStdinTerminal() && term.IsTerminal(int(os.Stdout.Fd()))
}

func newDashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Full-screen fleet dashboard with event feed and actions",
		Long: `Full-screen, refreshing terminal view of the fleet: Human-needed rows first,
one named row per home that could not be read, and the event feed.

Actions on the selected row run the existing munsu command as a subprocess of
this binary after showing its exact argv and a confirm keypress; the command's
own refusals decide. Needs an interactive terminal on stdin and stdout; use
'munsu fleet view' otherwise.`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if !dashboardTTY() {
				return usageError("not_a_terminal",
					"Run `munsu fleet view` for a non-interactive fleet view",
					"dashboard needs an interactive terminal on stdin and stdout")
			}
			exe, err := os.Executable()
			if err != nil {
				return operationError("executable_unknown", "Run munsu from a resolvable path", err.Error())
			}
			_, err = tea.NewProgram(newDashboardModel(ctx.Home, exe)).Run()
			return err
		}),
	}
}
