package fleet

import (
	"errors"
	"fmt"
	"strings"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

type SpawnSoldierConfig struct {
	Harness string
	Model   string
	Effort  string
	Mode    string
}

type SpawnProjectConfig struct {
	Frozen           fleetconfig.ResolvedSnapshot
	SnapshotDigest   string
	DispatchAutonomy string
	ProjectName      string
	ProjectPath      string
	ReviewStep       taskauthority.DeliveryStep
	ForgeStep        taskauthority.DeliveryStep
	Soldier          SpawnSoldierConfig
}

func deliveryStep(step fleetconfig.ResolvedStep) taskauthority.DeliveryStep {
	return taskauthority.DeliveryStep{
		Baseline: step.Baseline, Adapter: step.Adapter, Path: step.Path,
		Args: append([]string(nil), step.Args...), ProbeState: step.ProbeState,
		Version: step.Version, Reason: step.Reason,
	}
}

func resolvedStep(step taskauthority.DeliveryStep) fleetconfig.ResolvedStep {
	return fleetconfig.ResolvedStep{
		Baseline: step.Baseline, Adapter: step.Adapter, Path: step.Path,
		Args: append([]string(nil), step.Args...), ProbeState: step.ProbeState,
		Version: step.Version, Reason: step.Reason,
	}
}

// ResolveSpawnProjectConfig loads the only config surface authorized by the
// resolved dispatch policy (issue #546 Slice 6, ADR-0008 §6): CaptainMediated
// reads the Captain's assigned published snapshot; GeneralDirect resolves the
// requested project from the typed fleet and project documents. The opposite
// read — or any read without a resolved policy — fails closed with an empty
// config.
//
// contract is the task's durable delivery contract, or nil for a task that has
// none. When present it is the mode authority for the launch; every other
// field of the snapshot resolves exactly as it does without one.
func ResolveSpawnProjectConfig(homeDir string, args Args, policy DispatchPolicy, contract *taskauthority.DeliveryContract) (SpawnProjectConfig, error) {
	if contract != nil && contract.Mode != taskauthority.DeliveryModeForSteps(contract.Review, contract.Forge) {
		return SpawnProjectConfig{}, fmt.Errorf("task delivery contract mode disagrees with captured review and forge steps")
	}
	var (
		snapshot fleetconfig.ResolvedSnapshot
		err      error
	)
	switch policy {
	case DispatchPolicyCaptainMediated:
		snapshot, err = fleetconfig.LoadPublishedSnapshot(homeDir)
		if err == nil && contract != nil {
			snapshot, err = snapshot.WithSteps(resolvedStep(contract.Review), resolvedStep(contract.Forge))
		}
	case DispatchPolicyGeneralDirect:
		if contract != nil {
			snapshot, err = resolveCapturedProjectSnapshot(homeDir, args.ProjectName, resolvedStep(contract.Review), resolvedStep(contract.Forge))
		} else {
			// Resolve the immutable project snapshot without substituting CLI
			// identities. Explicit flags are assertions about the resolved snapshot,
			// not a second configuration authority.
			snapshot, err = ResolveProjectSnapshot(homeDir, args.ProjectName)
		}
	default:
		return SpawnProjectConfig{}, fmt.Errorf("unresolved dispatch policy %q", policy)
	}
	if err != nil {
		return SpawnProjectConfig{}, classifySnapshotError(args.ProjectName, err)
	}
	resolved := snapshot.Config()
	if policy == DispatchPolicyCaptainMediated && args.ProjectName != "" && resolved.Project != args.ProjectName {
		return SpawnProjectConfig{}, fleetconfig.Remediate(
			fleetconfig.RemediateIncompatibleSnapshot,
			"publish a snapshot for the Captain's owning project",
			fmt.Errorf("published snapshot project %q does not match requested project %q", resolved.Project, args.ProjectName),
		)
	}
	if err := validateResolvedDispatchProfiles(resolved.DispatchProfiles); err != nil {
		return SpawnProjectConfig{}, err
	}
	if err := validateSpawnIdentityAssertions(args, resolved.Backend, resolved.SoldierHarness); err != nil {
		return SpawnProjectConfig{}, err
	}
	// The delivery mode is derived from the captured review and forge steps;
	// no separate mode decision exists to compose.
	mode := taskauthority.DeliveryModeForSteps(deliveryStep(resolved.ReviewStep), deliveryStep(resolved.ForgeStep))
	selection := resolveSnapshotDispatchSelection(resolved, args)
	selection.Harness = firstNonEmpty(args.HarnessFlag, selection.Harness, resolved.SoldierHarness)
	selection.Model = firstNonEmpty(args.ModelFlag, selection.Model, resolved.Model)
	selection.Effort = firstNonEmpty(args.EffortFlag, selection.Effort)
	if selection.Harness == "" {
		return SpawnProjectConfig{}, fleetconfig.Remediate(
			fleetconfig.RemediateInvalidProfile,
			"set soldierHarness or a matching dispatch profile in data/projects.json",
			fmt.Errorf("project %q resolved no Soldier harness", args.ProjectName),
		)
	}
	if err := harness.ValidateHarness(selection.Harness); err != nil {
		return SpawnProjectConfig{}, fleetconfig.Remediate(
			fleetconfig.RemediateInvalidProfile,
			"fix the project's dispatch profile or soldierHarness in data/projects.json",
			err,
		)
	}
	return SpawnProjectConfig{
		Frozen:           snapshot,
		SnapshotDigest:   resolved.Digest,
		DispatchAutonomy: resolved.DispatchAutonomy,
		ProjectName:      resolved.Project,
		ProjectPath:      resolved.ProjectPath,
		ReviewStep:       deliveryStep(resolved.ReviewStep),
		ForgeStep:        deliveryStep(resolved.ForgeStep),
		Soldier: SpawnSoldierConfig{
			Harness: selection.Harness,
			Model:   selection.Model,
			Effort:  selection.Effort,
			Mode:    mode,
		},
	}, nil
}

// validateSpawnIdentityAssertions refuses an explicit backend or harness flag
// that contradicts the resolved project snapshot.
func validateSpawnIdentityAssertions(args Args, backendName, harnessName string) error {
	checks := []struct {
		name       string
		explicit   string
		configured string
	}{
		{name: "backend", explicit: args.Backend, configured: backendName},
		{name: "harness", explicit: args.HarnessFlag, configured: harnessName},
	}
	for _, check := range checks {
		explicit := strings.TrimSpace(check.explicit)
		configured := strings.TrimSpace(check.configured)
		if explicit != "" && configured != "" && explicit != configured {
			return fmt.Errorf("spawn %s %q conflicts with resolved project snapshot value %q; update the project overlay or omit the flag", check.name, explicit, configured)
		}
	}
	return nil
}

// ResolveGeneralHomeBackend resolves the session backend identity for a home
// without task context from its typed snapshot surface. A published snapshot
// is authoritative when present; otherwise the typed fleet base document is
// used.
//
// There is no auto-detection and no fallback identity: an empty identity is a
// typed failure, never a device/PATH/env choice.
func ResolveGeneralHomeBackend(homeDir string) (string, error) {
	if fleetconfig.PublishedSnapshotAvailable(homeDir) {
		snapshot, err := fleetconfig.LoadPublishedSnapshot(homeDir)
		if err != nil {
			return "", err
		}
		return snapshot.Config().Backend, nil
	}
	base, err := fleetconfig.LoadFleetBase(homeDir)
	if err != nil {
		return "", err
	}
	if base.Config.Backend == "" {
		return "", fmt.Errorf("general home %q resolved no session backend identity: set backend in the fleet base config", homeDir)
	}
	return base.Config.Backend, nil
}

func classifySnapshotError(projectName string, err error) error {
	msg := err.Error()
	switch {
	case errors.Is(err, ErrNotFound), strings.Contains(msg, "not found"), strings.Contains(msg, "unknown project"):
		return fleetconfig.Remediate(
			fleetconfig.RemediateUnknownProject,
			fmt.Sprintf("register project %q in the Fleet project registry", projectName),
			err,
		)
	case strings.Contains(msg, "schema"), strings.Contains(msg, "schemaVersion"), strings.Contains(msg, "reading typed config document"):
		return fleetconfig.Remediate(
			fleetconfig.RemediateIncompatibleSnapshot,
			"migrate typed config documents to the supported schema versions",
			err,
		)
	default:
		return err
	}
}

func validateResolvedDispatchProfiles(profiles []fleetconfig.DispatchProfile) error {
	for _, profile := range profiles {
		if profile.Harness != "" {
			if err := harness.ValidateHarness(profile.Harness); err != nil {
				return fleetconfig.Remediate(fleetconfig.RemediateInvalidProfile, "fix or remove the invalid dispatch profile harness", err)
			}
		}
		for _, candidate := range profile.Use {
			if candidate.Harness == "" {
				return fleetconfig.Remediate(fleetconfig.RemediateInvalidProfile, "set a harness for every dispatch profile candidate", fmt.Errorf("dispatch profile %q has candidate without harness", profile.Name))
			}
			if err := harness.ValidateHarness(candidate.Harness); err != nil {
				return fleetconfig.Remediate(fleetconfig.RemediateInvalidProfile, "fix or remove the invalid dispatch profile candidate", err)
			}
		}
	}
	return nil
}

func resolveSnapshotDispatchSelection(resolved fleetconfig.ResolvedProjectConfig, args Args) harness.DispatchSelection {
	cfg := &harness.DispatchConfig{
		DefaultHarness: resolved.SoldierHarness,
		DefaultModel:   resolved.Model,
		Profiles:       resolved.DispatchProfiles,
	}
	return harness.ResolveDispatchSelection(cfg, spawnTaskDescription(args))
}

func spawnTaskDescription(args Args) string {
	if strings.TrimSpace(args.TaskDescription) != "" {
		return args.TaskDescription
	}
	if args.ID != "" {
		return args.ID
	}
	return args.ProjectName
}
