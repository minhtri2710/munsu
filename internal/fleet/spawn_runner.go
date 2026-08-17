package fleet

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/domain"
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
	homeDir               string
	effectiveMode         string
	requestedMode         string // what was asked for (--mode flag, project registry, or config/default-mode)
	fallbackReason        string // why effective mode differs from requested mode
	allowDirectPRFallback bool   // explicit configured direct-PR policy; fallback only with it
	projPath              string
	wtPath                string
	harness               string
	model                 string
	effort                string
	taskScoutScope        string
	taskScoutBudget       int64
	launchCmd             string
	endpoints             EndpointCapabilities
	endpoint              CreatedEndpoint
	briefData             []byte
	windowID              string
	spawnRole             string
	incarnation           string // opaque generation-bound endpoint incarnation, minted once per launch
	projectConfig         SpawnProjectConfig
	projectConfigLoaded   bool

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

	// launch intent state: the durable pre-acquisition intent committed by
	// beginLaunchIntent (CanonicalBeginSpawnRequest) plus the deterministic
	// launch/window identity and one-time worktree/endpoint reservation
	// fences it owns. On recovery the same identity is re-derived, never a
	// freshly minted intent.
	taskID        domain.TaskID
	launch        *taskauthority.LaunchIntent
	launchID      string
	windowLabel   string
	launchReentry bool // the intent pre-existed this run (recovery, not first attempt)

	// launch submission evidence: the exact endpoint command submitted for
	// the launch and its sha256 digest, durably recorded before submission.
	launchCommand       string
	launchCommandDigest string

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

// mintEndpointIncarnation produces a fresh opaque endpoint incarnation for a
// first-time launch attempt. An error aborts the launch (never a shared
// sentinel), because the incarnation is integral to the freshness identity.
// Recovery reuses the persisted LaunchIntent incarnation instead of minting.
func (r *Runner) mintEndpointIncarnation() (string, error) {
	mint := r.args.IncarnationMint
	if mint == nil {
		mint = defaultIncarnationMint
	}
	return mint()
}

// authorizeEndpoint concludes Fleet's authorization for a probe of an endpoint
// the runner holds acquisition evidence for (BEO-16/P1a): the in-process
// CreatedEndpoint from CreateReserved (fresh acquisition) or the durable
// AcquiredEndpoint (recovery) both tie the exact handle to the launch
// incarnation. The proof revalidates the CURRENT canonical generation/revision
// at authorization time. Raw probe liveness is promoted to Live() only with
// that explicit acquisition evidence; a probe alone is never Live()/Absent().
func (r *Runner) authorizeEndpoint(obs SpawnEndpointObservation, ep CreatedEndpoint) SpawnEndpointObservation {
	gen, rev := r.canonicalGenerationRevision()
	return authorizeLive(obs, exactEndpointProof{
		backend:     ep.Backend,
		handle:      ep.Handle,
		incarnation: ep.Incarnation,
		leaseID:     r.epReservationID(),
		fenceToken:  r.epFenceToken(),
		generation:  gen,
		revision:    rev,
		acquired:    true, // in-process creation receipt or durable AcquiredEndpoint
	})
}

// canonicalGenerationRevision revalidates the current canonical aggregate
// generation/revision at authorization time. A task with no aggregate or no
// current generation fails closed (returns 0,0).
func (r *Runner) canonicalGenerationRevision() (uint64, uint64) {
	if r.args.Authority == nil {
		return 0, 0
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return 0, 0
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return 0, 0
	}
	return uint64(agg.Generation), uint64(agg.Revision)
}

// Run executes the full spawn orchestration sequence.
func (r *Runner) Run() (string, error) {
	if err := r.resolveHome(); err != nil {
		return "", err
	}
	if err := r.checkSupervision(); err != nil {
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
	if err := r.checkSupervision(); err != nil {
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
	// Durable launch intent is committed BEFORE any worktree, endpoint, or
	// process acquisition (Task 4.1 / #412): the canonical aggregate carries
	// the immutable pre-acquisition intent (snapshot digest, explicit
	// Backend/Harness identities, deterministic launch/window identity, one-time
	// worktree/endpoint reservation fences) before backend.GetWorktree or
	// endpoint Create/Submit. Recovery with the same Operation ID/generation
	// re-adopts the committed intent and never mints a different one.
	if err := r.beginLaunchIntent(); err != nil {
		return "", err
	}
	success := false
	if err := r.checkSupervision(); err != nil {
		return "", err
	}
	// Fail-closed worktree return is phase-aware: a worktree the aggregate
	// durably owns (bound under the launch fence) is never returned to the
	// pool; only unbound acquisitions are returned on failure.
	defer func() {
		if !success && r.wtPath != "" && r.worktreeReturnAllowed() {
			_ = backend.ReturnWorktree(r.homeDir, r.wtPath)
		}
	}()
	if err := r.acquireWorktree(); err != nil {
		return "", err
	}
	bound, err := r.bindWorktree()
	if err != nil {
		return "", err
	}

	if err := r.resolveHarness(); err != nil {
		return "", err
	}
	r.resolveLaunchConfig()
	if err := r.createAttestation(); err != nil {
		return "", err
	}
	if err := r.buildSoldierPrompt(bound); err != nil {
		return "", err
	}
	if err := r.checkAttestation(); err != nil {
		return "", err
	}
	if err := r.createSession(); err != nil {
		return "", err
	}
	// The acquired endpoint is attached durably (AttachEndpoint) before any
	// process submission; the launch evidence is recorded durably before the
	// submission so a crash can never leave an unguarded duplicate launch.
	if err := r.attachEndpoint(); err != nil {
		return "", err
	}
	if err := r.submitLaunch(); err != nil {
		return "", err
	}
	if err := r.writeLaunchManifest(); err != nil {
		return "", err
	}
	if err := r.waitAndInjectBrief(); err != nil {
		return "", err
	}
	if err := r.verifyEndpointReadyBeforePersist(); err != nil {
		return "", err
	}
	if err := r.writeTaskMeta(); err != nil {
		return "", err
	}
	spawned, err := r.confirmSpawn()
	if err != nil {
		return "", err
	}
	// The accepted capability attestation becomes authoritative evidence
	// after ConfirmSpawn: the authoritative Generation comes from the
	// ConfirmSpawn receipt. A failure keeps the observation runtime-only and
	// is reported as a warning — the task is already working and the window
	// is live, so the spawn itself never rolls back.
	if err := r.attachAttestation(spawned.Generation); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
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
// the task is present and dispatchable through the canonical Task Authority.
// Normal Captain flow is:
//
//	munsu task start <id>  →  munsu spawn <id>
//
// Working without live meta/window MUST allow spawn without --force.
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
			return fmt.Errorf("captain task authority: task %s already has a live soldier session (window=%s); refuse duplicate live execution", r.args.ID, win)
		}
		if pane := meta["herdr_pane_id"]; pane != "" {
			return fmt.Errorf("captain task authority: task %s already has a live soldier session (pane=%s); refuse duplicate live execution", r.args.ID, pane)
		}
	}

	agg, err := r.taskAggregate()
	if err != nil {
		return err
	}
	switch agg.Phase {
	case taskauthority.PhaseWorking:
		// start→spawn: working without live window/pane is allowed.
		return nil
	case taskauthority.PhaseDone:
		return fmt.Errorf("captain task authority: task %s is already done; reopen requires General instruction", r.args.ID)
	case taskauthority.PhaseBlocked:
		return fmt.Errorf("captain task authority: task %s is blocked; resolve dependencies before dispatch", r.args.ID)
	case taskauthority.PhaseQueued:
		return nil
	default:
		return fmt.Errorf("captain task authority: task %s has unexpected phase %q", r.args.ID, agg.Phase)
	}
}

// taskAggregate reads one task's current canonical aggregate through the
// composed Authority, failing closed when the task has no canonical record.
func (r *Runner) taskAggregate() (taskauthority.Aggregate, error) {
	if r.args.Authority == nil {
		return taskauthority.Aggregate{}, fmt.Errorf("task authority is not composed for spawn")
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return taskauthority.Aggregate{}, fmt.Errorf("task %q has no canonical Task Authority record; register it with 'task add %q \"<description>\" --kind %s' before spawning: %w", r.args.ID, r.args.ID, r.args.Kind, err)
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return taskauthority.Aggregate{}, fmt.Errorf("task %q has no canonical Task Authority record; register it with 'task add %q \"<description>\" --kind %s' before spawning: %w", r.args.ID, r.args.ID, r.args.Kind, err)
	}
	return agg, nil
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
		r.allowDirectPRFallback = resolved.AllowDirectPRFallback
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
// Runs before any worktree/pane/meta/status side effects; it only
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
				h, err = harness.ResolveSoldierFromSnapshot(r.projectConfig.Frozen.Config())
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
// worktree/pane/meta/status side effects. An absent policy preserves
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

// Phase 5: checkBacklogAuthority verifies the task is uniquely present in the
// canonical Task Authority and dispatchable. Fail closed unless the task is
// present and ready, or --reopen is used.
func (r *Runner) checkBacklogAuthority() error {
	if err := RecoverTaskHandoffs(r.homeDir); err != nil {
		return err
	}
	agg, err := r.taskAggregate()
	if err != nil {
		return err
	}

	// Check already-live: existing meta with window means a soldier session exists
	meta, metaErr := home.ReadMeta(r.homeDir, r.args.ID)
	metaExists := metaErr == nil && meta["window"] != ""

	// State-based checks. Working without live meta is start→spawn — allow.
	switch agg.Phase {
	case taskauthority.PhaseBlocked:
		if !r.args.Reopen {
			return fmt.Errorf("lifecycle guard: task %q is blocked; use --reopen to force dispatch or clear the blocker first", r.args.ID)
		}
	case taskauthority.PhaseDone:
		if !r.args.Reopen {
			return fmt.Errorf("lifecycle guard: task %q is done; use --reopen to reopen", r.args.ID)
		}
	case taskauthority.PhaseWorking:
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

func (r *Runner) checkSupervision() error {
	// Supervision gate: spawn fails closed when the watcher lease is degraded.
	// Durable Dispatch Holds are evaluated atomically inside the Task Authority
	// ConfirmSpawn operation, never here (Task 4.3, ADR-0007 §8).
	return CheckSupervisionForDispatch(r.homeDir, home.DispatchActionSpawn)
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
	err := preflight(r.projPath)
	if err == nil {
		return nil
	}
	// The no-mistakes gate preflight reported an exact blocker. Fail closed
	// with supported delivery-mode guidance unless the operator explicitly
	// configured the direct-PR fallback policy; the fallback records the
	// blocker as audit evidence (fallbackReason → attestation → task meta).
	var blocker *GateBlockerError
	if r.allowDirectPRFallback && errors.As(err, &blocker) {
		fmt.Fprintf(os.Stderr, "warning: no-mistakes delivery blocked (%s); falling back to direct-PR under the configured allow-direct-pr-fallback policy: %s\n", blocker.Category, blocker.Detail)
		r.effectiveMode = "direct-PR"
		r.fallbackReason = fmt.Sprintf("no-mistakes blocked (%s): %s; direct-PR fallback under configured allow-direct-pr-fallback policy", blocker.Category, blocker.Detail)
		return nil
	}
	return err
}

// Phase 8: acquireWorktree acquires the worktree owned by the launch
// intent's one-time worktree reservation (GetWorktreeReserved — the FIRST
// attempt consumes the durable reservation identity). It is re-entrant under
// the committed launch intent: when the aggregate already holds the durable
// worktree binding (recovery after a crash), the bound path is adopted and no
// provider call happens. A recovery of an acquired-but-unbound reservation is
// passed to the provider, which must return the SAME reservation-owned
// worktree (git fallback: deterministic reservation-keyed path) or fail
// closed (treehouse: no reservation-keyed recovery) — never allocate a
// replacement. The binding identity is verified at bindWorktree.
func (r *Runner) acquireWorktree() error {
	if r.args.Authority != nil {
		if taskID, err := domain.NewTaskID(r.args.ID); err == nil {
			if agg, err := r.args.Authority.Get(taskID); err == nil && agg.Worktree != nil {
				r.wtPath = agg.Worktree.Path
				return nil
			}
		}
	}
	reservationID := r.wtReservationID()
	if reservationID == "" {
		return fmt.Errorf("acquiring worktree: no launch worktree reservation; reservation-aware acquisition is mandatory for canonical launches")
	}
	wtPath, err := backend.GetWorktreeReserved(r.homeDir, r.projPath, true, reservationID, r.launchReentry)
	if err != nil {
		if backend.IsWorktreeReservationRecoveryUnsupported(err) {
			return fmt.Errorf("acquiring worktree: %w — DEPENDENCY_REQUEST (treehouse CLI has no reservation-keyed get/recover)", err)
		}
		return fmt.Errorf("acquiring worktree: %w", err)
	}
	// Canonicalize the acquired path (EvalSymlinks) so the launch artifact and
	// the durable binding use the SAME identity on every attempt of the
	// launch: a re-entry adopts the canonical bound path, so a symlinked home
	// (e.g. /var -> /private/var) must never produce a different artifact.
	canonical, err := canonicalExistingPath(wtPath)
	if err != nil {
		return fmt.Errorf("acquiring worktree: resolving acquired path: %w", err)
	}
	r.wtPath = canonical
	return nil
}

// wtReservationID returns the launch intent's one-time worktree reservation
// identity (empty when no intent is committed).
func (r *Runner) wtReservationID() string {
	if r.launch != nil {
		return r.launch.WorktreeReservationID
	}
	return ""
}

// worktreeReturnAllowed reports whether the acquired worktree may be returned
// to the pool on failure. Phase-aware: a worktree the aggregate durably owns
// (bound under the launch fence) must never be returned; only unbound
// acquisitions are returned. When no Authority is composed there is no
// canonical ownership, so the legacy return-on-failure semantics apply.
func (r *Runner) worktreeReturnAllowed() bool {
	if r.args.Authority == nil {
		return true
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return true
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil || agg.Worktree == nil {
		return true
	}
	// The aggregate owns a worktree binding: never return it (whether it is
	// this worktree or a different one we do not own).
	return false
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
	if result.BinaryOnPath == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: r.harness, Reason: "binary-absent"}
	}
	if result.AuthConfigured == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: r.harness, Reason: "auth-absent"}
	}
	return nil
}

// beginLaunchIntent resolves the current canonical Task aggregate/Generation
// and commits the deterministic pre-acquisition launch intent (BeginSpawn)
// BEFORE any worktree, endpoint, or process acquisition. The intent is
// constructed from the immutable snapshot digest plus the explicit
// Backend/Harness/model/effort/mode/kind/project/parent identities and the
// deterministic launch/window identity and one-time worktree/endpoint
// reservation fences. A retry of the same launch re-derives the identical
// intent: a committed intent that matches is re-adopted (never minted
// anew); a different committed intent, a stale generation, or a non-queued
// phase fails closed.
func (r *Runner) beginLaunchIntent() error {
	if r.args.Authority == nil {
		return fmt.Errorf("launch intent: task authority is not composed for spawn")
	}
	if r.endpoints == nil {
		return fmt.Errorf("launch intent: endpoint capabilities are not composed; reservation-aware endpoint create/recover is mandatory and must be present BEFORE acquisition")
	}
	if !r.projectConfigLoaded || r.projectConfig.SnapshotDigest == "" {
		return fmt.Errorf("launch intent: spawn requires the typed project snapshot (snapshot digest); no snapshot identity to bind")
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return fmt.Errorf("launch intent: %w", err)
	}
	r.taskID = taskID
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return fmt.Errorf("launch intent: resolving task %s: %w", r.args.ID, err)
	}
	r.taskScoutScope = agg.Definition.ScoutScope
	r.taskScoutBudget = agg.Definition.ScoutRuntimeBudgetSecs
	prec := domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	// Resolve the opaque endpoint incarnation BEFORE any acquisition so a crash
	// after create but before attach can reuse the same token (BEO-16/P1a).
	// Recovery reuses the persisted LaunchIntent incarnation; a first attempt
	// mints it here and the mint error aborts (never a shared sentinel).
	endpointIncarnation := ""
	if agg.Launch != nil {
		endpointIncarnation = agg.Launch.EndpointIncarnation
	} else {
		inc, err := r.mintEndpointIncarnation()
		if err != nil {
			return fmt.Errorf("launch intent: minting endpoint incarnation: %w", err)
		}
		endpointIncarnation = inc
	}
	r.incarnation = endpointIncarnation
	req := r.buildBeginSpawnRequest(prec, agg.Generation, endpointIncarnation)
	if agg.Launch != nil {
		if !r.launchIntentMatches(req, *agg.Launch) {
			return fmt.Errorf("launch intent: task %s generation %s already holds a different launch intent; refuse to re-launch", r.args.ID, agg.Generation)
		}
		if agg.Phase != taskauthority.PhaseQueued {
			if agg.Phase == taskauthority.PhaseWorking && agg.Endpoint != nil && agg.AcquiredEndpoint != nil && agg.LaunchEvidence != nil {
				// Recovery after the final bind: the launch is complete under
				// this exact identity; the remaining phases replay idempotently.
				r.adoptLaunch(*agg.Launch)
				return nil
			}
			return fmt.Errorf("launch intent: task %s generation %s is %s, spawn requires queued; duplicate or stale live spawn refused", r.args.ID, agg.Generation, agg.Phase)
		}
		r.launchReentry = true
		r.adoptLaunch(*agg.Launch)
		return nil
	}
	if agg.Phase != taskauthority.PhaseQueued {
		return fmt.Errorf("launch intent: task %s generation %s is %s, spawn requires queued; duplicate live spawn refused", r.args.ID, agg.Generation, agg.Phase)
	}
	op, err := r.spawnOperation("begin", agg.Generation, req)
	if err != nil {
		return fmt.Errorf("launch intent: %w", err)
	}
	if _, err := r.args.Authority.BeginSpawn(op, req); err != nil {
		return fmt.Errorf("launch intent: %w", err)
	}
	r.launchReentry = false
	fresh, err := r.args.Authority.Get(taskID)
	if err != nil {
		return fmt.Errorf("launch intent: re-reading committed intent: %w", err)
	}
	if fresh.Launch == nil {
		return fmt.Errorf("launch intent: committed launch intent missing after BeginSpawn")
	}
	r.adoptLaunch(*fresh.Launch)
	return nil
}

// buildBeginSpawnRequest constructs the deterministic BeginSpawn request for
// the task's current generation from the immutable snapshot digest, the
// explicit launch identities, and the opaque endpoint incarnation resolved
// for this launch operation (reused from the persisted intent on recovery, or
// freshly minted on first attempt). Every derived value is reproducible on a
// retry (same Operation ID and digest); the incarnation is provided by the
// caller because it is opaque and cannot be re-derived.
func (r *Runner) buildBeginSpawnRequest(prec domain.Precondition, gen taskauthority.Generation, endpointIncarnation string) taskauthority.CanonicalBeginSpawnRequest {
	wtRes, wtFence, epRes, epFence := spawnReservationIdentities(r.args.ID, uint64(gen))
	return taskauthority.CanonicalBeginSpawnRequest{
		HomeID:                r.args.Authority.HomeID(),
		TaskID:                r.taskID,
		Precondition:          prec,
		SnapshotDigest:        r.projectConfig.SnapshotDigest,
		Backend:               r.projectConfig.Frozen.Config().Backend,
		Harness:               r.harness,
		Model:                 r.model,
		Effort:                r.effort,
		Mode:                  r.effectiveMode,
		Kind:                  r.args.Kind,
		Project:               r.projectConfig.ProjectName,
		ParentTaskID:          r.resolveParentCaptainID(),
		LaunchID:              fmt.Sprintf("launch-%s-%d", r.args.ID, uint64(gen)),
		WindowLabel:           fmt.Sprintf("%s-g%d", soldierTabLabel(r.args.ProjectName, r.args.ID), uint64(gen)),
		WorktreeReservationID: wtRes,
		WorktreeFenceToken:    wtFence,
		EndpointReservationID: epRes,
		EndpointFenceToken:    epFence,
		EndpointIncarnation:   endpointIncarnation,
		Reason:                "spawn",
	}
}

// launchIntentMatches reports whether a committed launch intent carries the
// identical immutable launch identity as the deterministic BeginSpawn request
// (mirrors the canonical no-op check; record metadata is excluded).
func (r *Runner) launchIntentMatches(req taskauthority.CanonicalBeginSpawnRequest, l taskauthority.LaunchIntent) bool {
	return l.SnapshotDigest == req.SnapshotDigest && l.Backend == req.Backend && l.Harness == req.Harness &&
		l.Model == req.Model && l.Effort == req.Effort && l.Mode == req.Mode && l.Kind == req.Kind &&
		l.Project == req.Project && l.ParentTaskID == req.ParentTaskID && l.LaunchID == req.LaunchID &&
		l.WindowLabel == req.WindowLabel && l.WorktreeReservationID == req.WorktreeReservationID &&
		l.WorktreeFenceToken == req.WorktreeFenceToken && l.EndpointReservationID == req.EndpointReservationID &&
		l.EndpointFenceToken == req.EndpointFenceToken && l.EndpointIncarnation == req.EndpointIncarnation
}

// adoptLaunch copies the committed launch intent identities onto the Runner
// so every later phase consumes the exact committed values (never a fresh
// derivation).
func (r *Runner) adoptLaunch(l taskauthority.LaunchIntent) {
	r.launch = &l
	r.launchID = l.LaunchID
	r.windowLabel = l.WindowLabel
}

// spawnReservationIdentities derives the one-time worktree/endpoint
// reservation IDs and fence tokens deterministically from the task and
// generation (distinct salts per identity) so a retry reproduces the exact
// intent and the fences are never reused across launches.
func spawnReservationIdentities(taskID string, gen uint64) (worktreeReservationID, worktreeFenceToken, endpointReservationID, endpointFenceToken string) {
	return "wtres-" + launchFence("wt-res", taskID, gen),
		"wtfence-" + launchFence("wt-fence", taskID, gen),
		"epres-" + launchFence("ep-res", taskID, gen),
		"epfence-" + launchFence("ep-fence", taskID, gen)
}

// launchFence returns a deterministic 16-hex-char fence identity for one
// reservation salt of one task generation.
func launchFence(salt, taskID string, gen uint64) string {
	h := sha256.Sum256([]byte("munsu-launch-fence:" + salt + ":" + taskID + ":" + strconv.FormatUint(gen, 10)))
	return hex.EncodeToString(h[:8])
}

// BoundWorktree is the proof that a launch target has been bound under the
// launch intent's worktree reservation fence AND classified as an isolated
// worktree by ClassifyIdentity (ADR-0009).
//
// Only bindWorktree returns one, and every phase that writes into the worktree
// takes one, so the invariant no longer rests on the phase ORDER inside Run():
// moving buildSoldierPrompt above bindWorktree does not compile, instead of
// silently dropping the only classification of the path being written to.
type BoundWorktree struct{ path string }

// Path returns the canonical path of the bound worktree.
func (b BoundWorktree) Path() string { return b.path }

// newBoundWorktree canonicalizes path and refuses anything ClassifyIdentity
// does not call an isolated worktree.
func newBoundWorktree(path string) (BoundWorktree, error) {
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return BoundWorktree{}, fmt.Errorf("resolving worktree path: %w", err)
	}
	identity, _, _, err := ClassifyIdentity(canonical)
	if err != nil {
		return BoundWorktree{}, err
	}
	if identity != Worktree {
		return BoundWorktree{}, fmt.Errorf("worktree binding target is %s, not worktree", identity)
	}
	return BoundWorktree{path: canonical}, nil
}

// bindWorktree binds the acquired worktree under the launch intent's one-time
// worktree reservation fence. When the aggregate already holds the binding
// (recovery), the exact identity is verified (intent fence) and the bound path
// adopted; a mismatch fails closed. Binding an already-bound generation is a
// canonical conflict, so a stale or reused intent never double-binds.
func (r *Runner) bindWorktree() (BoundWorktree, error) {
	if r.args.Authority == nil {
		return BoundWorktree{}, fmt.Errorf("binding worktree before endpoint launch: task authority is not composed for spawn")
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return BoundWorktree{}, fmt.Errorf("binding worktree before endpoint launch: %w", err)
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return BoundWorktree{}, fmt.Errorf("binding worktree before endpoint launch: %w", err)
	}
	prec := domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	if agg.Worktree != nil {
		if r.launch != nil && (agg.Worktree.LeaseID != r.launch.WorktreeReservationID || agg.Worktree.FenceToken != r.launch.WorktreeFenceToken) {
			return BoundWorktree{}, fmt.Errorf("binding worktree before endpoint launch: committed worktree binding does not match the launch worktree reservation fence; refuse")
		}
		// The adopted path is re-classified, not trusted. The fence proves the
		// binding belongs to THIS launch intent; it proves nothing about what
		// the path is now. Between two launches the worktree can be removed
		// (`git worktree remove`) and the path replaced by a primary checkout
		// or an ordinary directory, and this is the only classification the
		// recovery path ever gets before PersistLaunchFiles writes into it.
		bw, err := newBoundWorktree(agg.Worktree.Path)
		if err != nil {
			return BoundWorktree{}, fmt.Errorf("binding worktree before endpoint launch: adopting committed binding: %w", err)
		}
		r.wtPath = bw.Path()
		return bw, nil
	}
	var leaseID, fenceToken string
	if r.launch != nil {
		leaseID, fenceToken = r.launch.WorktreeReservationID, r.launch.WorktreeFenceToken
	} else {
		leaseID, fenceToken = newEndpointToken(), newEndpointToken()
	}
	binding, err := buildTaskWorktreeBinding(r.projPath, r.wtPath, leaseID, fenceToken)
	if err != nil {
		return BoundWorktree{}, fmt.Errorf("binding worktree before endpoint launch: %w", err)
	}
	req := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       r.args.Authority.HomeID(),
		TaskID:       taskID,
		Precondition: prec,
		Binding:      binding,
		Reason:       "spawn",
	}
	op, err := r.spawnOperation("bindwt", agg.Generation, req)
	if err != nil {
		return BoundWorktree{}, fmt.Errorf("binding worktree before endpoint launch: %w", err)
	}
	if _, err := r.args.Authority.BindWorktree(op, req); err != nil {
		return BoundWorktree{}, fmt.Errorf("binding worktree before endpoint launch: %w", err)
	}
	// buildTaskWorktreeBinding admitted only Worktree for binding.Path, so the
	// committed path carries the same proof the recovery branch re-derives.
	return BoundWorktree{path: binding.Path}, nil
}

// spawnOperation builds the deterministic Operation for one launch phase
// (same Operation ID and intent digest on retry, so the canonical surface
// replays the durable outcome instead of duplicating it).
func (r *Runner) spawnOperation(verb string, gen taskauthority.Generation, intent domain.Intent) (domain.Operation, error) {
	opID, err := domain.NewOperationID(fmt.Sprintf("spawn-%s-%s-%d", verb, r.args.ID, uint64(gen)))
	if err != nil {
		return domain.Operation{}, err
	}
	return domain.NewOperation(opID, intent)
}

func buildTaskWorktreeBinding(primaryPath, worktreePath, leaseID, fenceToken string) (taskauthority.WorktreeBinding, error) {
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
		LeaseID:            leaseID,
		FenceToken:         fenceToken,
		BoundAtUnix:        time.Now().Unix(),
	}, nil
}

// Phase 9: resolveHarness resolves the soldier harness.
// Precedence: already-resolved (preflight) > project config snapshot >
// --harness flag > dispatch profile match on brief > snapshot-only fail-closed
// resolution (ResolveSoldierFromSnapshot). There is no flat-file or Detect
// fallback: when the snapshot carries no soldier harness identity, resolution
// fails closed with ErrNoSoldierHarnessInSnapshot.
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
	h, err := harness.ResolveSoldierFromSnapshot(r.projectConfig.Frozen.Config())
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

// Phase 11: createSession creates a session window for the launch. It is
// re-entrant under the committed launch intent: recovery re-adopts the
// durably recorded acquired endpoint (never a replacement), and creation is
// reservation-aware from the FIRST attempt — the mandatory
// CreateReserved contract consumes the intent's exact endpoint reservation
// fence and find-or-creates the same endpoint, so a crash between create and
// durable attach is recovered instead of silently replaced.
func (r *Runner) createSession() error {
	if r.endpoints == nil {
		return fmt.Errorf("spawn endpoint capabilities are required")
	}
	// Recovery: adopt the durably recorded acquired endpoint.
	if acquired := r.recordedAcquiredEndpoint(); acquired != nil {
		if r.launch != nil && acquired.Backend != r.launch.Backend {
			return fmt.Errorf("recorded acquired endpoint backend %q does not match launch intent backend %q; refuse recovery", acquired.Backend, r.launch.Backend)
		}
		ep := CreatedEndpoint{
			Backend:      acquired.Backend,
			Handle:       acquired.Handle,
			SessionOwner: acquired.SessionOwner,
			WorkspaceID:  acquired.WorkspaceID,
			TabID:        acquired.TabID,
			Incarnation:  acquired.Incarnation,
		}
		r.incarnation = acquired.Incarnation
		status, err := r.endpoints.Probe(ep)
		if err != nil {
			return fmt.Errorf("verifying recorded pane %q on backend %q: %w", ep.Handle, ep.Backend, err)
		}
		authorized := r.authorizeEndpoint(status, ep)
		// Recovery only re-adopts a confirmed-live, authorized endpoint. The
		// durable AcquiredEndpoint IS the explicit acquisition receipt (tied
		// to the incarnation); without it — or with an ambiguous/unknown/
		// unresponsive/dead reading — recovery fails closed (no replacement
		// and no dispose) and ownership is preserved.
		if !authorized.Live() {
			return fmt.Errorf("recorded pane %q observation %s on backend %q; recovery fails closed (no replacement)", ep.Handle, authorized.State(), ep.Backend)
		}
		r.endpoint, r.windowID = ep, ep.Handle
		return nil
	}
	// The backend identity is the launch intent's explicit snapshot Backend
	// (fleet.ResolveProjectSnapshot → config.ResolveProject); the raw --backend
	// flag enters ONLY via the boundary override and is never consumed here.
	backendName := ""
	if r.launch != nil {
		backendName = r.launch.Backend
	}
	if backendName == "" {
		backendName = r.projectConfig.Frozen.Config().Backend
	}
	tabName := r.windowLabel
	if tabName == "" {
		tabName = soldierTabLabel(r.args.ProjectName, r.args.ID)
	}
	req := CreateRequest{
		Home:             r.homeDir,
		PreferredBackend: backendName,
		TabName:          tabName,
		Cwd:              r.wtPath,
		ReservationID:    r.epReservationID(),
		FenceToken:       r.epFenceToken(),
	}
	// The mandatory reservation-aware create contract find-or-creates the
	// endpoint under the exact reservation on EVERY call (first attempt AND
	// recovery): the same reservation returns the same endpoint, so a crash
	// between create and durable attach is recovered, never replaced.
	ep, err := r.endpoints.CreateReserved(req)
	if err != nil {
		return err
	}
	// The opaque incarnation is resolved (minted or reused) in beginLaunchIntent
	// before any acquisition; propagate it onto the created endpoint for the
	// attach/bind records and freshness authorization.
	ep.Incarnation = r.incarnation
	status, err := r.endpoints.Probe(ep)
	// A created endpoint is owned by this launch reservation. Alive/starting
	// (raw lifecycle) may proceed to durable attach; readiness is confirmed
	// after submit by verifyEndpointReadyBeforePersist (Fleet-authorized). Any
	// dead or ambiguous (unknown/unresponsive/stale) reading returns
	// readiness-pending WITHOUT disposing — the reservation and ownership are
	// preserved and no replacement is fabricated (BEO-16).
	if err != nil {
		return fmt.Errorf("verifying created pane %q on backend %q: %w", ep.Handle, ep.Backend, err)
	}
	if status.Lifecycle != LifecycleAlive && status.Lifecycle != LifecycleStarting {
		return fmt.Errorf("created pane %q observation %s on backend %q; readiness pending (no dispose, reservation preserved)", ep.Handle, status.State(), ep.Backend)
	}
	r.endpoint, r.windowID = ep, ep.Handle
	return nil
}

// attachEndpoint durably records the exact acquired endpoint identity
// (AttachEndpoint) under the launch intent's endpoint reservation fence,
// BEFORE any process submission. It is exact-generation/idempotent: recovery
// verifies the recorded identity matches the created endpoint and skips; a
// different recorded endpoint fails closed and can never be overwritten.
func (r *Runner) attachEndpoint() error {
	if r.args.Authority == nil {
		return fmt.Errorf("attaching acquired endpoint: task authority is not composed for spawn")
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return fmt.Errorf("attaching acquired endpoint: %w", err)
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return fmt.Errorf("attaching acquired endpoint: %w", err)
	}
	prec := domain.Of(uint64(agg.Generation), uint64(agg.Revision))
	if agg.AcquiredEndpoint != nil {
		a := *agg.AcquiredEndpoint
		if a.Backend != r.endpoint.Backend || a.Handle != r.endpoint.Handle ||
			a.SessionOwner != r.endpoint.SessionOwner || a.WorkspaceID != r.endpoint.WorkspaceID || a.TabID != r.endpoint.TabID || a.Incarnation != r.endpoint.Incarnation {
			return fmt.Errorf("attaching acquired endpoint: recorded acquired endpoint %s/%s does not match created endpoint %s/%s; refuse", a.Backend, a.Handle, r.endpoint.Backend, r.endpoint.Handle)
		}
		if r.launch != nil && (a.LeaseID != r.launch.EndpointReservationID || a.FenceToken != r.launch.EndpointFenceToken) {
			return fmt.Errorf("attaching acquired endpoint: recorded acquired endpoint does not match the launch endpoint reservation fence; refuse")
		}
		return nil
	}
	req := taskauthority.CanonicalAttachEndpointRequest{
		HomeID:       r.args.Authority.HomeID(),
		TaskID:       taskID,
		Precondition: prec,
		Backend:      r.endpoint.Backend,
		Handle:       r.endpoint.Handle,
		LeaseID:      r.epReservationID(),
		FenceToken:   r.epFenceToken(),
		SessionOwner: r.endpoint.SessionOwner,
		WorkspaceID:  r.endpoint.WorkspaceID,
		TabID:        r.endpoint.TabID,
		Incarnation:  r.endpoint.Incarnation,
		Reason:       "spawn",
	}
	op, err := r.spawnOperation("attach", agg.Generation, req)
	if err != nil {
		return fmt.Errorf("attaching acquired endpoint: %w", err)
	}
	if _, err := r.args.Authority.AttachEndpoint(op, req); err != nil {
		return fmt.Errorf("attaching acquired endpoint: %w", err)
	}
	return nil
}

// endpointDurablyAttached reports whether the aggregate already records the
// acquired endpoint (or the active endpoint binding) for the task's current
// generation. When no Authority is composed nothing is durably recorded.
func (r *Runner) endpointDurablyAttached() bool {
	if r.args.Authority == nil {
		return false
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return false
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return false
	}
	return agg.AcquiredEndpoint != nil || agg.Endpoint != nil
}

// recordedAcquiredEndpoint returns the aggregate's recorded acquired endpoint
// for the task's current generation, or nil.
func (r *Runner) recordedAcquiredEndpoint() *taskauthority.AcquiredEndpoint {
	if r.args.Authority == nil {
		return nil
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return nil
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return nil
	}
	if agg.AcquiredEndpoint == nil {
		return nil
	}
	cp := *agg.AcquiredEndpoint
	return &cp
}

// Phase 11a: buildSoldierPrompt builds the complete Soldier launch prompt,
// runs fail-closed validation, and persists durable files to the worktree.
// Must be called BEFORE createSession so that fail-closed checks happen before
// any session allocation. It writes into the BoundWorktree it is given, which
// only bindWorktree can produce — running before that phase is a compile
// error, not a lost check.
func (r *Runner) buildSoldierPrompt(bound BoundWorktree) error {
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
		TaskID:                 r.args.ID,
		TaskKind:               r.args.Kind,
		DeliveryMode:           r.effectiveMode,
		Repository:             r.args.ProjectName,
		ParentCaptainID:        parentCaptainID,
		ParentHome:             r.homeDir,
		WorktreePath:           bound.Path(),
		HomeDir:                r.homeDir,
		BriefContent:           briefData,
		RequiredSkills:         requiredSkills,
		OptionalSkills:         optionalSkills,
		HarnessName:            r.harness,
		ScoutScope:             r.taskScoutScope,
		ScoutRuntimeBudgetSecs: r.taskScoutBudget,
	}

	// Modes that do not hard-gate on skill presence still report what is absent,
	// so a later in-task failure is traceable to the launch host.
	if !requiredSkillsAreHardGate(input.TaskKind, input.DeliveryMode) {
		for _, name := range missingRequiredSkillBinaries(input.RequiredSkills) {
			fmt.Fprintf(os.Stderr, "skill diagnostic: required skill %q not on PATH (not blocking for kind=%s mode=%s)\n", name, input.TaskKind, input.DeliveryMode)
		}
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
	if err := PersistLaunchFiles(bound.Path(), charter, briefData, env, promptText); err != nil {
		return fmt.Errorf("persisting soldier launch files: %w", err)
	}

	// Build launch arguments with the complete prompt, passing model and effort.
	bin, args, err := BuildLaunchArgs(bound.Path(), r.harness, r.model, r.effort, promptText)
	if err != nil {
		return fmt.Errorf("building soldier launch arguments: %w", err)
	}
	r.launchBin = bin
	r.launchArgs = args

	return nil
}

// shipRequiredSkill is the CLI a ship task cannot complete without: every
// PR-producing delivery mode routes its GitHub work through it, and both the
// charter and the brief instruct the Soldier to use it.
const shipRequiredSkill = "gh-axi"

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
		{Name: "stuck-soldier-recovery", Role: "captain"},
		{Name: "no-mistakes", Role: "captain"},
		{Name: "bootstrap-diagnostics", Role: "general"},
		{Name: "harness-adapters", Role: "captain"},
	}

	// Determine required skills from task kind and delivery mode.
	// ship tasks always get shipRequiredSkill; scout tasks may not need GitHub.
	var requiredNames []string
	var optionalNames []string

	switch r.args.Kind {
	case "scout":
		requiredNames = []string{"qmd"}
		optionalNames = []string{"gh-axi"}
	default:
		// ship tasks: github required.
		requiredNames = []string{shipRequiredSkill}
		optionalNames = []string{"qmd", "chrome-devtools-axi"}
	}

	// Apply no-mistakes mode policy: no-mistakes requires shipRequiredSkill always.
	if r.effectiveMode == "no-mistakes" {
		requiredNames = append(requiredNames, shipRequiredSkill)
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

// Phase 12: submitLaunch builds the deterministic launch artifact, submits
// the exact command, and ONLY AFTER the submission succeeds durably records
// the launch evidence (RecordLaunch). A Submit error is NOT recorded as
// success: no LaunchEvidence is committed, so recovery may re-submit the same
// command under the same identity. The artifact's persistent guard makes the
// re-submission re-entrant — a second submission can never start a second
// Soldier process (same launch identity exits/no-ops; a different identity
// fails closed), and when the guard exists but readiness cannot be proven,
// recovery fails closed instead of launching another process.
func (r *Runner) submitLaunch() error {
	if r.args.Authority == nil {
		return fmt.Errorf("submitting launch: task authority is not composed for spawn")
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return fmt.Errorf("submitting launch: %w", err)
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return fmt.Errorf("submitting launch: %w", err)
	}
	snapshotDigest := ""
	if r.projectConfigLoaded {
		snapshotDigest = r.projectConfig.SnapshotDigest
	}
	artifact, err := buildLaunchArtifact(LaunchArtifactInput{
		WorktreePath:   r.wtPath,
		HomeDir:        r.homeDir,
		TaskID:         r.args.ID,
		SnapshotDigest: snapshotDigest,
		LaunchBin:      r.launchBin,
		LaunchArgs:     r.launchArgs,
		LaunchID:       r.launchID,
		Generation:     agg.Generation.String(),
		EndpointFence:  r.epFenceToken(),
	})
	if err != nil {
		return fmt.Errorf("submitting launch: %w", err)
	}
	r.launchCommand = artifact.Command
	r.launchCommandDigest = artifact.CommandDigest
	if agg.LaunchEvidence != nil {
		// A prior submission already succeeded and was durably recorded:
		// verify the exact launch identity AND command digest and skip (never
		// re-submit under the same launch identity; a changed digest is a
		// different submission and fails closed).
		if agg.LaunchEvidence.LaunchID != r.launchID || agg.LaunchEvidence.CommandDigest != r.launchCommandDigest {
			return fmt.Errorf("submitting launch: recorded launch evidence %s/%s does not match this launch %s/%s; refuse", agg.LaunchEvidence.LaunchID, agg.LaunchEvidence.CommandDigest, r.launchID, r.launchCommandDigest)
		}
		return nil
	}
	// Submit first. A submission error means the launch may not have started;
	// NO evidence is recorded, so recovery re-submits the same command under
	// the same identity (the artifact guard bounds the process count).
	if err := r.endpoints.Submit(r.endpoint, artifact.Command); err != nil {
		return fmt.Errorf("submitting launch: %w (no launch evidence recorded; recovery may re-submit the same command)", err)
	}
	// Only a successful submission is recorded as launch evidence.
	req := taskauthority.CanonicalRecordLaunchRequest{
		HomeID:        r.args.Authority.HomeID(),
		TaskID:        taskID,
		Precondition:  domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		LaunchID:      r.launchID,
		CommandDigest: r.launchCommandDigest,
		Reason:        "spawn",
	}
	op, err := r.spawnOperation("record", agg.Generation, req)
	if err != nil {
		return fmt.Errorf("submitting launch: %w", err)
	}
	if _, err := r.args.Authority.RecordLaunch(op, req); err != nil {
		// The process launched but the evidence could not be recorded. The
		// artifact guard prevents a second process on recovery; the re-entry
		// re-submits the same command and the guard no-ops.
		return fmt.Errorf("submitting launch: recording launch evidence: %w", err)
	}
	return nil
}

// Phase 13b: writeLaunchManifest writes the digest manifest after all launch
// artifacts exist. The manifest is written after submitLaunch creates the
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
// For harnesses that reached the launch submission, the prompt is in context;
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
		// Phase-aware disposal: the endpoint is durably recorded by the time
		// the readiness wait runs, so it is never disposed here; recovery
		// reuses the recorded endpoint and fails closed instead of replacing it.
		if !r.endpointDurablyAttached() {
			_ = r.endpoints.Dispose(r.endpoint)
		}
		return fmt.Errorf("harness %q handshake failed: %w", r.harness, err)
	}
	// No brief injection needed — the complete prompt was already provided
	// as a launch argument via submitLaunch / BuildLaunchArgs.
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
			gen, rev := r.canonicalGenerationRevision()
			// Negative authorization: only an authorized exact absence (narrow
			// dead + current generation/revision) means the window died.
			// Ambiguous readings are never "died" (BEO-16: unknown != dead).
			if dead := authorizeAbsence(status, exactEndpointProof{
				backend: r.endpoint.Backend, handle: r.endpoint.Handle, incarnation: r.endpoint.Incarnation,
				leaseID: r.epReservationID(), fenceToken: r.epFenceToken(),
				generation: gen, revision: rev,
			}); dead.Absent() {
				return fmt.Errorf("window died while waiting for ready")
			}
			// Positive readiness progression uses the raw lifecycle of the
			// fresh acquisition (alive/starting proceed); Live() freshness is
			// asserted by verifyEndpointReadyBeforePersist with the explicit
			// acquisition evidence.
			if status.Lifecycle != LifecycleAlive && status.Lifecycle != LifecycleStarting {
				return fmt.Errorf("endpoint observation %s while waiting for ready", status.State())
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
	if err != nil {
		return fmt.Errorf("verifying created pane %q on backend %q: %w", r.windowID, r.endpoint.Backend, err)
	}
	// Positive readiness gate: Live() requires explicit acquisition evidence
	// (the in-process creation receipt) plus a complete proof revalidated
	// under the current generation/revision.
	auth := r.authorizeEndpoint(status, r.endpoint)
	if !auth.Live() {
		// Never dispose on an ambiguous/unknown/starting/stale reading: the
		// endpoint is owned by this launch reservation, so readiness is
		// pending and ownership is preserved (no replacement, no dispose).
		return fmt.Errorf("created pane %q observation %s on backend %q before persisting state; readiness pending (no dispose)", r.windowID, auth.State(), r.endpoint.Backend)
	}
	return nil
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

// confirmSpawn persists the endpoint binding and the queued → working
// transition as one atomic Task Authority operation after the endpoint is
// verified ready and the task meta projection is written. The endpoint
// binding is an aggregate field, so it commits through the existing journal
// with the transition, the Revision advance, the typed audit event, and the
// durable idempotency receipt. A failed persistence leaves the task queued
// with no endpoint binding. The Runner fails closed when no Authority is
// composed, exactly like bindWorktree. The returned Result is the
// authoritative outcome of the spawn (the ConfirmSpawn receipt): its
// Generation is the exact generation the attestation evidence binds to
// (Task 7.3).
// confirmSpawn is the FINAL canonical bind: BindEndpoint commits the exact
// recorded endpoint identity, the launch intent's endpoint reservation fence,
// and the recorded launch evidence in ONE atomic operation that transitions
// queued → working. Only this operation transitions the phase; it requires the
// bound worktree, the recorded acquired endpoint (AttachEndpoint), the
// recorded launch evidence (RecordLaunch), and the exact intent fence when a
// launch intent exists. Recovery after the final bind (task already working
// under the identical launch) replays idempotently. The returned Outcome is
// the authoritative spawn receipt: its Generation is the exact generation the
// attestation evidence binds to.
func (r *Runner) confirmSpawn() (taskauthority.Outcome, error) {
	if r.args.Authority == nil {
		return taskauthority.Outcome{}, fmt.Errorf("confirming spawn: task authority is not composed for spawn")
	}
	taskID, err := domain.NewTaskID(r.args.ID)
	if err != nil {
		return taskauthority.Outcome{}, fmt.Errorf("confirming spawn: %w", err)
	}
	agg, err := r.args.Authority.Get(taskID)
	if err != nil {
		return taskauthority.Outcome{}, fmt.Errorf("confirming spawn: %w", err)
	}
	if agg.Phase == taskauthority.PhaseWorking || agg.Endpoint != nil {
		if r.launch != nil && (agg.Endpoint == nil || agg.Endpoint.LeaseID != r.launch.EndpointReservationID || agg.Endpoint.FenceToken != r.launch.EndpointFenceToken) {
			return taskauthority.Outcome{}, fmt.Errorf("confirming spawn: committed endpoint binding does not match the launch endpoint reservation fence; refuse")
		}
		return taskauthority.Outcome{TaskID: taskID, Generation: agg.Generation, Revision: agg.Revision, Phase: agg.Phase, Replayed: true}, nil
	}
	leaseID, fenceToken := r.epReservationID(), r.epFenceToken()
	if r.launch == nil {
		leaseID, fenceToken = newEndpointToken(), newEndpointToken()
	}
	binding := taskauthority.EndpointBinding{
		Backend:      r.endpoint.Backend,
		Handle:       r.endpoint.Handle,
		LeaseID:      leaseID,
		FenceToken:   fenceToken,
		SessionOwner: r.endpoint.SessionOwner,
		WorkspaceID:  r.endpoint.WorkspaceID,
		TabID:        r.endpoint.TabID,
		Incarnation:  r.endpoint.Incarnation,
		BoundAtUnix:  time.Now().Unix(),
	}
	req := taskauthority.CanonicalBindEndpointRequest{
		HomeID:       r.args.Authority.HomeID(),
		TaskID:       taskID,
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding:      binding,
		Reason:       "spawned",
	}
	op, err := r.spawnOperation("confirm", agg.Generation, req)
	if err != nil {
		return taskauthority.Outcome{}, fmt.Errorf("confirming spawn: %w", err)
	}
	out, err := r.args.Authority.BindEndpoint(op, req)
	if err != nil {
		return taskauthority.Outcome{}, fmt.Errorf("confirming spawn: %w", err)
	}
	return out, nil
}

// epReservationID and epFenceToken return the launch intent's one-time
// endpoint reservation identities (empty when no intent is committed).
func (r *Runner) epReservationID() string {
	if r.launch != nil {
		return r.launch.EndpointReservationID
	}
	return ""
}

func (r *Runner) epFenceToken() string {
	if r.launch != nil {
		return r.launch.EndpointFenceToken
	}
	return ""
}

func newEndpointToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", buf)
}

// Phase 14: writeTaskMeta writes the task metadata file. The side file is a
// pre-transition runtime observation projection (Task 4.2, Task 7.8
// adjudication): it carries the runtime-only fields that describe the live
// soldier session (window, worktree, harness, backend, mode, yolo, model,
// effort, config digest, launch manifest anchor, endpoint metadata) and never
// acts as a writer of record for authoritative Task Aggregate fields (kind,
// project, description, owner, generation, state). Existing projection
// fields are preserved; runtime fields are overlaid. Authoritative fields
// are reconciled from the canonical aggregate by the projection layer.
func (r *Runner) writeTaskMeta() error {
	yoloVal := "off"
	if r.args.Yolo {
		yoloVal = "on"
	}
	meta, err := home.ReadMeta(r.homeDir, r.args.ID)
	if err != nil {
		meta = make(map[string]string)
	}
	put := func(k, v string) {
		if v != "" {
			meta[k] = v
		}
	}
	put("window", r.windowID)
	put("worktree", r.wtPath)
	put("projpath", r.projPath)
	put("harness", r.harness)
	put("backend", r.endpoint.Backend)
	put("mode", r.effectiveMode)
	put("yolo", yoloVal)
	if r.model != "" {
		put("model", r.model)
	}
	if r.effort != "" {
		put("effort", r.effort)
	}
	if r.projectConfigLoaded {
		put("config_snapshot_digest", r.projectConfig.SnapshotDigest)
	}
	if r.manifestSHA256 != "" {
		put("launch_manifest_sha256", r.manifestSHA256)
	}

	for k, v := range r.endpoint.Metadata {
		put(k, v)
	}

	// The capability attestation fields (capability_attestation,
	// attestation_requested_mode, attestation_effective_mode,
	// attestation_fallback_reason) are NOT written here: this pre-transition
	// side file is runtime observations only (Task 4.2). The accepted
	// attestation becomes authoritative evidence through the Task Authority
	// after ConfirmSpawn (Task 7.3); the .meta fields are a post-confirm
	// runtime projection of that authoritative acceptance written by
	// projectAttestationEvidence, never a writer of record.

	if err := home.WriteMeta(r.homeDir, r.args.ID, meta); err != nil {
		return fmt.Errorf("writing task meta: %w", err)
	}
	return nil
}

// attachAttestation commits the accepted capability attestation as
// authoritative evidence through the composed Task Authority after
// ConfirmSpawn, then projects the acceptance into .meta. The expected
// generation comes from the ConfirmSpawn receipt, so the evidence binds the
// exact generation the spawn committed. The task is already working when
// this runs: a failure keeps the observation runtime-only and is surfaced by
// the caller as a warning, never rolling back the authoritative spawn.
func (r *Runner) attachAttestation(generation taskauthority.Generation) error {
	if r.attestation == nil {
		return nil
	}
	if r.args.Authority == nil {
		return fmt.Errorf("attaching attestation evidence: task authority is not composed for spawn")
	}
	// The accepted capability attestation is a runtime observation projected
	// into .meta on the exact confirmed generation; the canonical surface owns
	// no attestation primitive (the legacy delivery-plan/attestation aggregate
	// evidence did not survive the canonical cutover). A malformed reference
	// fails closed before the projection.
	if err := validateAttestationReference(r.attestation); err != nil {
		return fmt.Errorf("attaching attestation evidence: %w", err)
	}
	if err := projectAttestationEvidence(r.homeDir, r.args.ID, r.attestation, generation); err != nil {
		return err
	}
	return nil
}

// validateAttestationReference checks the acceptance shape of the capability
// attestation: the project and home bindings must be present so the projected
// observation is never a malformed reference.
func validateAttestationReference(att *CapabilityAttestation) error {
	if att == nil {
		return fmt.Errorf("attestation acceptance requires a capability attestation")
	}
	if strings.TrimSpace(att.Project) == "" {
		return fmt.Errorf("attestation acceptance requires a project binding")
	}
	if strings.TrimSpace(att.Home) == "" {
		return fmt.Errorf("attestation acceptance requires a home binding")
	}
	return nil
}

// AttestationProjectionError is the typed partial outcome of an attestation
// acceptance whose .meta projection could not be written. The authoritative
// state is never rolled back; the projection can be retried independently and
// replays idempotently.
type AttestationProjectionError struct {
	TaskID        string
	ProjectionErr error
}

func (e *AttestationProjectionError) Error() string {
	return fmt.Sprintf("attestation evidence committed for %s but projection failed: %v", e.TaskID, e.ProjectionErr)
}

func (e *AttestationProjectionError) Unwrap() error { return e.ProjectionErr }

// projectAttestationEvidence writes the .meta attestation fields as a runtime
// projection of the accepted capability attestation bound to the exact
// confirmed generation. The projection is one-directional: it mirrors the
// accepted observation and never writes into the Authority. A projection
// failure returns a typed partial error and never rolls back the
// authoritative spawn; the projection is retryable without replaying any
// canonical operation.
func projectAttestationEvidence(homeDir, taskID string, att *CapabilityAttestation, generation taskauthority.Generation) error {
	meta, err := home.ReadMeta(homeDir, taskID)
	if err != nil {
		meta = make(map[string]string)
	}
	data, err := json.Marshal(att)
	if err != nil {
		return &AttestationProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	meta[MetaCapabilityAttestation] = string(data)
	meta[MetaRequestedMode] = att.RequestedMode
	meta[MetaEffectiveMode] = att.EffectiveMode
	if att.FallbackReason != "" {
		meta[MetaFallbackReason] = att.FallbackReason
	}
	meta["attestation_generation"] = generation.String()
	if err := home.WriteMeta(homeDir, taskID, meta); err != nil {
		return &AttestationProjectionError{TaskID: taskID, ProjectionErr: err}
	}
	return nil
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
