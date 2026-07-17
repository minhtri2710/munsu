// Package spawn implements the crewmate spawn orchestration — the full
// sequence of resolving home, validating inputs, acquiring a worktree,
// launching the harness, and wiring the agent session.
package spawn

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
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

// Run executes the full spawn orchestration sequence by delegating to Runner.
//
//	resolve home → validate → brief exists → project path → worktree.AssertNotTangled
//	→ worktree.Get → resolve harness → model/effort → write .crew-launch.sh + .crew-brief.md + meta
//	→ start session → send brief → arm watcher
//
// On error after worktree lease, the worktree is returned to the pool (fail-closed).
func Run(args Args) (string, error) {
	return NewRunner(args).Run()
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
		return fmt.Errorf("delivery mode 'no-mistakes' requires the no-mistakes binary on PATH; run 'munsu doctor' or 'go install github.com/kunchenguid/no-mistakes@latest'")
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

// 5. When require-no-mistakes config is set, refuse fallback
if homeDir != "" {
if _, err := config.Get(homeDir, "require-no-mistakes"); err == nil {
return "", fmt.Errorf("config/require-no-mistakes is set but no-mistakes binary not found on PATH")
}
}

fmt.Fprintln(os.Stderr, "warning: no-mistakes not found on PATH; defaulting to direct-PR delivery mode. Install with: go install github.com/kunchenguid/no-mistakes@latest, or run 'munsu doctor'")
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
