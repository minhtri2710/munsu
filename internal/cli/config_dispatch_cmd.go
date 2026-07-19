package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/spf13/cobra"
)

func newConfigDispatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Manage soldier dispatch profiles (config/soldier-dispatch.json)",
		Long: `Manage the soldier dispatch profile file used at spawn time.

The file lives at $MUNSU_HOME/config/soldier-dispatch.json and selects
harness/model/effort from ordered profiles (match/when rules).

Spawn precedence: CLI --harness/--model/--effort > matched profile >
adapter template defaults.

Subcommands:
  show         Print the active dispatch config
  path         Print the file path
  set-default  Set default harness/model/effort
  add          Add or replace a named profile
  rm           Remove a named profile
  clear        Delete the dispatch file
`,
	}

	cmd.AddCommand(newConfigDispatchShowCmd())
	cmd.AddCommand(newConfigDispatchPathCmd())
	cmd.AddCommand(newConfigDispatchSetDefaultCmd())
	cmd.AddCommand(newConfigDispatchAddCmd())
	cmd.AddCommand(newConfigDispatchRmCmd())
	cmd.AddCommand(newConfigDispatchClearCmd())
	return cmd
}

func newConfigDispatchShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the active soldier dispatch config",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			path := harness.DispatchPath(ctx.Home)
			cfg, err := harness.LoadDispatch(path)
			if err != nil {
				if isMissingDispatch(err) {
					return writeContract(cmd, contract.Response[contract.MessageResult]{
						SchemaVersion: contract.SchemaVersion,
						Kind:          "config.dispatch",
						Status:        "success",
						Data:          contract.MessageResult{Message: "dispatch: <not set>\n  path: " + path},
					})
				}
				return err
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data:          contract.MessageResult{Message: formatDispatch(cfg, path)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newConfigDispatchPathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the soldier-dispatch.json path",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          contract.MessageResult{Message: harness.DispatchPath(ctx.Home)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newConfigDispatchSetDefaultCmd() *cobra.Command {
	var model, effort string
	cmd := &cobra.Command{
		Use:   "set-default <harness>",
		Short: "Set default harness (and optional model/effort) for dispatch",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			hName := args[0]
			if err := harness.ValidateHarness(hName); err != nil {
				return fmt.Errorf("set-default: %w", err)
			}
			path := harness.DispatchPath(ctx.Home)
			cfg, err := loadOrEmptyDispatch(path)
			if err != nil {
				return err
			}
			cfg.DefaultHarness = hName
			if cmd.Flags().Changed("model") {
				cfg.DefaultModel = model
			}
			if cmd.Flags().Changed("effort") {
				cfg.DefaultEffort = effort
			}
			// Keep object form in sync for dual-shape readers.
			cfg.Default = &harness.DispatchCandidate{
				Harness: cfg.DefaultHarness,
				Model:   cfg.DefaultModel,
				Effort:  cfg.DefaultEffort,
			}
			if err := harness.SaveDispatch(path, cfg); err != nil {
				return err
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data: contract.MessageResult{Message: fmt.Sprintf(
					"default set: harness=%s model=%s effort=%s\n  path: %s",
					cfg.DefaultHarness, emptyDash(cfg.DefaultModel), emptyDash(cfg.DefaultEffort), path,
				)},
			})
		}),
	}
	cmd.Flags().StringVar(&model, "model", "", "Default model id")
	cmd.Flags().StringVar(&effort, "effort", "", "Default effort/thinking level")
	configureContractCommand(cmd)
	return cmd
}

func newConfigDispatchAddCmd() *cobra.Command {
	var (
		name    string
		match   []string
		when    string
		hName   string
		model   string
		effort  string
		replace bool
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add or replace a dispatch profile",
		Long: `Add a named profile to soldier-dispatch.json.

Requires --name, --harness, and at least one of --match or --when.
If a profile with the same name exists, pass --replace to overwrite it.`,
		Args: NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if name == "" {
				return fmt.Errorf("add: --name is required")
			}
			if hName == "" {
				return fmt.Errorf("add: --harness is required")
			}
			if err := harness.ValidateHarness(hName); err != nil {
				return fmt.Errorf("add: %w", err)
			}
			if len(match) == 0 && when == "" {
				return fmt.Errorf("add: at least one --match or --when is required")
			}

			path := harness.DispatchPath(ctx.Home)
			cfg, err := loadOrEmptyDispatch(path)
			if err != nil {
				return err
			}

			idx := -1
			for i, p := range cfg.Profiles {
				if p.Name == name {
					idx = i
					break
				}
			}
			if idx >= 0 && !replace {
				return fmt.Errorf("add: profile %q already exists (pass --replace to overwrite)", name)
			}

			prof := harness.DispatchProfile{
				Name:    name,
				Match:   append([]string(nil), match...),
				When:    when,
				Harness: hName,
				Model:   model,
				Effort:  effort,
			}
			if idx >= 0 {
				cfg.Profiles[idx] = prof
			} else {
				cfg.Profiles = append(cfg.Profiles, prof)
			}
			if err := harness.SaveDispatch(path, cfg); err != nil {
				return err
			}
			action := "added"
			if idx >= 0 {
				action = "replaced"
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data: contract.MessageResult{Message: fmt.Sprintf(
					"profile %s: %s (harness=%s model=%s effort=%s)\n  path: %s",
					action, name, hName, emptyDash(model), emptyDash(effort), path,
				)},
			})
		}),
	}
	cmd.Flags().StringVar(&name, "name", "", "Profile name (required)")
	cmd.Flags().StringArrayVar(&match, "match", nil, "Match token or phrase (repeatable)")
	cmd.Flags().StringVar(&when, "when", "", "Free-form match prose (substring / phrase match)")
	cmd.Flags().StringVar(&hName, "harness", "", "Target harness (required)")
	cmd.Flags().StringVar(&model, "model", "", "Model id for this profile")
	cmd.Flags().StringVar(&effort, "effort", "", "Effort/thinking level for this profile")
	cmd.Flags().BoolVar(&replace, "replace", false, "Replace an existing profile with the same name")
	configureContractCommand(cmd)
	return cmd
}

func newConfigDispatchRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a named dispatch profile",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			name := args[0]
			path := harness.DispatchPath(ctx.Home)
			cfg, err := harness.LoadDispatch(path)
			if err != nil {
				return fmt.Errorf("rm: %w", err)
			}
			out := cfg.Profiles[:0]
			found := false
			for _, p := range cfg.Profiles {
				if p.Name == name {
					found = true
					continue
				}
				out = append(out, p)
			}
			if !found {
				return fmt.Errorf("rm: profile %q not found", name)
			}
			cfg.Profiles = out
			if err := harness.SaveDispatch(path, cfg); err != nil {
				return err
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data:          contract.MessageResult{Message: fmt.Sprintf("removed profile %q\n  path: %s", name, path)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newConfigDispatchClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete soldier-dispatch.json (no dispatch profiles)",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			path := harness.DispatchPath(ctx.Home)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			return writeContract(cmd, contract.Response[contract.MessageResult]{
				SchemaVersion: contract.SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data:          contract.MessageResult{Message: "dispatch cleared\n  path: " + path},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func loadOrEmptyDispatch(path string) (*harness.DispatchConfig, error) {
	cfg, err := harness.LoadDispatch(path)
	if err == nil {
		return cfg, nil
	}
	if isMissingDispatch(err) {
		return &harness.DispatchConfig{}, nil
	}
	return nil, err
}

func isMissingDispatch(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	// LoadDispatch wraps the read error.
	return strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "reading dispatch config")
}

func formatDispatch(cfg *harness.DispatchConfig, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "path: %s\n", path)
	fmt.Fprintf(&b, "default: harness=%s model=%s effort=%s\n",
		emptyDash(cfg.DefaultHarness), emptyDash(cfg.DefaultModel), emptyDash(cfg.DefaultEffort))
	if len(cfg.Profiles) == 0 {
		b.WriteString("profiles: (none)\n")
		return strings.TrimSpace(b.String())
	}
	fmt.Fprintf(&b, "profiles: %d\n", len(cfg.Profiles))
	for i, p := range cfg.Profiles {
		label := p.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		match := strings.Join(p.Match, ", ")
		if match == "" && p.When != "" {
			match = p.When
		}
		if match == "" {
			match = "-"
		}
		fmt.Fprintf(&b, "  - %s: harness=%s model=%s effort=%s match=%q\n",
			label, emptyDash(p.Harness), emptyDash(p.Model), emptyDash(p.Effort), match)
	}
	return strings.TrimSpace(b.String())
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
