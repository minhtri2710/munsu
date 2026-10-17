package cli

import (
	"fmt"
	"io"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/fleet"
)

// printNextSteps prints, in order, only the steps the home still needs after init.
func printNextSteps(w io.Writer, homeDir string, res *bootstrap.Result) {
	type step struct{ cmd, what string }
	var steps []step
	if res.Backend == "" {
		steps = append(steps, step{"munsu config set backend herdr|tmux", "Set the session backend (spawning requires one)"})
	}
	if !hasConfig(res.Configs, "SOLDIER_HARNESS") {
		steps = append(steps, step{"munsu config set soldier-harness <name>", "Set the soldier harness (none detected)"})
	}
	if len(res.MissingTools) > 0 {
		steps = append(steps, step{"munsu doctor", "Show fix commands for the diagnostics above"})
	}
	if projects, err := fleet.List(homeDir); err != nil || len(projects) == 0 {
		steps = append(steps, step{"munsu project add <name> <path-or-url>", "Register a project"})
	}
	steps = append(steps, step{"munsu session-start", "Start a session"})

	width := 0
	for _, s := range steps {
		width = max(width, len(s.cmd))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Next steps:")
	fmt.Fprintln(w)
	for _, s := range steps {
		fmt.Fprintf(w, "  %-*s  %s\n", width, s.cmd, s.what)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Home:  %s\n", homeDir)
}

func hasConfig(configs []bootstrap.ConfigDiagnostic, key string) bool {
	for _, c := range configs {
		if c.Key == key {
			return true
		}
	}
	return false
}
