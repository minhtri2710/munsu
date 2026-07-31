package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/spf13/cobra"
)

func newConfigDispatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Manage soldier dispatch profiles (config/base.json)",
		Long: `Manage the soldier dispatch profile file used at spawn time.

The dispatch profiles live in the fleet base document at $MUNSU_HOME/config/base.json
and select harness/model/effort from ordered profiles (match/when rules).

Spawn precedence: CLI --harness/--model/--effort > matched profile >
adapter template defaults.

Subcommands:
  show         Print the active dispatch config
  path         Print the file path
  set-default  Set default harness/model
  add          Add or replace a named profile
  rm           Remove a named profile
  clear        Clear all dispatch profiles
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
			basePath := filepath.Join(ctx.Home, config.BaseDocumentPath)
			base, err := config.LoadFleetBase(ctx.Home)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return writeContract(cmd, Response[MessageResult]{
						SchemaVersion: SchemaVersion,
						Kind:          "config.dispatch",
						Status:        "success",
						Data:          MessageResult{Message: "dispatch: <not set>\n  path: " + basePath},
					})
				}
				return err
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data:          MessageResult{Message: formatDispatchFromBase(base, basePath)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newConfigDispatchPathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the fleet base document path",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "message",
				Status:        "success",
				Data:          MessageResult{Message: filepath.Join(ctx.Home, config.BaseDocumentPath)},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newConfigDispatchSetDefaultCmd() *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "set-default <harness>",
		Short: "Set default harness (and optional model) for dispatch",
		Args:  ExactArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			hName := args[0]
			if err := harness.ValidateHarness(hName); err != nil {
				return fmt.Errorf("set-default: %w", err)
			}
			base, err := loadOrEmptyBase(ctx.Home)
			if err != nil {
				return err
			}
			base.Config.SoldierHarness = hName
			if cmd.Flags().Changed("model") {
				base.Config.Model = model
			}
			if err := config.StoreFleetBase(ctx.Home, base); err != nil {
				return err
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data: MessageResult{Message: fmt.Sprintf(
					"default set: harness=%s model=%s\n  path: %s",
					base.Config.SoldierHarness, emptyDash(base.Config.Model), filepath.Join(ctx.Home, config.BaseDocumentPath),
				)},
			})
		}),
	}
	cmd.Flags().StringVar(&model, "model", "", "Default model id")
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
		Long: `Add a named profile to the fleet base document.

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

			base, err := loadOrEmptyBase(ctx.Home)
			if err != nil {
				return err
			}

			idx := -1
			for i, p := range base.Config.DispatchProfiles {
				if p.Name == name {
					idx = i
					break
				}
			}
			if idx >= 0 && !replace {
				return fmt.Errorf("add: profile %q already exists (pass --replace to overwrite)", name)
			}

			prof := config.DispatchProfile{
				Name:    name,
				Match:   append([]string(nil), match...),
				When:    when,
				Harness: hName,
				Model:   model,
				Effort:  effort,
			}
			if idx >= 0 {
				base.Config.DispatchProfiles[idx] = prof
			} else {
				base.Config.DispatchProfiles = append(base.Config.DispatchProfiles, prof)
			}
			if err := config.StoreFleetBase(ctx.Home, base); err != nil {
				return err
			}
			action := "added"
			if idx >= 0 {
				action = "replaced"
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data: MessageResult{Message: fmt.Sprintf(
					"profile %s: %s (harness=%s model=%s effort=%s)\n  path: %s",
					action, name, hName, emptyDash(model), emptyDash(effort), filepath.Join(ctx.Home, config.BaseDocumentPath),
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
			base, err := config.LoadFleetBase(ctx.Home)
			if err != nil {
				return fmt.Errorf("rm: %w", err)
			}
			out := base.Config.DispatchProfiles[:0]
			found := false
			for _, p := range base.Config.DispatchProfiles {
				if p.Name == name {
					found = true
					continue
				}
				out = append(out, p)
			}
			if !found {
				return fmt.Errorf("rm: profile %q not found", name)
			}
			base.Config.DispatchProfiles = out
			if err := config.StoreFleetBase(ctx.Home, base); err != nil {
				return err
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data:          MessageResult{Message: fmt.Sprintf("removed profile %q\n  path: %s", name, filepath.Join(ctx.Home, config.BaseDocumentPath))},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func newConfigDispatchClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear all dispatch profiles from the fleet base document",
		Args:  NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			base, err := config.LoadFleetBase(ctx.Home)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return writeContract(cmd, Response[MessageResult]{
						SchemaVersion: SchemaVersion,
						Kind:          "config.dispatch",
						Status:        "success",
						Data:          MessageResult{Message: "dispatch: <not set>"},
					})
				}
				return err
			}
			base.Config.DispatchProfiles = nil
			base.Config.SoldierHarness = ""
			base.Config.Model = ""
			if err := config.StoreFleetBase(ctx.Home, base); err != nil {
				return err
			}
			return writeContract(cmd, Response[MessageResult]{
				SchemaVersion: SchemaVersion,
				Kind:          "config.dispatch",
				Status:        "success",
				Data:          MessageResult{Message: "dispatch cleared"},
			})
		}),
	}
	configureContractCommand(cmd)
	return cmd
}

func loadOrEmptyBase(homeDir string) (config.FleetBaseDocument, error) {
	base, err := config.LoadFleetBase(homeDir)
	if err == nil {
		return base, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return config.FleetBaseDocument{
			SchemaVersion: config.FleetBaseSchemaVersion,
		}, nil
	}
	return base, err
}

func formatDispatchFromBase(base config.FleetBaseDocument, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "path: %s\n", path)
	fmt.Fprintf(&b, "default: harness=%s model=%s\n",
		emptyDash(base.Config.SoldierHarness), emptyDash(base.Config.Model))
	if len(base.Config.DispatchProfiles) == 0 {
		b.WriteString("profiles: (none)\n")
		return strings.TrimSpace(b.String())
	}
	fmt.Fprintf(&b, "profiles: %d\n", len(base.Config.DispatchProfiles))
	for i, p := range base.Config.DispatchProfiles {
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