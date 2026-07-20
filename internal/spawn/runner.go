package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/captain"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/hometag"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/scope"
	"github.com/minhtri2710/munsu/internal/session"
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
	bk            session.Backend
	bkName        string
	briefData     []byte
	windowID      string
}

// NewRunner creates a Runner for the given Args.
func NewRunner(args Args) *Runner {
	return &Runner{args: args}
}

// Run executes the full spawn orchestration sequence.
func (r *Runner) Run() (string, error) {
	if err := r.resolveHome(); err != nil {
		return "", err
	}
	if err := r.checkSpawnAuthority(); err != nil {
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
	r.warnMissingBacklog()
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
	if err := r.createSession(); err != nil {
		return "", err
	}
	r.bootstrapWindow()
	r.writeBriefToWorktree()
	if err := r.waitAndInjectBrief(); err != nil {
		return "", err
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
	if endpointKind, found, err := currentEndpointKind(r.homeDir); err != nil {
		return fmt.Errorf("spawn authority: resolving current endpoint: %w", err)
	} else if found {
		if endpointKind == "captain" {
			return authorizeSpawn("captain", r.homeDir, cwd)
		}
		return fmt.Errorf("spawn authority: managed soldier endpoints cannot spawn; delegate to the general or a captain")
	}
	return authorizeSpawn(os.Getenv("MUNSU_ROLE"), r.homeDir, cwd)
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

// Phase 5: warnMissingBacklog warns if tasks-axi is available but no backlog row exists.
func (r *Runner) warnMissingBacklog() {
	if _, err := exec.LookPath("tasks-axi"); err != nil {
		return
	}
	chk := exec.Command("tasks-axi", "show", r.args.ID)
	if out, err := chk.CombinedOutput(); err != nil || strings.Contains(string(out), "not found") {
		fmt.Fprintf(os.Stderr, "warning: task %s has no backlog row; register it with 'backlog add %s \"<description>\" --kind %s' to track lifecycle\n",
			r.args.ID, r.args.ID, r.args.Kind)
	}
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

// Phase 9: resolveHarness resolves the soldier harness.
// Precedence: --harness flag > dispatch profile match on brief > Soldier() chain.
// When soldier-dispatch.json is active and no --harness is set, prefer
// ResolveDispatchSelection over the bare DefaultHarness shortcut in Soldier().
func (r *Runner) resolveHarness() error {
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
	var bk session.Backend
	var bkName string
	if r.args.Session != nil {
		bk = r.args.Session
		bkName = "test"
	} else {
		var err error
		bk, bkName, err = session.Resolve(r.homeDir, r.args.Backend)
		if err != nil {
			return err
		}
	}

	// If herdr backend, set Cwd so NewWindow can pass --cwd.
	if hb, ok := bk.(*session.HerdrBackend); ok && r.wtPath != "" {
		hb.Cwd = r.wtPath
	}

	windowID, err := bk.NewWindow(hometag.WorkspaceTag(r.homeDir), soldierTabLabel(r.args.ProjectName, r.args.ID))
	if err != nil {
		return fmt.Errorf("backend %q not available: %w. Configure via --backend flag, config/backend file, or HERDR_ENV env", bkName, err)
	}
	r.bk = bk
	r.bkName = bkName
	r.windowID = windowID
	return nil
}

// Phase 12: bootstrapWindow writes a launch script and sends it to the session.
func (r *Runner) bootstrapWindow() {
	if r.launchCmd == "" {
		return
	}
	launchScript := filepath.Join(r.wtPath, ".soldier-launch.sh")
	scriptContent := "#!/usr/bin/env bash\nset -e\nexport MUNSU_HOME=" + fmt.Sprintf("%q", r.homeDir) + "\nexport MUNSU_ROLE=soldier\nexport MUNSU_TASK_ID=" + fmt.Sprintf("%q", r.args.ID) + "\nexport MUNSU_PARENT_STATUS=" + fmt.Sprintf("%q", r.homeDir) + "\n" + r.launchCmd + "\n"
	if writeErr := os.WriteFile(launchScript, []byte(scriptContent), 0755); writeErr != nil {
		fmt.Fprintf(os.Stderr, "warning: writing launch script: %v\n", writeErr)
	}
	fullCmd := fmt.Sprintf("cd %s && bash .soldier-launch.sh", r.wtPath)
	if sendErr := r.bk.SendKeys(r.windowID, fullCmd); sendErr != nil {
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

// Phase 13b: waitAndInjectBrief waits for the harness to be ready, then
// injects the brief prompt.
func (r *Runner) waitAndInjectBrief() error {
	if len(r.briefData) == 0 {
		return nil
	}
	if err := r.waitForHarnessReady(60); err != nil {
		capture, _ := r.bk.Capture(r.windowID, 60)
		_ = task.AppendStatus(r.homeDir, r.args.ID, "failed: harness not ready")
		dataDir := filepath.Join(r.homeDir, "data", r.args.ID)
		_ = os.MkdirAll(dataDir, 0755)
		failContent := fmt.Sprintf("harness=%s\nerror=%v\n\nlast capture:\n%s\n", r.harness, err, capture)
		_ = os.WriteFile(filepath.Join(dataDir, "ready-fail.txt"), []byte(failContent), 0644)
		_ = r.bk.Teardown(r.windowID)
		return fmt.Errorf("harness %q not ready within timeout: %w", r.harness, err)
	}
	// Brief settle: let harness present clean prompt before one-liner.
	time.Sleep(500 * time.Millisecond)
	// Inject brief: all harnesses use file-based .soldier-brief.md.
	_ = r.bk.SendKeys(r.windowID, "read and execute .soldier-brief.md")
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
			capture, _ := r.bk.Capture(r.windowID, 60)
			return fmt.Errorf("harness not ready after %ds: last capture: %q", timeoutSec, capture)
		case <-ticker.C:
			if !r.bk.Alive(r.windowID) {
				return fmt.Errorf("window died while waiting for ready")
			}
			capture, err := r.bk.Capture(r.windowID, 60)
			if err != nil {
				continue
			}
			// Dialog handlers: auto-answer trust prompts before checking ready patterns.
			if !trustHandled && harness.IsTrustPrompt(capture, r.harness) {
				_ = r.bk.SendKeys(r.windowID, "")
				trustHandled = true
				continue
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
		"backend":  r.bkName,
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

	// Backend extras: write herdr_* fields when the backend provides them.
	if ex, ok := r.bk.(session.BackendMetaExtras); ok {
		for k, v := range ex.MetaExtras() {
			meta[k] = v
		}
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
