package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/brief"
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

// Phase 9: resolveHarness resolves the crewmate harness.
func (r *Runner) resolveHarness() error {
	if r.args.HarnessFlag != "" {
		if err := harness.ValidateHarness(r.args.HarnessFlag); err != nil {
			return fmt.Errorf("--harness: %w", err)
		}
		r.harness = r.args.HarnessFlag
		return nil
	}
	h, err := harness.Crew(r.homeDir)
	if err != nil {
		return fmt.Errorf("resolving harness: %w", err)
	}
	r.harness = h
	return nil
}

// Phase 10: resolveLaunchConfig resolves model, effort, and launch command
// from the harness adapter template.
func (r *Runner) resolveLaunchConfig() {
	adapter, ok := harness.GetAdapter(r.harness)
	if !ok {
		return
	}
	tmpl := adapter.LaunchTemplate
	if tmpl.DefaultModel != "" {
		r.model = tmpl.DefaultModel
	}
	if tmpl.DefaultEffort != "" {
		r.effort = tmpl.DefaultEffort
	}
	r.launchCmd = harness.LaunchString(r.harness, tmpl)
}

// Phase 11: createSession creates a session window for the crewmate.
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

	windowID, err := bk.NewWindow(hometag.Tag(r.homeDir), r.args.ID)
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
	launchScript := filepath.Join(r.wtPath, ".crew-launch.sh")
	scriptContent := "#!/usr/bin/env bash\nset -e\nexport MUNSU_HOME=" + fmt.Sprintf("%q", r.homeDir) + "\n" + r.launchCmd + "\n"
	if writeErr := os.WriteFile(launchScript, []byte(scriptContent), 0755); writeErr != nil {
		fmt.Fprintf(os.Stderr, "warning: writing launch script: %v\n", writeErr)
	}
	fullCmd := fmt.Sprintf("cd %s && bash .crew-launch.sh", r.wtPath)
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
	briefWorktreePath := filepath.Join(r.wtPath, ".crew-brief.md")
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
	// Inject brief: all harnesses use file-based .crew-brief.md.
	_ = r.bk.SendKeys(r.windowID, "read and execute .crew-brief.md")
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
	fmt.Printf("Spawned crewmate %s\n", r.args.ID)
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
