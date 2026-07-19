package cli

import (
	"fmt"
	"os/exec"

	"github.com/minhtri2710/munsu/internal/afk"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/integrate"
	"github.com/minhtri2710/munsu/internal/scope"
	"github.com/minhtri2710/munsu/internal/supervision"
)

// CapabilityDiagnostics groups all extended capability reports for doctor output.
type CapabilityDiagnostics struct {
	Integrations []IntegrationDiagnostic
	Watcher      *WatcherDiagnostic
	General      *GeneralDiagnostic
	ScopeResult  *ScopeDiagnostic
}

// IntegrationDiagnostic reports the integration state for one harness.
type IntegrationDiagnostic struct {
	Harness string
	OnPath  bool
	ExePath string
	State   string // "installed", "drifted", "unsupported", "absent", "error"
	Drifted bool
	Detail  string
}

func (d IntegrationDiagnostic) String() string {
	pathLabel := "MISSING"
	if d.OnPath {
		pathLabel = "found at " + d.ExePath
	}
	s := fmt.Sprintf("  %s: %s, integration: %s", d.Harness, pathLabel, d.State)
	if d.Detail != "" {
		s += " (" + d.Detail + ")"
	}
	if d.Drifted {
		s += " [DRIFTED]"
	}
	return s
}

func (d IntegrationDiagnostic) Fix() string {
	switch {
	case d.State == "absent":
		return fmt.Sprintf("munsu integrate install --harness %s", d.Harness)
	case d.Drifted:
		return fmt.Sprintf("munsu integrate repair --harness %s", d.Harness)
	default:
		return ""
	}
}

// WatcherDiagnostic reports watcher identity and version status.
type WatcherDiagnostic struct {
	Identity       *supervision.WatcherIdentity
	Running        bool
	VersionMatched bool
	CliVersion     string
}

func (d *WatcherDiagnostic) String() string {
	if d.Identity == nil || !d.Running {
		return "watcher: not running or identity unverified"
	}
	s := fmt.Sprintf("watcher: %s", supervision.IdentitySummary(d.Identity))
	switch {
	case !d.VersionMatched && d.CliVersion != "":
		s += fmt.Sprintf(", CLI version: %s [VERSION MISMATCH]", d.CliVersion)
	case d.CliVersion != "":
		s += fmt.Sprintf(", CLI version: %s (match)", d.CliVersion)
	}
	return s
}

func (d *WatcherDiagnostic) Fix() string {
	if d.Identity == nil || !d.Running {
		return "munsu watch ensure"
	}
	if !d.VersionMatched {
		return "munsu watch-arm --restart"
	}
	return ""
}

// GeneralDiagnostic reports general target resolution status.
type GeneralDiagnostic struct {
	Result afk.TargetResult
	Err    error
}

func (d *GeneralDiagnostic) String() string {
	if d.Err != nil {
		return fmt.Sprintf("general target: error: %v", d.Err)
	}
	if d.Result.Source == afk.Unsupported {
		return fmt.Sprintf("general target: %s (%s)", d.Result.Source, d.Result.SourceDetail)
	}
	return fmt.Sprintf("general target: resolved via %s → %q", d.Result.Source, d.Result.Handle)
}

func (d *GeneralDiagnostic) Fix() string {
	if d.Result.Source == afk.Unsupported {
		return "set config/general-pane or ensure TMUX_PANE/HERDR_ENV is active"
	}
	return ""
}

// ScopeDiagnostic reports git-worktree identity and no-mistakes gate capability.
type ScopeDiagnostic struct {
	Identity string
	GateCap  string
	Path     string
	Err      error
}

func (d *ScopeDiagnostic) String() string {
	if d.Err != nil {
		return fmt.Sprintf("scope: %s (%s) at %s [error: %v]", d.Identity, d.GateCap, d.Path, d.Err)
	}
	return fmt.Sprintf("scope: %s (%s) at %s", d.Identity, d.GateCap, d.Path)
}

// CollectCapabilities populates the extended capability diagnostics for doctor.
// It is read-only and never mutates home. Every external call is guarded so
// doctor never panics on a misconfigured home.
func CollectCapabilities(home, cwd, version string) *CapabilityDiagnostics {
	cd := &CapabilityDiagnostics{}

	// 1. Integration matrix — one row per known harness
	cd.Integrations = collectIntegrationDiagnostics(home, cwd)

	// 2. Watcher identity — compare identity file against CLI version
	cd.Watcher = collectWatcherDiagnostic(home, version)

	// 3. General target — resolve wake/general pane handle
	cd.General = collectGeneralDiagnostic(home)

	// 4. Scope classification
	cd.ScopeResult = collectScopeDiagnostic(cwd)

	return cd
}

func collectIntegrationDiagnostics(home, cwd string) []IntegrationDiagnostic {
	diags := make([]IntegrationDiagnostic, 0, len(harness.KnownHarnesses))
	for _, name := range harness.KnownHarnesses {
		d := IntegrationDiagnostic{Harness: name}

		// Check if harness binary is on PATH
		if path, err := exec.LookPath(name); err == nil {
			d.OnPath = true
			d.ExePath = path
		}

		// Check integration status.
		result, err := integrate.Status(home, cwd, name, integrate.ScopeProject)
		if err != nil {
			d.State = "error"
			d.Detail = fmt.Sprintf("status check failed: %v", err)
		} else {
			d.State = result.State
			d.Drifted = result.Drifted
			if result.State == "unsupported" {
				d.Detail = "native adapter: not implemented"
			} else if result.Drifted && result.Message != "" {
				d.Detail = result.Message
			}
		}

		diags = append(diags, d)
	}
	return diags
}

func collectWatcherDiagnostic(home, version string) *WatcherDiagnostic {
	d := &WatcherDiagnostic{CliVersion: version}

	id := supervision.ReadIdentity(home)
	if id == nil {
		return d
	}
	d.Identity = id
	d.Running = supervision.ValidatePIDOwnership(home, id.PID)

	if version != "" && id.BuildVersion != "" {
		d.VersionMatched = id.BuildVersion == version
	}
	return d
}

func collectGeneralDiagnostic(home string) *GeneralDiagnostic {
	d := &GeneralDiagnostic{}

	result, err := afk.ResolveTargetWithSource(home)
	if err != nil {
		d.Err = err
		return d
	}
	d.Result = result

	if err := afk.ValidateTargetOwnership(&result); err != nil {
		d.Err = fmt.Errorf("ownership validation: %w", err)
	}
	return d
}

func collectScopeDiagnostic(cwd string) *ScopeDiagnostic {
	d := &ScopeDiagnostic{Path: cwd}
	r := scope.Classify(cwd)
	d.Identity = r.Identity.String()
	d.GateCap = r.GateCap.String()
	d.Path = r.CanonicalPath
	if r.Err != nil {
		d.Err = r.Err
	}
	return d
}
