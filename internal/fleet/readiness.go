package fleet

import (
	"fmt"
	"sort"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
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

// ProfileHarnessReadiness is the launch readiness of a harness a dispatch
// profile can select, probed with the harness checks a spawn runs.
type ProfileHarnessReadiness struct {
	Harness string
	Err     error
}

// ProjectReadiness is what a soldier launch for one project would be refused
// on, probed without a task, worktree, or endpoint. Harness is the project's
// default soldier harness, the one a spawn with no matching profile resolves
// to. Err is nil when every spawn preflight for that harness passes. Profiles
// lists each other harness a dispatch profile can select, probed on its own.
type ProjectReadiness struct {
	Project  string
	Mode     string
	Harness  string
	Err      error
	Profiles []ProfileHarnessReadiness
}

// ProbeProjectReadiness runs the preflights a General-direct spawn of project
// would run, in spawn's order: the delivery mode and the harness. The dispatch profile is selected with no task, so no profile matches
// on the project's name. It reads project configuration and probes tools; it
// mutates nothing.
func ProbeProjectReadiness(homeDir, project string) (ProjectReadiness, error) {
	cfg, err := resolveSpawnProjectConfig(homeDir, Args{ProjectName: project}, DispatchPolicyGeneralDirect, nil, "")
	if err != nil {
		return ProjectReadiness{}, err
	}
	out := ProjectReadiness{Project: project, Mode: cfg.Soldier.Mode, Harness: cfg.Soldier.Harness}
	out.Err = defaultLaunchError(homeDir, cfg)
	for _, h := range profileHarnesses(cfg.Frozen.Config().DispatchProfiles, out.Harness) {
		out.Profiles = append(out.Profiles, ProfileHarnessReadiness{Harness: h, Err: launchHarnessError(homeDir, h)})
	}
	return out, nil
}

func defaultLaunchError(homeDir string, cfg SpawnProjectConfig) error {
	if err := deliveryPreflight(cfg.Soldier.Mode, cfg.ProjectPath, cfg.ForgeStep); err != nil {
		return err
	}
	return launchHarnessError(homeDir, cfg.Soldier.Harness)
}

// launchHarnessError is the harness half of a spawn's preflight: the binary
// and auth check, then the verified launch contract.
func launchHarnessError(homeDir, harnessName string) error {
	if err := SoldierHarnessPreflight(homeDir, harnessName); err != nil {
		return err
	}
	return soldierLaunchContract(harnessName)
}

// profileHarnesses returns the distinct harnesses the dispatch profiles can
// select, excluding the default harness, in sorted order.
func profileHarnesses(profiles []fleetconfig.DispatchProfile, defaultHarness string) []string {
	seen := map[string]bool{"": true, "default": true, defaultHarness: true}
	var out []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, p := range profiles {
		add(p.Harness)
		for _, candidate := range p.Use {
			add(candidate.Harness)
		}
	}
	sort.Strings(out)
	return out
}
