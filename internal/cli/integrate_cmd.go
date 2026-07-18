package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/integrate"
	"github.com/spf13/cobra"
)

type integrateFlags struct {
	harness string
	scope   string
	dryRun  bool
}

func newIntegrateCmd() *cobra.Command {
	var flags integrateFlags

	cmd := &cobra.Command{
		Use:   "integrate",
		Short: "Install, repair, or inspect native harness integration",
		Long: `Manage native integration artifacts for the agent harness.

Integration is opt-in: no harness-level hooks or extensions are created
during 'munsu init'. Use 'munsu integrate install --harness <name>' to
install native hooks (Pi extensions, Claude hooks, etc.) for your
detected or chosen agent harness.

Commands:
  install   Install integration artifacts for a harness
  repair    Detect drift and repair integration artifacts
  status    Show integration status for a harness

Flags:
  --harness          Target harness (default: auto-detect)
  --scope            Installation scope: "user" (default) or "project"
  --dry-run          Report what would change without writing`,
	}
	configureContractCommand(cmd)

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install native integration artifacts for a harness",
		Long: `Install integration artifacts for the specified harness.

Idempotent: repeated install with identical content is a no-op.
Owned artifacts are marked; existing user content is backed up.

Use --harness to specify the target harness. When omitted, the
currently active harness is auto-detected.`,
		Args: cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runIntegrateInstall(cmd, ctx, flags)
		}),
	}
	configureContractCommand(installCmd)

	repairCmd := &cobra.Command{
		Use:   "repair",
		Short: "Detect drift and repair integration artifacts",
		Long: `Check installed integration artifacts for health, ownership marker
presence, and version compatibility. Repair drifted or missing
content by re-installing owned artifacts.`,
		Args: cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runIntegrateRepair(cmd, ctx, flags)
		}),
	}
	configureContractCommand(repairCmd)

	statusCmd := &cobra.Command{
		Use:   "status [harness]",
		Short: "Show integration status for a harness",
		Long: `Show whether integration artifacts are installed, healthy, drifted,
or unsupported for the specified or auto-detected harness.

Reports the manifest version, installed-at timestamp, and current
health state.`,
		Args: cobra.MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			if len(args) > 0 {
				flags.harness = args[0]
			}
			return runIntegrateStatus(cmd, ctx, flags)
		}),
	}
	configureContractCommand(statusCmd)

	cmd.PersistentFlags().StringVar(&flags.harness, "harness", "", "Target harness (default: auto-detect)")
	cmd.PersistentFlags().StringVar(&flags.scope, "scope", "user", "Installation scope: user (default) or project")
	cmd.PersistentFlags().BoolVar(&flags.dryRun, "dry-run", false, "Report what would change without writing")

	cmd.AddCommand(installCmd)
	cmd.AddCommand(repairCmd)
	cmd.AddCommand(statusCmd)

	return cmd
}

func resolveIntegrateScope(raw string) (integrate.Scope, error) {
	switch raw {
	case "user":
		return integrate.ScopeUser, nil
	case "project":
		return integrate.ScopeProject, nil
	default:
		return "", fmt.Errorf("unsupported scope %q: must be 'user' or 'project'", raw)
	}
}

func runIntegrateInstall(cmd *cobra.Command, ctx Ctx, flags integrateFlags) error {
	scope, err := resolveIntegrateScope(flags.scope)
	if err != nil {
		return err
	}

	harnessName := flags.harness
	if harnessName == "" {
		detected, err := detectHarnessForIntegrate(ctx.Home)
		if err != nil {
			return fmt.Errorf("auto-detect harness: %w (specify --harness)", err)
		}
		harnessName = detected
	}

	cwd, _ := os.Getwd()
	result, err := integrate.Install(ctx.Home, cwd, harnessName, scope, flags.dryRun)
	if err != nil {
		return err
	}

	return writeContract(cmd, contract.Response[integrateResultData]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "integrate.install",
		Status:        "success",
		Data: integrateResultData{
			Harness:     result.Harness,
			State:       result.State,
			Message:     result.Message,
			Version:     result.Version,
			InstalledAt: result.InstalledAt,
		},
	})
}

func runIntegrateRepair(cmd *cobra.Command, ctx Ctx, flags integrateFlags) error {
	scope, err := resolveIntegrateScope(flags.scope)
	if err != nil {
		return err
	}

	harnessName := flags.harness
	if harnessName == "" {
		detected, err := detectHarnessForIntegrate(ctx.Home)
		if err != nil {
			return fmt.Errorf("auto-detect harness: %w (specify --harness)", err)
		}
		harnessName = detected
	}

	cwd, _ := os.Getwd()
	result, err := integrate.Repair(ctx.Home, cwd, harnessName, scope, flags.dryRun)
	if err != nil {
		return err
	}

	return writeContract(cmd, contract.Response[integrateResultData]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "integrate.repair",
		Status:        "success",
		Data: integrateResultData{
			Harness:     result.Harness,
			State:       result.State,
			Message:     result.Message,
			Version:     result.Version,
			InstalledAt: result.InstalledAt,
		},
	})
}

func runIntegrateStatus(cmd *cobra.Command, ctx Ctx, flags integrateFlags) error {
	scope, err := resolveIntegrateScope(flags.scope)
	if err != nil {
		return err
	}

	harnessName := flags.harness
	if harnessName == "" {
		detected, err := detectHarnessForIntegrate(ctx.Home)
		if err == nil {
			harnessName = detected
		}
	}

	cwd, _ := os.Getwd()
	result, err := integrate.Status(ctx.Home, cwd, harnessName, scope)
	if err != nil {
		return err
	}

	return writeContract(cmd, contract.Response[integrateResultData]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "integrate.status",
		Status:        "success",
		Data: integrateResultData{
			Harness:     result.Harness,
			State:       result.State,
			Message:     result.Message,
			Version:     result.Version,
			InstalledAt: result.InstalledAt,
			Drifted:     result.Drifted,
		},
	})
}

func detectHarnessForIntegrate(homeDir string) (string, error) {
	return harness.Crew(homeDir)
}

type integrateResultData struct {
	Harness     string `json:"harness"`
	State       string `json:"state"`
	Message     string `json:"message,omitempty"`
	Version     string `json:"version,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	Drifted     bool   `json:"drifted,omitempty"`
}

func renderIntegrateResult(data integrateResultData) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("harness:    %s\n", data.Harness))
	b.WriteString(fmt.Sprintf("state:      %s\n", data.State))
	if data.Message != "" {
		b.WriteString(fmt.Sprintf("message:    %s\n", data.Message))
	}
	if data.Version != "" {
		b.WriteString(fmt.Sprintf("version:    %s\n", data.Version))
	}
	if data.InstalledAt != "" {
		b.WriteString(fmt.Sprintf("installed:  %s\n", data.InstalledAt))
	}
	return strings.TrimSpace(b.String())
}
