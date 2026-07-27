package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backlog"
	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/hometag"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/scope"
	"github.com/minhtri2710/munsu/internal/soldier"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/worktree"
)

// Runner orchestrates the full spawn sequence through private phase methods.
// It encapsulates all intermediate state so the public Run function is a thin
// delegate call.
type Runner struct {
	args Args

	// phase state populated during Run
	homeDir       string
	effectiveMode string
	projPath      string
	wtPath        string
	harness       string
	model         string
	effort        string
	launchCmd     string
	endpoints     EndpointCapabilities
	endpoint      CreatedEndpoint
	briefData     []byte
	windowID      string
	spawnRole     string

	// soldier launch prompt state
	prompt     string
	promptEnv  *soldier.LaunchEnvelope
	launchArgs []string
	launchBin  string
}

// NewRunner creates a Runner for the given Args.
func NewRunner(args Args) *Runner {
	return &Runner{args: args, endpoints: args.Endpoints}
}

// Run executes the full spawn orchestration sequence.
func (r *Runner) Run() (string, error) {
	if err := r.resolveHome(); err != nil {
		return "", err
	}
	if err := r.checkSpawnAuthority(); err != nil {
		return "", err
	}
	if err := r.checkCaptainBacklogAuthority(); err != nil {
		return "", err
	}
	if err := r.resolveMode(); err != nil {
		return "", err
	}
	if err := r.validateHarnessFlag(); err != nil {
		return "", err
	}
	if err := r.preflightBrief(); err != nil {
		return "", err
	}
	if err := r.checkBacklogAuthority(); err != nil {
		return "", err
	}
	if err := r.resolveProject(); err != nil {
		return "", err
	}
	if err := r.checkTangle(); err != nil {
		return "", err
	}
	if err := r.preflightNoMistakes(); err != nil {
		return "", err
	}
	if err := r.preflightDelivery(); err != nil {
		return "", err
	}
	if err := r.checkScopeGate(); err != nil {
		return "", err
	}
	if err := r.preflightHarness(); err != nil {
		return "", err
	}
	if err := r.acquireWorktree(); err != nil {
		return "", err
	}
	success := false
	// Fail-closed: return worktree on any subsequent error (but NOT on success).
	defer func() {
		if !success && r.wtPath != "" {
			_ = worktree.Return(r.homeDir, r.wtPath)
		}
	}()

	if err := r.resolveHarness(); err != nil {
		return "", err
	}
	r.resolveLaunchConfig()
	if err := r.buildSoldierPrompt(); err != nil {
		return "", err
	}
	if err := r.createSession(); err != nil {
		return "", err
	}
	r.bootstrapWindow()
	r.writeBriefToWorktree()
	if err := r.waitAndInjectBrief(); err != nil {
		return "", err
	}
	status, err := r.endpoints.Probe(r.endpoint)
	if err != nil || !status.Alive {
		_ = r.endpoints.Dispose(r.endpoint)
		if err != nil {
			return "", fmt.Errorf("verifying created pane %q on backend %q: %w", r.windowID, r.endpoint.Backend, err)
		}
		return "", fmt.Errorf("created pane %q failed verification on backend %q before persisting state", r.windowID, r.endpoint.Backend)
	}
	r.writeTaskMeta()
	r.appendSpawnedStatus()
	r.printEndpointInfo()
	r.armWatcher()
	success = true
	return r.windowID, nil
}

// Phase 1: resolveHome resolves the munsu home directory.
func (r *Runner) resolveHome() error {
	if r.args.HomeDir != "" {
		r.homeDir = r.args.HomeDir
		return nil
	}
	h, err := home.Resolve("")
	if err != nil {
		return fmt.Errorf("resolving home: %w", err)
	}
	r.homeDir = h
	return nil
}

func (r *Runner) checkSpawnAuthority() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("spawn authority: resolving current directory: %w", err)
	}
	var role string
	if endpointKind, found, err := currentEndpointKind(r.homeDir); err != nil {
		return fmt.Errorf("spawn authority: resolving current endpoint: %w", err)
	} else if found {
		if endpointKind == "captain" {
			role = "captain"
		} else {
			return fmt.Errorf("spawn authority: managed soldier endpoints cannot spawn; delegate to the general or a captain")
		}
	} else {
		role = os.Getenv("MUNSU_ROLE")
	}
	r.spawnRole = role
	return authorizeSpawn(role, r.homeDir, cwd)
}

var tmuxWindowForPane = func(pane string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{window_id}").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux pane %s window lookup: %s", pane, strings.TrimSpace(string(out)))
	}
	window := strings.TrimSpace(string(out))
	if window == "" {
		return "", fmt.Errorf("tmux pane %s window lookup returned empty window id", pane)
	}
	return window, nil
}

func currentEndpointKind(homeDir string) (string, bool, error) {
	herdrPane := strings.TrimSpace(os.Getenv("HERDR_PANE_ID"))
	tmuxPane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if herdrPane == "" && tmuxPane == "" {
		return "", false, nil
	}
	tmuxWindow := ""
	if tmuxPane != "" {
		var err error
		tmuxWindow, err = tmuxWindowForPane(tmuxPane)
		if err != nil {
			return "", false, err
		}
	}
	entries, err := os.ReadDir(task.StateDir(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".meta")
		meta, err := task.ReadMeta(homeDir, id)
		if err != nil {
			continue
		}
		if herdrPane != "" && meta["herdr_pane_id"] == herdrPane {
			return meta["kind"], true, nil
		}
		if tmuxWindow != "" {
			window := meta["window"]
			if window == tmuxWindow || strings.HasSuffix(window, ":"+tmuxWindow) {
				return meta["kind"], true, nil
			}
		}
	}
	return "", false, nil
}

func authorizeSpawn(role, homeDir, cwd string) error {
	switch role {
	case "captain":
		if _, err := captain.ValidateProvenance(homeDir); err != nil {
			return fmt.Errorf("spawn authority: invalid captain identity: %w", err)
		}
		canonicalHome, err := canonicalExistingPath(homeDir)
		if err != nil {
			return fmt.Errorf("spawn authority: resolving captain home: %w", err)
		}
		canonicalCWD, err := canonicalExistingPath(cwd)
		if err != nil {
			return fmt.Errorf("spawn authority: resolving captain cwd: %w", err)
		}
		if canonicalCWD != canonicalHome {
			return fmt.Errorf("spawn authority: captain must spawn from its home %s, current directory is %s", canonicalHome, canonicalCWD)
		}
		return nil
	case "soldier":
		return fmt.Errorf("spawn authority: regular soldiers cannot spawn; delegate to the general or a captain")
	case "", "general":
		identity, _, _, err := scope.ClassifyIdentity(cwd)
		if err != nil {
			return fmt.Errorf("spawn authority: classifying current checkout: %w", err)
		}
		if identity == scope.Worktree {
			return fmt.Errorf("spawn authority: linked-worktree callers cannot spawn; delegate to the general or a captain")
		}
		return nil
	default:
		return fmt.Errorf("spawn authority: unknown MUNSU_ROLE %q; expected general, captain, or soldier", role)
	}
}

func canonicalExistingPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

// checkCaptainBacklogAuthority validates that when the spawner role is captain,
// the task is present and dispatchable. Normal Captain flow is:
//
//	tasks-axi start <id>  →  munsu spawn <id>
//
// Backlog in_flight without live meta/window MUST allow spawn without --force.
// Refuse only duplicate live execution (meta window/pane). --force is emergency bypass only.
func (r *Runner) checkCaptainBacklogAuthority() error {
	if r.args.Force {
		return nil
	}
	if r.spawnRole != "captain" {
		return nil
	}

	// Already-live = meta proves a soldier was spawned (window or pane id).
	// Kind-only meta (e.g. task add) is NOT live execution.
	if meta, err := task.ReadMeta(r.homeDir, r.args.ID); err == nil {
		if win := meta["window"]; win != "" {
			return fmt.Errorf("captain backlog authority: task %s already has a live soldier session (window=%s); refuse duplicate live execution", r.args.ID, win)
		}
		if pane := meta["herdr_pane_id"]; pane != "" {
			return fmt.Errorf("captain backlog authority: task %s already has a live soldier session (pane=%s); refuse duplicate live execution", r.args.ID, pane)
		}
	}

	// Check backlog state via selected backlog authority.
	state, blocked, found, err := readBacklogTaskState(r.homeDir, r.args.ID)
	if err != nil {
		// If backlog doesn't exist or can't be read, allow through but warn.
		fmt.Fprintf(os.Stderr, "warning: cannot read backlog for captain authority check: %v\n", err)
		return nil
	}
	if !found {
		return fmt.Errorf("captain backlog authority: task %s not found in backlog; register it with 'backlog add %s \"<description>\"'", r.args.ID, r.args.ID)
	}
	switch normalizeBacklogState(state) {
	case "in-flight":
		// start→spawn: backlog In flight without live window/pane is allowed.
		return nil
	case "done":
		return fmt.Errorf("captain backlog authority: task %s is already done; reopen requires General instruction", r.args.ID)
	case "blocked":
		return fmt.Errorf("captain backlog authority: task %s is blocked (blocked-by: %s); resolve dependencies before dispatch", r.args.ID, blocked)
	case "queued":
		if blocked != "" {
			return fmt.Errorf("captain backlog authority: task %s is blocked-by %s; resolve dependencies before dispatch", r.args.ID, blocked)
		}
		return nil
	default:
		return fmt.Errorf("captain backlog authority: task %s has unexpected state %q", r.args.ID, state)
	}
}

// normalizeBacklogState maps tasks-axi (in_flight) and file-backend (in-flight).
func normalizeBacklogState(state string) string {
	switch strings.TrimSpace(state) {
	case "in_flight", "in-flight", "inflight":
		return "in-flight"
	default:
		return strings.TrimSpace(state)
	}
}

// readBacklogTaskState reads the backlog state via the selected backlog authority.
// Returns the state string, blocked-by value (always empty from GetItem),
// whether found, and any error.
var readBacklogTaskState = func(homeDir, id string) (string, string, bool, error) {
	item, found, err := backlog.GetItem(homeDir, id)
	if err != nil {
		return "", "", false, fmt.Errorf("reading backlog state for %s: %w", id, err)
	}
	if !found {
		return "", "", false, nil
	}
	return item.State.String(), "", true, nil
}

// Phase 2: resolveMode resolves the effective delivery mode.
func (r *Runner) resolveMode() error {
	mode, err := effectiveModeForSpawn(r.homeDir, r.args)
	if err != nil {
		return err
	}
	r.effectiveMode = mode
	return nil
}

// Phase 3: validateHarnessFlag validates --harness flag if set.
func (r *Runner) validateHarnessFlag() error {
	if r.args.HarnessFlag == "" {
		return nil
	}
	return harness.ValidateHarness(r.args.HarnessFlag)
}

// Phase 4: preflightBrief checks that a brief exists before spawning.
func (r *Runner) preflightBrief() error {
	if !brief.Exists(r.homeDir, r.args.ID) {
		return fmt.Errorf("no brief found for task %s: scaffold it with 'munsu brief %s %s' before spawning",
			r.args.ID, r.args.ID, r.args.ProjectName)
	}
	return nil
}

// Phase 5: checkBacklogAuthority verifies the task is uniquely queued+ready in the backlog.
// Fail closed unless the task is uniquely present and ready, or --reopen is used.
func (r *Runner) checkBacklogAuthority() error {
	item, found, err := backlog.GetItem(r.homeDir, r.args.ID)
	if err != nil {
		return fmt.Errorf("lifecycle guard: reading backlog: %w", err)
	}
	if !found {
		return fmt.Errorf("lifecycle guard: task %q not found in backlog; register it with 'backlog add %q \"<description>\" --kind %s' before spawning", r.args.ID, r.args.ID, r.args.Kind)
	}

	// Check for duplicate IDs in backlog
	dup, err := backlog.HasDuplicate(r.homeDir, r.args.ID)
	if err != nil {
		return fmt.Errorf("lifecycle guard: checking for duplicates: %w", err)
	}
	if dup {
		return fmt.Errorf("lifecycle guard: task %q has duplicate entries in backlog; resolve duplicates before spawning", r.args.ID)
	}

	// Check already-live: existing meta with window means a soldier session exists
	meta, metaErr := task.ReadMeta(r.homeDir, r.args.ID)
	metaExists := metaErr == nil && meta["window"] != ""

	// State-based checks. Backlog In flight without live meta is start→spawn — allow.
	switch item.State {
	case backlog.StateBlocked:
		if !r.args.Reopen {
			return fmt.Errorf("lifecycle guard: task %q is blocked; use --reopen to force dispatch or clear the blocker first", r.args.ID)
		}
	case backlog.StateDone:
		if !r.args.Reopen {
			return fmt.Errorf("lifecycle guard: task %q is done; use --reopen to reopen", r.args.ID)
		}
	case backlog.StateInFlight:
		// Allow when no live session; refuse only duplicate live execution.
		if metaExists && !r.args.Reopen {
			return fmt.Errorf("lifecycle guard: task %q is already in-flight with a live session; refuse duplicate live execution", r.args.ID)
		}
		return nil
	}

	// Live session without matching state still refuses (stale meta after teardown failure).
	if metaExists && !r.args.Reopen {
		return fmt.Errorf("lifecycle guard: task %q already has a live soldier session; refuse duplicate live execution", r.args.ID)
	}

	return nil
}

// Phase 6: resolveProject resolves the project repo path from registry.
func (r *Runner) resolveProject() error {
	projPath, err := project.ResolveRepoPath(r.homeDir, r.args.ProjectName)
	if err != nil {
		return fmt.Errorf("resolving project %q: %w", r.args.ProjectName, err)
	}
	r.projPath = projPath
	return nil
}

// Phase 7: checkTangle verifies no worktree tangle (unless yolo).
func (r *Runner) checkTangle() error {
	if r.args.Yolo {
		return nil
	}
	return worktree.AssertNotTangled(r.projPath, r.args.ProjectName)
}

// checkScopeGate refuses no-mistakes gate agents before worktree allocation.
func (r *Runner) checkScopeGate() error {
	if err := scope.GateRefusalError(r.projPath); err != nil {
		return fmt.Errorf("scope gate: %w", err)
	}
	return nil
}

func (r *Runner) preflightNoMistakes() error {
	if r.effectiveMode != "no-mistakes" {
		return nil
	}
	preflight := r.args.NoMistakesPreflight
	if preflight == nil {
		preflight = defaultNoMistakesPreflight
	}
	return preflight(r.projPath)
}

// Phase 8: acquireWorktree acquires a leased worktree from the pool.
func (r *Runner) acquireWorktree() error {
	wtPath, err := worktree.Get(r.homeDir, r.projPath, true)
	if err != nil {
		return fmt.Errorf("acquiring worktree: %w", err)
	}
	r.wtPath = wtPath
	return nil
}

// preflightHarness resolves the harness name early and runs readiness preflight.
// This happens before worktree acquisition so known errors fail before allocating
// any resources. Unknown-level preflight results pass through without error.
func (r *Runner) preflightHarness() error {
	harnessName := r.args.HarnessFlag
	if harnessName == "" {
		if sel, ok := r.dispatchSelection(); ok && sel.Harness != "" {
			harnessName = sel.Harness
		} else {
			h, err := harness.Soldier(r.homeDir)
			if err != nil {
				return fmt.Errorf("resolving harness for preflight: %w", err)
			}
			harnessName = h
		}
	}

	result, err := harness.Preflight(harnessName)
	if err != nil {
		return err
	}
	if result.AdapterKnown == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: harnessName, Reason: "adapter-unknown"}
	}
	if result.BinaryOnPath == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: harnessName, Reason: "binary-absent"}
	}
	if result.AuthConfigured == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: harnessName, Reason: "auth-absent"}
	}
	r.harness = harnessName
	return nil
}

// Phase 9: resolveHarness resolves the soldier harness.
// Precedence: --harness flag > dispatch profile match on brief > Soldier() chain.
// When soldier-dispatch.json is active and no --harness is set, prefer
// ResolveDispatchSelection over the bare DefaultHarness shortcut in Soldier().
func (r *Runner) resolveHarness() error {
	if r.harness != "" {
		return nil // already resolved by preflightHarness
	}
	if r.args.HarnessFlag != "" {
		if err := harness.ValidateHarness(r.args.HarnessFlag); err != nil {
			return fmt.Errorf("--harness: %w", err)
		}
		r.harness = r.args.HarnessFlag
		return nil
	}
	if sel, ok := r.dispatchSelection(); ok && sel.Harness != "" {
		if err := harness.ValidateHarness(sel.Harness); err != nil {
			return fmt.Errorf("dispatch harness: %w", err)
		}
		r.harness = sel.Harness
		return nil
	}
	h, err := harness.Soldier(r.homeDir)
	if err != nil {
		return fmt.Errorf("resolving harness: %w", err)
	}
	r.harness = h
	return nil
}

// dispatchSelection loads soldier-dispatch.json and matches against the brief body.
func (r *Runner) dispatchSelection() (harness.DispatchSelection, bool) {
	path := harness.DispatchPath(r.homeDir)
	cfg, err := harness.LoadDispatch(path)
	if err != nil {
		return harness.DispatchSelection{}, false
	}
	desc := r.taskDescription()
	return harness.ResolveDispatchSelection(cfg, desc), true
}

// taskDescription returns text used to match dispatch profiles (brief body or id).
func (r *Runner) taskDescription() string {
	briefPath := brief.Path(r.homeDir, r.args.ID)
	if data, err := os.ReadFile(briefPath); err == nil {
		s := strings.TrimSpace(string(data))
		if s != "" {
			return s
		}
	}
	return r.args.ID
}

// Phase 10: resolveLaunchConfig resolves model, effort, and launch command.
// Precedence: CLI --model/--effort > dispatch profile > adapter template defaults.
func (r *Runner) resolveLaunchConfig() {
	adapter, ok := harness.GetAdapter(r.harness)
	if !ok {
		return
	}
	tmpl := adapter.LaunchTemplate

	// Template defaults first.
	r.model = tmpl.DefaultModel
	r.effort = tmpl.DefaultEffort

	// Dispatch profile overrides template defaults (when present).
	if sel, ok := r.dispatchSelection(); ok {
		if sel.Model != "" {
			r.model = sel.Model
		}
		if sel.Effort != "" {
			r.effort = sel.Effort
		}
	}

	// Explicit CLI flags win.
	if r.args.ModelFlag != "" {
		r.model = r.args.ModelFlag
	}
	if r.args.EffortFlag != "" {
		r.effort = r.args.EffortFlag
	}

	r.launchCmd = harness.LaunchStringWith(r.harness, tmpl, r.model, r.effort)
}
func soldierTabLabel(projectName, taskID string) string {
	return "mu-" + labelComponent(projectName) + "-" + labelComponent(taskID)
}

func labelComponent(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// Phase 11: createSession creates a session window for the soldier.
func (r *Runner) createSession() error {
	if r.endpoints == nil {
		return fmt.Errorf("spawn endpoint capabilities are required")
	}
	ep, err := r.endpoints.Create(CreateRequest{Home: r.homeDir, PreferredBackend: r.args.Backend, WorkspaceName: hometag.WorkspaceTag(r.homeDir), TabName: soldierTabLabel(r.args.ProjectName, r.args.ID), Cwd: r.wtPath})
	if err != nil {
		return err
	}
	status, err := r.endpoints.Probe(ep)
	if err != nil || !status.Alive {
		_ = r.endpoints.Dispose(ep)
		if err != nil {
			return fmt.Errorf("verifying created pane %q on backend %q: %w", ep.Handle, ep.Backend, err)
		}
		return fmt.Errorf("created pane %q failed verification on backend %q", ep.Handle, ep.Backend)
	}
	r.endpoint, r.windowID = ep, ep.Handle
	return nil
}

// Phase 11a: buildSoldierPrompt builds the complete Soldier launch prompt,
// runs fail-closed validation, and persists durable files to the worktree.
// Must be called AFTER acquireWorktree and BEFORE createSession so that
// fail-closed checks happen before any session allocation.
func (r *Runner) buildSoldierPrompt() error {
	// Read brief content from the registered brief path.
	briefPath := brief.Path(r.homeDir, r.args.ID)
	briefData, readErr := os.ReadFile(briefPath)
	if readErr != nil {
		return fmt.Errorf("reading brief %s: %w", briefPath, readErr)
	}
	r.briefData = briefData

	// Resolve parent captain ID from the home's endpoint meta.
	parentCaptainID := r.resolveParentCaptainID()

	// Resolve required and optional skills deterministically.
	// Selection order: task kind/mode/lifecycle policy, not keyword guessing.
	requiredSkills, optionalSkills, skillDiags := r.resolveSkills()
	for _, d := range skillDiags {
		fmt.Fprintf(os.Stderr, "skill diagnostic: %s\n", d)
	}

	// Build the prompt input struct.
	input := soldier.LaunchPromptInput{
		TaskID:          r.args.ID,
		TaskKind:        r.args.Kind,
		DeliveryMode:    r.effectiveMode,
		Repository:      r.args.ProjectName,
		ParentCaptainID: parentCaptainID,
		ParentHome:      r.homeDir,
		WorktreePath:    r.wtPath,
		HomeDir:         r.homeDir,
		BriefContent:    briefData,
		RequiredSkills:  requiredSkills,
		OptionalSkills:  optionalSkills,
		HarnessName:     r.harness,
	}

	// Fail-closed: validate before building.
	if err := soldier.FailClosedDuringLaunch(input); err != nil {
		return fmt.Errorf("pre-launch fail-closed: %w", err)
	}

	// Build the complete prompt and envelope.
	promptText, env, err := soldier.BuildLaunchPrompt(input)
	if err != nil {
		return fmt.Errorf("building soldier launch prompt: %w", err)
	}
	r.prompt = promptText
	r.promptEnv = env

	// Persist durable files to the worktree.
	charter := soldier.DefaultCharter(r.args.ID, r.args.Kind, r.effectiveMode)
	if err := soldier.PersistLaunchFiles(r.wtPath, charter, briefData, env, promptText); err != nil {
		return fmt.Errorf("persisting soldier launch files: %w", err)
	}

	// Build launch arguments with the complete prompt, passing model and effort.
	bin, args, err := soldier.BuildLaunchArgs(r.wtPath, r.harness, r.model, r.effort, promptText)
	if err != nil {
		return fmt.Errorf("building soldier launch arguments: %w", err)
	}
	r.launchBin = bin
	r.launchArgs = args

	return nil
}

// resolveSkills returns deterministic required/optional skills for the current
// spawn based on task kind, delivery mode, and lifecycle policy.
// Uses explicit typed declarations — no keyword guessing or load-all.
func (r *Runner) resolveSkills() (required, optional []soldier.SkillEntry, diags []string) {
	// Catalog of skills available to spawns.
	// In a production system this would come from a registry or config file;
	// here we build it from known skills and their authority classifications.
	catalog := []soldier.SkillEntry{
		// Soldier-applicable skills
		{Name: "gh-axi", Role: "soldier"},
		{Name: "chrome-devtools-axi", Role: "soldier"},
		{Name: "qmd", Role: "soldier"},

		// Captain-only skills (will be denied by authority classification)
		{Name: "captain-provisioning", Role: "captain"},
		{Name: "munsu-ops", Role: "soldier"}, // explicitly denied in denylist
		{Name: "tasks-axi", Role: "soldier"}, // explicitly denied in denylist (backlog mutation)
		{Name: "stuck-soldier-recovery", Role: "captain"},
		{Name: "no-mistakes", Role: "captain"},
		{Name: "bootstrap-diagnostics", Role: "general"},
		{Name: "harness-adapters", Role: "captain"},
	}

	// Determine required skills from task kind and delivery mode.
	// ship tasks always get gh-axi; scout tasks may not need GitHub.
	var requiredNames []string
	var optionalNames []string

	switch r.args.Kind {
	case "scout":
		requiredNames = []string{"qmd"}
		optionalNames = []string{"gh-axi"}
	default:
		// ship tasks: github required.
		requiredNames = []string{"gh-axi"}
		optionalNames = []string{"qmd", "chrome-devtools-axi"}
	}

	// Apply no-mistakes mode policy: no-mistakes requires gh-axi always.
	if r.effectiveMode == "no-mistakes" {
		requiredNames = append(requiredNames, "gh-axi")
	}

	required, optional, diags = soldier.CollectSkills(catalog, requiredNames, optionalNames)
	return required, optional, diags
}

// resolveParentCaptainID returns the parent captain ID from the endpoint meta.
// Returns empty string when not running under a captain.
func (r *Runner) resolveParentCaptainID() string {
	if r.spawnRole == "captain" {
		if id, err := captain.ValidateProvenance(r.homeDir); err == nil {
			return id
		}
	}
	// For general launches, use a generic identifier.
	return "general"
}

// shQuote wraps s in single quotes, escaping embedded single quotes.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Phase 12: bootstrapWindow writes a launch script and sends it to the session.
// Uses the full prompt arguments built by buildSoldierPrompt.
// Fails closed when prompt args are empty (unsupported harness path).
func (r *Runner) bootstrapWindow() {
	if r.launchBin == "" || len(r.launchArgs) == 0 {
		// BuildLaunchArgs already fail-closed for unsupported harnesses.
		// If we reach here without prompt args, no fallback — fail closed.
		fmt.Fprintf(os.Stderr, "error: no soldier launch arguments — harness does not support prompt-arg delivery\n")
		return
	}

	// Build a shell script that sources identity env then execs with full prompt args.
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("cd ")
	b.WriteString(shQuote(r.wtPath))
	b.WriteString("\n")
	b.WriteString("export MUNSU_HOME=")
	b.WriteString(shQuote(r.homeDir))
	b.WriteString("\n")
	b.WriteString("export MUNSU_ROLE=soldier\n")
	b.WriteString("export MUNSU_TASK_ID=")
	b.WriteString(shQuote(r.args.ID))
	b.WriteString("\n")
	b.WriteString("export MUNSU_PARENT_STATUS=")
	b.WriteString(shQuote(r.homeDir))
	b.WriteString("\n")
	b.WriteString("exec ")
	b.WriteString(shQuote(r.launchBin))
	for _, arg := range r.launchArgs {
		b.WriteString(" ")
		b.WriteString(shQuote(arg))
	}
	b.WriteString("\n")

	launchScript := filepath.Join(r.wtPath, ".soldier-launch.sh")
	if writeErr := os.WriteFile(launchScript, []byte(b.String()), 0755); writeErr != nil {
		fmt.Fprintf(os.Stderr, "warning: writing launch script: %v\n", writeErr)
	}
	fullCmd := fmt.Sprintf("bash %s", shQuote(launchScript))
	if sendErr := r.endpoints.Submit(r.endpoint, fullCmd); sendErr != nil {
		fmt.Fprintf(os.Stderr, "warning: sending harness launch command: %v\n", sendErr)
	}
}

// Phase 13a: writeBriefToWorktree writes the brief file into the worktree.
func (r *Runner) writeBriefToWorktree() {
	briefPath := brief.Path(r.homeDir, r.args.ID)
	data, readErr := os.ReadFile(briefPath)
	if readErr != nil {
		if !os.IsNotExist(readErr) {
			fmt.Fprintf(os.Stderr, "warning: reading brief: %v\n", readErr)
		}
		return
	}
	r.briefData = data
	briefWorktreePath := filepath.Join(r.wtPath, ".soldier-brief.md")
	if writeErr := os.WriteFile(briefWorktreePath, data, 0644); writeErr != nil {
		fmt.Fprintf(os.Stderr, "warning: writing brief to worktree: %v\n", writeErr)
	}
}

// Phase 13b: waitForReady waits for the harness to be ready. No brief injection
// is needed — the full prompt was already passed as a launch argument.
// For harnesses that reached bootstrapWindow, the prompt is in context;
// this is a pure handshake wait with error handling.
func (r *Runner) waitAndInjectBrief() error {
	if len(r.briefData) == 0 {
		return nil
	}
	// The prompt was already delivered as a launch argument. We still wait
	// for harness readiness to catch launch failures early.
	if err := r.waitForHarnessReady(60); err != nil {
		capture, _ := r.endpoints.Capture(r.endpoint, 60)
		_ = task.AppendStatus(r.homeDir, r.args.ID, "failed: harness handshake")
		dataDir := filepath.Join(r.homeDir, "data", r.args.ID)
		_ = os.MkdirAll(dataDir, 0755)
		failContent := fmt.Sprintf("harness=%s\nerror=%v\n\nlast capture:\n%s\n", r.harness, err, capture)
		_ = os.WriteFile(filepath.Join(dataDir, "ready-fail.txt"), []byte(failContent), 0644)
		_ = r.endpoints.Dispose(r.endpoint)
		return fmt.Errorf("harness %q handshake failed: %w", r.harness, err)
	}
	// No brief injection needed — the complete prompt was already provided
	// as a launch argument via bootstrapWindow / BuildLaunchArgs.
	return nil
}

// waitForHarnessReady polls the session pane until the harness shows a ready
// signature or the timeout expires.
func (r *Runner) waitForHarnessReady(timeoutSec int) error {
	trustHandled := false

	deadline := time.After(time.Duration(timeoutSec) * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			capture, _ := r.endpoints.Capture(r.endpoint, 60)
			return fmt.Errorf("harness not ready after %ds: last capture: %q", timeoutSec, capture)
		case <-ticker.C:
			status, probeErr := r.endpoints.Probe(r.endpoint)
			if probeErr != nil {
				return fmt.Errorf("probing bound endpoint: %w", probeErr)
			}
			if !status.Alive {
				return fmt.Errorf("window died while waiting for ready")
			}
			capture, err := r.endpoints.Capture(r.endpoint, 60)
			if err != nil {
				continue
			}
			// Dialog handlers: auto-answer trust prompts before checking ready patterns.
			if !trustHandled && harness.IsTrustPrompt(capture, r.harness) {
				_ = r.endpoints.Submit(r.endpoint, "")
				trustHandled = true
				continue
			}
			// Check for failure patterns and abort early when detected.
			if harness.HasFailurePattern(capture, r.harness) {
				return fmt.Errorf("harness %q detected launch failure: %q", r.harness, capture)
			}
			if harness.HasReadyPattern(capture, r.harness) {
				return nil
			}
		}
	}
}

// Phase 14: writeTaskMeta writes the task metadata file.
func (r *Runner) writeTaskMeta() {
	yoloVal := "off"
	if r.args.Yolo {
		yoloVal = "on"
	}
	meta := map[string]string{
		"window":   r.windowID,
		"worktree": r.wtPath,
		"project":  r.args.ProjectName,
		"projpath": r.projPath,
		"harness":  r.harness,
		"backend":  r.endpoint.Backend,
		"kind":     r.args.Kind,
		"mode":     r.effectiveMode,
		"yolo":     yoloVal,
	}
	if r.model != "" {
		meta["model"] = r.model
	}
	if r.effort != "" {
		meta["effort"] = r.effort
	}

	for k, v := range r.endpoint.Metadata {
		meta[k] = v
	}

	if err := task.WriteMeta(r.homeDir, r.args.ID, meta); err != nil {
		fmt.Fprintf(os.Stderr, "warning: writing task meta: %v\n", err)
	}
}

// Phase 15: appendSpawnedStatus appends the working: spawned status line.
func (r *Runner) appendSpawnedStatus() {
	_ = task.AppendStatus(r.homeDir, r.args.ID, "working: spawned")
}

// Phase 16: printEndpointInfo prints the spawn endpoint information.
func (r *Runner) printEndpointInfo() {
	yoloVal := "off"
	if r.args.Yolo {
		yoloVal = "on"
	}
	fmt.Printf("Spawned soldier %s\n", r.args.ID)
	fmt.Printf("  window:   %s\n", r.windowID)
	fmt.Printf("  worktree: %s\n", r.wtPath)
	fmt.Printf("  projpath: %s\n", r.projPath)
	fmt.Printf("  project:  %s\n", r.args.ProjectName)
	fmt.Printf("  harness:  %s\n", r.harness)
	if r.model != "" {
		fmt.Printf("  model:    %s\n", r.model)
	}
	if r.effort != "" {
		fmt.Printf("  effort:   %s\n", r.effort)
	}
	fmt.Printf("  kind:     %s\n", r.args.Kind)
	fmt.Printf("  mode:     %s\n", r.effectiveMode)
	fmt.Printf("  yolo:     %s\n", yoloVal)
}

// Phase 17: armWatcher arms the watcher if requested.
func (r *Runner) armWatcher() {
	if !r.args.Arm || r.args.ArmFunc == nil {
		return
	}
	if armErr := r.args.ArmFunc(r.homeDir); armErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to ensure watcher: %v\n  Run 'munsu watch ensure' to repair supervision.\n", armErr)
	}
}
