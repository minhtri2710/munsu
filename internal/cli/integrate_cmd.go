package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/bootstrap"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/spf13/cobra"
)

type integrateFlags struct {
	harness  string
	scope    string
	dryRun   bool
	command  string
	filePath string
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
  --file-path    Target path of a native file-write tool call (Write/Edit/...)
  --harness      Output shape: "pi" (default, JSON contract), "claude" (native deny exit 2 + stderr), "codex" (stderr plaintext + exit 2), "grok" (stdout decision=deny object + exit 2), "opencode" (stderr plaintext + exit 2, same as codex), or "agy" (stdout decision JSON + exit 0)`,
		Args: cobra.MaximumNArgs(1),
		RunE: withHome(func(cmd *cobra.Command, args []string, ctx Ctx) error {
			checkPath, _ := os.Getwd()
			if len(args) > 0 {
				checkPath = args[0]
			}
			return runSafetyCheck(cmd, checkPath, flags.command, flags.filePath, flags.harness)
		}),
	}
	safetyCmd.Flags().StringVar(&flags.command, "command", "", "Command to evaluate for blocking rules")
	safetyCmd.Flags().StringVar(&flags.filePath, "file-path", "", "File path a native write tool is about to touch")
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

func resolveIntegrateScope(raw string) (bootstrap.Scope, error) {
	switch raw {
	case "user":
		return bootstrap.ScopeUser, nil
	case "project":
		return bootstrap.ScopeProject, nil
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
	result, err := bootstrap.Install(ctx.Home, cwd, harnessName, scope, flags.dryRun)
	if err != nil {
		return err
	}

	return writeContract(cmd, Response[integrateResultData]{
		SchemaVersion: SchemaVersion,
		Kind:          "bootstrap.install",
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
	result, err := bootstrap.Repair(ctx.Home, cwd, harnessName, scope, flags.dryRun)
	if err != nil {
		return err
	}

	return writeContract(cmd, Response[integrateResultData]{
		SchemaVersion: SchemaVersion,
		Kind:          "bootstrap.repair",
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
	result, err := bootstrap.Status(ctx.Home, cwd, harnessName, scope)
	if err != nil {
		return err
	}

	return writeContract(cmd, Response[integrateResultData]{
		SchemaVersion: SchemaVersion,
		Kind:          "bootstrap.status",
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

// exitWithCode is overridable for testing. Defaults to os.Exit.
var exitWithCode = func(code int) {
	os.Exit(code)
}

// applyPatchToolNames are the tools that ship an entire patch document under a
// command-shaped key. Codex is the measured case: its PreToolUse payload is
// `{"tool_name":"apply_patch","tool_input":{"command":"<raw patch text>"}}`, so
// the tool name — not the key — is the only thing that tells a patch body apart
// from a shell command.
var applyPatchToolNames = map[string]bool{
	"apply_patch": true,
	"ApplyPatch":  true,
}

// toolPayload is the classified tool call a PreToolUse hook has to decide on.
// Exactly one channel is populated: a shell command, a native write target, or
// a patch document. Keeping them apart is the whole point — a patch body routed
// into the command channel is scanned by the shell-blocking rules, which refuses
// patches whose *content* happens to mention a blocked command while letting the
// patch's actual write targets through unexamined.
type toolPayload struct {
	command   string
	filePath  string
	isPatch   bool
	patchBody string
}

// readStdinForToolPayload reads the harness tool payload from stdin exactly
// once, classifies it by tool name, and extracts the one channel that call
// belongs to. Everything comes from the same JSON object and stdin is not
// rewindable, so a single reader owns the extraction.
//
// Field names are tried per harness shape. A payload that carries nothing
// yields an empty toolPayload, which every caller treats as "nothing to check".
func readStdinForToolPayload() (toolPayload, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return toolPayload{}, fmt.Errorf("reading stdin: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return toolPayload{}, nil
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return toolPayload{}, nil // not JSON, return empty
	}

	// Containers, in the order harnesses nest their tool arguments:
	// Claude/Codex .tool_input, agy .toolCall.args, Grok .toolInput, flat.
	containers := []map[string]interface{}{}
	if ti, ok := payload["tool_input"].(map[string]interface{}); ok {
		containers = append(containers, ti)
	}
	toolCall, _ := payload["toolCall"].(map[string]interface{})
	if toolCall != nil {
		if args, ok := toolCall["args"].(map[string]interface{}); ok {
			containers = append(containers, args)
		}
	}
	if ti, ok := payload["toolInput"].(map[string]interface{}); ok {
		containers = append(containers, ti)
	}
	containers = append(containers, payload)

	commandKeys := []string{"command", "CommandLine"}
	// notebook_path is NotebookEdit's target; the rest cover the PascalCase and
	// camelCase spellings the other harnesses use for the same argument.
	filePathKeys := []string{"file_path", "filePath", "FilePath", "notebook_path", "notebookPath", "path", "Path"}

	// The tool name sits beside the arguments, not inside them, so it is read
	// from the envelope only.
	nameContainers := []map[string]interface{}{payload}
	if toolCall != nil {
		nameContainers = append(nameContainers, toolCall)
	}
	toolName := firstStringField(nameContainers, []string{"tool_name", "toolName", "name"})

	if applyPatchToolNames[toolName] {
		return toolPayload{
			isPatch:   true,
			patchBody: firstStringField(containers, commandKeys),
		}, nil
	}

	return toolPayload{
		command:  firstStringField(containers, commandKeys),
		filePath: firstStringField(containers, filePathKeys),
	}, nil
}

// firstStringField returns the first non-empty string value found by scanning
// keys in order within each container, containers in order.
func firstStringField(containers []map[string]interface{}, keys []string) string {
	for _, container := range containers {
		for _, key := range keys {
			if v, ok := container[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

// patchEnvelopeBegin and patchEnvelopeEnd delimit the apply-patch document.
const (
	patchEnvelopeBegin = "*** Begin Patch"
	patchEnvelopeEnd   = "*** End Patch"
)

// patchTargetHeaders are the line prefixes that name a file the patch writes to.
var patchTargetHeaders = []string{
	"*** Add File: ",
	"*** Update File: ",
	"*** Delete File: ",
	"*** Move to: ",
}

// applyPatchTargets reads the write targets out of an apply-patch document.
//
// The format is line-oriented and only its envelope matters here: file headers
// sit at column 0, while every content line inside a hunk is prefixed with
// '+', '-' or a space. So a patch cannot forge a header from its own content,
// and it cannot hide one either — which is what makes a header scan sufficient
// and a full diff parser unnecessary.
//
// A body that does not parse is an error, never an empty target list: the
// caller refuses on error, because a tool whose coverage munsu has declared
// must not pass unexamined just because its payload was unreadable.
func applyPatchTargets(body string) ([]string, error) {
	var targets []string
	began, ended := false, false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch strings.TrimSpace(line) {
		case patchEnvelopeBegin:
			began = true
			continue
		case patchEnvelopeEnd:
			ended = true
			continue
		}
		if !began || ended {
			continue
		}
		for _, header := range patchTargetHeaders {
			if strings.HasPrefix(line, header) {
				if target := strings.TrimSpace(strings.TrimPrefix(line, header)); target != "" {
					targets = append(targets, target)
				}
			}
		}
	}
	if !began || !ended {
		return nil, fmt.Errorf("missing %q / %q envelope", patchEnvelopeBegin, patchEnvelopeEnd)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no file header found between %q and %q", patchEnvelopeBegin, patchEnvelopeEnd)
	}
	return targets, nil
}

// patchWriteTargets returns the write targets of an apply-patch call, so the
// call is decided on the same rule a native Write/Edit call is decided on.
// Patch paths are relative to the directory the tool call runs in, so they are
// resolved against checkPath.
func patchWriteTargets(checkPath, body string) ([]string, error) {
	targets, err := applyPatchTargets(body)
	if err != nil {
		return nil, err
	}
	resolved := make([]string, 0, len(targets))
	for _, target := range targets {
		if !filepath.IsAbs(target) {
			target = filepath.Join(checkPath, target)
		}
		resolved = append(resolved, target)
	}
	return resolved, nil
}

func runSafetyCheck(cmd *cobra.Command, checkPath string, checkCommand string, checkFilePath string, harnessFlag string) error {
	// Harnesses that deliver the tool payload on stdin: read it once and take
	// whichever channel the tool call actually belongs to.
	effectiveCommand := checkCommand
	effectiveFilePath := checkFilePath
	payload := toolPayload{}
	if (harnessFlag == "claude" || harnessFlag == "grok" || harnessFlag == "codex" || harnessFlag == "agy") &&
		effectiveCommand == "" && effectiveFilePath == "" {
		stdinPayload, err := readStdinForToolPayload()
		if err == nil {
			payload = stdinPayload
			effectiveCommand = payload.command
			effectiveFilePath = payload.filePath
		}
	}

	// Evaluate scope/gate safety.
	result := bootstrap.SafetyCheck(checkPath)

	// Evaluate command blocking rules.
	block := false
	reason := ""
	// Every channel that names a write target contributes to one list: the
	// refusal below and the narrowing further down must agree on what this call
	// writes (ADR-0014).
	var writeTargets []string
	if payload.isPatch {
		// A patch document never reaches the shell-blocking ladder below: its
		// body is file content, not a command line. An unreadable payload is a
		// refusal, not a pass: munsu has declared this tool covered, so it must
		// not go unexamined.
		targets, err := patchWriteTargets(checkPath, payload.patchBody)
		if err != nil {
			block = true
			reason = "apply_patch payload is not a readable patch (" + err.Error() +
				"); refusing rather than letting an unreadable write through"
		}
		writeTargets = targets
	} else if effectiveCommand != "" {
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

		if !block {
			if gitBlock, gitReason := evaluateGitMutationSafety(checkPath, effectiveCommand); gitBlock {
				block = true
				reason = gitReason
			}
		}

		// A shell command is decided on the targets it names, not on where the
		// session happens to stand: an absolute path into the shared checkout
		// used to pass unopposed from a valid worktree (ADR-0014 §1).
		var shellAmbiguous bool
		writeTargets, shellAmbiguous = shellWriteTargets(checkPath, effectiveCommand)
		if shellAmbiguous && !block {
			block = true
			reason = "Shell write target is ambiguous; refusing to proceed."
		}

		nmHome := os.Getenv("NM_HOME")
		if nmHome == "" {
			if h, err := os.UserHomeDir(); err == nil {
				nmHome = filepath.Join(h, ".no-mistakes")
			}
		}
		_ = nmHome
	}

	// Native file-write tools carry their target in the payload rather than in
	// a command string, so they are evaluated on the path alone.
	if effectiveFilePath != "" {
		writeTargets = append(writeTargets, effectiveFilePath)
	}

	if !block {
		block, reason = evaluateWriteTargets(writeTargets)
	}

	// The unrelated-cwd refusal is scoped to what this guard protects: a call
	// that names write targets, all of them outside the bound repository's
	// primary checkout, is not shared-state access just because the session
	// stands somewhere git does not recognize. Only that one refusal is
	// narrowed, only when a binding exists to compare against, and only for
	// calls that named a target at all (ADR-0014 §3).
	if result.GateRefused && !block && len(writeTargets) > 0 &&
		result.Identity == "unrelated" && result.Error == bootstrap.UnrelatedCheckoutRefusal {
		if _, bound := bootstrap.BoundRepositoryCommonDir(); bound {
			result.GateRefused = false
			result.Error = ""
		}
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
	data := SafetyCheckData{
		Identity:       result.Identity,
		GateCapability: result.GateCapability,
		CanonicalPath:  result.CanonicalPath,
		GateRefused:    result.GateRefused,
		Block:          block,
		Reason:         reason,
		Error:          result.Error,
	}

	return writeContract(cmd, Response[interface{}]{
		SchemaVersion: SchemaVersion,
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
	scopeResult := bootstrap.CheckSessionScope(ctx.Home)
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
		// Read the parent PID using the platform-specific implementation.
		ppid := readParentPID(pid)
		if ppid <= 1 {
			return false
		}
		pid = ppid
	}
	return false
}
