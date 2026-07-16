// Package spawn implements the crewmate spawn orchestration — the full
// sequence of resolving home, validating inputs, acquiring a worktree,
// launching the harness, and wiring the agent session.
package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/brief"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
	"github.com/minhtri2710/munsu/internal/worktree"
)

// Args holds all input parameters for spawning a crewmate.
type Args struct {
	ID          string
	ProjectName string
	Kind        string
	Mode        string // --mode flag value; empty=auto-detect
	ProjectMode string // project registry mode (raw, not defaulted); empty = resolve from registry
	Yolo        bool
	Backend     string          // --backend flag value; empty = auto-detect
	HarnessFlag string          // --harness flag value; empty = resolve from config
	HomeDir     string          // if empty, resolved via home.Resolve
	Session     session.Backend // injectable session backend; nil = resolve at runtime
	Arm         bool
	ArmFunc     func(homeDir string) error // injectable arm function; nil = no auto-arm
}

// ValidDeliveryModes lists the accepted delivery mode values.
var ValidDeliveryModes = map[string]bool{
	"no-mistakes": true,
	"direct-PR":   true,
	"local-only":  true,
}

// ValidateDeliveryMode returns an error if the mode is not a known value.
func ValidateDeliveryMode(mode string) error {
	if mode == "" {
		return nil // empty is allowed (will use registry default)
	}
	if !ValidDeliveryModes[mode] {
		return fmt.Errorf("invalid delivery mode %q: must be one of: no-mistakes, direct-PR, local-only", mode)
	}
	return nil
}

// noMistakesOnPath returns true if the no-mistakes binary is found on PATH.
func noMistakesOnPath() bool {
	_, err := exec.LookPath("no-mistakes")
	return err == nil
}

// EnsureDeliveryModeRunnable validates that an explicit non-empty mode is runnable.
// If mode is "no-mistakes" and the binary is not on PATH, returns a hard error
// with install guidance.
func EnsureDeliveryModeRunnable(mode string) error {
	if mode == "no-mistakes" && !noMistakesOnPath() {
		return fmt.Errorf("delivery mode 'no-mistakes' requires the no-mistakes binary on PATH; run 'munsu doctor' or 'go install github.com/minhtri2710/no-mistakes@latest'")
	}
	return nil
}

// ResolveDeliveryMode resolves the effective delivery mode following this precedence:
//  1. explicitMode — non-empty --mode flag value
//  2. projectMode — mode from project registry (if non-empty)
//  3. config/default-mode — optional config file under homeDir
//  4. Auto — no-mistakes on PATH → no-mistakes, else → direct-PR (with message)
//
// Rules:
//   - An explicit --mode=no-mistakes with missing binary is a hard error.
//   - An explicit direct-PR/local-only is OK even when no-mistakes binary exists.
//   - A registry/config/auto no-mistakes with missing binary falls through to direct-PR.
func ResolveDeliveryMode(homeDir string, explicitMode string, projectMode string) (string, error) {
	// 1. Explicit --mode flag
	if explicitMode != "" {
		if err := ValidateDeliveryMode(explicitMode); err != nil {
			return "", err
		}
		// Hard error if user explicitly asked for no-mistakes but binary is missing
		if err := EnsureDeliveryModeRunnable(explicitMode); err != nil {
			return "", err
		}
		return explicitMode, nil
	}

	// 2. Project registry mode
	if projectMode != "" {
		if err := ValidateDeliveryMode(projectMode); err != nil {
			return "", err
		}
		// If registry explicitly set no-mistakes and binary is missing → hard error
		if err := EnsureDeliveryModeRunnable(projectMode); err != nil {
			return "", err
		}
		return projectMode, nil
	}

	// 3. config/default-mode (optional)
	if homeDir != "" {
		cfg, err := config.Get(homeDir, "default-mode")
		if err == nil && cfg != "" {
			if err := ValidateDeliveryMode(cfg); err != nil {
				return "", err
			}
			if err := EnsureDeliveryModeRunnable(cfg); err != nil {
				return "", err
			}
			return cfg, nil
		}
	}

	// 4. Auto: no-mistakes on PATH → no-mistakes, else → direct-PR
	if noMistakesOnPath() {
		return "no-mistakes", nil
	}

	fmt.Fprintln(os.Stderr, "warning: no-mistakes not found on PATH; defaulting to direct-PR delivery mode. Install with: go install github.com/minhtri2710/no-mistakes@latest, or run 'munsu doctor'")
	return "direct-PR", nil
}

// effectiveModeForSpawn resolves the effective delivery mode for a spawn operation.
// It falls back to project.Mode when ProjectMode is not set in args.
func effectiveModeForSpawn(homeDir string, args Args) (string, error) {
	projectMode := args.ProjectMode
	if projectMode == "" {
		if m, _, err := project.Mode(homeDir, args.ProjectName); err == nil {
			projectMode = m
		}
	}
	return ResolveDeliveryMode(homeDir, args.Mode, projectMode)
}

// ReadyPatterns maps each harness to a set of substrings that indicate
// the agent is ready for input.
var ReadyPatterns = map[string][]string{
	harness.Pi:     {">", "Agent:", "What would you like", "checkpoint", "thinking off", "◆"},
	harness.Agy:    {"esc to cancel", "Ready for your prompt", "What would you like"},
	harness.Claude: {">", "ready"},
}

// DefaultReadyPatterns are patterns checked for any harness not in the map.
var DefaultReadyPatterns = []string{">", "$"}

// TrustPromptPatterns maps each harness to patterns that indicate a
// first-run folder-trust dialog the agent is blocking on.
var TrustPromptPatterns = map[string][]string{
	harness.Agy: {"Do you trust", "Yes, I trust this folder"},
	harness.Pi:  {"Trust project folder", "→ Trust", "Do not trust"},
}

// IsTrustPrompt reports whether capture contains a harness-specific trust
// prompt that should be auto-dismissed with Enter.
func IsTrustPrompt(capture, harnessName string) bool {
	patterns, ok := TrustPromptPatterns[harnessName]
	if !ok {
		return false
	}
	for _, p := range patterns {
		if strings.Contains(capture, p) {
			return true
		}
	}
	return false
}

// waitForHarnessReady polls the session pane until the harness shows a ready
// signature or the timeout (in seconds) expires. Returns nil when ready,
// an error if the timeout expires or the pane dies.
func waitForHarnessReady(bk session.Backend, windowID, harnessName string, timeoutSec int) error {
	patterns := ReadyPatterns[harnessName]
	if patterns == nil {
		patterns = DefaultReadyPatterns
	}

	// trustHandled tracks whether we've auto-accepted a per-worktree trust prompt.
	trustHandled := false

	deadline := time.After(time.Duration(timeoutSec) * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			// Grab final capture for diagnostic
			capture, _ := bk.Capture(windowID, 60)
			return fmt.Errorf("harness not ready after %ds: last capture: %q", timeoutSec, capture)
		case <-ticker.C:
			// Check if pane is still alive
			if !bk.Alive(windowID) {
				return fmt.Errorf("window died while waiting for ready")
			}
			capture, err := bk.Capture(windowID, 60)
			if err != nil {
				continue
			}

			// Dialog handlers: auto-answer trust prompts before checking ready patterns.
			if !trustHandled && IsTrustPrompt(capture, harnessName) {
				_ = bk.SendKeys(windowID, "")
				trustHandled = true
				continue
			}

			for _, p := range patterns {
				if strings.Contains(capture, p) {
					return nil
				}
			}
		}
	}
}

// Run executes the full spawn orchestration sequence:
//
//	resolve home → validate → brief exists → project path → worktree.AssertNotTangled
//	→ worktree.Get → resolve harness → model/effort → write .crew-launch.sh + .crew-brief.md + meta
//	→ start session → send brief → arm watcher
//
// On error after worktree lease, the worktree is returned to the pool (fail-closed).
func Run(args Args) (windowID string, err error) {
	// 1. Resolve home
	homeDir := args.HomeDir
	if homeDir == "" {
		homeDir, err = home.Resolve("")
		if err != nil {
			return "", fmt.Errorf("resolving home: %w", err)
		}
	}

	// 2. Resolve effective delivery mode
	effectiveMode, err := effectiveModeForSpawn(homeDir, args)
	if err != nil {
		return "", err
	}

	// 3. Validate --harness flag before brief existence check (cheap, user-facing)
	if args.HarnessFlag != "" {
		if err := harness.ValidateHarness(args.HarnessFlag); err != nil {
			return "", fmt.Errorf("--harness: %w", err)
		}
	}

	// 4. Preflight: require brief to exist before spawning
	if !brief.Exists(homeDir, args.ID) {
		return "", fmt.Errorf("no brief found for task %s: scaffold it with 'munsu brief %s %s' before spawning", args.ID, args.ID, args.ProjectName)
	}

	// 5. Warn if tasks-axi available but no backlog row for this id
	if _, err := exec.LookPath("tasks-axi"); err == nil {
		chk := exec.Command("tasks-axi", "show", args.ID)
		if out, err := chk.CombinedOutput(); err != nil || strings.Contains(string(out), "not found") {
			fmt.Fprintf(os.Stderr, "warning: task %s has no backlog row; register it with 'backlog add %s \"<description>\" --kind %s' to track lifecycle\n", args.ID, args.ID, args.Kind)
		}
	}

	// 6. Resolve project repo path from registry
	projPath, err := project.ResolveRepoPath(homeDir, args.ProjectName)
	if err != nil {
		return "", fmt.Errorf("resolving project %q: %w", args.ProjectName, err)
	}

	// 7. Check for worktree tangle (unless yolo)
	if !args.Yolo {
		if err := worktree.AssertNotTangled(projPath, args.ProjectName); err != nil {
			return "", err
		}
	}

	// 8. Acquire leased worktree
	wtPath, err := worktree.Get(homeDir, projPath, true)
	if err != nil {
		return "", fmt.Errorf("acquiring worktree: %w", err)
	}
	// On any subsequent error, return the worktree to pool (fail-closed).
	defer func() {
		if err != nil {
			_ = worktree.Return(homeDir, wtPath)
		}
	}()

	// 9. Resolve crewmate harness (--harness flag overrides)
	var h string
	if args.HarnessFlag != "" {
		if err := harness.ValidateHarness(args.HarnessFlag); err != nil {
			return "", fmt.Errorf("--harness: %w", err)
		}
		h = args.HarnessFlag
	} else {
		h, err = harness.Crew(homeDir)
		if err != nil {
			return "", fmt.Errorf("resolving harness: %w", err)
		}
	}

	// 10. Resolve model/effort from template
	var model, effort string
	var launchCmd string
	if tmpl, ok := harness.Templates[h]; ok {
		model = tmpl.DefaultModel
		if tmpl.DefaultEffort != "" {
			effort = tmpl.DefaultEffort
		}
		launchCmd = harness.LaunchString(h, tmpl)
	}

	// 11. Create session window
	var bk session.Backend
	var bkName string
	if args.Session != nil {
		bk = args.Session
		bkName = "test" // injected backend for tests
	} else {
		bk, bkName, err = session.Resolve(homeDir, args.Backend)
		if err != nil {
			return "", err
		}
	}
	windowID, err = bk.NewWindow("munsu", args.ID)
	if err != nil {
		return "", fmt.Errorf("backend %q not available: %w. Configure via --backend flag, config/backend file, or HERDR_ENV env", bkName, err)
	}

	// 12. Bootstrap window: cd to worktree and launch harness
	if launchCmd != "" {
		launchScript := filepath.Join(wtPath, ".crew-launch.sh")
		scriptContent := "#!/usr/bin/env bash\nset -e\nexport MUNSU_HOME=" + fmt.Sprintf("%q", homeDir) + "\n" + launchCmd + "\n"
		if writeErr := os.WriteFile(launchScript, []byte(scriptContent), 0755); writeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: writing launch script: %v\n", writeErr)
		}
		fullCmd := fmt.Sprintf("cd %s && bash .crew-launch.sh", wtPath)
		if sendErr := bk.SendKeys(windowID, fullCmd); sendErr != nil {
			fmt.Fprintf(os.Stderr, "warning: sending harness launch command: %v\n", sendErr)
		}
	}

	// 13a. Write brief into worktree for agent file access
	var briefData []byte
	briefPath := brief.Path(homeDir, args.ID)
	if data, readErr := os.ReadFile(briefPath); readErr == nil {
		briefData = data
		briefWorktreePath := filepath.Join(wtPath, ".crew-brief.md")
		if writeErr := os.WriteFile(briefWorktreePath, briefData, 0644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: writing brief to worktree: %v\n", writeErr)
		}
	} else if !os.IsNotExist(readErr) {
		fmt.Fprintf(os.Stderr, "warning: reading brief: %v\n", readErr)
	}

	// 13b. Wait for harness ready signature before injecting brief
	if len(briefData) > 0 {
		if err := waitForHarnessReady(bk, windowID, h, 60); err != nil {
			capture, _ := bk.Capture(windowID, 60)
			_ = task.AppendStatus(homeDir, args.ID, "failed: harness not ready")
			dataDir := filepath.Join(homeDir, "data", args.ID)
			_ = os.MkdirAll(dataDir, 0755)
			failContent := fmt.Sprintf("harness=%s\nerror=%v\n\nlast capture:\n%s\n", h, err, capture)
			_ = os.WriteFile(filepath.Join(dataDir, "ready-fail.txt"), []byte(failContent), 0644)
			_ = bk.Teardown(windowID)
			return "", fmt.Errorf("harness %q not ready within timeout: %w", h, err)
		}

		// Brief settle: let harness present clean prompt before one-liner
		time.Sleep(500 * time.Millisecond)
		// Inject brief: all harnesses use file-based .crew-brief.md
		_ = bk.SendKeys(windowID, "read and execute .crew-brief.md")
	}

	// 14. Write task meta
	yoloVal := "off"
	if args.Yolo {
		yoloVal = "on"
	}
	meta := map[string]string{
		"window":   windowID,
		"worktree": wtPath,
		"project":  args.ProjectName,
		"projpath": projPath,
		"harness":  h,
		"backend":  bkName,
		"kind":     args.Kind,
		"mode":     effectiveMode,
		"yolo":     yoloVal,
	}
	if model != "" {
		meta["model"] = model
	}
	if effort != "" {
		meta["effort"] = effort
	}
	if err := task.WriteMeta(homeDir, args.ID, meta); err != nil {
		fmt.Fprintf(os.Stderr, "warning: writing task meta: %v\n", err)
	}

	// 15. Append working: spawned status
	_ = task.AppendStatus(homeDir, args.ID, "working: spawned")

	// 16. Print endpoint info
	fmt.Printf("Spawned crewmate %s\n", args.ID)
	fmt.Printf("  window:   %s\n", windowID)
	fmt.Printf("  worktree: %s\n", wtPath)
	fmt.Printf("  projpath: %s\n", projPath)
	fmt.Printf("  project:  %s\n", args.ProjectName)
	fmt.Printf("  harness:  %s\n", h)
	if model != "" {
		fmt.Printf("  model:    %s\n", model)
	}
	if effort != "" {
		fmt.Printf("  effort:   %s\n", effort)
	}
	fmt.Printf("  kind:     %s\n", args.Kind)
	fmt.Printf("  mode:     %s\n", effectiveMode)
	fmt.Printf("  yolo:     %s\n", yoloVal)

	// 17. Arm watcher if requested (warn-only on failure)
	if args.Arm && args.ArmFunc != nil {
		if armErr := args.ArmFunc(homeDir); armErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to arm watcher: %v\n  Run 'munsu watch-arm' manually to start the watcher.\n", armErr)
		}
	}

	return windowID, nil
}
