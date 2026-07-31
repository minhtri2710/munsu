// Package fleet implements the compatibility matrix for gating mutations
// through operation-specific compatibility requirements.
//
// Each operation declares its own set of requirements. The matrix maps
// operations to their requirement checks. Version inequality alone never
// blocks — only specific incompatibilities (contract version mismatch,
// required integration missing, corrupt decoding) reject a mutation.
// Corrupt or incompatible decoding is never force-overridable.
package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/home"
)

// Operation identifies a distinct mutation that can be gated by compatibility.
type Operation string

const (
	OpTaskMutation   Operation = "task-mutation"
	OpSpawn          Operation = "spawn"
	OpCaptainLaunch  Operation = "captain-launch"
	OpCaptainRecover Operation = "captain-recovery"
	OpDelivery       Operation = "delivery"
	OpMigration      Operation = "migration"
	OpSelfUpdate     Operation = "self-update"
	OpTeardown       Operation = "teardown"
)

// String returns the human-readable label for the operation.
func (op Operation) String() string {
	return string(op)
}

// RequirementResult captures the outcome of one compatibility requirement check.
type RequirementResult struct {
	Name        string `json:"name"`
	Satisfied   bool   `json:"satisfied"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

// CheckResult captures the overall compatibility check for one operation.
type CheckResult struct {
	Operation    Operation            `json:"operation"`
	Compatible   bool                 `json:"compatible"`
	Requirements []RequirementResult  `json:"requirements"`
}

// IsCompatible reports whether the check passed. It is a shorthand for
// inspecting the Compatible field.
func (r *CheckResult) IsCompatible() bool {
	return r.Compatible
}

// FormatErrors returns a human-readable summary of all unsatisfied requirements.
func (r *CheckResult) FormatErrors() string {
	if r.Compatible {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("operation %q is blocked by compatibility requirements:\n", r.Operation))
	for _, req := range r.Requirements {
		if !req.Satisfied {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", req.Name, req.Detail))
			if req.Remediation != "" {
				b.WriteString(fmt.Sprintf("    remediation: %s\n", req.Remediation))
			}
		}
	}
	return b.String()
}

// requirementCheck is a function that evaluates one compatibility dimension.
type requirementCheck func() RequirementResult

// CheckOperation checks whether the given operation is compatible in the
// current environment. It returns a CheckResult with per-requirement results.
// The caller should inspect IsCompatible to decide whether to proceed.
// This function is safe to call repeatedly — it does not modify any state.
func CheckOperation(op Operation, homeDir string) *CheckResult {
	checks := requirementsFor(op, homeDir)
	result := &CheckResult{Operation: op, Compatible: true}
	for _, check := range checks {
		rr := check()
		result.Requirements = append(result.Requirements, rr)
		if !rr.Satisfied {
			result.Compatible = false
		}
	}
	return result
}

// MustBeCompatible panics if the operation is not compatible. This is a
// convenience for test helpers; production code should use CheckOperation
// and handle the result gracefully.
func MustBeCompatible(op Operation, homeDir string) {
	result := CheckOperation(op, homeDir)
	if !result.Compatible {
		panic(result.FormatErrors())
	}
}

// requirementsFor returns the set of requirement checks for the given operation.
// The matrix defines what each operation needs to be compatible.
func requirementsFor(op Operation, homeDir string) []requirementCheck {
	// Shared checks that apply to all operations.
	base := []requirementCheck{
		func() RequirementResult { return checkHomeReadable(homeDir) },
	}

	switch op {
	case OpTaskMutation:
		return append(base,
			func() RequirementResult { return checkTaskMetaDecodable(homeDir) },
		)

	case OpSpawn:
		return append(base,
			func() RequirementResult { return checkTaskMetaDecodable(homeDir) },
			func() RequirementResult { return checkHarnessBinary(homeDir, "soldier") },
			func() RequirementResult { return checkDeliveryModeCompatible(homeDir) },
		)

	case OpCaptainLaunch:
		return append(base,
			func() RequirementResult { return checkCaptainProvenance(homeDir) },
			func() RequirementResult { return checkCaptainIntegration(homeDir) },
			func() RequirementResult { return checkHarnessBinary(homeDir, "captain") },
		)

	case OpCaptainRecover:
		return append(base,
			func() RequirementResult { return checkCaptainProvenance(homeDir) },
			func() RequirementResult { return checkCaptainIntegration(homeDir) },
			func() RequirementResult { return checkHarnessBinary(homeDir, "captain") },
		)

	case OpDelivery:
		return append(base,
			func() RequirementResult { return checkTaskMetaDecodable(homeDir) },
			func() RequirementResult { return checkGhAvailability() },
			func() RequirementResult { return checkDeliveryModeCompatible(homeDir) },
		)

	case OpMigration:
		return append(base,
			func() RequirementResult { return checkLegacyConfigExists(homeDir) },
		)

	case OpSelfUpdate:
		return append(base,
			func() RequirementResult { return checkSelfUpdatePrerequisites() },
		)

	case OpTeardown:
		return append(base,
			func() RequirementResult { return checkTaskMetaDecodable(homeDir) },
		)

	default:
		return base
	}
}

// checkHomeReadable verifies the home directory exists and is readable.
func checkHomeReadable(homeDir string) RequirementResult {
	if homeDir == "" {
		return RequirementResult{
			Name:      "readable-home",
			Satisfied: false,
			Detail:    "home directory path is empty",
			Remediation: "Set MUNSU_HOME or use --home to specify a valid home directory",
		}
	}
	info, err := os.Stat(homeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return RequirementResult{
				Name:      "readable-home",
				Satisfied: false,
				Detail:    fmt.Sprintf("home directory %q does not exist", homeDir),
				Remediation: fmt.Sprintf("Run 'munsu init' to create %s", homeDir),
			}
		}
		return RequirementResult{
			Name:      "readable-home",
			Satisfied: false,
			Detail:    fmt.Sprintf("cannot access home directory %q: %v", homeDir, err),
			Remediation: "Check filesystem permissions and retry",
		}
	}
	if !info.IsDir() {
		return RequirementResult{
			Name:      "readable-home",
			Satisfied: false,
			Detail:    fmt.Sprintf("home path %q is not a directory", homeDir),
			Remediation: "Remove the file at the path or set MUNSU_HOME to a directory",
		}
	}
	return RequirementResult{
		Name:      "readable-home",
		Satisfied: true,
		Detail:    "home directory is readable",
	}
}

// checkTaskMetaDecodable verifies that task meta files in the home directory
// can be decoded. This is intentionally broad — corrupt meta is never
// force-overridable; it blocks the mutation regardless of --force.
func checkTaskMetaDecodable(homeDir string) RequirementResult {
	stateDir := filepath.Join(homeDir, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return RequirementResult{
			Name:      "decodable-task-meta",
			Satisfied: true,
			Detail:    "no task meta files to check",
		}
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		metaPath := filepath.Join(stateDir, entry.Name())
		if entry.IsDir() {
			return RequirementResult{
				Name:      "decodable-task-meta",
				Satisfied: false,
				Detail:    fmt.Sprintf("task meta path %s is a directory, not a file", entry.Name()),
				Remediation: fmt.Sprintf("Remove the directory at: %s", metaPath),
			}
		}
		if _, err := home.ReadMeta(homeDir, strings.TrimSuffix(entry.Name(), ".meta")); err != nil {
			return RequirementResult{
				Name:      "decodable-task-meta",
				Satisfied: false,
				Detail:    fmt.Sprintf("task meta %s cannot be decoded: %v", entry.Name(), err),
				Remediation: fmt.Sprintf("Remove or repair the corrupt meta file: %s", metaPath),
			}
		}
	}
	return RequirementResult{
		Name:      "decodable-task-meta",
		Satisfied: true,
		Detail:    "all task meta files are decodable",
	}
}

// checkHarnessBinary verifies that the required harness binary is on PATH.
// role is "soldier" or "captain".
func checkHarnessBinary(homeDir, role string) RequirementResult {
	var h string
	var err error
	switch role {
	case "soldier":
		h, err = harness.Soldier(homeDir)
	case "captain":
		h, err = harness.Captain(homeDir)
	default:
		return RequirementResult{
			Name:      "harness-binary",
			Satisfied: false,
			Detail:    fmt.Sprintf("unknown role %q", role),
		}
	}
	if err != nil || h == "" {
		return RequirementResult{
			Name:      "harness-binary",
			Satisfied: true,
			Detail:    "no harness configured, skipping binary check",
		}
	}
	a, ok := harness.GetAdapter(h)
	if !ok {
		return RequirementResult{
			Name:      "harness-binary",
			Satisfied: false,
			Detail:    fmt.Sprintf("harness %q is not in the adapter registry", h),
			Remediation: "Configure a supported harness: pi, codex, claude, agy, grok, opencode",
		}
	}
	binName := a.Name
	if _, err := exec.LookPath(binName); err != nil {
		return RequirementResult{
			Name:      "harness-binary",
			Satisfied: false,
			Detail:    fmt.Sprintf("harness binary %q not found on PATH", binName),
			Remediation: fmt.Sprintf("Install %s or configure a different harness with 'munsu config set %s-harness <name>'", binName, role),
		}
	}
	return RequirementResult{
		Name:      "harness-binary",
		Satisfied: true,
		Detail:    fmt.Sprintf("harness binary %q found on PATH", binName),
	}
}

// checkDeliveryModeCompatible verifies that if the delivery mode requires
// no-mistakes, the binary is available and compatible.
func checkDeliveryModeCompatible(homeDir string) RequirementResult {
	mode, err := ResolveDeliveryMode(homeDir, "", "")
	if err != nil {
		return RequirementResult{
			Name:      "delivery-mode-compatible",
			Satisfied: false,
			Detail:    fmt.Sprintf("resolving delivery mode: %v", err),
			Remediation: "Check delivery mode configuration with 'munsu doctor'",
		}
	}
	if mode != "no-mistakes" {
		return RequirementResult{
			Name:      "delivery-mode-compatible",
			Satisfied: true,
			Detail:    fmt.Sprintf("delivery mode %q does not require no-mistakes", mode),
		}
	}
	probe := NoMistakesProbe()
	switch probe.State {
	case backend.Ready:
		return RequirementResult{
			Name:      "delivery-mode-compatible",
			Satisfied: true,
			Detail:    fmt.Sprintf("no-mistakes %s is compatible", probe.Version),
		}
	case backend.Absent:
		return RequirementResult{
			Name:      "delivery-mode-compatible",
			Satisfied: false,
			Detail:    "no-mistakes binary is not on PATH",
			Remediation: "Install no-mistakes: 'go install github.com/kunchenguid/no-mistakes@latest' or 'munsu doctor'",
		}
	case backend.Unsupported:
		return RequirementResult{
			Name:      "delivery-mode-compatible",
			Satisfied: false,
			Detail:    fmt.Sprintf("no-mistakes version %s is unsupported: %s", probe.Version, probe.Detail),
			Remediation: fmt.Sprintf("Upgrade no-mistakes to >= %s", MinNoMistakesVersion),
		}
	case backend.Failed:
		return RequirementResult{
			Name:      "delivery-mode-compatible",
			Satisfied: false,
			Detail:    fmt.Sprintf("no-mistakes probe failed: %s", probe.Detail),
			Remediation: "Run 'munsu doctor' to diagnose and repair the no-mistakes installation",
		}
	default:
		return RequirementResult{
			Name:      "delivery-mode-compatible",
			Satisfied: false,
			Detail:    fmt.Sprintf("unexpected probe state: %s", probe.State),
			Remediation: "Run 'munsu doctor' to diagnose",
		}
	}
}

// checkCaptainProvenance verifies that the captain home has valid provenance.
func checkCaptainProvenance(homeDir string) RequirementResult {
	markerPath := filepath.Join(homeDir, home.CaptainProvenanceMarkerName)
	_, err := os.Stat(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return RequirementResult{
				Name:      "captain-provenance",
				Satisfied: false,
				Detail:    fmt.Sprintf("captain provenance marker %q not found", markerPath),
				Remediation: "Seed the captain home with 'munsu captain seed <id> <home-path>'",
			}
		}
		return RequirementResult{
			Name:      "captain-provenance",
			Satisfied: false,
			Detail:    fmt.Sprintf("checking provenance marker: %v", err),
			Remediation: "Check filesystem permissions and retry",
		}
	}
	return RequirementResult{
		Name:      "captain-provenance",
		Satisfied: true,
		Detail:    "captain provenance is valid",
	}
}

// checkCaptainIntegration verifies that the canonical integration (Pi) is
// installed when the resolved harness is Pi.
func checkCaptainIntegration(homeDir string) RequirementResult {
	h, err := harness.Captain(homeDir)
	if err != nil || h == "" {
		return RequirementResult{
			Name:      "captain-integration",
			Satisfied: true,
			Detail:    "no harness resolved, skipping integration check",
		}
	}
	if h != harness.Pi {
		return RequirementResult{
			Name:      "captain-integration",
			Satisfied: true,
			Detail:    fmt.Sprintf("canonical Pi integration is not required for harness %q", h),
		}
	}

	// Pi harness integration check — deferred to the runtime bridge so it
	// can be initialized in the CLI without circular imports.
	// When the bridge is not initialized, the check passes (deferred).
	integrator, err := bootstrapLookupIntegrator()
	if err != nil {
		return RequirementResult{
			Name:      "captain-integration",
			Satisfied: true,
			Detail:    "integration check deferred to runtime bridge",
		}
	}
	status, err := integrator.Status(homeDir, h)
	if err != nil {
		return RequirementResult{
			Name:      "captain-integration",
			Satisfied: false,
			Detail:    fmt.Sprintf("integration check error: %v", err),
			Remediation: "Run 'munsu integrate repair --harness pi --scope project' to repair Pi integration",
		}
	}
	switch status.State {
	case "installed":
		return RequirementResult{
			Name:      "captain-integration",
			Satisfied: true,
			Detail:    fmt.Sprintf("integrated with %s (%s)", status.Harness, status.Scope),
		}
	case "absent":
		return RequirementResult{
			Name:      "captain-integration",
			Satisfied: false,
			Detail:    fmt.Sprintf("integration absent for %s", status.Harness),
			Remediation: fmt.Sprintf("Run 'munsu integrate repair --harness %s --scope project' to install Pi integration", status.Harness),
		}
	case "drifted":
		return RequirementResult{
			Name:      "captain-integration",
			Satisfied: false,
			Detail:    fmt.Sprintf("integration drifted for %s: %s", status.Harness, status.Message),
			Remediation: fmt.Sprintf("Run 'munsu integrate repair --harness %s --scope project' to repair Pi integration", status.Harness),
		}
	default:
		return RequirementResult{
			Name:      "captain-integration",
			Satisfied: false,
			Detail:    fmt.Sprintf("unexpected integration state %q", status.State),
			Remediation: "Run 'munsu integrate status --harness pi' to check integration status",
		}
	}
}

// checkGhAvailability verifies that gh-axi or gh binary is available for
// delivery operations that require provider access.
func checkGhAvailability() RequirementResult {
	if _, err := exec.LookPath("gh-axi"); err == nil {
		return RequirementResult{
			Name:      "gh-available",
			Satisfied: true,
			Detail:    "gh-axi available on PATH",
		}
	}
	if _, err := exec.LookPath("gh"); err == nil {
		return RequirementResult{
			Name:      "gh-available",
			Satisfied: true,
			Detail:    "gh available on PATH",
		}
	}
	return RequirementResult{
		Name:      "gh-available",
		Satisfied: false,
		Detail:    "neither gh-axi nor gh found on PATH",
		Remediation: "Install gh-axi or gh CLI: 'brew install gh' or 'gh-axi install'",
	}
}

// checkLegacyConfigExists verifies that legacy config files exist for migration.
func checkLegacyConfigExists(homeDir string) RequirementResult {
	legacyPaths := []string{
		filepath.Join(homeDir, "data", "captains.md"),
		filepath.Join(homeDir, "data", "projects.md"),
		filepath.Join(homeDir, "config", "soldier-dispatch.json"),
	}
	typedPaths := []string{
		filepath.Join(homeDir, config.BaseDocumentPath),
		filepath.Join(homeDir, config.CaptainDocumentPath),
		filepath.Join(homeDir, config.ProjectDocumentPath),
	}
	allTypedPresent := true
	for _, p := range typedPaths {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			allTypedPresent = false
			break
		}
	}
	if allTypedPresent {
		return RequirementResult{
			Name:      "legacy-config-exists",
			Satisfied: true,
			Detail:    "typed documents already present, no migration needed",
		}
	}
	hasLegacy := false
	for _, p := range legacyPaths {
		if _, err := os.Stat(p); err == nil {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		return RequirementResult{
			Name:      "legacy-config-exists",
			Satisfied: false,
			Detail:    "no legacy config files found to migrate",
			Remediation: "No migration needed — configuration is already current",
		}
	}
	return RequirementResult{
		Name:      "legacy-config-exists",
		Satisfied: true,
		Detail:    "legacy config files found, migration is possible",
	}
}

// checkSelfUpdatePrerequisites verifies that the install root is a git repo
// with a clean worktree on the default branch.
func checkSelfUpdatePrerequisites() RequirementResult {
	execPath, err := os.Executable()
	if err != nil {
		return RequirementResult{
			Name:      "self-update-prerequisites",
			Satisfied: false,
			Detail:    fmt.Sprintf("cannot resolve executable path: %v", err),
			Remediation: "Ensure munsu is installed from a git repository",
		}
	}
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}
	installRoot, err := homeGitRoot(filepath.Dir(realPath))
	if err != nil {
		return RequirementResult{
			Name:      "self-update-prerequisites",
			Satisfied: false,
			Detail:    fmt.Sprintf("cannot find git repository from %s: %v", filepath.Dir(realPath), err),
			Remediation: "Install munsu from a git repository for self-update support",
		}
	}
	out, err := homeGitRun(installRoot, "status", "--porcelain")
	if err != nil {
		return RequirementResult{
			Name:      "self-update-prerequisites",
			Satisfied: false,
			Detail:    fmt.Sprintf("checking worktree status: %v", err),
			Remediation: "Run 'git status' in the install root to diagnose",
		}
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		return RequirementResult{
			Name:      "self-update-prerequisites",
			Satisfied: false,
			Detail:    fmt.Sprintf("worktree at %s has uncommitted changes", installRoot),
			Remediation: "Commit or stash changes before updating",
		}
	}
	defaultBranch, err := homeDefaultBranch(installRoot)
	if err != nil {
		return RequirementResult{
			Name:      "self-update-prerequisites",
			Satisfied: false,
			Detail:    fmt.Sprintf("cannot resolve default branch: %v", err),
			Remediation: "Check git remote configuration",
		}
	}
	cb, err := homeCurrentBranch(installRoot)
	if err != nil {
		return RequirementResult{
			Name:      "self-update-prerequisites",
			Satisfied: false,
			Detail:    fmt.Sprintf("cannot determine current branch: %v", err),
			Remediation: "Switch to the default branch with 'git checkout <default-branch>'",
		}
	}
	if cb != defaultBranch {
		return RequirementResult{
			Name:      "self-update-prerequisites",
			Satisfied: false,
			Detail:    fmt.Sprintf("on branch %q, expected default branch %q", cb, defaultBranch),
			Remediation: fmt.Sprintf("Switch to the default branch: 'git checkout %s'", defaultBranch),
		}
	}
	return RequirementResult{
		Name:      "self-update-prerequisites",
		Satisfied: true,
		Detail:    fmt.Sprintf("install root %s is ready for update", installRoot),
	}
}

// IntegrationStatusChecker is the interface for checking and managing integration status.
type IntegrationStatusChecker interface {
	Status(homeDir, harness string) (IntegrationStatusInfo, error)
	EnsureCaptain(homeDir string) error
}

// IntegrationStatusInfo describes the state of a harness integration.
type IntegrationStatusInfo struct {
	Harness string
	Scope   string
	State   string // "installed", "absent", "drifted"
	Message string
}

// bootstrapLookupIntegrator is a bridge to the bootstrap package's
// integration status capability. It is resolved at runtime to avoid
// circular imports between internal/fleet and internal/bootstrap.
var bootstrapLookupIntegrator = func() (IntegrationStatusChecker, error) {
	return nil, fmt.Errorf("bootstrap integration bridge not initialized; call SetBootstrapIntegrator first")
}

// SetBootstrapIntegrator sets the bridge function that the compatibility
// matrix uses to check Pi integration status. This is called during CLI
// initialization to break the circular dependency between internal/fleet
// and internal/bootstrap.
func SetBootstrapIntegrator(fn func() (IntegrationStatusChecker, error)) {
	bootstrapLookupIntegrator = fn
}

// homeGitRoot walks up from dir to find a git repository root.
var homeGitRoot = func(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	current := abs
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no git repository found from %s", dir)
		}
		current = parent
	}
}

// homeGitRun runs git with Dir fixed to root.
var homeGitRun = func(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	return cmd.Output()
}

// homeDefaultBranch resolves the default branch name.
var homeDefaultBranch = func(root string) (string, error) {
	out, err := homeGitRun(root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		ref, refErr := homeGitRun(root, "rev-parse", "--abbrev-ref", "HEAD")
		if refErr == nil {
			branch := strings.TrimSpace(string(ref))
			if branch != "" && branch != "HEAD" {
				return branch, nil
			}
		}
		for _, candidate := range []string{"main", "master"} {
			if _, err := homeGitRun(root, "rev-parse", "--verify", candidate); err == nil {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("cannot resolve default branch for %s: %w", root, err)
	}
	ref := strings.TrimSpace(string(out))
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1], nil
}

// homeCurrentBranch returns the checked-out branch name.
var homeCurrentBranch = func(root string) (string, error) {
	out, err := homeGitRun(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("determining current branch: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "", fmt.Errorf("not on a branch (detached HEAD)")
	}
	return branch, nil
}