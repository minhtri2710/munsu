package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
	command string
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
  install        Install integration artifacts for a harness
  repair         Detect drift and repair integration artifacts
  status         Show integration status for a harness
  safety-check   Evaluate scope/gate and command safety for a path

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

	safetyCmd := &cobra.Command{
		Use:   "safety-check [path]",
		Short: "Evaluate scope/gate and command safety for a path",
		Long: `Evaluate whether the given path (or cwd) is safe for fleet lifecycle operations.

Returns structured safety assessment including identity classification,
gate detection, and optional command-blocking rules.

Results:
  identity         primary | worktree | unrelated
  gate_capability  gate-present | gate-absent | gate-unknown
  gate_refused     true if gate is active
  block            true if the supplied --command should be blocked
  reason           explanation when blocked

Flags:
  --command      Command string to evaluate for blocking rules (for tool_call safety)`,
		Args: cobra.MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			checkPath, _ := os.Getwd()
			if len(args) > 0 {
				checkPath = args[0]
			}
			return runSafetyCheck(cmd, checkPath, flags.command)
		}),
	}
	safetyCmd.Flags().StringVar(&flags.command, "command", "", "Command to evaluate for blocking rules")
	configureContractCommand(safetyCmd)

	cmd.AddCommand(installCmd)
	cmd.AddCommand(repairCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(safetyCmd)

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

func runSafetyCheck(cmd *cobra.Command, checkPath string, checkCommand string) error {
	// Evaluate scope/gate safety.
	result := integrate.SafetyCheck(checkPath)

	// Evaluate command blocking rules.
	block := false
	reason := ""
	if checkCommand != "" {
		if strings.Contains(checkCommand, "munsu watch arm") ||
			strings.Contains(checkCommand, "munsu watch ensure") {
			block = true
			reason = "Use 'munsu guard' or 'munsu watch run' for inspection; watcher lifecycle is managed automatically."
		}

		if strings.Contains(checkCommand, "cd .no-mistakes") ||
			strings.Contains(checkCommand, "cd ~/.no-mistakes") ||
			strings.Contains(checkCommand, "/.no-mistakes/") {
			if !strings.Contains(checkCommand, "guard") && !strings.Contains(checkCommand, "doctor") {
				block = true
				reason = "No-mistakes managed directories are not regular projects."
			}
		}

		nmHome := os.Getenv("NM_HOME")
		if nmHome == "" {
			if h, err := os.UserHomeDir(); err == nil {
				nmHome = filepath.Join(h, ".no-mistakes")
			}
		}
		_ = nmHome
	}

	data := struct {
		Identity       string `json:"identity"`
		GateCapability string `json:"gate_capability"`
		CanonicalPath  string `json:"canonical_path,omitempty"`
		GateRefused    bool   `json:"gate_refused"`
		Block          bool   `json:"block,omitempty"`
		Reason         string `json:"reason,omitempty"`
		Error          string `json:"error,omitempty"`
	}{
		Identity:       result.Identity,
		GateCapability: result.GateCapability,
		CanonicalPath:  result.CanonicalPath,
		GateRefused:    result.GateRefused,
		Block:          block,
		Reason:         reason,
		Error:          result.Error,
	}

	return writeContract(cmd, contract.Response[interface{}]{
		SchemaVersion: contract.SchemaVersion,
		Kind:          "integrate.safety-check",
		Status:        "success",
		Data:          data,
	})
}
