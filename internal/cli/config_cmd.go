package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/spf13/cobra"
)

// wellKnownConfigKeys lists the commonly used config keys for display.
var wellKnownConfigKeys = []string{
	"backend",
	"crew-harness",
	"secondmate-harness",
	"backlog-backend",
	"default-mode",
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read, write, and view munsu configuration",
		Long: `Read, write, and view munsu configuration.

Configuration values are stored as files under $MUNSU_HOME/config/<key>.
Each value can be overridden at runtime by setting the environment variable
MUNSU_<KEY>_OVERRIDE (e.g. MUNSU_BACKEND_OVERRIDE=tmux).

Known config keys: ` + strings.Join(wellKnownConfigKeys, ", ") + `.
`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			val, err := config.Get(ctx.Home, args[0])
			if err != nil {
				return err
			}
			fmt.Println(val)
			return nil
		}),
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  ExactArgs(2),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			key, value := args[0], args[1]
			// Validate harness keys against KnownHarnesses
			if key == "crew-harness" || key == "secondmate-harness" {
				if err := harness.ValidateHarness(value); err != nil {
					return fmt.Errorf("config set %s: %w", key, err)
				}
			}
			return config.Set(ctx.Home, key, value)
		}),
	})
	cmd.AddCommand(newConfigShowCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show resolved configuration values with source",
		Args:  NoArgs,
		Long: `Display all well-known configuration values and their sources.

Each value is resolved using the same precedence as the rest of munsu:
  MUNSU_<KEY>_OVERRIDE environment variable > file at $MUNSU_HOME/config/<key>.
Values that are not set are shown as "<not set>".

Override environment variables:
  MUNSU_BACKEND_OVERRIDE, MUNSU_CREW_HARNESS_OVERRIDE,
  MUNSU_SECONDMATE_HARNESS_OVERRIDE, MUNSU_BACKLOG_BACKEND_OVERRIDE,
  MUNSU_DEFAULT_MODE_OVERRIDE
`,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			showConfig(ctx.Home)
			return nil
		}),
	}
}

// showConfig prints all well-known configuration values with their source.
func showConfig(homeDir string) {
	fmt.Printf("%-30s %s\n", "KEY", "VALUE")
	fmt.Println(strings.Repeat("-", 80))

	// Home path
	fmt.Printf("%-30s %s\n", "home", homeDir)

	for _, key := range wellKnownConfigKeys {
		envKey := fmt.Sprintf("MUNSU_%s_OVERRIDE", strings.ToUpper(key))
		val, err := config.Get(homeDir, key)
		if err != nil {
			// Check if env override exists (config.Get returns it, but if
			// neither file nor env is set, it returns an error).
			if envVal, ok := os.LookupEnv(envKey); ok {
				fmt.Printf("%-30s %s (env: %s)\n", key, envVal, envKey)
			} else {
				fmt.Printf("%-30s <not set>\n", key)
			}
			continue
		}
		// Determine source
		if _, ok := os.LookupEnv(envKey); ok {
			fmt.Printf("%-30s %s (env: %s)\n", key, val, envKey)
		} else {
			fmt.Printf("%-30s %s (file: %s)\n", key, val, config.ConfigDir(homeDir)+"/"+key)
		}
	}

	// Show additional keys that happen to exist.
	additionalKeys := findExtraConfigKeys(homeDir)
	if len(additionalKeys) > 0 {
		fmt.Println()
		fmt.Println("Additional config keys:")
		for _, key := range additionalKeys {
			val, err := config.Get(homeDir, key)
			if err == nil {
				fmt.Printf("  %-26s %s\n", key, val)
			}
		}
	}
}

// findExtraConfigKeys lists config files that are not in the well-known list.
func findExtraConfigKeys(homeDir string) []string {
	known := make(map[string]bool, len(wellKnownConfigKeys))
	for _, k := range wellKnownConfigKeys {
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
