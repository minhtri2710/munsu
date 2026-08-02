package fleet

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// Runner orchestrates the full spawn sequence through private phase methods.
// It encapsulates all intermediate state so the public Run function is a thin
// delegate call.
type Runner struct {
	args Args

	// phase state populated during Run
	homeDir             string
	effectiveMode       string
	requestedMode       string // what was asked for (--mode flag, project registry, or config/default-mode)
	fallbackReason      string // why effective mode differs from requested mode
	projPath            string
	wtPath              string
	harness             string
	model               string
	effort              string
	launchCmd           string
	endpoints           EndpointCapabilities
	endpoint            CreatedEndpoint
	briefData           []byte
	windowID            string
	spawnRole           string
	projectConfig       SpawnProjectConfig
	projectConfigLoaded bool

	// dispatchSel caches the single dispatch-selection resolution so the quota
	// selector is invoked at most once per spawn and the selection used for
	// allowlist validation is the same one used for preflight and launch.
	dispatchSel      *harness.DispatchSelection
	dispatchResolved bool

	// soldier launch prompt state
	prompt     string
	promptEnv  *LaunchEnvelope
	launchArgs []string
	launchBin  string

	// manifestSHA256 is the SHA-256 digest of the written launch manifest,
	// persisted to task metadata for external anchoring.
	manifestSHA256 string

	// attestation is the capability attestation snapshot created during mode
	// resolution and checked before soldier launch.
	attestation *CapabilityAttestation
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
	if err := r.checkDispatchHold(); err != nil {
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
	if err := r.resolveEffectiveIdentity(); err != nil {
		return "", err
	}
	if err := r.checkModelAllowlist(); err != nil {
		return "", err
	}
	if err := r.preflightBrief(); err != nil {
		return "", err
	}
	if err := r.checkBacklogAuthority(); err != nil {
		return "", err
	}
	if err := r.checkDispatchHold(); err != nil {
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
	if err := r.checkDispatchHold(); err != nil {
		return "", err
	}
	// Fail-closed: return worktree on any subsequent error (but NOT on success).
	defer func() {
		if !success && r.wtPath != "" {
			_ = backend.ReturnWorktree(r.homeDir, r.wtPath)
		}
	}()
	if err := r.bindWorktree(); err != nil {
		return "", err
	}

	if err := r.resolveHarness(); err != nil {
		return "", err
	}
	r.resolveLaunchConfig()
	if err := r.createAttestation(); err != nil {
		return "", err
	}
	if err := r.buildSoldierPrompt(); err != nil {
		return "", err
	}
	if err := r.checkAttestation(); err != nil {
		return "", err
	}
	if err := r.createSession(); err != nil {
		return "", err
	}
	r.bootstrapWindow()
	if err := r.writeLaunchManifest(); err != nil {
		_ = r.endpoints.Dispose(r.endpoint)
		return "", err
	}
	if err := r.waitAndInjectBrief(); err != nil {
		return "", err
	}
	if err := r.verifyEndpointReadyBeforePersist(); err != nil {
		return "", err
	}
	if err := r.bindEndpoint(); err != nil {
		_ = r.endpoints.Dispose(r.endpoint)
		return "", err
	}
	if err := r.writeTaskMeta(); err != nil {
		_ = r.endpoints.Dispose(r.endpoint)
		return "", err
	}
	if err := r.markWorkingAfterBinding(); err != nil {
		_ = r.endpoints.Dispose(r.endpoint)
		return "", err
	}
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
	entries, err := os.ReadDir(home.StateDir(homeDir))
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
		meta, err := home.ReadMeta(homeDir, id)
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
		if _, err := home.ValidateCaptainProvenance(homeDir); err != nil {
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
		identity, _, _, err := ClassifyIdentity(cwd)
		if err != nil {
			return fmt.Errorf("spawn authority: classifying current checkout: %w", err)
		}
		if identity == Worktree {
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
	if meta, err := home.ReadMeta(r.homeDir, r.args.ID); err == nil {
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
	item, found, err := GetItem(homeDir, id)
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
	if TypedConfigAvailable(r.homeDir) {
		args := r.args
		args.TaskDescription = r.taskDescription()
		resolved, err := ResolveSpawnProjectConfig(r.homeDir, args, r.spawnRole)
		if err != nil {
			return err
		}
		r.projectConfig = resolved
		r.projectConfigLoaded = true
		r.effectiveMode = resolved.Soldier.Mode
		r.requestedMode = r.args.Mode
		if r.requestedMode == "" {
			r.requestedMode = r.effectiveMode
		}
		return nil
	}
	// Determine requested mode (the first non-empty value in precedence).
	r.requestedMode = r.args.Mode
	if r.requestedMode == "" {
		if pm, _, _ := Mode(r.homeDir, r.args.ProjectName); pm != "" {
			r.requestedMode = pm
		}
	}
	mode, err := effectiveModeForSpawn(r.homeDir, r.args)
	if err != nil {
		return err
	}
	r.effectiveMode = mode
	// Capture fallback reason when modes differ.
	if r.requestedMode != "" && r.requestedMode != r.effectiveMode {
		r.fallbackReason = fmt.Sprintf("requested mode %q resolved to %q", r.requestedMode, r.effectiveMode)
	}
	return nil
}

// Phase 3: validateHarnessFlag validates --harness flag if set.
func (r *Runner) validateHarnessFlag() error {
	if r.args.HarnessFlag == "" {
		return nil
	}
	return harness.ValidateHarness(r.args.HarnessFlag)
}

// Phase 3a: resolveEffectiveIdentity resolves the exact harness/model/effort
// once, caching the dispatch selection, and stores the identity on the Runner
// so allowlist validation, preflight, and launch all use the same selection.
// Runs before any backlog/worktree/pane/meta/status side effects; it only
// reads project config, dispatch config, and harness metadata.
func (r *Runner) resolveEffectiveIdentity() error {
	if r.harness == "" {
		h := r.args.HarnessFlag
		if h == "" && r.projectConfigLoaded {
			h = r.projectConfig.Soldier.Harness
		}
		if h == "" {
			if sel, ok := r.dispatchSelection(); ok && sel.Harness != "" {
				h = sel.Harness
			} else {
				var err error
				h, err = harness.Soldier(r.homeDir)
				if err != nil {
					return fmt.Errorf("resolving harness: %w", err)
				}
			}
		}
		r.harness = h
	}
	r.resolveModelAndEffort()
	return nil
}

// resolveModelAndEffort resolves the effective model/effort with the same
// precedence as the launch: adapter template defaults, then project config or
// the cached dispatch selection, then explicit CLI flags.
func (r *Runner) resolveModelAndEffort() {
	adapter, ok := harness.GetAdapter(r.harness)
	if !ok {
		return
	}
	tmpl := adapter.LaunchTemplate
	r.model = tmpl.DefaultModel
	r.effort = tmpl.DefaultEffort
	if r.projectConfigLoaded {
		if r.projectConfig.Soldier.Model != "" {
			r.model = r.projectConfig.Soldier.Model
		}
		if r.projectConfig.Soldier.Effort != "" {
			r.effort = r.projectConfig.Soldier.Effort
		}
	} else if sel, ok := r.dispatchSelection(); ok {
		if sel.Model != "" {
			r.model = sel.Model
		}
		if sel.Effort != "" {
			r.effort = sel.Effort
		}
	}
	if r.args.ModelFlag != "" {
		r.model = r.args.ModelFlag
	}
	if r.args.EffortFlag != "" {
		r.effort = r.args.EffortFlag
	}
}

// Phase 3b: checkModelAllowlist enforces the optional munsu model allowlist on
// the already-resolved effective identity (resolveEffectiveIdentity) before any
// backlog/worktree/pane/meta/status side effects. An absent policy preserves
// compatibility; an unresolved identity under an active policy fails closed.
func (r *Runner) checkModelAllowlist() error {
	present, err := harness.ModelAllowlistPresent(r.homeDir)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	return harness.CheckModelAllowed(r.homeDir, r.harness, r.model)
}

// Phase 4: preflightBrief checks that a brief exists before spawning.
func (r *Runner) preflightBrief() error {
	if err := RecoverTaskHandoffs(r.homeDir); err != nil {
		return err
	}
	if !Exists(r.homeDir, r.args.ID) {
		return fmt.Errorf("no brief found for task %s: scaffold it with 'munsu brief %s %s' before spawning",
			r.args.ID, r.args.ID, r.args.ProjectName)
	}
	return nil
}

// Phase 5: checkBacklogAuthority verifies the task is uniquely queued+ready in the backlog.
// Fail closed unless the task is uniquely present and ready, or --reopen is used.
func (r *Runner) checkBacklogAuthority() error {
	if err := RecoverTaskHandoffs(r.homeDir); err != nil {
		return err
	}
	item, found, err := GetItem(r.homeDir, r.args.ID)
	if err != nil {
		return fmt.Errorf("lifecycle guard: reading backlog: %w", err)
	}
	if !found {
		return fmt.Errorf("lifecycle guard: task %q not found in backlog; register it with 'backlog add %q \"<description>\" --kind %s' before spawning", r.args.ID, r.args.ID, r.args.Kind)
	}

	// Check for duplicate IDs in backlog
	dup, err := HasDuplicate(r.homeDir, r.args.ID)
	if err != nil {
		return fmt.Errorf("lifecycle guard: checking for duplicates: %w", err)
	}
	if dup {
		return fmt.Errorf("lifecycle guard: task %q has duplicate entries in backlog; resolve duplicates before spawning", r.args.ID)
	}

	// Check already-live: existing meta with window means a soldier session exists
	meta, metaErr := home.ReadMeta(r.homeDir, r.args.ID)
	metaExists := metaErr == nil && meta["window"] != ""

	// State-based checks. Backlog In flight without live meta is start→spawn — allow.
	switch item.State {
	case StateBlocked:
		if !r.args.Reopen {
			return fmt.Errorf("lifecycle guard: task %q is blocked; use --reopen to force dispatch or clear the blocker first", r.args.ID)
		}
	case StateDone:
		if !r.args.Reopen {
			return fmt.Errorf("lifecycle guard: task %q is done; use --reopen to reopen", r.args.ID)
		}
	case StateInFlight:
		// Allow when no live session; refuse only duplicate live execution.
		if metaExists && !r.args.Reopen {
			return fmt.Errorf("lifecycle guard: task %q is already in-flight with a live session; refuse duplicate live execution", r.args.ID)
		}
		return r.ensureTaskAggregate(item)
	}

	// Live session without matching state still refuses (stale meta after teardown failure).
	if metaExists && !r.args.Reopen {
		return fmt.Errorf("lifecycle guard: task %q already has a live soldier session; refuse duplicate live execution", r.args.ID)
	}

	return r.ensureTaskAggregate(item)
}

func (r *Runner) ensureTaskAggregate(item Item) error {
	if _, ok, err := home.ReadCurrentTaskAggregate(r.homeDir, r.args.ID); err != nil {
		return fmt.Errorf("lifecycle guard: reading task aggregate: %w", err)
	} else if !ok {
		if _, err := home.CreateTaskAggregate(r.homeDir, r.args.ID, "", item.Description, item.Kind, item.Repo); err != nil {
			return fmt.Errorf("lifecycle guard: creating task aggregate: %w", err)
		}
	}
	return nil
}

func (r *Runner) checkDispatchHold() error {
	generation := ""
	project := r.args.ProjectName
	parentID := ""
	if aggregate, ok, err := home.ReadCurrentTaskAggregate(r.homeDir, r.args.ID); err != nil {
		return err
	} else if ok {
		generation = aggregate.Generation
		parentID = aggregate.ParentTaskID
		if project == "" {
			project = aggregate.Project
		}
	}
	return home.CheckDispatchHold(r.homeDir, home.DispatchActionSpawn, r.args.ID, project, generation, parentID)
}

// Phase 6: resolveProject resolves the project repo path from registry.
func (r *Runner) resolveProject() error {
	if r.projectConfigLoaded {
		r.projPath = r.projectConfig.ProjectPath
		return nil
	}
	projPath, err := ResolveRepoPath(r.homeDir, r.args.ProjectName)
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
	return backend.AssertNotTangled(r.projPath, r.args.ProjectName)
}

// checkScopeGate refuses no-mistakes gate agents before worktree allocation.
func (r *Runner) checkScopeGate() error {
	if err := GateRefusalError(r.projPath); err != nil {
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
	wtPath, err := backend.GetWorktree(r.homeDir, r.projPath, true)
	if err != nil {
		return fmt.Errorf("acquiring worktree: %w", err)
	}
	r.wtPath = wtPath
	return nil
}

// preflightHarness runs harness readiness preflight on the already-resolved
// harness (see resolveEffectiveIdentity) before worktree acquisition so known
// errors fail before allocating any resources. Unknown-level preflight results
// pass through without error.
func (r *Runner) preflightHarness() error {
	if r.harness == "" {
		if err := r.resolveEffectiveIdentity(); err != nil {
			return err
		}
	}

	result, err := harness.Preflight(r.harness)
	if err != nil {
		return err
	}
	if result.AdapterKnown == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: r.harness, Reason: "adapter-unknown"}
	}
	if result.BinaryOnPath == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: r.harness, Reason: "binary-absent"}
	}
	if result.AuthConfigured == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: r.harness, Reason: "auth-absent"}
	}
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
	if r.projectConfigLoaded && r.projectConfig.Soldier.Harness != "" {
		r.harness = r.projectConfig.Soldier.Harness
		return nil
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

// dispatchSelection loads the typed config dispatch profiles and matches
// against the brief body. The first resolution is cached so the selection is
// computed exactly once per spawn (the quota selector must not run twice).
// Checks the published snapshot first (captain context), then the fleet base
// document (general context).
func (r *Runner) dispatchSelection() (harness.DispatchSelection, bool) {
	if r.dispatchResolved {
		if r.dispatchSel == nil {
			return harness.DispatchSelection{}, false
		}
		return *r.dispatchSel, true
	}
	defer func() { r.dispatchResolved = true }()
	// 1. Try published snapshot (captain context).
	snapshot, err := config.LoadPublishedSnapshot(r.homeDir)
	if err == nil {
		cfg := snapshot.Config()
		if len(cfg.DispatchProfiles) > 0 || cfg.SoldierHarness != "" {
			dispatch := &harness.DispatchConfig{
				DefaultHarness: cfg.SoldierHarness,
				DefaultModel:   cfg.Model,
				Profiles:       append([]harness.DispatchProfile(nil), cfg.DispatchProfiles...),
			}
			desc := r.taskDescription()
			sel := harness.ResolveDispatchSelection(dispatch, desc)
			r.dispatchSel = &sel
			return sel, true
		}
	}

	// 2. Try fleet base document (general context).
	base, err := config.LoadFleetBase(r.homeDir)
	if err == nil && (len(base.Config.DispatchProfiles) > 0 || base.Config.SoldierHarness != "") {
		dispatch := &harness.DispatchConfig{
			DefaultHarness: base.Config.SoldierHarness,
			DefaultModel:   base.Config.Model,
			Profiles:       append([]harness.DispatchProfile(nil), base.Config.DispatchProfiles...),
		}
		desc := r.taskDescription()
		sel := harness.ResolveDispatchSelection(dispatch, desc)
		r.dispatchSel = &sel
		return sel, true
	}

	return harness.DispatchSelection{}, false
}

// taskDescription returns text used to match dispatch profiles (brief body or id).
func (r *Runner) taskDescription() string {
	briefPath := Path(r.homeDir, r.args.ID)
	if data, err := os.ReadFile(briefPath); err == nil {
		s := strings.TrimSpace(string(data))
		if s != "" {
			return s
		}
	}
	return r.args.ID
}

// Phase 10: resolveLaunchConfig finalizes the launch command from the identity
// already resolved by resolveEffectiveIdentity (harness, model, effort). The
// selection is resolved once so validation, preflight, and launch all share it.
func (r *Runner) resolveLaunchConfig() {
	adapter, ok := harness.GetAdapter(r.harness)
	if !ok {
		return
	}
	r.launchCmd = harness.LaunchStringWith(r.harness, adapter.LaunchTemplate, r.model, r.effort)
}

// createAttestation creates a capability attestation snapshot after the harness
// and launch config are resolved. It captures the current state of all delivery
// capabilities and binds them to the project, home, harness, gate agent, and modes.
func (r *Runner) createAttestation() error {
	gateAgent := r.harness
	if r.harness == "" {
		gateAgent = "unknown"
	}

	// Build a fallback policy when the effective mode differs from requested.
	var fallbackPolicy *FallbackPolicy
	if r.fallbackReason != "" && r.effectiveMode != r.requestedMode {
		fallbackPolicy = &FallbackPolicy{
			AuthorizedMode: r.effectiveMode,
			Reason:         r.fallbackReason,
		}
	}

	r.attestation = CreateCapabilityAttestation(
		r.args.ProjectName,
		r.homeDir,
		r.harness,
		gateAgent,
		r.requestedMode,
		r.effectiveMode,
		r.fallbackReason,
		fallbackPolicy,
	)
	return nil
}

// checkAttestation verifies that the capability attestation is still valid
// before soldier launch. Late capability loss is handled by preserving work
// and either proceeding with a pre-authorized fallback or blocking for a
// parent Decision.
func (r *Runner) checkAttestation() error {
	if r.attestation == nil {
		return nil
	}
	result := HandleLateCapabilityLoss(r.attestation)
	if !result.Changed {
		return nil
	}
	if result.CanProceed {
		if result.FallbackMode != "" {
			fmt.Fprintf(os.Stderr, "warning: late capability loss, falling back to %s: %s\n", result.FallbackMode, result.Detail)
			r.effectiveMode = result.FallbackMode
			if r.fallbackReason == "" {
				r.fallbackReason = result.Detail
			} else {
				r.fallbackReason += "; " + result.Detail
			}
		}
		return nil
	}
	return fmt.Errorf("launch blocked: %s", result.BlockReason)
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

// Phase 11: createSession creates a session window for the
func (r *Runner) createSession() error {
	if r.endpoints == nil {
		return fmt.Errorf("spawn endpoint capabilities are required")
	}
	ep, err := r.endpoints.Create(CreateRequest{Home: r.homeDir, PreferredBackend: r.args.Backend, TabName: soldierTabLabel(r.args.ProjectName, r.args.ID), Cwd: r.wtPath})
	if err != nil {
		return err
	}
	status, err := r.endpoints.Probe(ep)
	if err != nil || (status.State != EndpointAlive && status.State != EndpointStarting) {
		_ = r.endpoints.Dispose(ep)
		if err != nil {
			return fmt.Errorf("verifying created pane %q on backend %q: %w", ep.Handle, ep.Backend, err)
		}
		return fmt.Errorf("created pane %q observation %s on backend %q", ep.Handle, status.State, ep.Backend)
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
	briefPath := Path(r.homeDir, r.args.ID)
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
	input := LaunchPromptInput{
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
	if err := FailClosedDuringLaunch(input); err != nil {
		return fmt.Errorf("pre-launch fail-closed: %w", err)
	}

	// Build the complete prompt and envelope.
	promptText, env, err := BuildLaunchPrompt(input)
	if err != nil {
		return fmt.Errorf("building soldier launch prompt: %w", err)
	}
	r.prompt = promptText
	r.promptEnv = env

	// Persist durable files to the worktree.
	charter := DefaultCharter(r.args.ID, r.args.Kind, r.effectiveMode)
	if err := PersistLaunchFiles(r.wtPath, charter, briefData, env, promptText); err != nil {
		return fmt.Errorf("persisting soldier launch files: %w", err)
	}

	// Build launch arguments with the complete prompt, passing model and effort.
	bin, args, err := BuildLaunchArgs(r.wtPath, r.harness, r.model, r.effort, promptText)
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
func (r *Runner) resolveSkills() (required, optional []SkillEntry, diags []string) {
	// Catalog of skills available to spawns.
	// In a production system this would come from a registry or config file;
	// here we build it from known skills and their authority classifications.
	catalog := []SkillEntry{
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

	required, optional, diags = CollectSkills(catalog, requiredNames, optionalNames)
	return required, optional, diags
}

// resolveParentCaptainID returns the parent captain ID from the endpoint meta.
// Returns empty string when not running under a captain.
func (r *Runner) resolveParentCaptainID() string {
	if r.spawnRole == "captain" {
		if id, err := home.ValidateCaptainProvenance(r.homeDir); err == nil {
			return id
		}
	}
	// For general launches, use a generic identifier.
	return "general"
}

// shQuote wraps s in single quotes, escaping embedded single quotes.
func spawnShQuote(s string) string {
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
	b.WriteString(spawnShQuote(r.wtPath))
	b.WriteString("\n")
	b.WriteString("export MUNSU_HOME=")
	b.WriteString(spawnShQuote(r.homeDir))
	b.WriteString("\n")
	b.WriteString("export MUNSU_ROLE=soldier\n")
	b.WriteString("export MUNSU_TASK_ID=")
	b.WriteString(spawnShQuote(r.args.ID))
	b.WriteString("\n")
	b.WriteString("export MUNSU_PARENT_STATUS=")
	b.WriteString(spawnShQuote(r.homeDir))
	b.WriteString("\n")
	if r.projectConfigLoaded {
		b.WriteString("export MUNSU_CONFIG_SNAPSHOT_DIGEST=")
		b.WriteString(spawnShQuote(r.projectConfig.SnapshotDigest))
		b.WriteString("\n")
	}
	b.WriteString("exec ")
	b.WriteString(spawnShQuote(r.launchBin))
	for _, arg := range r.launchArgs {
		b.WriteString(" ")
		b.WriteString(spawnShQuote(arg))
	}
	b.WriteString("\n")

	launchScript := filepath.Join(r.wtPath, ".soldier-launch.sh")
	if writeErr := os.WriteFile(launchScript, []byte(b.String()), 0755); writeErr != nil {
		fmt.Fprintf(os.Stderr, "warning: writing launch script: %v\n", writeErr)
	}
	fullCmd := fmt.Sprintf("bash %s", spawnShQuote(launchScript))
	if sendErr := r.endpoints.Submit(r.endpoint, fullCmd); sendErr != nil {
		fmt.Fprintf(os.Stderr, "warning: sending harness launch command: %v\n", sendErr)
	}
}

// Phase 13b: writeLaunchManifest writes the digest manifest after all launch
// artifacts exist. The manifest is written after bootstrapWindow creates the
// launch script, so all artifacts are present.
func (r *Runner) writeLaunchManifest() error {
	entries := []ManifestEntry{}
	for _, name := range []string{CharterName, BriefName, EnvelopeName, PromptName, LaunchScriptName} {
		entry, err := ManifestEntryForFile(r.wtPath, name, DisposalPolicyCleanable)
		if err != nil {
			return fmt.Errorf("building manifest entry for %s: %w", name, err)
		}
		entries = append(entries, entry)
	}
	manifest := BuildManifest(entries)

	// Include the legacy brief migration policy in the manifest so retirement
	// can verify .soldier-md cleanup during the bounded migration window.
	policy := LegacyBriefMatchCanonicalV1
	manifest.LegacyBriefMigration = &policy

	digest, err := WriteManifest(r.wtPath, manifest)
	if err != nil {
		return fmt.Errorf("writing launch manifest: %w", err)
	}
	r.manifestSHA256 = digest
	return nil
}

// Phase 13c: waitForReady waits for the harness to be ready. No brief injection
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
		_ = home.AppendStatus(r.homeDir, r.args.ID, "failed: harness handshake")
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
	poll := time.NewTimer(0)
	defer poll.Stop()

	for {
		select {
		case <-deadline:
			capture, _ := r.endpoints.Capture(r.endpoint, 60)
			return fmt.Errorf("harness not ready after %ds: last capture: %q", timeoutSec, capture)
		case <-poll.C:
			status, probeErr := r.endpoints.Probe(r.endpoint)
			if probeErr != nil {
				return fmt.Errorf("probing bound endpoint: %w", probeErr)
			}
			if status.State == EndpointDead {
				return fmt.Errorf("window died while waiting for ready")
			}
			if status.State == EndpointUnresponsive || status.State == EndpointUnresolved || status.State == EndpointUnknown || status.State == EndpointStaleIdentity {
				return fmt.Errorf("endpoint observation %s while waiting for ready", status.State)
			}
			capture, err := r.endpoints.Capture(r.endpoint, 60)
			if err != nil {
				continue
			}
			// Dialog handlers: auto-answer trust prompts before checking ready patterns.
			if !trustHandled && harness.IsTrustPrompt(capture, r.harness) {
				_ = r.endpoints.Submit(r.endpoint, "")
				trustHandled = true
				poll.Reset(2 * time.Second)
				continue
			}
			// Check for failure patterns and abort early when detected.
			if harness.HasFailurePattern(capture, r.harness) {
				return fmt.Errorf("harness %q detected launch failure: %q", r.harness, capture)
			}
			if harness.HasReadyPattern(capture, r.harness) {
				return nil
			}
			poll.Reset(2 * time.Second)
		}
	}
}

func (r *Runner) verifyEndpointReadyBeforePersist() error {
	status, err := r.endpoints.Probe(r.endpoint)
	if err != nil || status.State != EndpointAlive {
		_ = r.endpoints.Dispose(r.endpoint)
		if err != nil {
			return fmt.Errorf("verifying created pane %q on backend %q: %w", r.windowID, r.endpoint.Backend, err)
		}
		return fmt.Errorf("created pane %q observation %s on backend %q before persisting state", r.windowID, status.State, r.endpoint.Backend)
	}
	return nil
}

func (r *Runner) bindWorktree() error {
	if r.args.Authority == nil {
		return fmt.Errorf("binding worktree before endpoint launch: task authority is not composed for spawn")
	}
	agg, err := r.args.Authority.Get(r.args.ID)
	if err != nil {
		return fmt.Errorf("binding worktree before endpoint launch: %w", err)
	}
	binding, err := buildTaskWorktreeBinding(r.projPath, r.wtPath)
	if err != nil {
		return fmt.Errorf("binding worktree before endpoint launch: %w", err)
	}
	if _, err := r.args.Authority.BindWorktree(taskauthority.BindWorktreeRequest{
		OperationID:        fmt.Sprintf("spawn-bind-wt-%s-%d", r.args.ID, agg.Generation),
		Actor:              r.spawnActor(),
		TaskID:             r.args.ID,
		ExpectedGeneration: agg.Generation,
		Binding:            binding,
		Reason:             "spawn",
	}); err != nil {
		return fmt.Errorf("binding worktree before endpoint launch: %w", err)
	}
	return nil
}

// spawnActor resolves the authoritative actor identity of the rank running
// the spawn from the exact home, matching the legacy home fallback: captain
// identity for captain homes, otherwise the home identity.
func (r *Runner) spawnActor() taskauthority.Actor {
	identity, rank, err := home.ReadHomeIdentity(r.homeDir)
	if err != nil {
		identity = filepath.Base(r.homeDir)
		rank = home.RankGeneral
	}
	owner := identity
	if rank == home.RankCaptain {
		owner = "captain:" + identity
	}
	return taskauthority.Actor{ID: owner, Rank: string(rank)}
}

func buildTaskWorktreeBinding(primaryPath, worktreePath string) (taskauthority.WorktreeBinding, error) {
	canonicalWorktree, err := canonicalExistingPath(worktreePath)
	if err != nil {
		return taskauthority.WorktreeBinding{}, fmt.Errorf("resolving worktree path: %w", err)
	}
	identity, gitDir, commonDir, err := ClassifyIdentity(canonicalWorktree)
	if err != nil {
		return taskauthority.WorktreeBinding{}, err
	}
	if identity != Worktree {
		return taskauthority.WorktreeBinding{}, fmt.Errorf("worktree binding target is %s, not worktree", identity)
	}
	repoIdentity := commonDir
	if primaryPath != "" {
		_, _, primaryCommonDir, err := ClassifyIdentity(primaryPath)
		if err != nil {
			return taskauthority.WorktreeBinding{}, fmt.Errorf("classifying repository identity: %w", err)
		}
		repoIdentity = primaryCommonDir
	}
	head, err := gitRevParseForBinding(canonicalWorktree, "HEAD")
	if err != nil {
		return taskauthority.WorktreeBinding{}, fmt.Errorf("reading worktree head: %w", err)
	}
	return taskauthority.WorktreeBinding{
		RepositoryIdentity: repoIdentity,
		Path:               canonicalWorktree,
		GitDir:             gitDir,
		CommonDir:          commonDir,
		Head:               head,
		LeaseID:            newEndpointToken(),
		FenceToken:         newEndpointToken(),
		BoundAtUnix:        time.Now().Unix(),
	}, nil
}

func gitRevParseForBinding(dir, flag string) (string, error) {
	cmd := exec.Command("git", "rev-parse", flag)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *Runner) bindEndpoint() error {
	agg, ok, err := home.ReadCurrentTaskAggregate(r.homeDir, r.args.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("task aggregate %s has no current generation for endpoint binding", r.args.ID)
	}
	binding := home.TaskEndpointBinding{
		Backend:      r.endpoint.Backend,
		Handle:       r.endpoint.Handle,
		LeaseID:      newEndpointToken(),
		FenceToken:   newEndpointToken(),
		SessionOwner: r.endpoint.SessionOwner,
		WorkspaceID:  r.endpoint.WorkspaceID,
		TabID:        r.endpoint.TabID,
		BoundAtUnix:  time.Now().Unix(),
	}
	if err := home.BindTaskEndpoint(r.homeDir, r.args.ID, agg.Generation, binding); err != nil {
		return fmt.Errorf("binding endpoint before working: %w", err)
	}
	return nil
}

func (r *Runner) markWorkingAfterBinding() error {
	if _, _, err := home.UpdateCurrentTaskAggregateState(r.homeDir, r.args.ID, "working", "spawned"); err != nil {
		return fmt.Errorf("marking task working after endpoint binding: %w", err)
	}
	return nil
}

func newEndpointToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}

// Phase 14: writeTaskMeta writes the task metadata file.
func (r *Runner) writeTaskMeta() error {
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
	if r.projectConfigLoaded {
		meta["config_snapshot_digest"] = r.projectConfig.SnapshotDigest
	}
	if r.manifestSHA256 != "" {
		meta["launch_manifest_sha256"] = r.manifestSHA256
	}

	for k, v := range r.endpoint.Metadata {
		meta[k] = v
	}

	// Persist capability attestation for lifecycle visibility.
	if r.attestation != nil {
		meta[MetaCapabilityAttestation] = r.attestationJSON()
		meta[MetaRequestedMode] = r.attestation.RequestedMode
		meta[MetaEffectiveMode] = r.attestation.EffectiveMode
		if r.attestation.FallbackReason != "" {
			meta[MetaFallbackReason] = r.attestation.FallbackReason
		}
	}

	if err := home.WriteMeta(r.homeDir, r.args.ID, meta); err != nil {
		return fmt.Errorf("writing task meta: %w", err)
	}
	return nil
}

// attestationJSON returns the JSON serialization of the attestation.
func (r *Runner) attestationJSON() string {
	if r.attestation == nil {
		return ""
	}
	data, err := json.Marshal(r.attestation)
	if err != nil {
		return ""
	}
	return string(data)
}

// Phase 15: appendSpawnedStatus appends the working: spawned status line.
func (r *Runner) appendSpawnedStatus() {
	_ = home.AppendStatus(r.homeDir, r.args.ID, "working: spawned")
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
	if r.requestedMode != "" && r.requestedMode != r.effectiveMode {
		fmt.Printf("  requested: %s\n", r.requestedMode)
	}
	if r.fallbackReason != "" {
		fmt.Printf("  reason:   %s\n", r.fallbackReason)
	}
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
