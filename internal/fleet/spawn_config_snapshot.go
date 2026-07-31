package fleet

import (
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
	Frozen         fleetconfig.ResolvedSnapshot
	SnapshotDigest string
	ProjectName    string
	ProjectPath    string
	Soldier        SpawnSoldierConfig
}

func ResolveSpawnProjectConfig(homeDir string, args Args, rank string) (SpawnProjectConfig, error) {
	snapshot, err := fleetconfig.LoadResolvedSnapshot(homeDir, args.ProjectName, fleetconfig.BoundaryOverrides{})
	if err != nil {
		return SpawnProjectConfig{}, classifySnapshotError(args.ProjectName, err)
	}
	resolved := snapshot.Config()
	if err := validateResolvedDispatchProfiles(resolved.DispatchProfiles); err != nil {
		return SpawnProjectConfig{}, err
	}

	mode, err := ResolveDeliveryMode(homeDir, args.Mode, normalizeSnapshotDeliveryMode(resolved.DefaultMode))
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
		Frozen:         snapshot,
		SnapshotDigest: resolved.Digest,
		ProjectName:    resolved.Project,
		ProjectPath:    resolved.ProjectPath,
		Soldier: SpawnSoldierConfig{
			Harness: selection.Harness,
			Model:   selection.Model,
			Effort:  selection.Effort,
			Mode:    mode,
		},
	}, nil
}

func normalizeSnapshotDeliveryMode(mode string) string {
	if mode == "direct-pr" {
		return "direct-PR"
	}
	return mode
}

func TypedConfigAvailable(homeDir string) bool {
	for _, path := range []string{fleetconfig.BaseDocumentPath, fleetconfig.CaptainDocumentPath, fleetconfig.ProjectDocumentPath} {
		if _, err := os.Stat(filepath.Join(homeDir, path)); err == nil {
			return true
		}
	}
	return false
}

func classifySnapshotError(projectName string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unknown project"):
		return fleetconfig.Remediate(
			fleetconfig.RemediateUnknownProject,
			fmt.Sprintf("register project %q in data/projects.json", projectName),
			err,
		)
	case strings.Contains(msg, "schemaVersion"), strings.Contains(msg, "reading typed config document"):
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
