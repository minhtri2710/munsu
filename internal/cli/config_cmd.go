package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read, write, and view munsu configuration",
		Long: `Read, write, and view munsu configuration.

Configuration values are stored as files under $MUNSU_HOME/config/<key>.
The backend key reports the persisted snapshot Backend (the published config
snapshot or the fleet base document's typed Backend); other keys report the
persisted file value.

Known config keys: ` + strings.Join(config.KnownKeys, ", ") + `.
`,
	}
	getCmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key := args[0]
			// backend is a persisted snapshot identity (published snapshot or fleet
			// base document's typed Backend), never a live env/PATH probe. Report
			// the persisted snapshot Backend, or a typed missing-input when none is
			// persisted.
			if key == "backend" {
				resolved, err := fleet.ResolveGeneralHomeBackend(ctx.Home)
				if err != nil || resolved == "" {
					return usageError("missing_input", "Set backend in the fleet base config and rerun `munsu config get backend`", "no persisted backend identity is available")
				}
				return writeContract(cmd, Response[MessageResult]{
					SchemaVersion: SchemaVersion,
					Kind:          "message",
					Status:        "success",
					Data:          MessageResult{Message: resolved},
				})
			}
			val, err := config.Get(ctx.Home, key)
			if err != nil {
				if config.IsKnownKey(key) {
					return nil
				}
				return err
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          MessageResult{Message: val},
			})
		}),
	}
	configureContractCommand(getCmd)
	cmd.AddCommand(getCmd)

	setCmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key, value := args[0], args[1]
			// Validate harness pins. captain-harness accepts multi-token
			// lines: "<harness> [<model>] [<effort>]". soldier-harness is bare name only.
			switch key {
			case "soldier-harness":
				if err := harness.ValidateHarness(value); err != nil {
					return fmt.Errorf("config set %s: %w", key, err)
				}
			case "captain-harness":
				prof := harness.ParseHarnessLine(value)
				if prof.Harness == "" && strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "default" {
					// bare "default" is allowed (unset sentinel)
					if !strings.HasPrefix(strings.TrimSpace(value), "#") {
						return fmt.Errorf("config set %s: empty harness token in %q (want \"<harness> [<model>] [<effort>]\")", key, value)
					}
				}
				if prof.Harness != "" {
					if err := harness.ValidateHarness(prof.Harness); err != nil {
						return fmt.Errorf("config set %s: %w", key, err)
					}
				}
			case "model-allowlist":
				// One <harness>:<model> identity per line; empty (deny-all) is allowed.
				if err := harness.ValidateModelAllowlist(value); err != nil {
					return fmt.Errorf("config set %s: %w", key, err)
				}
			}
			return config.Set(ctx.Home, key, value)
		}),
	}
	cmd.AddCommand(setCmd)
	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigDispatchCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show resolved configuration values with source",
		Args:  NoArgs,
		Long: `Display all persisted configuration values and their source.

Each value is read from the config file at $MUNSU_HOME/config/<key>.
Values that are not set are shown as "<not set>".
`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "config.show",
				Status:        "success",
				Data:          MessageResult{Message: showConfig(ctx.Home)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

// showConfig returns all well-known configuration values with their source.
func showConfig(homeDir string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-30s %s\n", "KEY", "VALUE"))
	b.WriteString(strings.Repeat("-", 80) + "\n")

	// Home path
	b.WriteString(fmt.Sprintf("%-30s %s\n", "home", homeDir))

	for _, key := range config.KnownKeys {
		val, err := config.Get(homeDir, key)
		if err != nil {
			b.WriteString(fmt.Sprintf("%-30s <not set>\n", key))
			continue
		}
		b.WriteString(fmt.Sprintf("%-30s %s (file: %s)\n", key, val, config.ConfigDir(homeDir)+"/"+key))
	}

	// Show additional keys that happen to exist.
	additionalKeys := findExtraConfigKeys(homeDir)
	if len(additionalKeys) > 0 {
		b.WriteString("\nAdditional config keys:\n")
		for _, key := range additionalKeys {
			val, err := config.Get(homeDir, key)
			if err == nil {
				b.WriteString(fmt.Sprintf("  %-26s %s\n", key, val))
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// findExtraConfigKeys lists config files that are not in the well-known list.
func findExtraConfigKeys(homeDir string) []string {
	known := make(map[string]bool, len(config.KnownKeys))
	for _, k := range config.KnownKeys {
		known[k] = true
	}
	entries, err := os.ReadDir(config.ConfigDir(homeDir))
	if err != nil {
		return nil
	}
	var extra []string
	for _, e := range entries {
		if !e.IsDir() && !known[e.Name()] {
			extra = append(extra, e.Name())
		}
	}
	sort.Strings(extra)
	return extra
}
