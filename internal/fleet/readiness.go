package fleet

import (
	"fmt"

	"github.com/minhtri2710/munsu/internal/fence"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
)

// SoldierHarnessPreflight is the harness half of a soldier launch's readiness:
// the binary and auth preflight a fenced claude seat logs in with. Spawn refuses
// on the same result before any worktree, endpoint, or process exists; doctor
// asks the same question without spawning.
func SoldierHarnessPreflight(homeDir, harnessName string) error {
	var claudeSeat *harness.ClaudeSeat
	if harnessName == harness.Claude && fence.Supported() == nil {
		cfg, err := harness.HumanClaudeConfig(home.StateDir(homeDir))
		if err != nil {
			return err
		}
		claudeSeat = &harness.ClaudeSeat{Config: cfg, BuildDir: harness.SeatBuildDir(homeDir)}
	}
	result, err := harness.Preflight(harnessName, claudeSeat)
	if err != nil {
		return err
	}
	if result.BinaryOnPath == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: harnessName, Reason: "binary-absent"}
	}
	if result.AuthConfigured == harness.PreflightAbsent {
		return &harness.PreflightError{Harness: harnessName, Reason: "auth-absent"}
	}
	return nil
}

// soldierLaunchContract refuses a harness without a verified soldier launch
// contract; BuildLaunchArgs and the readiness probe both ask it.
func soldierLaunchContract(harnessName string) error {
	adapter, ok := harness.GetAdapter(harnessName)
	if !ok {
		return fmt.Errorf("soldier launch: harness %q is not a verified harness", harnessName)
	}
	if !adapter.SoldierLaunch.Supported {
		return fmt.Errorf("soldier launch: harness %q does not have a verified prompt-arg contract", harnessName)
	}
	if len(adapter.QuestionDeny) == 0 {
		return fmt.Errorf("soldier launch: harness %q cannot deny its ask-the-user tool; decision-hold is the only question path", harnessName)
	}
	return nil
}

// ProjectReadiness is what a soldier launch for one project would be refused
// on, probed without a task, worktree, or endpoint. Err is nil when every
// spawn preflight passes.
type ProjectReadiness struct {
	Project string
	Mode    string
	Harness string
	Err     error
}

// ProbeProjectReadiness runs the preflights a General-direct spawn of project
// would run, in spawn's order: the no-mistakes gate, the delivery mode, and the
// harness. It reads project configuration and probes tools; it mutates nothing.
func ProbeProjectReadiness(homeDir, project string) (ProjectReadiness, error) {
	cfg, err := ResolveSpawnProjectConfig(homeDir, Args{ProjectName: project}, DispatchPolicyGeneralDirect, nil)
	if err != nil {
		return ProjectReadiness{}, err
	}
	out := ProjectReadiness{Project: project, Mode: cfg.Soldier.Mode, Harness: cfg.Soldier.Harness}
	if out.Err = reviewPreflight(out.Mode, cfg.ProjectPath, cfg.ReviewStep, defaultNoMistakesPreflight); out.Err != nil {
		return out, nil
	}
	if out.Err = deliveryPreflight(out.Mode, cfg.ProjectPath, cfg.ForgeStep); out.Err != nil {
		return out, nil
	}
	if out.Err = SoldierHarnessPreflight(homeDir, out.Harness); out.Err != nil {
		return out, nil
	}
	out.Err = soldierLaunchContract(out.Harness)
	return out, nil
}
