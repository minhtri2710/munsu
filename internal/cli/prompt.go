package cli

import (
	"errors"
	"os"

	"charm.land/huh/v2"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// terminals reports whether stdin and stdout are terminals. Tests replace it.
var terminals = func() (stdin, stdout bool) {
	return isStdinTerminal(), term.IsTerminal(int(os.Stdout.Fd()))
}

func interactive() bool {
	stdin, stdout := terminals()
	return stdin && stdout
}

// runPrompt runs a huh form. Tests replace it.
var runPrompt = func(form *huh.Form) error { return form.Run() }

var errAborted = errors.New("aborted")

func prompt(form *huh.Form) error {
	err := runPrompt(form)
	if errors.Is(err, huh.ErrUserAborted) {
		return errAborted
	}
	return err
}

// exactArgsOrPrompt is ExactArgs(n), except that on an interactive terminal
// fewer args pass so promptArgs can ask for the rest.
func exactArgsOrPrompt(n int) cobra.PositionalArgs {
	exact := ExactArgs(n)
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n && interactive() {
			return nil
		}
		return exact(cmd, args)
	}
}

// promptArgs asks for each positional arg past args, one input per title.
func promptArgs(args []string, titles ...string) ([]string, error) {
	if len(args) >= len(titles) {
		return args, nil
	}
	values := make([]string, len(titles))
	copy(values, args)
	var fields []huh.Field
	for i := len(args); i < len(titles); i++ {
		fields = append(fields, huh.NewInput().Title(titles[i]).Value(&values[i]))
	}
	if err := prompt(huh.NewForm(huh.NewGroup(fields...))); err != nil {
		return nil, err
	}
	return values, nil
}

// promptProject asks for a registered project, or returns cause when it cannot.
func promptProject(homeDir string, cause error) (string, error) {
	if !interactive() {
		return "", cause
	}
	projects, err := fleet.List(homeDir)
	if err != nil || len(projects) == 0 {
		return "", cause
	}
	options := make([]huh.Option[string], len(projects))
	for i, p := range projects {
		options[i] = huh.NewOption(p.Name, p.Name)
	}
	var name string
	if err := prompt(huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Project").Options(options...).Value(&name)))); err != nil {
		return "", err
	}
	return name, nil
}
