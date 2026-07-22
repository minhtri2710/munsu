package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/minhtri2710/munsu/internal/contract"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/integrate"
	"github.com/minhtri2710/munsu/internal/session"
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
  install             Install integration artifacts for a harness
  repair              Detect drift and repair integration artifacts
  status              Show integration status for a harness
  safety-check        Evaluate scope/gate and command safety for a path
  sessionstart-nudge  Print session-start instruction or stay silent (for Claude SessionStart hook)

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
	installCmd.Flags().StringVar(&flags.harness, "harness", "", "Target harness (default: auto-detect)")
	installCmd.Flags().StringVar(&flags.scope, "scope", "user", "Installation scope: user or project")
	installCmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "Report what would change without writing")

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
	repairCmd.Flags().StringVar(&flags.harness, "harness", "", "Target harness (default: auto-detect)")
	repairCmd.Flags().StringVar(&flags.scope, "scope", "user", "Installation scope: user or project")
	repairCmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "Report what would change without writing")

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
	statusCmd.Flags().StringVar(&flags.scope, "scope", "user", "Check this installation scope: user or project")

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
  --command      Command string to evaluate for blocking rules (for tool_call safety)
  --harness      Output shape: "pi" (default, JSON contract), "claude" (native deny exit 2 + stderr), "codex" (stderr plaintext + exit 2), "grok" (stdout decision=deny object + exit 2), "opencode" (stderr plaintext + exit 2, same as codex), or "agy" (stdout decision JSON + exit 0)`,
		Args: cobra.MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			checkPath, _ := os.Getwd()
			if len(args) > 0 {
				checkPath = args[0]
			}
			return runSafetyCheck(cmd, checkPath, flags.command, flags.harness)
		}),
	}
	safetyCmd.Flags().StringVar(&flags.command, "command", "", "Command to evaluate for blocking rules")
	safetyCmd.Flags().StringVar(&flags.harness, "harness", "", "Output shape: pi (default), claude, grok, codex, opencode, or agy")
	configureContractCommand(safetyCmd)

	sessionstartNudgeCmd := &cobra.Command{
		Use:   "sessionstart-nudge",
		Short: "Print session-start instruction or stay silent (for Claude SessionStart hook)",
		Long: `Lightweight SessionStart hook command for Claude.

Checks:
1. NO_MISTAKES_GATE gate agent → silent, exit 0
2. Primary checkout scope → silent, exit 0 if not primary
3. Lock ancestry (8 parents) → silent, exit 0 if lock pid is in ancestry

When all checks pass, prints exactly one instruction line and exits 0.
Always exits 0 because Claude SessionStart exit 2 blocks session init.`,
		Args: cobra.NoArgs,
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			return runSessionStartNudge(cmd, ctx)
		}),
	}

	cmd.AddCommand(installCmd)
	cmd.AddCommand(repairCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(safetyCmd)
	cmd.AddCommand(sessionstartNudgeCmd)

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
	return harness.Soldier(homeDir)
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

// exitWithCode is overridable for testing. Defaults to os.Exit.
var exitWithCode = func(code int) {
	os.Exit(code)
}

// readStdinForCommand reads JSON from stdin and extracts the command field.
// Supports Claude shape (.tool_input.command), Grok shape (.toolInput.command),
// and plain JSON with command.
func readStdinForCommand() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "", nil
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", nil // not JSON, return empty
	}

	// Try Claude shape: .tool_input.command
	if ti, ok := payload["tool_input"].(map[string]interface{}); ok {
		if cmd, ok := ti["command"].(string); ok && cmd != "" {
			return cmd, nil
		}
	}
	// Try agy shape: .toolCall.args.CommandLine (PascalCase nested)
	if tc, ok := payload["toolCall"].(map[string]interface{}); ok {
		if args, ok := tc["args"].(map[string]interface{}); ok {
			if cmd, ok := args["CommandLine"].(string); ok && cmd != "" {
				return cmd, nil
			}
		}
	}
	// Try Grok shape: .toolInput.command (camelCase)
	if ti, ok := payload["toolInput"].(map[string]interface{}); ok {
		if cmd, ok := ti["command"].(string); ok && cmd != "" {
			return cmd, nil
		}
	}
	// Try non-nested "command" key
	if cmd, ok := payload["command"].(string); ok && cmd != "" {
		return cmd, nil
	}

	return "", nil
}

func runSafetyCheck(cmd *cobra.Command, checkPath string, checkCommand string, harnessFlag string) error {
	// When --harness claude or --harness grok and no --command, try to read command from stdin
	effectiveCommand := checkCommand
	if (harnessFlag == "claude" || harnessFlag == "grok" || harnessFlag == "codex" || harnessFlag == "agy") && effectiveCommand == "" {
		stdinCommand, err := readStdinForCommand()
		if err == nil && stdinCommand != "" {
			effectiveCommand = stdinCommand
		}
	}

	// Evaluate scope/gate safety.
	result := integrate.SafetyCheck(checkPath)

	// Evaluate command blocking rules.
	block := false
	reason := ""
	if effectiveCommand != "" {
		if strings.Contains(effectiveCommand, "munsu watch arm") ||
			strings.Contains(effectiveCommand, "munsu watch ensure") ||
			strings.Contains(effectiveCommand, "munsu watch stop") {
			block = true
			reason = "Use 'munsu guard' or 'munsu watch run' for inspection; watcher lifecycle is managed automatically."
		} else if strings.Contains(effectiveCommand, "munsu watch") &&
			!strings.Contains(effectiveCommand, "run") &&
			!strings.Contains(effectiveCommand, "--help") {
			// Block bare `munsu watch` (daemon mode) but allow `munsu watch run` and --help.
			block = true
			reason = "Watcher lifecycle is managed automatically; use 'munsu watch run' for inspection."
		}

		if strings.Contains(effectiveCommand, "cd .no-mistakes") ||
			strings.Contains(effectiveCommand, "cd ~/.no-mistakes") ||
			strings.Contains(effectiveCommand, "/.no-mistakes/") {
			if !strings.Contains(effectiveCommand, "guard") && !strings.Contains(effectiveCommand, "doctor") {
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

	// Determine effective block: gate_refused also blocks
	effectiveBlock := block || result.GateRefused
	if result.GateRefused && reason == "" {
		reason = result.Error
		if reason == "" {
			reason = "Scope/gate refuses action in this directory."
		}
	}

	// Harness-specific output shapes
	if harnessFlag == "claude" {
		if effectiveBlock {
			// Claude deny: stdout EMPTY, stderr JSON, exit 2
			denyJSON, _ := json.Marshal(map[string]interface{}{
				"hookSpecificOutput": map[string]interface{}{
					"hookEventName":      "PreToolUse",
					"permissionDecision": "deny",
				},
				"systemMessage": "[safety-block] " + reason,
			})
			fmt.Fprint(os.Stderr, string(denyJSON))
			exitWithCode(2)
		}
		// Claude allow: exit 0, both streams empty
		return nil
	}

	if harnessFlag == "grok" {
		if effectiveBlock {
			// Grok deny: stdout decision=deny JSON object, exit 2
			denyJSON, _ := json.Marshal(map[string]interface{}{
				"decision": "deny",
				"reason":   "[safety-block] " + reason,
			})
			fmt.Fprintln(os.Stdout, string(denyJSON))
			exitWithCode(2)
		}
		// Grok allow: exit 0, stdout empty
		return nil
	}

	if harnessFlag == "codex" {
		if effectiveBlock {
			// Codex deny: stderr PLAIN TEXT, exit 2 (NO JSON)
			fmt.Fprint(os.Stderr, "[safety-block] "+reason)
			exitWithCode(2)
		}
		// Codex allow: exit 0, both streams empty
		return nil
	}

	if harnessFlag == "agy" {
		if effectiveBlock {
			// Agy deny: stdout JSON decision/reason + exit 0
			// agy gates on the stdout decision field, NOT exit code.
			denyJSON, _ := json.Marshal(map[string]interface{}{
				"decision": "deny",
				"reason":   "[safety-block] " + reason,
			})
			fmt.Fprintln(os.Stdout, string(denyJSON))
			exitWithCode(0)
			return nil
		}
		// Agy allow: stdout {"decision":"allow"}, exit 0
		allowJSON, _ := json.Marshal(map[string]interface{}{
			"decision": "allow",
		})
		fmt.Fprintln(os.Stdout, string(allowJSON))
		return nil
	}

	if harnessFlag == "opencode" {
		if effectiveBlock {
			// OpenCode deny: stderr plaintext, exit 2 — the plugin throws on exit 2.
			fmt.Fprint(os.Stderr, "[safety-block] "+reason)
			exitWithCode(2)
		}
		// OpenCode allow: exit 0
		return nil
	}

	// Default Pi-shaped output: JSON contract on stdout
	data := contract.SafetyCheckData{
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

// runSessionStartNudge implements the session-start nudge contract -- the single
// shared authority for printing a session-start instruction or staying silent.
// Every silence and error path exits 0 because SessionStart hooks that treat
// non-zero as blocking session init must never fail closed.
//
// Contract:
//
//  1. Silence gates (exit 0, no output):
//     - Gate agent: NO_MISTAKES_GATE set
//     - Non-primary checkout / out-of-scope path
//     - Lock already held by an ancestor process (PID in ancestry chain)
//
//  2. Active nudge (one line when gates pass):
//     - Print exactly one deterministic instruction to run munsu session-start
//     - No multi-line digests (digest belongs to munsu session-start itself)
//
//  3. Always exit 0:
//     - Exit 2 blocks Claude/Codex-class session init, so all paths exit 0
//
//  4. Retry:
//     - If session-start fails before exactly-once success is recorded,
//     the next eligible session-start event may retry (lock not acquired
//     means lock ancestry check passes, so nudge runs again)
//     - Do not record exactly-once success before the underlying command succeeds
//     - Retries remain silence-gated (still no nudge under gate/non-primary/lock-held)
//     - No busy-loop inside the hook; one attempt per harness event
//
//  5. Pi exception:
//     - Pi may keep native full munsu session-start on session_start
//     IF AND ONLY IF it preserves primary/safety gates, exactly-once per
//     native session (including reload), and does not double-run session-start
//     after the lock is already held
func runSessionStartNudge(cmd *cobra.Command, ctx Ctx) error {
	// 1. Check for gate agent (NO_MISTAKES_GATE)
	if _, present := os.LookupEnv("NO_MISTAKES_GATE"); present {
		return nil
	}

	// 2. Check primary scope
	scopeResult := session.CheckSessionScope(ctx.Home)
	if !scopeResult.IsPrimary {
		return nil
	}

	// 3. Check lock ancestry (8 parents of current pid)
	if lockPIDInAncestry(ctx.Home) {
		return nil
	}

	// 4. All checks passed — print the nudge
	fmt.Println("Run `munsu session-start` now, exactly once, before executing any other instructions.")
	return nil
}

// lockPIDInAncestry reads state/.lock and walks up to 8 parent PIDs
// to check if the lock holder is in the current process ancestry.
func lockPIDInAncestry(homeDir string) bool {
	lockPath := filepath.Join(homeDir, "state", ".lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}

	var lockPID int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &lockPID); err != nil {
		return false
	}
	if lockPID <= 1 {
		return false
	}

	// Walk up to 8 parents from current PID
	pid := os.Getpid()
	for i := 0; i < 8; i++ {
		if pid == lockPID {
			return true
		}
		// Read parent PID from /proc (Linux) or fallback to ps
		ppid := readParentPID(pid)
		if ppid <= 1 {
			return false
		}
		pid = ppid
	}
	return false
}

// readParentPID reads the parent PID of the given PID.
// Tries /proc first (Linux), falls back to `ps` (macOS/BSD).
func readParentPID(pid int) int {
	// Try /proc/[pid]/status (Linux)
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	if data, err := os.ReadFile(statusPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				var ppid int
				if _, err := fmt.Sscanf(line, "PPid:%d", &ppid); err == nil {
					return ppid
				}
			}
		}
	}

	// Fallback to `ps -o ppid= -p <pid>` (macOS/BSD)
	cmd := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return -1
	}
	ppidStr := strings.TrimSpace(string(out))
	if ppidStr == "" {
		return -1
	}
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil || ppid <= 0 {
		return -1
	}
	return ppid
}
