package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/minhtri2710/munsu/internal/fleet"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// SkewClassification is the typed reason a runtime identity component is skewed.
type SkewClassification string

const (
	SkewPathShadowing            SkewClassification = "path_shadowing"
	SkewDirtyOrUnverifiableBuild SkewClassification = "dirty_or_unverifiable_build"
	SkewPackagedInstall          SkewClassification = "packaged_install"
	SkewWatcherMismatch          SkewClassification = "watcher_mismatch"
	SkewIntegrationMismatch      SkewClassification = "integration_mismatch"
)

// RuntimeIdentity exposes every read-only identity input operators need before mutation.
type RuntimeIdentity struct {
	ProtocolVersion   int                          `json:"protocol_version"`
	RunningExecutable ExecutableIdentity           `json:"running_executable"`
	PATHExecutable    ExecutableIdentity           `json:"path_executable"`
	Build             BuildProvenance              `json:"build"`
	SourceCheckouts   []SourceCheckoutIdentity     `json:"source_checkouts,omitempty"`
	Watcher           *WatcherRuntimeIdentity      `json:"watcher,omitempty"`
	Captains          []CaptainRuntimeIdentity     `json:"captains,omitempty"`
	Integrations      []IntegrationRuntimeIdentity `json:"integrations,omitempty"`
	Skew              []SkewFinding                `json:"skew,omitempty"`
}

type ExecutableIdentity struct {
	Path   string `json:"path,omitempty"`
	Digest string `json:"digest,omitempty"`
	Error  string `json:"error,omitempty"`
}

type BuildProvenance struct {
	CLIVersion    string `json:"cli_version,omitempty"`
	ModulePath    string `json:"module_path,omitempty"`
	ModuleVersion string `json:"module_version,omitempty"`
	VCSRevision   string `json:"vcs_revision,omitempty"`
	VCSTime       string `json:"vcs_time,omitempty"`
	VCSModified   bool   `json:"vcs_modified"`
	Available     bool   `json:"available"`
}

type SourceCheckoutIdentity struct {
	Path     string `json:"path"`
	Revision string `json:"revision,omitempty"`
	Dirty    bool   `json:"dirty"`
	Error    string `json:"error,omitempty"`
}

type WatcherRuntimeIdentity struct {
	Component        string `json:"component"`
	Home             string `json:"home,omitempty"`
	Executable       string `json:"executable,omitempty"`
	ExecutableDigest string `json:"executable_digest,omitempty"`
	BuildVersion     string `json:"build_version,omitempty"`
	ProtocolVersion  int    `json:"protocol_version,omitempty"`
	CommitSHA        string `json:"commit_sha,omitempty"`
	Running          bool   `json:"running"`
}

type CaptainRuntimeIdentity struct {
	ID             string                  `json:"id"`
	Home           string                  `json:"home,omitempty"`
	SourceCheckout *SourceCheckoutIdentity `json:"source_checkout,omitempty"`
	Watcher        *WatcherRuntimeIdentity `json:"watcher,omitempty"`
}

type IntegrationRuntimeIdentity struct {
	Harness        string `json:"harness"`
	Scope          Scope  `json:"scope"`
	State          string `json:"state"`
	Version        string `json:"version,omitempty"`
	ManifestPath   string `json:"manifest_path,omitempty"`
	ManifestSchema string `json:"manifest_schema,omitempty"`
	ContentDigest  string `json:"content_digest,omitempty"`
	Drifted        bool   `json:"drifted"`
	Message        string `json:"message,omitempty"`
	Remediation    string `json:"remediation,omitempty"`
}

type SkewFinding struct {
	Classification SkewClassification `json:"classification"`
	Component      string             `json:"component"`
	Detail         string             `json:"detail,omitempty"`
	Remediation    string             `json:"remediation"`
}

func (f SkewFinding) String() string {
	line := fmt.Sprintf("%s: %s", f.Classification, f.Component)
	if f.Detail != "" {
		line += " (" + f.Detail + ")"
	}
	return line
}

type runtimeIdentityProbe struct {
	executable        func() (string, error)
	lookPath          func(string) (string, error)
	buildInfo         func() (*debug.BuildInfo, bool)
	readWatcher       func(string) *orchestrator.WatcherIdentity
	validateWatcher   func(string, int) bool
	listCaptains      func(string) ([]fleet.Info, error)
	integrationStatus func(string, string, string, Scope) (*IntegrationResult, error)
	git               func(string, ...string) (string, error)
}

var defaultRuntimeIdentityProbe = func() runtimeIdentityProbe {
	return runtimeIdentityProbe{
		executable:        os.Executable,
		lookPath:          exec.LookPath,
		buildInfo:         debug.ReadBuildInfo,
		readWatcher:       orchestrator.ReadIdentity,
		validateWatcher:   orchestrator.ValidatePIDOwnership,
		listCaptains:      fleet.ListCaptains,
		integrationStatus: Status,
		git: func(dir string, args ...string) (string, error) {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			out, err := cmd.Output()
			return strings.TrimSpace(string(out)), err
		},
	}
}

func CollectRuntimeIdentity(homeDir, cwd, cliVersion string) RuntimeIdentity {
	return collectRuntimeIdentity(homeDir, cwd, cliVersion, defaultRuntimeIdentityProbe())
}

func collectRuntimeIdentity(homeDir, cwd, cliVersion string, probe runtimeIdentityProbe) RuntimeIdentity {
	probe = fillRuntimeIdentityProbe(probe)
	id := RuntimeIdentity{ProtocolVersion: orchestrator.ProtocolVersion, Build: collectBuildProvenance(cliVersion, probe)}

	id.RunningExecutable = collectRunningExecutable(probe)
	id.PATHExecutable = collectPATHExecutable(probe)
	classifyExecutableSkew(&id)
	classifyBuildSkew(&id)

	id.SourceCheckouts = collectSourceCheckouts(homeDir, cwd, probe)

	if watcher := collectWatcherRuntime("watcher", homeDir, probe); watcher != nil {
		id.Watcher = watcher
		classifyWatcherSkew(&id, *watcher, "munsu watch ensure --restart")
	}

	captains, err := probe.listCaptains(homeDir)
	if err == nil {
		for _, c := range captains {
			capID := strings.TrimSpace(c.ID)
			capHome := strings.TrimSpace(c.Home)
			ci := CaptainRuntimeIdentity{ID: capID, Home: capHome}
			if capHome != "" {
				checkout := collectSourceCheckout(capHome, probe)
				ci.SourceCheckout = &checkout
				ci.Watcher = collectWatcherRuntime("captain:"+capID+" watcher", capHome, probe)
				if ci.Watcher != nil {
					classifyWatcherSkew(&id, *ci.Watcher, "munsu captain recover "+capID)
				}
			}
			id.Captains = append(id.Captains, ci)
		}
	}

	id.Integrations = collectIntegrationRuntime(homeDir, cwd, probe)
	for _, integ := range id.Integrations {
		if integ.Drifted || integ.State == "drifted" {
			id.Skew = append(id.Skew, SkewFinding{
				Classification: SkewIntegrationMismatch,
				Component:      "integration:" + integ.Harness,
				Detail:         integ.Message,
				Remediation:    integ.Remediation,
			})
		}
	}

	return id
}

func fillRuntimeIdentityProbe(probe runtimeIdentityProbe) runtimeIdentityProbe {
	defaults := defaultRuntimeIdentityProbe()
	if probe.executable == nil {
		probe.executable = defaults.executable
	}
	if probe.lookPath == nil {
		probe.lookPath = defaults.lookPath
	}
	if probe.buildInfo == nil {
		probe.buildInfo = defaults.buildInfo
	}
	if probe.readWatcher == nil {
		probe.readWatcher = defaults.readWatcher
	}
	if probe.validateWatcher == nil {
		probe.validateWatcher = defaults.validateWatcher
	}
	if probe.listCaptains == nil {
		probe.listCaptains = defaults.listCaptains
	}
	if probe.integrationStatus == nil {
		probe.integrationStatus = defaults.integrationStatus
	}
	if probe.git == nil {
		probe.git = defaults.git
	}
	return probe
}

func collectRunningExecutable(probe runtimeIdentityProbe) ExecutableIdentity {
	path, err := probe.executable()
	if err != nil {
		return ExecutableIdentity{Error: err.Error()}
	}
	return executableIdentity(path)
}

func collectPATHExecutable(probe runtimeIdentityProbe) ExecutableIdentity {
	path, err := probe.lookPath("munsu")
	if err != nil {
		return ExecutableIdentity{Error: err.Error()}
	}
	return executableIdentity(path)
}

func executableIdentity(path string) ExecutableIdentity {
	canonical := canonicalizeExecutablePath(path)
	digest, err := fileDigest(canonical)
	if err != nil {
		return ExecutableIdentity{Path: canonical, Error: err.Error()}
	}
	return ExecutableIdentity{Path: canonical, Digest: digest}
}

func canonicalizeExecutablePath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func collectBuildProvenance(cliVersion string, probe runtimeIdentityProbe) BuildProvenance {
	if strings.TrimSpace(cliVersion) == "" {
		cliVersion = orchestrator.BuildVersion
	}
	bp := BuildProvenance{CLIVersion: cliVersion}
	info, ok := probe.buildInfo()
	if ok && info != nil {
		bp.Available = true
		bp.ModulePath = info.Main.Path
		bp.ModuleVersion = info.Main.Version
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				bp.VCSRevision = setting.Value
			case "vcs.time":
				bp.VCSTime = setting.Value
			case "vcs.modified":
				bp.VCSModified = setting.Value == "true"
			}
		}
	}
	if bp.VCSRevision == "" {
		bp.VCSRevision = orchestrator.CommitSHA
	}
	return bp
}

func classifyExecutableSkew(id *RuntimeIdentity) {
	if id.RunningExecutable.Path == "" || id.PATHExecutable.Path == "" {
		return
	}
	if id.RunningExecutable.Path == id.PATHExecutable.Path && id.RunningExecutable.Digest == id.PATHExecutable.Digest {
		return
	}
	detail := fmt.Sprintf("running=%s path=%s", id.RunningExecutable.Path, id.PATHExecutable.Path)
	if id.RunningExecutable.Digest != "" && id.PATHExecutable.Digest != "" && id.RunningExecutable.Digest != id.PATHExecutable.Digest {
		detail += " digest differs"
	}
	id.Skew = append(id.Skew, SkewFinding{
		Classification: SkewPathShadowing,
		Component:      "path:munsu",
		Detail:         detail,
		Remediation:    fmt.Sprintf("Update PATH so %s is selected before %s, or reinstall munsu at %s.", id.RunningExecutable.Path, id.PATHExecutable.Path, id.PATHExecutable.Path),
	})
}

func classifyBuildSkew(id *RuntimeIdentity) {
	hasPackagedVersion := id.Build.ModuleVersion != "" && id.Build.ModuleVersion != "(devel)"
	switch {
	case !id.Build.Available || id.Build.VCSModified || id.Build.VCSRevision == "" && !hasPackagedVersion:
		id.Skew = append(id.Skew, SkewFinding{
			Classification: SkewDirtyOrUnverifiableBuild,
			Component:      "build:provenance",
			Detail:         "build provenance is dirty or unavailable",
			Remediation:    "Rebuild munsu from a clean checkout and rerun diagnostics.",
		})
	case id.Build.VCSRevision == "" && hasPackagedVersion:
		id.Skew = append(id.Skew, SkewFinding{
			Classification: SkewPackagedInstall,
			Component:      "build:provenance",
			Detail:         "packaged module version has no embedded VCS revision",
			Remediation:    "No mutation required; install from source if commit-level provenance is required.",
		})
	}
}

func collectSourceCheckouts(homeDir, cwd string, probe runtimeIdentityProbe) []SourceCheckoutIdentity {
	seen := map[string]bool{}
	var out []SourceCheckoutIdentity
	for _, path := range []string{cwd, homeDir} {
		checkout := collectSourceCheckout(path, probe)
		if checkout.Path == "" || seen[checkout.Path] {
			continue
		}
		seen[checkout.Path] = true
		out = append(out, checkout)
	}
	return out
}

func collectSourceCheckout(path string, probe runtimeIdentityProbe) SourceCheckoutIdentity {
	canonical := home.Canonical(path)
	checkout := SourceCheckoutIdentity{Path: canonical}
	if canonical == "" {
		return checkout
	}
	if rev, err := probe.git(canonical, "rev-parse", "--short=12", "HEAD"); err == nil {
		checkout.Revision = rev
	} else {
		checkout.Error = err.Error()
	}
	if status, err := probe.git(canonical, "status", "--porcelain"); err == nil {
		checkout.Dirty = strings.TrimSpace(status) != ""
	} else if checkout.Error == "" {
		checkout.Error = err.Error()
	}
	return checkout
}

func collectWatcherRuntime(component, homeDir string, probe runtimeIdentityProbe) *WatcherRuntimeIdentity {
	watcher := probe.readWatcher(homeDir)
	if watcher == nil {
		return nil
	}
	executable := canonicalizeExecutablePath(watcher.Executable)
	digest, _ := fileDigest(executable)
	return &WatcherRuntimeIdentity{
		Component:        component,
		Home:             watcher.Home,
		Executable:       executable,
		ExecutableDigest: digest,
		BuildVersion:     watcher.BuildVersion,
		ProtocolVersion:  watcher.ProtocolVersion,
		CommitSHA:        watcher.CommitSHA,
		Running:          probe.validateWatcher(homeDir, watcher.PID),
	}
}

func classifyWatcherSkew(id *RuntimeIdentity, watcher WatcherRuntimeIdentity, remediation string) {
	mismatched := false
	var details []string
	if watcher.Executable != "" && watcher.Executable != "unknown" && id.RunningExecutable.Path != "" && watcher.Executable != id.RunningExecutable.Path {
		mismatched = true
		details = append(details, fmt.Sprintf("executable=%s running=%s", watcher.Executable, id.RunningExecutable.Path))
	}
	if watcher.ExecutableDigest != "" && id.RunningExecutable.Digest != "" && watcher.ExecutableDigest != id.RunningExecutable.Digest {
		mismatched = true
		details = append(details, "executable digest differs")
	}
	if watcher.CommitSHA != "" && id.Build.VCSRevision != "" && !orchestrator.NewBuildIdentity(watcher.CommitSHA).Matches(orchestrator.NewBuildIdentity(id.Build.VCSRevision)) {
		mismatched = true
		details = append(details, fmt.Sprintf("commit=%s running=%s", watcher.CommitSHA, id.Build.VCSRevision))
	}
	if !mismatched {
		return
	}
	id.Skew = append(id.Skew, SkewFinding{
		Classification: SkewWatcherMismatch,
		Component:      watcher.Component,
		Detail:         strings.Join(details, "; "),
		Remediation:    remediation,
	})
}

func collectIntegrationRuntime(homeDir, cwd string, probe runtimeIdentityProbe) []IntegrationRuntimeIdentity {
	var out []IntegrationRuntimeIdentity
	for _, name := range harness.KnownHarnesses {
		manifestPath := ManifestPath(homeDir, name, ScopeProject, cwd)
		manifest := readIntegrationManifest(manifestPath)
		status, err := probe.integrationStatus(homeDir, cwd, name, ScopeProject)
		if err != nil {
			out = append(out, IntegrationRuntimeIdentity{
				Harness:        name,
				Scope:          ScopeProject,
				State:          "error",
				ManifestPath:   manifestPath,
				ManifestSchema: manifest.SchemaVersion,
				Version:        manifest.Version,
				ContentDigest:  manifest.ContentDigest,
				Drifted:        true,
				Message:        err.Error(),
				Remediation:    fmt.Sprintf("munsu integrate repair --harness %s --scope project", name),
			})
			continue
		}
		out = append(out, IntegrationRuntimeIdentity{
			Harness:        status.Harness,
			Scope:          status.Scope,
			State:          status.State,
			Version:        firstNonEmpty(status.Version, manifest.Version),
			ManifestPath:   manifestPath,
			ManifestSchema: manifest.SchemaVersion,
			ContentDigest:  manifest.ContentDigest,
			Drifted:        status.Drifted,
			Message:        status.Message,
			Remediation:    integrationRemediation(*status),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Harness < out[j].Harness })
	return out
}

func readIntegrationManifest(path string) Manifest {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}
	}
	return manifest
}

func integrationRemediation(status IntegrationResult) string {
	scope := status.Scope
	if scope == "" {
		scope = ScopeProject
	}
	if status.Drifted || status.State == "drifted" {
		return fmt.Sprintf("munsu integrate repair --harness %s --scope %s", status.Harness, scope)
	}
	if status.State == "absent" {
		return fmt.Sprintf("munsu integrate install --harness %s --scope %s", status.Harness, scope)
	}
	return ""
}

func RuntimeIdentityLines(id *RuntimeIdentity) []string {
	if id == nil {
		return nil
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("protocol_version: %d", id.ProtocolVersion))
	lines = append(lines, fmt.Sprintf("running_executable: %s digest=%s", displayValue(id.RunningExecutable.Path), displayValue(id.RunningExecutable.Digest)))
	if id.RunningExecutable.Error != "" {
		lines = append(lines, "running_executable_error: "+id.RunningExecutable.Error)
	}
	lines = append(lines, fmt.Sprintf("path_executable: %s digest=%s", displayValue(id.PATHExecutable.Path), displayValue(id.PATHExecutable.Digest)))
	if id.PATHExecutable.Error != "" {
		lines = append(lines, "path_executable_error: "+id.PATHExecutable.Error)
	}
	lines = append(lines, fmt.Sprintf("build: cli=%s module=%s version=%s vcs=%s time=%s modified=%t", displayValue(id.Build.CLIVersion), displayValue(id.Build.ModulePath), displayValue(id.Build.ModuleVersion), displayValue(id.Build.VCSRevision), displayValue(id.Build.VCSTime), id.Build.VCSModified))
	for _, checkout := range id.SourceCheckouts {
		lines = append(lines, formatSourceCheckout("source_checkout", checkout))
	}
	if id.Watcher != nil {
		lines = append(lines, formatWatcherRuntime(*id.Watcher))
	}
	for _, captain := range id.Captains {
		lines = append(lines, fmt.Sprintf("captain: %s home=%s", displayValue(captain.ID), displayValue(captain.Home)))
		if captain.SourceCheckout != nil {
			lines = append(lines, formatSourceCheckout("captain_source_checkout", *captain.SourceCheckout))
		}
		if captain.Watcher != nil {
			lines = append(lines, formatWatcherRuntime(*captain.Watcher))
		}
	}
	for _, integ := range id.Integrations {
		line := fmt.Sprintf("integration: %s scope=%s state=%s version=%s manifest=%s content_digest=%s", displayValue(integ.Harness), integ.Scope, displayValue(integ.State), displayValue(integ.Version), displayValue(integ.ManifestPath), displayValue(integ.ContentDigest))
		if integ.Message != "" {
			line += " detail=" + integ.Message
		}
		lines = append(lines, line)
	}
	if len(id.Skew) == 0 {
		lines = append(lines, "skew: none")
	} else {
		for _, finding := range id.Skew {
			lines = append(lines, fmt.Sprintf("skew: %s component=%s detail=%s remediation=%s", finding.Classification, finding.Component, displayValue(finding.Detail), finding.Remediation))
		}
	}
	return lines
}

func formatSourceCheckout(prefix string, checkout SourceCheckoutIdentity) string {
	line := fmt.Sprintf("%s: %s revision=%s dirty=%t", prefix, displayValue(checkout.Path), displayValue(checkout.Revision), checkout.Dirty)
	if checkout.Error != "" {
		line += " error=" + checkout.Error
	}
	return line
}

func formatWatcherRuntime(watcher WatcherRuntimeIdentity) string {
	return fmt.Sprintf("%s: home=%s executable=%s digest=%s version=%s proto=%d commit=%s running=%t", watcher.Component, displayValue(watcher.Home), displayValue(watcher.Executable), displayValue(watcher.ExecutableDigest), displayValue(watcher.BuildVersion), watcher.ProtocolVersion, displayValue(watcher.CommitSHA), watcher.Running)
}

func displayValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
