package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	fleetconfig "github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
)

type SpawnSoldierConfig struct {
	Harness string
	Model   string
	Effort  string
	Mode    string
}

type SpawnProjectConfig struct {
	Frozen                fleetconfig.ResolvedSnapshot
	SnapshotDigest        string
	DispatchAutonomy      string
	ProjectName           string
	ProjectPath           string
	AllowDirectPRFallback bool
	Soldier               SpawnSoldierConfig
}

// ResolveSpawnProjectConfig loads the only config surface authorized by the
// resolved dispatch policy (issue #546 Slice 6, ADR-0008 §6): CaptainMediated
// reads the Captain's assigned published snapshot; GeneralDirect reads the
// fleet base document. The opposite read — or any read without a resolved
// policy — fails closed.
func ResolveSpawnProjectConfig(homeDir string, args Args, policy DispatchPolicy) (SpawnProjectConfig, error) {
	var (
		snapshot fleetconfig.ResolvedSnapshot
		err      error
	)
	switch policy {
	case DispatchPolicyCaptainMediated:
		snapshot, err = fleetconfig.LoadPublishedSnapshot(homeDir)
	case DispatchPolicyGeneralDirect:
		// Resolve the immutable project snapshot without substituting CLI
		// identities. Explicit flags are assertions about the resolved snapshot,
		// not a second configuration authority.
		snapshot, err = ResolveProjectSnapshot(homeDir, args.ProjectName, fleetconfig.BoundaryOverrides{})
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

	// Compose the mode decision from the resolved snapshot: the typed default
	// mode and the resolved require-no-mistakes are the single authority. When
	// the default mode is unset, ResolveDeliveryMode auto-detects (no-mistakes
	// on PATH, else direct-PR) and refuses fallback when require-no-mistakes is
	// set — preserving the unset → direct-PR default semantics.
	if err := validateSpawnIdentityAssertions(args, resolved.Backend, resolved.SoldierHarness, normalizeSnapshotDeliveryMode(resolved.DefaultMode)); err != nil {
		return SpawnProjectConfig{}, err
	}
	mode, err := ResolveDeliveryMode(args.Mode, normalizeSnapshotDeliveryMode(resolved.DefaultMode), resolved.RequireNoMistakes)
	if err != nil {
		return SpawnProjectConfig{}, err
	}
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
		Frozen:                snapshot,
		SnapshotDigest:        resolved.Digest,
		DispatchAutonomy:      resolved.DispatchAutonomy,
		ProjectName:           resolved.Project,
		ProjectPath:           resolved.ProjectPath,
		AllowDirectPRFallback: resolved.AllowDirectPRFallback,
		Soldier: SpawnSoldierConfig{
			Harness: selection.Harness,
			Model:   selection.Model,
			Effort:  selection.Effort,
			Mode:    mode,
		},
	}, nil
}

func validateSpawnIdentityAssertions(args Args, backendName, harnessName, mode string) error {
	checks := []struct {
		name       string
		explicit   string
		configured string
		normalize  func(string) string
	}{
		{name: "backend", explicit: args.Backend, configured: backendName, normalize: strings.TrimSpace},
		{name: "harness", explicit: args.HarnessFlag, configured: harnessName, normalize: strings.TrimSpace},
		{name: "mode", explicit: args.Mode, configured: mode, normalize: normalizeSnapshotDeliveryMode},
	}
	for _, check := range checks {
		explicit := check.normalize(check.explicit)
		configured := check.normalize(check.configured)
		if explicit != "" && configured != "" && explicit != configured {
			return fmt.Errorf("spawn %s %q conflicts with resolved project snapshot value %q; update the project overlay or omit the flag", check.name, explicit, configured)
		}
	}
	return nil
}

func normalizeSnapshotDeliveryMode(mode string) string {
	if mode == "direct-pr" {
		return "direct-PR"
	}
	return mode
}

func TypedConfigAvailable(homeDir string) bool {
	if fleetconfig.PublishedSnapshotAvailable(homeDir) {
		return true
	}
	if _, err := os.Stat(filepath.Join(homeDir, fleetconfig.BaseDocumentPath)); err == nil {
		return true
	}
	return false
}

// ResolveGeneralHomeBackend resolves the session backend identity for a home
// without task context from the typed snapshot surface, mirroring the
// ResolveSpawnProjectConfig rank precedence:
//   - captain context: the published config snapshot (the composed
//     config.ResolveProject output; its Backend is required non-empty by
//     strict snapshot validation).
//   - general context: the fleet base document's typed Backend.
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
