// Package captain manages persistent domain supervisors (captains).
package captain

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
	"github.com/minhtri2710/munsu/internal/harness"
	"github.com/minhtri2710/munsu/internal/hometag"
	"github.com/minhtri2710/munsu/internal/integrate"
	"github.com/minhtri2710/munsu/internal/marker"
	"github.com/minhtri2710/munsu/internal/project"
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// ProvenanceMarkerName is the marker file written to a seeded captain home root.
const ProvenanceMarkerName = ".munsu-captain-home"

// ProvenanceVersion is the current provenance marker format version.
const ProvenanceVersion = "munsu-v2"

// ConvergeLockName is the converge-specific lock file under parent state.
const ConvergeLockName = ".captain-converge.lock"

// NudgePendingDir is the directory under parent state for pending nudge markers.
const NudgePendingDir = ".captain-nudge-pending"

// captainPiExtensionNames are project-local Pi extensions loaded with -e at captain launch.
// Order matches captain launch: turnend guard then watch bridge, plus munsu integrate.
var captainPiExtensionNames = []string{
	"munsu-pi-integration.ts",
	"munsu-captain-turnend-guard.ts",
	"munsu-captain-pi-watch.ts",
	"fm-primary-turnend-guard.ts",
	"fm-primary-pi-watch.ts",
}

// EnsureCaptainPiExtensions installs project-scoped Pi extensions under captainHome/.pi/extensions
// so Launch buildLaunchArgs can always pass -e for munsu integrate + captain-compat names.
// Idempotent. Soft-skips when pi is unavailable (non-pi fleets / offline test hosts).
func EnsureCaptainPiExtensions(captainHome string) error {
	if _, err := ValidateProvenance(captainHome); err != nil {
		return fmt.Errorf("refusing pi extensions on unmarked home %s: %w", captainHome, err)
	}
	adpt := &integrate.PiAdapter{
		HomeDir: captainHome,
		Cwd:     captainHome,
		Scope:   string(integrate.ScopeProject),
	}
	target, _, _, err := adpt.InstallPiExtension()
	if err != nil {
		// Soft-skip when pi/munsu tooling is missing — captain may run a non-pi harness.
		msg := err.Error()
		if strings.Contains(msg, "pi not found") ||
			strings.Contains(msg, "Install Pi") ||
			strings.Contains(msg, "pi API capability") ||
			strings.Contains(msg, "cannot check pi version") ||
			strings.Contains(msg, "cannot parse pi version") ||
			strings.Contains(msg, "pi version") ||
			strings.Contains(msg, "cannot resolve munsu binary path") ||
			strings.Contains(msg, "munsu not found") {
			return nil
		}
		return fmt.Errorf("installing munsu-pi-integration: %w", err)
	}
	// Mirror integrate content under munsu-captain-* names so Launch -e matches captain shape.
	content, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("reading installed pi extension: %w", err)
	}
	extDir := filepath.Join(captainHome, ".pi", "extensions")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		return fmt.Errorf("creating .pi/extensions: %w", err)
	}
	for _, name := range []string{"munsu-captain-turnend-guard.ts", "munsu-captain-pi-watch.ts"} {
		dst := filepath.Join(extDir, name)
		if err := atomicWriteFile(dst, content, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

type Info struct {
	ID      string
	Home    string
	Scope   string
	Project string
	Added   string
}

// --- Injectable seams for testing ---

var lookPath = exec.LookPath

// newSessionBackend resolves and returns a session backend for the parent home.
// Override in tests to inject a fake backend.
var newSessionBackend = func(parentHome string) (session.Backend, string, error) {
	return session.Resolve(parentHome, "")
}

// convergeLockAcquire acquires the converge lock exclusively.
// Override in tests to avoid fd leaks.
var convergeLockAcquire = func(parentHome string) (func(), error) {
	return acquireExclusiveLock(filepath.Join(parentHome, "state", ConvergeLockName))
}

// gitRun is the git command runner. Override in tests.
var gitRun = func(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// launchCmd builds a shell-safe command string for sending via session backend.
// Override in tests.
var launchCmd = func(binPath string, args []string, captainHome string) (string, error) {
	return buildLaunchScript(binPath, args, captainHome)
}

// --- Helpers ---

// shQuote wraps s in single quotes, escaping any embedded single quotes
// for safe shell evaluation.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// buildLaunchScript writes a bash launch script into the general home and
// returns a fish-safe command that runs it. Herdr panes may use fish, so the
// bash-only identity/env plumbing must not be typed directly into the pane.
func buildLaunchScript(binPath string, args []string, cwd string) (string, error) {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("set -euo pipefail\n")
	b.WriteString("cd ")
	b.WriteString(shQuote(cwd))
	b.WriteString("\n")
	b.WriteString("export MUNSU_HOME=")
	b.WriteString(shQuote(cwd))
	b.WriteString("\n")
	b.WriteString("export MUNSU_ROLE=captain\n")
	b.WriteString("exec ")
	b.WriteString(shQuote(binPath))
	for _, arg := range args {
		b.WriteString(" ")
		b.WriteString(shQuote(arg))
	}
	b.WriteString("\n")
	scriptPath := filepath.Join(cwd, ".captain-launch.sh")
	if err := os.WriteFile(scriptPath, []byte(b.String()), 0755); err != nil {
		return "", fmt.Errorf("writing captain launch script: %w", err)
	}
	return "bash " + shQuote(scriptPath), nil
}

// sha256Content returns the hex SHA-256 digest of data.
func sha256Content(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// taskIDForCaptain returns the task ID used in state metadata for a captain.
func taskIDForCaptain(smID string) string {
	return "captain:" + smID
}

// --- Seed / Provenance ---

// DefaultCharter returns the idle-by-default Captain charter with General return-channel rules.
// parentHome must be the General home whose state/captain:<id>.status is the escalation file.
func DefaultCharter(id, parentHome string) string {
	statusFile := filepath.Join(parentHome, "state", taskIDForCaptain(id)+".status")
	return fmt.Sprintf(`# Charter: %s (Captain)

## Domain
You are a persistent domain Captain under the General fleet hierarchy:
General → Captain → Soldier.

This home is yours. Operate only on work the General routes to you.
Never invent surveys, audits, or self-directed "find work" tasks.
An empty queue is healthy.

## Requests from the General
Incoming pane text may be:
1. Marked with a leading %s followed by an invisible separator — a General-routed request.
2. Unmarked — the human captain typing directly into your pane (stay conversational).

When a message carries the General marker:
- Do the work.
- Answer via the STATUS path below, never chat-only. The General does not read this chat.
- Terse result: one status line is the whole answer.
- Detailed result: write a doc under this home's data/ and append a status line that points to it.

## Escalation / return channel
Material captain-relevant outcomes append ONE line to the General status file:
  echo "{state}: {one short line}" >> %s

States: working, needs-decision, blocked, paused, done, failed, resolved.
Key material phases with [key=<slug>] so later done/failed/resolved supersede them.
Routine Soldier supervision, heartbeats, and retries stay inside THIS home and must not touch that file.

## Spawn authority
Spawn Soldier only from this Captain home. Never launch another Captain.
`, id, marker.FromGeneralLabel, shQuote(statusFile))
}

// Seed creates a new captain home with a charter brief and a provenance marker.
// When charter is empty, DefaultCharter(id, parentHome) is used. parentHome may be
// empty only when an explicit charter is provided.
func Seed(id, homePath, charter string) error {
	return SeedWithParent(id, homePath, "", charter)
}

// SeedWithParent is Seed with an explicit General parent home for default charter generation.
func SeedWithParent(id, homePath, parentHome, charter string) error {
	if err := os.MkdirAll(homePath, 0755); err != nil {
		return fmt.Errorf("creating captain home %s: %w", homePath, err)
	}

	for _, dir := range []string{"state", "data", "config", "projects"} {
		if err := os.MkdirAll(filepath.Join(homePath, dir), 0755); err != nil {
			return fmt.Errorf("creating %s/%s: %w", homePath, dir, err)
		}
	}

	if strings.TrimSpace(charter) == "" {
		if parentHome == "" {
			return fmt.Errorf("seeding captain %s: empty charter requires parent home for return-channel path", id)
		}
		charter = DefaultCharter(id, parentHome)
	}

	agentsPath := filepath.Join(homePath, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte(charter), 0644); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}

	if err := SeedProvenance(homePath, id); err != nil {
		return fmt.Errorf("seeding provenance marker: %w", err)
	}

	if parentHome != "" {
		if err := Register(parentHome, id, homePath, "", ""); err != nil {
			return fmt.Errorf("registering captain %s: %w", id, err)
		}
		// Inherit General config + project registry so soldiers need not re-add projects.
		if err := ConfigPush(parentHome, homePath); err != nil {
			return fmt.Errorf("seed inherit: %w", err)
		}
	}

	// Install project-scoped Pi captain extensions so Launch -e always has files.
	if err := EnsureCaptainPiExtensions(homePath); err != nil {
		return fmt.Errorf("installing captain pi extensions: %w", err)
	}

	fmt.Printf("Seeded captain %s at %s\n", id, homePath)
	return nil
}

// canonicalHome returns the fully-resolved, absolute path for homePath.
// Fails closed: any resolution error returns an error — no raw fallback.
func canonicalHome(homePath string) (string, error) {
	canon, err := filepath.EvalSymlinks(homePath)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks: %w", err)
	}
	abs, err := filepath.Abs(canon)
	if err != nil {
		return "", fmt.Errorf("resolving absolute path: %w", err)
	}
	return abs, nil
}

// SeedProvenance writes the provenance marker to a captain home root.
// Fails closed if canonical home cannot be determined.
func SeedProvenance(homePath, id string) error {
	canonical, err := canonicalHome(homePath)
	if err != nil {
		return fmt.Errorf("cannot determine canonical home for %s: %w", homePath, err)
	}
	content := fmt.Sprintf("%s\n%s\n%s\n", ProvenanceVersion, id, canonical)
	markerPath := filepath.Join(homePath, ProvenanceMarkerName)
	return os.WriteFile(markerPath, []byte(content), 0644)
}

// ValidateProvenance reads and validates the provenance marker.
// Rejects v1 (missing canonical-home), extra fields, and copied/moved homes.
func ValidateProvenance(homePath string) (string, error) {
	markerPath := filepath.Join(homePath, ProvenanceMarkerName)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("captain home %s has no %s marker — run 'munsu captain seed' or 'munsu captain migrate'", homePath, ProvenanceMarkerName)
		}
		return "", fmt.Errorf("reading provenance marker %s: %w", markerPath, err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 4)
	if len(lines) < 3 {
		return "", fmt.Errorf("provenance marker %s is malformed: expected exactly 3 lines (version, id, canonical-home), got %d", markerPath, len(lines))
	}
	if len(lines) > 3 {
		return "", fmt.Errorf("provenance marker %s has extra content — expected exactly 3 lines", markerPath)
	}
	version := strings.TrimSpace(lines[0])
	if version != ProvenanceVersion {
		return "", fmt.Errorf("provenance marker %s has unsupported version %q (expected %q)", markerPath, version, ProvenanceVersion)
	}
	id := strings.TrimSpace(lines[1])
	if id == "" {
		return "", fmt.Errorf("provenance marker %s has empty id", markerPath)
	}
	storedHome := strings.TrimSpace(lines[2])
	if storedHome == "" {
		return "", fmt.Errorf("provenance marker %s has empty canonical-home", markerPath)
	}
	// Verify canonical home match — rejects copied/moved homes.
	actualCanon, err := canonicalHome(homePath)
	if err != nil {
		return "", fmt.Errorf("cannot verify canonical home for copied/move check: %w", err)
	}
	if actualCanon != storedHome {
		return "", fmt.Errorf("provenance marker home %q does not match actual canonical home %q — captain may have been copied/moved", storedHome, actualCanon)
	}
	return id, nil
}

// Validate checks a captain home for full structural correctness:
//   - provenance marker exists and is valid
//   - AGENTS.md exists
//   - state/data/config dirs exist
//   - home path is not a parent home, project home, or fake/system path
//   - canonical home (abs, resolved) matches the expected parent containment
func Validate(homePath, parentHome string) error {
	if _, err := ValidateProvenance(homePath); err != nil {
		return err
	}

	for _, dir := range []string{"state", "data", "config"} {
		fi, err := os.Stat(filepath.Join(homePath, dir))
		if err != nil {
			return fmt.Errorf("missing %s/ directory: %w", dir, err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s/ exists but is not a directory", dir)
		}
	}

	agentsPath := filepath.Join(homePath, "AGENTS.md")
	if _, err := os.Stat(agentsPath); err != nil {
		return fmt.Errorf("missing AGENTS.md: %w", err)
	}

	// Refuse fake/project/primary homes using canonical path.
	absHome, err := canonicalHome(homePath)
	if err != nil {
		return fmt.Errorf("resolving captain home path: %w", err)
	}
	absParent, err := canonicalHome(parentHome)
	if err != nil {
		return fmt.Errorf("resolving parent home path: %w", err)
	}
	if absHome == absParent {
		return fmt.Errorf("captain home %s is the parent home itself — refuse", homePath)
	}

	bareName := filepath.Base(absHome)
	if bareName == "fake" || bareName == "project" || bareName == "primary" {
		return fmt.Errorf("captain home %s uses reserved name %q — refuse", homePath, bareName)
	}

	return nil
}

// validateStructure checks that a captain home has the expected directory
// structure and AGENTS.md, WITHOUT requiring a provenance marker.
// Used by Migrate before it writes the marker.
func validateStructure(homePath string) error {
	for _, dir := range []string{"state", "data", "config"} {
		fi, err := os.Stat(filepath.Join(homePath, dir))
		if err != nil {
			return fmt.Errorf("missing %s/ directory: %w", dir, err)
		}
		if !fi.IsDir() {
			return fmt.Errorf("%s/ exists but is not a directory", dir)
		}
	}
	agentsPath := filepath.Join(homePath, "AGENTS.md")
	if _, err := os.Stat(agentsPath); err != nil {
		return fmt.Errorf("missing AGENTS.md: %w", err)
	}
	return nil
}

// Migrate writes a provenance marker into an existing seeded captain home.
// It checks structural validity before writing and refuses fake/project/primary homes.
func Migrate(homePath, id string) error {
	refuted := filepath.Base(homePath)
	if refuted == "fake" || refuted == "project" || refuted == "primary" {
		return fmt.Errorf("refusing migrate: home %s uses reserved name %q", homePath, refuted)
	}
	if err := validateStructure(homePath); err != nil {
		return fmt.Errorf("migrate pre-check failed: %w", err)
	}
	return SeedProvenance(homePath, id)
}

// --- Registry ---

// RegistryPath returns the path to the authoritative captain registry.
func RegistryPath(parentHome string) string {
	return filepath.Join(parentHome, "data", "captains.md")
}

// Register appends a captain to the parent registry if not already present.
// Format matches ParseRegistry: "- <id> - (home: <path>; scope: <scope>; projects: <project>; added: <date>)".
func Register(parentHome, id, homePath, scope, project string) error {
	if id == "" || homePath == "" {
		return fmt.Errorf("register requires id and home path")
	}
	canon, err := canonicalHome(homePath)
	if err != nil {
		return err
	}
	path := RegistryPath(parentHome)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	existing, err := ParseRegistry(path)
	if err != nil {
		existing = nil
	}
	for _, e := range existing {
		if e.ID == id {
			return nil // already registered
		}
	}
	added := time.Now().UTC().Format("2006-01-02")
	meta := fmt.Sprintf("home: %s; scope: %s; projects: %s; added: %s", canon, scope, project, added)
	line := fmt.Sprintf("- %s - (%s)\n", id, meta)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if st, _ := f.Stat(); st != nil && st.Size() == 0 {
		if _, err := f.WriteString("# Captains\n\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(line)
	return err
}

// Unregister removes a captain id from the parent registry.
// Missing registry or missing id is a no-op (idempotent cleanup).
func Unregister(parentHome, id string) error {
	if id == "" {
		return fmt.Errorf("unregister requires id")
	}
	path := RegistryPath(parentHome)
	existing, err := ParseRegistry(path)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return nil
	}
	found := false
	var kept []Info
	for _, e := range existing {
		if e.ID == id {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("rewriting registry %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString("# Captains\n\n"); err != nil {
		return err
	}
	for _, e := range kept {
		meta := fmt.Sprintf("home: %s; scope: %s; projects: %s; added: %s", e.Home, e.Scope, e.Project, e.Added)
		if _, err := fmt.Fprintf(f, "- %s - (%s)\n", e.ID, meta); err != nil {
			return fmt.Errorf("writing registry: %w", err)
		}
	}
	return nil
}

// ParseRegistry parses the generals registry file and returns Info entries.
func ParseRegistry(registryPath string) ([]Info, error) {
	f, err := os.Open(registryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening registry %s: %w", registryPath, err)
	}
	defer f.Close()

	var mates []Info
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		parts := strings.SplitN(rest, " - ", 2)
		if len(parts) < 1 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		if id == "" {
			continue
		}
		entry := Info{ID: id}

		if len(parts) >= 2 {
			metaPart := parts[1]
			if idx := strings.LastIndex(metaPart, "("); idx >= 0 {
				meta := metaPart[idx+1:]
				if endIdx := strings.LastIndex(meta, ")"); endIdx >= 0 {
					meta = meta[:endIdx]
				}
				entry.Home = extractMetaValue(meta, "home:")
				entry.Scope = extractMetaValue(meta, "scope:")
				entry.Project = extractMetaValue(meta, "projects:")
				entry.Added = extractMetaValue(meta, "added:")
			}
		}

		mates = append(mates, entry)
	}
	return mates, scanner.Err()
}

func extractMetaValue(meta, key string) string {
	parts := strings.Split(meta, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, key) {
			v := strings.TrimSpace(strings.TrimPrefix(p, key))
			return v
		}
	}
	return ""
}

// List returns all registered captains by reading the authoritative registry.
func List(parentHome string) ([]Info, error) {
	return ParseRegistry(RegistryPath(parentHome))
}

// --- Launch (session-backed) ---

// buildLaunchArgs returns the harness binary name and argument list for a captain launch.
// Matches the verified pi captain shape: cwd at home + prompt bytes only.
// No shell-expression prompt, no project-path argv, no "--" separator.
func buildLaunchArgs(captainHome, h, parentHome string) (string, []string, error) {
	adapter, ok := harness.GetAdapter(h)
	if !ok {
		return "", nil, fmt.Errorf("captain launch: harness %q is not a verified harness", h)
	}
	contract := adapter.CaptainLaunch
	if !contract.Supported {
		return "", nil, fmt.Errorf("captain launch: harness %q does not have a verified captain launch contract", h)
	}
	if !contract.CwdAtHome || !contract.PromptArg {
		return "", nil, fmt.Errorf("captain launch: harness %q has an incomplete captain launch contract", h)
	}
	if contract.ProjectArg {
		return "", nil, fmt.Errorf("captain launch: harness %q must not pass a project path arg", h)
	}

	charter, err := os.ReadFile(filepath.Join(captainHome, "AGENTS.md"))
	if err != nil {
		return "", nil, fmt.Errorf("reading captain charter: %w", err)
	}

	// Model/effort: config/captain-harness multi-token line, then config/model.
	prof, _ := harness.CaptainProfileFromHome(parentHome)
	args := []string{}
	if prof.Model != "" && adapter.LaunchTemplate.ModelFlag != "" {
		args = append(args, adapter.LaunchTemplate.ModelFlag, prof.Model)
	}
	if prof.Effort != "" && adapter.LaunchTemplate.EffortFlag != "" {
		args = append(args, adapter.LaunchTemplate.EffortFlag, prof.Effort)
	}
	args = append(args, adapter.LaunchTemplate.ExtraArgs...)
	// Pi captain homes get project-local integrate + captain-compat extensions via -e.
	if adapter.Name == "pi" {
		for _, name := range captainPiExtensionNames {
			path := filepath.Join(captainHome, ".pi", "extensions", name)
			if _, err := os.Stat(path); err == nil {
				args = append(args, "-e", path)
			}
		}
	}
	if contract.Separator != "" {
		args = append(args, contract.Separator)
	}
	args = append(args, string(charter))

	return adapter.Name, args, nil
}

func refuseNestedCaptainLaunch(parentHome string) error {
	if os.Getenv("MUNSU_ROLE") == "captain" {
		return fmt.Errorf("captains cannot launch other captains; spawn soldiers in their own home instead")
	}
	markerPath := filepath.Join(parentHome, ProvenanceMarkerName)
	if _, err := os.Stat(markerPath); err == nil {
		if _, validateErr := ValidateProvenance(parentHome); validateErr != nil {
			return fmt.Errorf("active home has invalid captain provenance: %w", validateErr)
		}
		return fmt.Errorf("captain home %s cannot launch another captain", parentHome)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking active home provenance: %w", err)
	}
	return nil
}

// Launch starts a captain using a session-backed endpoint.
// It validates provenance, resolves the harness, creates a new window via
// the session backend, sends a shell-safe launch script, then writes task
// meta with kind=captain and endpoint metadata only after launch succeeds.
func Launch(captainHome, parentHome string) error {
	if err := refuseNestedCaptainLaunch(parentHome); err != nil {
		return err
	}
	if _, err := ValidateProvenance(captainHome); err != nil {
		return fmt.Errorf("provenance validation failed for %s: %w", captainHome, err)
	}

	// Pre-launch bootstrap: push inherited config, then local FF.
	if err := ConfigPush(parentHome, captainHome); err != nil {
		return fmt.Errorf("pre-launch config-push: %w", err)
	}
	if _, _, err := safeFF(captainHome, parentHome); err != nil {
		// Non-git captain homes proceed; only fail when the home is a real clone.
		if _, stErr := os.Stat(filepath.Join(captainHome, ".git")); stErr == nil {
			return fmt.Errorf("pre-launch fast-forward: %w", err)
		}
	}

	h, err := harness.Captain(parentHome)
	if err != nil {
		return fmt.Errorf("resolving captain harness: %w", err)
	}

	binName, args, err := buildLaunchArgs(captainHome, h, parentHome)
	if err != nil {
		return err
	}

	bk, bkName, err := newSessionBackend(parentHome)
	if err != nil {
		return fmt.Errorf("resolving session backend: %w", err)
	}

	markerID, err := ValidateProvenance(captainHome)
	if err != nil {
		return fmt.Errorf("revalidating captain provenance: %w", err)
	}
	canonicalCaptainHome, err := canonicalHome(captainHome)
	if err != nil {
		return fmt.Errorf("canonicalizing captain home: %w", err)
	}

	containerLabel := hometag.WorkspaceTag(canonicalCaptainHome)
	if hb, ok := bk.(*session.HerdrBackend); ok {
		hb.Cwd = canonicalCaptainHome
	}
	windowID, err := bk.NewWindow(containerLabel, "mu-captain-"+markerID)
	if err != nil {
		return fmt.Errorf("creating captain window: %w", err)
	}

	binPath, err := lookPath(binName)
	if err != nil {
		bk.Teardown(windowID)
		return fmt.Errorf("%s harness not found on PATH: %w", binName, err)
	}

	// Build and send shell-safe launch script.
	cmdLine, err := launchCmd(binPath, args, canonicalCaptainHome)
	if err != nil {
		bk.Teardown(windowID)
		return fmt.Errorf("building launch script: %w", err)
	}
	if err := bk.SendKeys(windowID, cmdLine); err != nil {
		bk.Teardown(windowID)
		return fmt.Errorf("sending launch command: %w", err)
	}

	// Persist task meta only after successful launch.
	meta := map[string]string{
		"kind":    "captain",
		"home":    canonicalCaptainHome,
		"window":  windowID,
		"backend": bkName,
		"harness": h,
		"sm_id":   markerID,
	}

	if me, ok := bk.(session.BackendMetaExtras); ok {
		for k, v := range me.MetaExtras() {
			meta[k] = v
		}
	}

	taskID := taskIDForCaptain(markerID)
	if err := task.WriteMeta(parentHome, taskID, meta); err != nil {
		bk.Teardown(windowID)
		return fmt.Errorf("writing captain task meta: %w", err)
	}

	fmt.Printf("Launched captain %s (window=%s, harness=%s) in %s\n",
		markerID, windowID, binName, captainHome)
	return nil
}

// inFlightSoldierIDs returns task ids in captainHome/state with kind ship|scout.
func inFlightSoldierIDs(captainHome string) ([]string, error) {
	stateDir := filepath.Join(captainHome, "state")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".meta") || strings.HasPrefix(name, ".") {
			continue
		}
		id := strings.TrimSuffix(name, ".meta")
		meta, err := task.ReadMeta(captainHome, id)
		if err != nil {
			continue
		}
		kind := meta["kind"]
		if kind == "ship" || kind == "scout" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// --- Retire ---

// Retire tears down a captain using its session-backed endpoint.
// It reads task meta, validates kind/sm_id/home before any action,
// then signals the endpoint via the session backend. Errors from backend
// operations (SendKeys, Teardown) are returned — never silently swallowed.
// Without force, refuse when the captain home still has in-flight soldiers
// (state/*.meta with kind ship|scout). force skips that gate.
// removeHome=true removes the captain home directory after teardown.
// On success, the captain is unregistered from parent data/captains.md.
func Retire(captainHome, parentHome string, removeHome, force bool) error {
	markerID, err := ValidateProvenance(captainHome)
	if err != nil {
		return fmt.Errorf("refusing to retire unowned home %s: %w", captainHome, err)
	}
	canonicalCaptainHome, err := canonicalHome(captainHome)
	if err != nil {
		return fmt.Errorf("refusing to retire home with ambiguous identity %s: %w", captainHome, err)
	}

	if !force {
		inFlight, err := inFlightSoldierIDs(captainHome)
		if err != nil {
			return fmt.Errorf("refusing to retire: cannot scan captain home for in-flight soldiers: %w", err)
		}
		if len(inFlight) > 0 {
			return fmt.Errorf("refusing to retire captain %s: %d in-flight soldier(s) (%s); use --force to override",
				markerID, len(inFlight), strings.Join(inFlight, ", "))
		}
	}

	taskID := taskIDForCaptain(markerID)
	meta, metaErr := task.ReadMeta(parentHome, taskID)

	if metaErr == nil {
		// Validate meta fields before use.
		if meta["kind"] != "captain" {
			return fmt.Errorf("refusing to retire: task meta kind=%q, expected \"captain\"", meta["kind"])
		}
		if meta["sm_id"] != markerID {
			return fmt.Errorf("refusing to retire: task meta sm_id=%q does not match captain marker id %q", meta["sm_id"], markerID)
		}
		if meta["home"] != canonicalCaptainHome {
			return fmt.Errorf("refusing to retire: task meta home=%q does not match canonical captain home %q", meta["home"], canonicalCaptainHome)
		}

		windowID := meta["window"]
		if windowID == "" {
			return fmt.Errorf("refusing to retire: no window in task meta for captain %s", markerID)
		}

		bk, _, bkErr := session.BackendForTask(parentHome, meta)
		if bkErr != nil {
			return fmt.Errorf("refusing to retire: cannot resolve backend for captain %s: %w", markerID, bkErr)
		}

		if bk.Alive(windowID) {
			if sendErr := bk.SendKeys(windowID, "/quit"); sendErr != nil {
				return fmt.Errorf("failed to send /quit to captain %s: %w", markerID, sendErr)
			}
			fmt.Printf("  sent /quit to %s\n", markerID)
			time.Sleep(500 * time.Millisecond)
			if bk.Alive(windowID) {
				if tdErr := bk.Teardown(windowID); tdErr != nil {
					return fmt.Errorf("failed to teardown captain %s window: %w", markerID, tdErr)
				}
			}
		} else {
			if tdErr := bk.Teardown(windowID); tdErr != nil {
				return fmt.Errorf("failed to teardown captain %s window: %w", markerID, tdErr)
			}
		}
		// Clear parent task meta so husk prune and fleet snapshot stop treating this
		// captain as live. Status log is retained as historical return-channel evidence.
		metaPath := filepath.Join(parentHome, "state", taskID+".meta")
		if rmErr := os.Remove(metaPath); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("failed to remove captain task meta %s: %w", taskID, rmErr)
		}
		fmt.Printf("  cleared parent meta for %s\n", taskID)
	} else {
		// Provenance exists but no meta — captain was never launched.
		fmt.Printf("  captain %s has no task meta (never launched)\n", markerID)
	}

	if err := Unregister(parentHome, markerID); err != nil {
		return fmt.Errorf("unregistering captain %s: %w", markerID, err)
	}
	fmt.Printf("  unregistered %s from parent registry\n", markerID)

	if removeHome {
		if err := os.RemoveAll(captainHome); err != nil {
			return fmt.Errorf("removing captain home %s: %w", captainHome, err)
		}
		fmt.Printf("Retired and removed captain home %s\n", captainHome)
	} else {
		fmt.Printf("Retired captain at %s (home retained)\n", captainHome)
	}

	return nil
}

// --- Handoff ---

// Handoff moves backlog items from the parent home to a captain atomically.
// All requested keys must preclassify as queued before the command runs.
// extractTaskStateFromShow parses the state field from tasks-axi show output.
// Returns empty string if not found.
func extractTaskStateFromShow(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "state:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "state:"))
		}
	}
	return ""
}

// isTasksAxiBackend checks whether config/backlog-backend is set to tasks-axi or unset.
// Override in tests.
var isTasksAxiBackend = func(parentHome string) bool {
	val, err := config.Get(parentHome, "backlog-backend")
	if err != nil {
		// Config key not found — default is tasks-axi.
		return true
	}
	return val == "tasks-axi"
}

// Handoff moves backlog items from the parent home to a captain atomically.
// All requested keys must preclassify as queued before the command runs.
// Tasks-axi mv is the only supported backend. On failure, both files remain
// unchanged (atomic via tasks-axi mv).
// Validates canonical captain destination and uses absolute --file paths.
func Handoff(parentHome, captainHome string, itemKeys []string) error {
	if _, err := ValidateProvenance(captainHome); err != nil {
		return fmt.Errorf("refusing handoff to unmarked home %s: %w", captainHome, err)
	}
	// Validate canonical captain home path.
	absSM, err := filepath.Abs(captainHome)
	if err != nil {
		return fmt.Errorf("resolving captain home: %w", err)
	}
	absParent, err := filepath.Abs(parentHome)
	if err != nil {
		return fmt.Errorf("resolving parent home: %w", err)
	}
	if absSM == absParent {
		return fmt.Errorf("refusing handoff: destination is parent home itself")
	}

	// Ensure backlog backend explicitly supports tasks-axi operations.
	if !isTasksAxiBackend(parentHome) {
		return fmt.Errorf("backlog backend is not set to tasks-axi — handoff requires tasks-axi")
	}

	// Build absolute backlog paths.
	srcBacklog := filepath.Join(parentHome, "data", "backlog.md")
	dstBacklog := filepath.Join(captainHome, "data", "backlog.md")

	// Create destination backlog directory if needed.
	os.MkdirAll(filepath.Dir(dstBacklog), 0755)

	path, err := lookPath("tasks-axi")
	if err != nil {
		return fmt.Errorf("tasks-axi not found: %w", err)
	}

	// Preclassify: verify all requested keys exist and are queued.
	for _, key := range itemKeys {
		showOut, showErr := exec.Command(path, "show", key, "--file", srcBacklog).CombinedOutput()
		if showErr != nil {
			return fmt.Errorf("handoff: key %s not found in source backlog: %s", key, strings.TrimSpace(string(showOut)))
		}

		state := extractTaskStateFromShow(string(showOut))
		if state == "" {
			return fmt.Errorf("handoff: key %s has no parseable state — only queued items may be handed off", key)
		}
		if state != "queued" {
			return fmt.Errorf("handoff: key %s has state %q, only queued items may be handed off", key, state)
		}
	}

	// All keys verified as queued. Run tasks-axi mv atomically with absolute paths.
	cliArgs := []string{"mv"}
	cliArgs = append(cliArgs, itemKeys...)
	cliArgs = append(cliArgs, "--to", dstBacklog)
	cliArgs = append(cliArgs, "--file", srcBacklog)

	cmd := exec.Command(path, cliArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tasks-axi mv failed (both files unchanged): %w", err)
	}

	for _, key := range itemKeys {
		fmt.Printf("handed-off %s\n", key)
	}
	return nil
}

// --- Config inheritance ---

func getInheritableList() []string {
	env := os.Getenv("MUNSU_INHERITABLE_CONFIG")
	if env != "" {
		return strings.Split(env, ":")
	}
	return []string{"soldier-harness", "soldier-dispatch.json", "backlog-backend"}
}

func isInheritable(name string, list []string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil {
		if info, statErr := os.Stat(path); statErr == nil && string(existing) == string(data) && info.Mode().Perm() == mode.Perm() {
			return nil
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".munsu-inherit-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("setting temp file mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// resolveDeepestAncestor resolves symlinks on the deepest existing ancestor path,
// then appends the non-existent suffix. This avoids EvalSymlinks failure on
// paths with non-existent leaf segments while still catching symlink escapes.
func resolveDeepestAncestor(path string) (string, error) {
	candidate := path
	for {
		_, err := os.Stat(candidate)
		if err == nil {
			// Found existing ancestor. Resolve symlinks on it.
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", err
			}
			abs, err := filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			// Append remaining suffix.
			suffix, _ := filepath.Rel(candidate, path)
			if suffix != "." {
				return filepath.Join(abs, suffix), nil
			}
			return abs, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			// Reached root — path is entirely non-existent.
			return filepath.Abs(path)
		}
		candidate = parent
	}
}

// isSafeConfigPath checks that dst is safely contained within captainHome
// and does not symlink-escape into parentHome.
func isSafeConfigPath(dst, parentHome, captainHome string) bool {
	smCanon, err := canonicalHome(captainHome)
	if err != nil {
		return false
	}

	// Canonicalize dst via deepest-ancestor resolution to handle non-existent paths.
	canonDst, err := resolveDeepestAncestor(dst)
	if err != nil {
		return false
	}

	// Must be under smCanon (filepath.Rel containment).
	rel, err := filepath.Rel(smCanon, canonDst)
	if err != nil {
		return false
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}

	// Must NOT be under parentHome (prevents symlink escape from smHome to parent).
	parentCanon, err := canonicalHome(parentHome)
	if err != nil {
		return false
	}
	parentRel, err := filepath.Rel(parentCanon, canonDst)
	if err == nil && !strings.HasPrefix(parentRel, "..") && !filepath.IsAbs(parentRel) {
		// dst is under parentHome. Only allowed if also under smCanon.
		if !strings.HasPrefix(canonDst, smCanon+string(filepath.Separator)) && canonDst != smCanon {
			return false
		}
	}

	return true
}

func pushConfigFile(parentHome, captainHome, name string, logFn func(action, name string)) error {
	src := filepath.Join(parentHome, "config", name)
	dst := filepath.Join(captainHome, "config", name)

	if !isSafeConfigPath(dst, parentHome, captainHome) {
		return fmt.Errorf("config path %s escapes captain container — refuse", dst)
	}
	// Check git tracking BEFORE write — tracked destination must remain byte-identical.
	if isGitTracked(filepath.Dir(dst), filepath.Base(dst)) {
		return fmt.Errorf("inheritance destination %s is tracked in captain git — must be gitignored", name)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logFn("skipped", name+" — "+err.Error())
		return nil
	}

	if err := atomicWriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	logFn("pushed", name)

	return nil
}

func pushSharedFile(parentHome, captainHome string, logFn func(action, name string)) error {
	src := filepath.Join(parentHome, "data", "general-shared.md")
	dst := filepath.Join(captainHome, "data", "general-shared.md")

	if !isSafeConfigPath(dst, parentHome, captainHome) {
		return fmt.Errorf("general-shared.md path escapes captain container — refuse")
	}
	// Check git tracking BEFORE write.
	if isGitTracked(filepath.Dir(dst), filepath.Base(dst)) {
		return fmt.Errorf("general-shared.md is tracked in captain git — must be gitignored")
	}

	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logFn("skipped", "general-shared.md — "+err.Error())
		return nil
	}

	if err := atomicWriteFile(dst, data, 0444); err != nil {
		return fmt.Errorf("writing general-shared.md: %w", err)
	}
	logFn("pushed", "general-shared.md")

	return nil
}

// pushProjectsRegistry copies the parent's data/projects.md into the captain
// home. Entries keep absolute path descriptions so ResolveRepoPath works
// without cloning into the captain projects/ tree (no auto-clone).
func pushProjectsRegistry(parentHome, captainHome string, logFn func(action, name string)) error {
	src := project.RegistryPath(parentHome)
	dst := project.RegistryPath(captainHome)

	if !isSafeConfigPath(dst, parentHome, captainHome) {
		return fmt.Errorf("projects.md path escapes captain container — refuse")
	}
	if isGitTracked(filepath.Dir(dst), filepath.Base(dst)) {
		return fmt.Errorf("projects.md is tracked in captain git — must be gitignored")
	}

	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			// Mirror deletion: parent has no registry → remove captain copy if present.
			if _, stErr := os.Stat(dst); stErr == nil {
				if err := os.Remove(dst); err != nil {
					logFn("delete-failed", "projects.md — "+err.Error())
					return fmt.Errorf("mirror deletion: removing projects.md: %w", err)
				}
				logFn("deleted", "projects.md")
			}
			return nil
		}
		logFn("skipped", "projects.md — "+err.Error())
		return nil
	}

	// Validate parent registry before writing so captains never inherit a corrupt file.
	if _, err := project.ListFromFile(src); err != nil {
		return fmt.Errorf("reading parent projects.md: %w", err)
	}

	if err := atomicWriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("writing projects.md: %w", err)
	}
	logFn("pushed", "projects.md")
	return nil
}

func isGitTracked(dir, name string) bool {
	out, err := exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", name).CombinedOutput()
	return err == nil && len(out) > 0
}

// ConfigPush copies inheritable config from the parent home to the captain,
// mirrors deletions, pushes data/general-shared.md and data/projects.md,
// and logs actions.
func ConfigPush(parentHome, captainHome string) error {
	if _, err := ValidateProvenance(captainHome); err != nil {
		return fmt.Errorf("refusing config-push to unmarked home %s: %w", captainHome, err)
	}

	inheritable := getInheritableList()

	logPath := filepath.Join(captainHome, "state", "config-push.log")
	os.MkdirAll(filepath.Dir(logPath), 0755)
	logF, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening config-push.log: %w", err)
	}
	defer logF.Close()

	ts := time.Now().UTC().Format(time.RFC3339)
	log := func(action, name string) {
		line := fmt.Sprintf("%s\t%s\t%s\n", ts, action, name)
		logF.WriteString(line)
		fmt.Printf("  %s %s\n", action, name)
	}

	// Mirror deletions: remove inheritable files in captain that are absent in parent.
	// Validate safety BEFORE any deletion. Return error on unsafe/tracked paths.
	configDir := filepath.Join(captainHome, "config")
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if !isInheritable(name, inheritable) {
				continue
			}
			srcPath := filepath.Join(parentHome, "config", name)
			if _, err := os.Stat(srcPath); os.IsNotExist(err) {
				dstPath := filepath.Join(configDir, name)
				if !isSafeConfigPath(dstPath, parentHome, captainHome) {
					return fmt.Errorf("mirror deletion: %s path escapes captain container — refuse", name)
				}
				if isGitTracked(configDir, name) {
					return fmt.Errorf("mirror deletion: %s is tracked in captain git — must be gitignored", name)
				}
				if err := os.Remove(dstPath); err != nil {
					log("delete-failed", name+" — "+err.Error())
					return fmt.Errorf("mirror deletion: removing %s: %w", name, err)
				}
				log("deleted", name)
			}
		}
	}

	// Mirror deletion for general-shared.md — validate before mutation.
	sharedDst := filepath.Join(captainHome, "data", "general-shared.md")
	if _, err := os.Stat(filepath.Join(parentHome, "data", "general-shared.md")); os.IsNotExist(err) {
		if _, err := os.Stat(sharedDst); err == nil {
			if !isSafeConfigPath(sharedDst, parentHome, captainHome) {
				return fmt.Errorf("mirror deletion: general-shared.md path escapes captain container — refuse")
			}
			if isGitTracked(filepath.Dir(sharedDst), filepath.Base(sharedDst)) {
				return fmt.Errorf("mirror deletion: general-shared.md is tracked in captain git — must be gitignored")
			}
			if err := os.Remove(sharedDst); err != nil {
				log("delete-failed", "general-shared.md — "+err.Error())
				return fmt.Errorf("mirror deletion: removing general-shared.md: %w", err)
			}
			log("deleted", "general-shared.md")
		}
	}
	for _, name := range inheritable {
		if err := pushConfigFile(parentHome, captainHome, name, log); err != nil {
			return err
		}
	}

	if err := pushSharedFile(parentHome, captainHome, log); err != nil {
		return err
	}

	if err := pushProjectsRegistry(parentHome, captainHome, log); err != nil {
		return err
	}

	if err := EnsureCaptainPiExtensions(captainHome); err != nil {
		return fmt.Errorf("installing captain pi extensions: %w", err)
	}

	return nil
}

// --- Safe local fast-forward ---

// normalizeGitRemote maps equivalent GitHub remote URLs to a canonical form.
func normalizeGitRemote(remote string) string {
	// Strip protocol prefix for comparison.
	for _, prefix := range []string{"https://", "git@", "ssh://", "git://"} {
		remote = strings.TrimPrefix(remote, prefix)
	}
	// Map github.com SSH (git@github.com:user/repo) and HTTPS to same form.
	remote = strings.ReplaceAll(remote, ":", "/")
	remote = strings.TrimSuffix(remote, ".git")
	return strings.ToLower(remote)
}

// safeFF performs a LOCAL-only fast-forward of a captain clone to the
// parent's already-local default-branch commit. Verified: same canonical
// remote origin, correct branch/detached state, clean tree (ignoring only
// marker and local inherited paths), ancestor relationship, then git merge --ff-only.
func safeFF(captainHome, parentHome string) (before, after string, err error) {
	// Verify same canonical remote origin (allows independent clones, HTTPS/SSH equivalence).
	parentRemote, err := gitRun("-C", parentHome, "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("parent remote origin: %w", err)
	}
	smRemote, err := gitRun("-C", captainHome, "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("captain remote origin: %w", err)
	}
	if normalizeGitRemote(parentRemote) != normalizeGitRemote(smRemote) {
		return "", "", fmt.Errorf("captain remote %q differs from parent remote %q (canonical: %q vs %q)",
			smRemote, parentRemote, normalizeGitRemote(smRemote), normalizeGitRemote(parentRemote))
	}

	// Resolve default branch via origin/HEAD symbolic ref. No fallback.
	symRef, err := gitRun("-C", parentHome, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", "", fmt.Errorf("parent origin/HEAD symbolic ref missing — no default branch detected: %w", err)
	}
	// symRef looks like "refs/remotes/origin/main" — extract branch name.
	remoteRefParts := strings.SplitN(symRef, "/", 4)
	if len(remoteRefParts) < 4 || remoteRefParts[0] != "refs" || remoteRefParts[1] != "remotes" || remoteRefParts[2] != "origin" {
		return "", "", fmt.Errorf("unexpected origin/HEAD symbolic ref format: %q", symRef)
	}
	defaultBranch := remoteRefParts[3]

	// Verify the local branch ref exists.
	localRef := "refs/heads/" + defaultBranch
	if _, err := gitRun("-C", parentHome, "rev-parse", "--verify", localRef); err != nil {
		return "", "", fmt.Errorf("default branch %q (%s) does not exist locally", defaultBranch, localRef)
	}

	// Resolve the commit that the local default branch points to.
	defaultCommit, err := gitRun("-C", parentHome, "rev-parse", localRef)
	if err != nil {
		return "", "", fmt.Errorf("resolving default branch commit: %w", err)
	}

	// Branch check — captain must be on default branch or detached HEAD.
	smBranch, err := gitRun("-C", captainHome, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading captain branch: %w", err)
	}
	if smBranch != "HEAD" && smBranch != "" && smBranch != defaultBranch {
		return "", "", fmt.Errorf("captain is on branch %q, expected %q or detached HEAD", smBranch, defaultBranch)
	}

	// Clean check — reject ALL tracked changes; allow only gitignored untracked files.
	statusOut, err := gitRun("-C", captainHome, "status", "--porcelain")
	if err != nil {
		return "", "", fmt.Errorf("captain git status: %w", err)
	}
	if statusOut != "" {
		for _, line := range strings.Split(statusOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			xy := line[:2]
			if xy == "??" {
				// Untracked — allow only if git check-ignore confirms gitignored.
				path := strings.TrimSpace(line[2:])
				if _, err := gitRun("-C", captainHome, "check-ignore", "-q", "--", path); err == nil {
					continue // gitignored, OK
				}
				return "", "", fmt.Errorf("captain home %s has unignored untracked file: %s", captainHome, path)
			}
			// Any non-space character means tracked change (staged or unstaged).
			if xy[0] != ' ' || xy[1] != ' ' {
				return "", "", fmt.Errorf("captain home %s has tracked changes", captainHome)
			}
		}
	}

	before, err = gitRun("-C", captainHome, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading captain HEAD: %w", err)
	}

	// Check ancestry.
	mergeBase, err := gitRun("-C", captainHome, "merge-base", before, defaultCommit)
	if err != nil {
		return "", "", fmt.Errorf("merge-base failed: %w", err)
	}
	if mergeBase != before {
		return "", "", fmt.Errorf("captain %s is not an ancestor of parent default-branch commit %s — diverged or unequal history", before[:8], defaultCommit[:8])
	}

	if before == defaultCommit {
		return before, before, nil
	}

	fmt.Printf("  %s: fast-forward %s → %s\n", filepath.Base(captainHome), before[:8], defaultCommit[:8])

	_, err = gitRun("-C", captainHome, "merge", "--ff-only", defaultCommit)
	if err != nil {
		return "", "", fmt.Errorf("git merge --ff-only failed: %w", err)
	}

	after, err = gitRun("-C", captainHome, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading captain HEAD after ff: %w", err)
	}

	return before, after, nil
}

// --- Converge ---

// ConvergeLockPath returns the path to the converge lock.
func ConvergeLockPath(parentHome string) string {
	return filepath.Join(parentHome, "state", ConvergeLockName)
}

// acquireExclusiveLock creates and acquires an exclusive file lock using flock
// with LOCK_NB (fail-fast). If another process holds the lock, it returns an
// error immediately (never removes the lock file). Release verifies inode
// identity (os.SameFile) AND random token match before unlinking.
func acquireExclusiveLock(lockPath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	// Exclusive flock with LOCK_NB — fail fast, never block.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		// NEVER remove lockPath on failed acquisition — that would unlink
		// the owner's lock and permit a third party to acquire.
		return nil, fmt.Errorf("converge lock is held by another process — try again later: %w", err)
	}

	// Generate a cryptographically random token for generation-safe release.
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("generating random token: %w", err)
	}
	tokenHex := fmt.Sprintf("%x", token)

	if _, err := fmt.Fprintf(f, "%s\n", tokenHex); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("writing token to lock: %w", err)
	}
	f.Sync()

	released := false
	return func() {
		if released {
			return
		}
		released = true
		defer func() {
			syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
		}()

		// Verify inode identity: our fd must still point to the same file.
		fdStat, err := f.Stat()
		if err != nil {
			return
		}
		pathStat, err := os.Stat(lockPath)
		if err != nil {
			return
		}
		if !os.SameFile(fdStat, pathStat) {
			return // lock file was replaced by a different generation
		}

		// Verify token still matches — never remove a newer generation.
		data, err := os.ReadFile(lockPath)
		if err != nil {
			return
		}
		if strings.TrimSpace(string(data)) != tokenHex {
			return // token changed — different generation
		}

		os.Remove(lockPath)
	}, nil
}

// nudgeMarkerPath returns the path for a pending nudge marker.
func nudgeMarkerPath(parentHome, smID string) string {
	return filepath.Join(parentHome, "state", NudgePendingDir, smID+".pending")
}

// writeNudgeMarker creates a pending nudge marker with full metadata.
// Written before sending, removed only after successful SendKeys.
func writeNudgeMarker(parentHome, smID, smHome, commit, instructions, message string) error {
	dir := filepath.Join(parentHome, "state", NudgePendingDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	content := fmt.Sprintf("id=%s\nhome=%s\ncommit=%s\ninstructions=%s\nmessage=%s\n",
		smID, smHome, commit, instructions, message)
	return os.WriteFile(nudgeMarkerPath(parentHome, smID), []byte(content), 0644)
}

// readNudgeMarker reads and returns the fields from a pending nudge marker.
func readNudgeMarker(parentHome, smID string) (map[string]string, error) {
	data, err := os.ReadFile(nudgeMarkerPath(parentHome, smID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			result[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return result, nil
}

// removeNudgeMarker deletes a pending nudge marker.
func removeNudgeMarker(parentHome, smID string) {
	os.Remove(nudgeMarkerPath(parentHome, smID))
}

// Converge performs a locked convergence sweep over registered captains.
// Order: lock, validate registry/provenance, flush send outbox, retry pending
// nudges, safe ff, inheritance push, ownership-backed backend Alive check, and
// reread nudge only if instruction surface advanced.
func Converge(parentHome string, registered []Info) error {
	release, err := convergeLockAcquire(parentHome)
	if err != nil {
		return fmt.Errorf("acquiring converge lock: %w", err)
	}
	defer release()

	if len(registered) == 0 {
		return nil
	}

	var errs []string
	for _, sm := range registered {
		if sm.Home == "" {
			errs = append(errs, fmt.Sprintf("%s: missing home path", sm.ID))
			continue
		}

		markerID, err := ValidateProvenance(sm.Home)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: provenance validation failed: %v", sm.ID, err))
			continue
		}
		if markerID != sm.ID {
			errs = append(errs, fmt.Sprintf("%s: marker id %q does not match registry id %q", sm.ID, markerID, sm.ID))
			continue
		}

		// Deliver durable marked sends queued while the pane was dead.
		if flushErr := FlushSendOutbox(parentHome, sm); flushErr != nil {
			errs = append(errs, flushErr.Error())
		}

		// Retry existing nudge markers even without a new FF.
		if nudgeErr := retryNudge(parentHome, sm); nudgeErr != nil {
			errs = append(errs, nudgeErr.Error())
		}

		// Safe local fast-forward.
		before, after, ffErr := safeFF(sm.Home, parentHome)
		if ffErr != nil {
			errs = append(errs, fmt.Sprintf("%s: safe ff failed: %v", sm.ID, ffErr))
		} else if before != after {
			fmt.Printf("  %s: fast-forwarded %s → %s\n", sm.ID, before[:8], after[:8])
			if hasSurfaceDiff(sm.Home, before, after) {
				printGitContentDiff(sm.Home, before, after)
				digest, digestErr := instructionSurfaceDigest(sm.Home, after)
				if digestErr != nil {
					errs = append(errs, fmt.Sprintf("%s: computing instruction digest: %v", sm.ID, digestErr))
					continue
				}
				msg := fmt.Sprintf("instruction surface changed in %s", after[:8])
				if wErr := writeNudgeMarker(parentHome, sm.ID, sm.Home, after, digest, msg); wErr != nil {
					errs = append(errs, fmt.Sprintf("%s: writing nudge marker: %v", sm.ID, wErr))
				} else {
					// If alive, immediately send.
					if err := sendNudge(parentHome, sm); err != nil {
						errs = append(errs, err.Error())
					}
				}
			}
		}

		// Inheritance push.
		if err := ConfigPush(parentHome, sm.Home); err != nil {
			errs = append(errs, fmt.Sprintf("%s: config-push failed: %v", sm.ID, err))
		}

		// Ownership-backed backend Alive check.
		alive, aliveErr := checkAliveViaBackend(parentHome, sm)
		if aliveErr != nil {
			errs = append(errs, fmt.Sprintf("%s: alive check failed: %v", sm.ID, aliveErr))
			continue
		}
		_ = alive
	}

	if len(errs) > 0 {
		return fmt.Errorf("converge completed with %d error(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}

// --- Recover ---

// RecoverOutcome is the disposition of one captain during a recover sweep.
type RecoverOutcome string

const (
	// RecoverAlive: endpoint probed alive; no action taken.
	RecoverAlive RecoverOutcome = "alive"
	// RecoverSeeded: home present but never launched (no launch meta/window); no action.
	RecoverSeeded RecoverOutcome = "seeded"
	// RecoverRelaunched: endpoint was launched-but-dead and has been relaunched.
	RecoverRelaunched RecoverOutcome = "relaunched"
	// RecoverFailed: relaunch attempted and failed (e.g. unknown harness); see Error.
	RecoverFailed RecoverOutcome = "failed"
)

// RecoverEntry describes one captain's recover result.
type RecoverEntry struct {
	ID      string
	Home    string
	Outcome RecoverOutcome
	Error   string // non-empty when Outcome == RecoverFailed
}

// RecoverResult holds the aggregate outcome of a recover sweep.
type RecoverResult struct {
	Entries    []RecoverEntry
	Relaunched int
	Alive      int
	Seeded     int
	Failed     int
}

// String renders a human-readable summary for CLI output.
func (r *RecoverResult) String() string {
	if r == nil || len(r.Entries) == 0 {
		return "no captains registered"
	}
	var b strings.Builder
	for _, e := range r.Entries {
		switch e.Outcome {
		case RecoverAlive:
			fmt.Fprintf(&b, "  %s: alive\n", e.ID)
		case RecoverSeeded:
			fmt.Fprintf(&b, "  %s: seeded (not launched)\n", e.ID)
		case RecoverRelaunched:
			fmt.Fprintf(&b, "  %s: relaunched\n", e.ID)
		case RecoverFailed:
			fmt.Fprintf(&b, "  %s: FAILED: %s\n", e.ID, e.Error)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Recover probes each registered captain and relaunches launched-but-dead endpoints.
// It fails closed on unknown/unverified harnesses (Launch returns an error which is
// recorded on the entry) and continues with the remaining captains. Seeded-but-never-
// launched captains are reported but not launched. The sweep holds the converge lock so
// it does not race with an in-flight converge.
func Recover(parentHome string, registered []Info) (*RecoverResult, error) {
	res := &RecoverResult{}
	if len(registered) == 0 {
		return res, nil
	}

	release, err := convergeLockAcquire(parentHome)
	if err != nil {
		return res, fmt.Errorf("acquiring converge lock: %w", err)
	}
	defer release()

	for _, sm := range registered {
		entry := RecoverEntry{ID: sm.ID, Home: sm.Home}
		if sm.Home == "" {
			entry.Outcome = RecoverFailed
			entry.Error = "missing home path"
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}

		markerID, vErr := ValidateProvenance(sm.Home)
		if vErr != nil {
			entry.Outcome = RecoverFailed
			entry.Error = fmt.Sprintf("provenance validation failed: %v", vErr)
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}
		if markerID != sm.ID {
			entry.Outcome = RecoverFailed
			entry.Error = fmt.Sprintf("marker id %q does not match registry id %q", markerID, sm.ID)
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}

		alive, aliveErr := checkAliveViaBackend(parentHome, sm)
		if aliveErr != nil {
			// Backend resolution failure: cannot prove liveness, cannot safely relaunch.
			entry.Outcome = RecoverFailed
			entry.Error = fmt.Sprintf("alive check failed: %v", aliveErr)
			res.Failed++
			res.Entries = append(res.Entries, entry)
			continue
		}
		if alive {
			entry.Outcome = RecoverAlive
			res.Alive++
			res.Entries = append(res.Entries, entry)
			continue
		}

		// Not alive. Distinguish launched-but-dead (meta+window) from seeded-never-launched.
		taskID := taskIDForCaptain(sm.ID)
		meta, mErr := task.ReadMeta(parentHome, taskID)
		launched := false
		if mErr == nil && meta["kind"] == "captain" && meta["sm_id"] == sm.ID && meta["window"] != "" {
			launched = true
		}
		if !launched {
			entry.Outcome = RecoverSeeded
			res.Seeded++
			res.Entries = append(res.Entries, entry)
			continue
		}

		// Launched-but-dead: relaunch. Fail-closed on unknown harness inside Launch.
		if lErr := Launch(sm.Home, parentHome); lErr != nil {
			entry.Outcome = RecoverFailed
			entry.Error = lErr.Error()
			res.Failed++
		} else {
			entry.Outcome = RecoverRelaunched
			res.Relaunched++
		}
		res.Entries = append(res.Entries, entry)
	}
	return res, nil
}

// ProbeLiveness reports each registered captain as alive/dead/seeded/unknown without
// mutating anything. Used by session-start to surface dead endpoints. Never relaunches.
// Returns entries in registry order.
func ProbeLiveness(parentHome string, registered []Info) []LivenessProbe {
	if len(registered) == 0 {
		return nil
	}
	probes := make([]LivenessProbe, 0, len(registered))
	for _, sm := range registered {
		p := LivenessProbe{ID: sm.ID, Home: sm.Home}
		if sm.Home == "" {
			p.Status = "unknown"
			probes = append(probes, p)
			continue
		}
		if _, err := ValidateProvenance(sm.Home); err != nil {
			p.Status = "unknown"
			probes = append(probes, p)
			continue
		}
		// CaptainStatus: alive | dead | seeded | unknown.
		p.Status = fleetCaptainStatus(parentHome, sm.ID, sm.Home)
		probes = append(probes, p)
	}
	return probes
}

// LivenessProbe is one captain's non-mutating liveness reading.
type LivenessProbe struct {
	ID     string
	Home   string
	Status string // alive | dead | seeded | unknown
}

// fleetCaptainStatus is a package-level seam over fleet.CaptainStatus so this file does
// not import fleet (avoid cycles). Wired by the CLI layer via SetFleetCaptainStatus.
// Falls back to "unknown" when unwired.
var fleetCaptainStatus = func(parentHome, captainID, homeDir string) string {
	return "unknown"
}

// SetFleetCaptainStatus installs the fleet.CaptainStatus probe used by ProbeLiveness.
// Called from the CLI layer (internal/cli) to avoid an import cycle.
func SetFleetCaptainStatus(fn func(parentHome, captainID, homeDir string) string) {
	fleetCaptainStatus = fn
}

// checkAliveViaBackend checks if a captain is alive using the session backend.
// It reads task meta, validates kind/sm_id/home before use, and uses backend.Alive.
func checkAliveViaBackend(parentHome string, sm Info) (bool, error) {
	taskID := taskIDForCaptain(sm.ID)
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		return false, nil // not yet launched
	}

	if meta["kind"] != "captain" {
		return false, nil
	}
	if meta["sm_id"] != sm.ID {
		return false, nil
	}
	canonSM, err := canonicalHome(sm.Home)
	if err != nil {
		return false, fmt.Errorf("canonicalizing captain home: %w", err)
	}
	if meta["home"] != canonSM {
		return false, nil
	}

	windowID := meta["window"]
	if windowID == "" {
		return false, nil
	}

	bk, _, bkErr := backendForTask(parentHome, meta)
	if bkErr != nil {
		return false, fmt.Errorf("resolving backend: %w", bkErr)
	}

	return bk.Alive(windowID), nil
}

// instructionSurfaceDigest returns a deterministic digest of the tracked instruction
// surface at commit. Git object IDs bind the digest to exact file content and paths.
func instructionSurfaceDigest(home, commit string) (string, error) {
	tree, err := gitRun("-C", home, "ls-tree", "-r", "--full-tree", commit, "--",
		"AGENTS.md", "bin/", ".agents/skills/")
	if err != nil {
		return "", err
	}
	return sha256Content([]byte(tree)), nil
}

// hasSurfaceDiff reports whether the tracked instruction surface changed.
func hasSurfaceDiff(home, before, after string) bool {
	beforeDigest, err := instructionSurfaceDigest(home, before)
	if err != nil {
		return false
	}
	afterDigest, err := instructionSurfaceDigest(home, after)
	return err == nil && beforeDigest != afterDigest
}

// sendNudge sends a short re-read message to a captain via its
// session-backed endpoint. It reads task meta, validates endpoint
// identity, sends the message, removes the pending marker, and
// updates applied instruction identity only after success.
// On failure, the marker remains.
func sendNudge(parentHome string, sm Info) error {
	taskID := taskIDForCaptain(sm.ID)
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		return fmt.Errorf("%s: no task meta — marker remains", sm.ID)
	}

	// Validate endpoint meta before use.
	if meta["kind"] != "captain" {
		return fmt.Errorf("%s: meta kind=%q, expected captain — marker remains", sm.ID, meta["kind"])
	}
	if meta["sm_id"] != sm.ID {
		return fmt.Errorf("%s: meta sm_id=%q does not match — marker remains", sm.ID, meta["sm_id"])
	}
	canonSM, err := canonicalHome(sm.Home)
	if err != nil {
		return fmt.Errorf("%s: cannot canonicalize home — marker remains: %v", sm.ID, err)
	}
	if meta["home"] != canonSM {
		return fmt.Errorf("%s: meta home=%q does not match canonical home %q — marker remains", sm.ID, meta["home"], canonSM)
	}

	windowID := meta["window"]
	if windowID == "" {
		return fmt.Errorf("%s: no window in meta — marker remains", sm.ID)
	}

	bk, _, bkErr := session.BackendForTask(parentHome, meta)
	if bkErr != nil {
		return fmt.Errorf("%s: cannot resolve backend — marker remains: %v", sm.ID, bkErr)
	}

	if !bk.Alive(windowID) {
		return fmt.Errorf("%s: endpoint not alive — marker remains", sm.ID)
	}

	// Read pending marker to validate content before sending.
	marker, markerErr := readNudgeMarker(parentHome, sm.ID)
	if markerErr != nil || marker == nil {
		return fmt.Errorf("%s: no pending nudge marker — marker remains", sm.ID)
	}
	// Validate marker fields against registry and endpoint.
	if marker["id"] != sm.ID {
		return fmt.Errorf("%s: marker id=%q does not match registry id %q — marker remains", sm.ID, marker["id"], sm.ID)
	}
	canonMarkerHome, err := canonicalHome(marker["home"])
	if err != nil || canonMarkerHome != canonSM {
		return fmt.Errorf("%s: marker home=%q does not match canonical home %q — marker remains", sm.ID, marker["home"], canonSM)
	}
	if marker["commit"] == "" {
		return fmt.Errorf("%s: marker has empty commit — marker remains", sm.ID)
	}
	// Verify the marker binds to an exact commit and instruction surface.
	if _, err := gitRun("-C", sm.Home, "rev-parse", "--verify", marker["commit"]+"^{commit}"); err != nil {
		return fmt.Errorf("%s: marker commit %q is not a valid commit in captain repo — marker remains", sm.ID, marker["commit"])
	}
	expectedDigest, err := instructionSurfaceDigest(sm.Home, marker["commit"])
	if err != nil {
		return fmt.Errorf("%s: cannot compute marker instruction digest — marker remains: %v", sm.ID, err)
	}
	if marker["instructions"] != expectedDigest {
		return fmt.Errorf("%s: marker instruction digest does not match commit %s — marker remains", sm.ID, marker["commit"])
	}
	expectedMessage := fmt.Sprintf("instruction surface changed in %s", marker["commit"][:8])
	if marker["message"] != expectedMessage {
		return fmt.Errorf("%s: marker message %q does not match %q — marker remains", sm.ID, marker["message"], expectedMessage)
	}

	// SendKeys one short re-read message. Never send charter content.
	if err := bk.SendKeys(windowID, "/re-read-agents"); err != nil {
		return fmt.Errorf("%s: send failed — marker remains: %v", sm.ID, err)
	}

	// After successful SendKeys, update durable meta with actual applied
	// commit and deterministic digest BEFORE removing marker.
	meta["applied_commit"] = marker["commit"]
	meta["applied_digest"] = marker["instructions"]
	if metaErr := task.WriteMeta(parentHome, taskID, meta); metaErr != nil {
		return fmt.Errorf("%s: meta update failed after send (marker remains): %v", sm.ID, metaErr)
	}

	// Only remove marker after WriteMeta succeeded.
	removeNudgeMarker(parentHome, sm.ID)

	fmt.Printf("  %s: nudge sent (commit=%s, digest=%.12s...), marker cleared\n", sm.ID, marker["commit"][:8], marker["instructions"])
	return nil
}

// retryNudge checks for a pending parent-home nudge marker and attempts
// to resolve the endpoint and send the re-read message. On success,
// the marker is removed and applied instruction identity updated.
// On failure, the marker remains for the next converge cycle.
func retryNudge(parentHome string, sm Info) error {
	marker, err := readNudgeMarker(parentHome, sm.ID)
	if err != nil {
		return fmt.Errorf("%s: reading nudge marker: %v", sm.ID, err)
	}
	if marker == nil {
		return nil // no pending nudge
	}
	// Attempt to send. If successful, marker is removed by sendNudge.
	// On failure, marker remains.
	return sendNudge(parentHome, sm)
}

// printGitContentDiff prints the content diff for key files between two commits.
func printGitContentDiff(home, before, after string) {
	if before == after {
		return
	}
	for _, file := range []string{"AGENTS.md", "bin/", ".agents/skills/"} {
		diff, err := gitRun("-C", home, "diff", "--no-color", before, after, "--", file)
		if err == nil && diff != "" {
			fmt.Printf("    diff in %s:\n", file)
			for _, line := range strings.Split(diff, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
}
