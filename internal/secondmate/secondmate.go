// Package secondmate manages persistent domain supervisors (secondmates).
package secondmate

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
	"github.com/minhtri2710/munsu/internal/session"
	"github.com/minhtri2710/munsu/internal/task"
)

// ProvenanceMarkerName is the marker file written to a seeded secondmate home root.
const ProvenanceMarkerName = ".munsu-secondmate-home"

// ProvenanceVersion is the current provenance marker format version.
const ProvenanceVersion = "munsu-v2"

// ConvergeLockName is the converge-specific lock file under parent state.
const ConvergeLockName = ".secondmate-converge.lock"

// NudgePendingDir is the directory under parent state for pending nudge markers.
const NudgePendingDir = ".secondmate-nudge-pending"

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
var launchCmd = func(binPath string, args []string, secondmateHome string) (string, error) {
	return buildLaunchScript(binPath, args, secondmateHome)
}

// --- Helpers ---

// shQuote wraps s in single quotes, escaping any embedded single quotes
// for safe shell evaluation.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// buildLaunchScript writes a bash launch script into the secondmate home and
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
	b.WriteString("export MUNSU_ROLE=secondmate\n")
	b.WriteString("exec ")
	b.WriteString(shQuote(binPath))
	for _, arg := range args {
		b.WriteString(" ")
		b.WriteString(shQuote(arg))
	}
	b.WriteString("\n")
	scriptPath := filepath.Join(cwd, ".secondmate-launch.sh")
	if err := os.WriteFile(scriptPath, []byte(b.String()), 0755); err != nil {
		return "", fmt.Errorf("writing secondmate launch script: %w", err)
	}
	return "bash " + shQuote(scriptPath), nil
}

// sha256Content returns the hex SHA-256 digest of data.
func sha256Content(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// taskIDForSecondmate returns the task ID used in state metadata for a secondmate.
func taskIDForSecondmate(smID string) string {
	return "secondmate:" + smID
}

// --- Seed / Provenance ---

// Seed creates a new secondmate home with a charter brief and a provenance marker.
func Seed(id, homePath, charter string) error {
	if err := os.MkdirAll(homePath, 0755); err != nil {
		return fmt.Errorf("creating secondmate home %s: %w", homePath, err)
	}

	for _, dir := range []string{"state", "data", "config", "projects"} {
		if err := os.MkdirAll(filepath.Join(homePath, dir), 0755); err != nil {
			return fmt.Errorf("creating %s/%s: %w", homePath, dir, err)
		}
	}

	agentsPath := filepath.Join(homePath, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte(charter), 0644); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}

	if err := SeedProvenance(homePath, id); err != nil {
		return fmt.Errorf("seeding provenance marker: %w", err)
	}

	fmt.Printf("Seeded secondmate %s at %s\n", id, homePath)
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

// SeedProvenance writes the provenance marker to a secondmate home root.
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
			return "", fmt.Errorf("secondmate home %s has no %s marker — run 'munsu secondmate seed' or 'munsu secondmate migrate'", homePath, ProvenanceMarkerName)
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
		return "", fmt.Errorf("provenance marker home %q does not match actual canonical home %q — secondmate may have been copied/moved", storedHome, actualCanon)
	}
	return id, nil
}

// Validate checks a secondmate home for full structural correctness:
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
		return fmt.Errorf("resolving secondmate home path: %w", err)
	}
	absParent, err := canonicalHome(parentHome)
	if err != nil {
		return fmt.Errorf("resolving parent home path: %w", err)
	}
	if absHome == absParent {
		return fmt.Errorf("secondmate home %s is the parent home itself — refuse", homePath)
	}

	bareName := filepath.Base(absHome)
	if bareName == "fake" || bareName == "project" || bareName == "primary" {
		return fmt.Errorf("secondmate home %s uses reserved name %q — refuse", homePath, bareName)
	}

	return nil
}

// validateStructure checks that a secondmate home has the expected directory
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

// Migrate writes a provenance marker into an existing seeded secondmate home.
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

// RegistryPath returns the path to the authoritative secondmate registry.
func RegistryPath(parentHome string) string {
	return filepath.Join(parentHome, "data", "secondmates.md")
}

// ParseRegistry parses the secondmates registry file and returns Info entries.
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

// List returns all registered secondmates by reading the authoritative registry.
func List(parentHome string) ([]Info, error) {
	return ParseRegistry(RegistryPath(parentHome))
}

// --- Launch (session-backed) ---

// buildLaunchArgs returns the harness binary name and argument list for a secondmate launch.
// Matches firstmate's verified pi secondmate shape: cwd at home + prompt bytes only.
// No shell-expression prompt, no project-path argv, no "--" separator.
func buildLaunchArgs(secondmateHome, h, parentHome string) (string, []string, error) {
	adapter, ok := harness.GetAdapter(h)
	if !ok {
		return "", nil, fmt.Errorf("secondmate launch: harness %q is not a verified harness", h)
	}
	contract := adapter.SecondmateLaunch
	if !contract.Supported {
		return "", nil, fmt.Errorf("secondmate launch: harness %q does not have a verified secondmate launch contract", h)
	}
	if !contract.CwdAtHome || !contract.PromptArg {
		return "", nil, fmt.Errorf("secondmate launch: harness %q has an incomplete secondmate launch contract", h)
	}
	if contract.ProjectArg {
		return "", nil, fmt.Errorf("secondmate launch: harness %q must not pass a project path arg", h)
	}

	charter, err := os.ReadFile(filepath.Join(secondmateHome, "AGENTS.md"))
	if err != nil {
		return "", nil, fmt.Errorf("reading secondmate charter: %w", err)
	}

	model, _ := config.Get(parentHome, "model")
	args := []string{}
	if model != "" && adapter.LaunchTemplate.ModelFlag != "" {
		args = append(args, adapter.LaunchTemplate.ModelFlag, model)
	}
	args = append(args, adapter.LaunchTemplate.ExtraArgs...)
	if contract.Separator != "" {
		args = append(args, contract.Separator)
	}
	args = append(args, string(charter))

	return adapter.Name, args, nil
}

func refuseNestedSecondmateLaunch(parentHome string) error {
	if os.Getenv("MUNSU_ROLE") == "secondmate" {
		return fmt.Errorf("secondmates cannot launch other secondmates; spawn crewmates in their own home instead")
	}
	markerPath := filepath.Join(parentHome, ProvenanceMarkerName)
	if _, err := os.Stat(markerPath); err == nil {
		if _, validateErr := ValidateProvenance(parentHome); validateErr != nil {
			return fmt.Errorf("active home has invalid secondmate provenance: %w", validateErr)
		}
		return fmt.Errorf("secondmate home %s cannot launch another secondmate", parentHome)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking active home provenance: %w", err)
	}
	return nil
}

// Launch starts a secondmate using a session-backed endpoint.
// It validates provenance, resolves the harness, creates a new window via
// the session backend, sends a shell-safe launch script, then writes task
// meta with kind=secondmate and endpoint metadata only after launch succeeds.
func Launch(secondmateHome, parentHome string) error {
	if err := refuseNestedSecondmateLaunch(parentHome); err != nil {
		return err
	}
	if _, err := ValidateProvenance(secondmateHome); err != nil {
		return fmt.Errorf("provenance validation failed for %s: %w", secondmateHome, err)
	}

	h, err := harness.Secondmate(parentHome)
	if err != nil {
		return fmt.Errorf("resolving secondmate harness: %w", err)
	}

	binName, args, err := buildLaunchArgs(secondmateHome, h, parentHome)
	if err != nil {
		return err
	}

	bk, bkName, err := newSessionBackend(parentHome)
	if err != nil {
		return fmt.Errorf("resolving session backend: %w", err)
	}

	markerID, err := ValidateProvenance(secondmateHome)
	if err != nil {
		return fmt.Errorf("revalidating secondmate provenance: %w", err)
	}
	canonicalSecondmateHome, err := canonicalHome(secondmateHome)
	if err != nil {
		return fmt.Errorf("canonicalizing secondmate home: %w", err)
	}

	containerLabel := hometag.WorkspaceTag(canonicalSecondmateHome)
	if hb, ok := bk.(*session.HerdrBackend); ok {
		hb.Cwd = canonicalSecondmateHome
	}
	windowID, err := bk.NewWindow(containerLabel, "mu-secondmate-"+markerID)
	if err != nil {
		return fmt.Errorf("creating secondmate window: %w", err)
	}

	binPath, err := lookPath(binName)
	if err != nil {
		bk.Teardown(windowID)
		return fmt.Errorf("%s harness not found on PATH: %w", binName, err)
	}

	// Build and send shell-safe launch script.
	cmdLine, err := launchCmd(binPath, args, canonicalSecondmateHome)
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
		"kind":    "secondmate",
		"home":    canonicalSecondmateHome,
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

	taskID := taskIDForSecondmate(markerID)
	if err := task.WriteMeta(parentHome, taskID, meta); err != nil {
		bk.Teardown(windowID)
		return fmt.Errorf("writing secondmate task meta: %w", err)
	}

	fmt.Printf("Launched secondmate %s (window=%s, harness=%s) in %s\n",
		markerID, windowID, binName, secondmateHome)
	return nil
}

// --- Retire ---

// Retire tears down a secondmate using its session-backed endpoint.
// It reads task meta, validates kind/sm_id/home before any action,
// then signals the endpoint via the session backend. Errors from backend
// operations (SendKeys, Teardown) are returned — never silently swallowed.
// removeHome=true removes the secondmate home directory after teardown.
func Retire(secondmateHome, parentHome string, removeHome bool) error {
	markerID, err := ValidateProvenance(secondmateHome)
	if err != nil {
		return fmt.Errorf("refusing to retire unowned home %s: %w", secondmateHome, err)
	}
	canonicalSecondmateHome, err := canonicalHome(secondmateHome)
	if err != nil {
		return fmt.Errorf("refusing to retire home with ambiguous identity %s: %w", secondmateHome, err)
	}

	taskID := taskIDForSecondmate(markerID)
	meta, metaErr := task.ReadMeta(parentHome, taskID)

	if metaErr == nil {
		// Validate meta fields before use.
		if meta["kind"] != "secondmate" {
			return fmt.Errorf("refusing to retire: task meta kind=%q, expected \"secondmate\"", meta["kind"])
		}
		if meta["sm_id"] != markerID {
			return fmt.Errorf("refusing to retire: task meta sm_id=%q does not match secondmate marker id %q", meta["sm_id"], markerID)
		}
		if meta["home"] != canonicalSecondmateHome {
			return fmt.Errorf("refusing to retire: task meta home=%q does not match canonical secondmate home %q", meta["home"], canonicalSecondmateHome)
		}

		windowID := meta["window"]
		if windowID == "" {
			return fmt.Errorf("refusing to retire: no window in task meta for secondmate %s", markerID)
		}

		bk, _, bkErr := session.BackendForTask(parentHome, meta)
		if bkErr != nil {
			return fmt.Errorf("refusing to retire: cannot resolve backend for secondmate %s: %w", markerID, bkErr)
		}

		if bk.Alive(windowID) {
			if sendErr := bk.SendKeys(windowID, "/quit"); sendErr != nil {
				return fmt.Errorf("failed to send /quit to secondmate %s: %w", markerID, sendErr)
			}
			fmt.Printf("  sent /quit to %s\n", markerID)
			time.Sleep(500 * time.Millisecond)
			if bk.Alive(windowID) {
				if tdErr := bk.Teardown(windowID); tdErr != nil {
					return fmt.Errorf("failed to teardown secondmate %s window: %w", markerID, tdErr)
				}
			}
		} else {
			if tdErr := bk.Teardown(windowID); tdErr != nil {
				return fmt.Errorf("failed to teardown secondmate %s window: %w", markerID, tdErr)
			}
		}
	} else {
		// Provenance exists but no meta — secondmate was never launched.
		fmt.Printf("  secondmate %s has no task meta (never launched)\n", markerID)
	}

	if removeHome {
		if err := os.RemoveAll(secondmateHome); err != nil {
			return fmt.Errorf("removing secondmate home %s: %w", secondmateHome, err)
		}
		fmt.Printf("Retired and removed secondmate home %s\n", secondmateHome)
	} else {
		fmt.Printf("Retired secondmate at %s (home retained)\n", secondmateHome)
	}

	return nil
}

// --- Handoff ---

// Handoff moves backlog items from the parent home to a secondmate atomically.
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

// Handoff moves backlog items from the parent home to a secondmate atomically.
// All requested keys must preclassify as queued before the command runs.
// Tasks-axi mv is the only supported backend. On failure, both files remain
// unchanged (atomic via tasks-axi mv).
// Validates canonical secondmate destination and uses absolute --file paths.
func Handoff(parentHome, secondmateHome string, itemKeys []string) error {
	if _, err := ValidateProvenance(secondmateHome); err != nil {
		return fmt.Errorf("refusing handoff to unmarked home %s: %w", secondmateHome, err)
	}
	// Validate canonical secondmate home path.
	absSM, err := filepath.Abs(secondmateHome)
	if err != nil {
		return fmt.Errorf("resolving secondmate home: %w", err)
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
	dstBacklog := filepath.Join(secondmateHome, "data", "backlog.md")

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
	return []string{"crew-harness", "crew-dispatch.json", "backlog-backend"}
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

// isSafeConfigPath checks that dst is safely contained within secondmateHome
// and does not symlink-escape into parentHome.
func isSafeConfigPath(dst, parentHome, secondmateHome string) bool {
	smCanon, err := canonicalHome(secondmateHome)
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

func pushConfigFile(parentHome, secondmateHome, name string, logFn func(action, name string)) error {
	src := filepath.Join(parentHome, "config", name)
	dst := filepath.Join(secondmateHome, "config", name)

	if !isSafeConfigPath(dst, parentHome, secondmateHome) {
		return fmt.Errorf("config path %s escapes secondmate container — refuse", dst)
	}
	// Check git tracking BEFORE write — tracked destination must remain byte-identical.
	if isGitTracked(filepath.Dir(dst), filepath.Base(dst)) {
		return fmt.Errorf("inheritance destination %s is tracked in secondmate git — must be gitignored", name)
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

func pushSharedFile(parentHome, secondmateHome string, logFn func(action, name string)) error {
	src := filepath.Join(parentHome, "data", "captain-shared.md")
	dst := filepath.Join(secondmateHome, "data", "captain-shared.md")

	if !isSafeConfigPath(dst, parentHome, secondmateHome) {
		return fmt.Errorf("captain-shared.md path escapes secondmate container — refuse")
	}
	// Check git tracking BEFORE write.
	if isGitTracked(filepath.Dir(dst), filepath.Base(dst)) {
		return fmt.Errorf("captain-shared.md is tracked in secondmate git — must be gitignored")
	}

	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logFn("skipped", "captain-shared.md — "+err.Error())
		return nil
	}

	if err := atomicWriteFile(dst, data, 0444); err != nil {
		return fmt.Errorf("writing captain-shared.md: %w", err)
	}
	logFn("pushed", "captain-shared.md")

	return nil
}

func isGitTracked(dir, name string) bool {
	out, err := exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", name).CombinedOutput()
	return err == nil && len(out) > 0
}

// ConfigPush copies inheritable config from the parent home to the secondmate,
// mirrors deletions, pushes data/captain-shared.md read-only, and logs actions.
func ConfigPush(parentHome, secondmateHome string) error {
	if _, err := ValidateProvenance(secondmateHome); err != nil {
		return fmt.Errorf("refusing config-push to unmarked home %s: %w", secondmateHome, err)
	}

	inheritable := getInheritableList()

	logPath := filepath.Join(secondmateHome, "state", "config-push.log")
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

	// Mirror deletions: remove inheritable files in secondmate that are absent in parent.
	// Validate safety BEFORE any deletion. Return error on unsafe/tracked paths.
	configDir := filepath.Join(secondmateHome, "config")
	if entries, err := os.ReadDir(configDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if !isInheritable(name, inheritable) {
				continue
			}
			srcPath := filepath.Join(parentHome, "config", name)
			if _, err := os.Stat(srcPath); os.IsNotExist(err) {
				dstPath := filepath.Join(configDir, name)
				if !isSafeConfigPath(dstPath, parentHome, secondmateHome) {
					return fmt.Errorf("mirror deletion: %s path escapes secondmate container — refuse", name)
				}
				if isGitTracked(configDir, name) {
					return fmt.Errorf("mirror deletion: %s is tracked in secondmate git — must be gitignored", name)
				}
				if err := os.Remove(dstPath); err != nil {
					log("delete-failed", name+" — "+err.Error())
					return fmt.Errorf("mirror deletion: removing %s: %w", name, err)
				}
				log("deleted", name)
			}
		}
	}

	// Mirror deletion for captain-shared.md — validate before mutation.
	sharedDst := filepath.Join(secondmateHome, "data", "captain-shared.md")
	if _, err := os.Stat(filepath.Join(parentHome, "data", "captain-shared.md")); os.IsNotExist(err) {
		if _, err := os.Stat(sharedDst); err == nil {
			if !isSafeConfigPath(sharedDst, parentHome, secondmateHome) {
				return fmt.Errorf("mirror deletion: captain-shared.md path escapes secondmate container — refuse")
			}
			if isGitTracked(filepath.Dir(sharedDst), filepath.Base(sharedDst)) {
				return fmt.Errorf("mirror deletion: captain-shared.md is tracked in secondmate git — must be gitignored")
			}
			if err := os.Remove(sharedDst); err != nil {
				log("delete-failed", "captain-shared.md — "+err.Error())
				return fmt.Errorf("mirror deletion: removing captain-shared.md: %w", err)
			}
			log("deleted", "captain-shared.md")
		}
	}
	for _, name := range inheritable {
		if err := pushConfigFile(parentHome, secondmateHome, name, log); err != nil {
			return err
		}
	}

	if err := pushSharedFile(parentHome, secondmateHome, log); err != nil {
		return err
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

// safeFF performs a LOCAL-only fast-forward of a secondmate clone to the
// parent's already-local default-branch commit. Verified: same canonical
// remote origin, correct branch/detached state, clean tree (ignoring only
// marker and local inherited paths), ancestor relationship, then git merge --ff-only.
func safeFF(secondmateHome, parentHome string) (before, after string, err error) {
	// Verify same canonical remote origin (allows independent clones, HTTPS/SSH equivalence).
	parentRemote, err := gitRun("-C", parentHome, "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("parent remote origin: %w", err)
	}
	smRemote, err := gitRun("-C", secondmateHome, "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("secondmate remote origin: %w", err)
	}
	if normalizeGitRemote(parentRemote) != normalizeGitRemote(smRemote) {
		return "", "", fmt.Errorf("secondmate remote %q differs from parent remote %q (canonical: %q vs %q)",
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

	// Branch check — secondmate must be on default branch or detached HEAD.
	smBranch, err := gitRun("-C", secondmateHome, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading secondmate branch: %w", err)
	}
	if smBranch != "HEAD" && smBranch != "" && smBranch != defaultBranch {
		return "", "", fmt.Errorf("secondmate is on branch %q, expected %q or detached HEAD", smBranch, defaultBranch)
	}

	// Clean check — reject ALL tracked changes; allow only gitignored untracked files.
	statusOut, err := gitRun("-C", secondmateHome, "status", "--porcelain")
	if err != nil {
		return "", "", fmt.Errorf("secondmate git status: %w", err)
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
				if _, err := gitRun("-C", secondmateHome, "check-ignore", "-q", "--", path); err == nil {
					continue // gitignored, OK
				}
				return "", "", fmt.Errorf("secondmate home %s has unignored untracked file: %s", secondmateHome, path)
			}
			// Any non-space character means tracked change (staged or unstaged).
			if xy[0] != ' ' || xy[1] != ' ' {
				return "", "", fmt.Errorf("secondmate home %s has tracked changes", secondmateHome)
			}
		}
	}

	before, err = gitRun("-C", secondmateHome, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading secondmate HEAD: %w", err)
	}

	// Check ancestry.
	mergeBase, err := gitRun("-C", secondmateHome, "merge-base", before, defaultCommit)
	if err != nil {
		return "", "", fmt.Errorf("merge-base failed: %w", err)
	}
	if mergeBase != before {
		return "", "", fmt.Errorf("secondmate %s is not an ancestor of parent default-branch commit %s — diverged or unequal history", before[:8], defaultCommit[:8])
	}

	if before == defaultCommit {
		return before, before, nil
	}

	fmt.Printf("  %s: fast-forward %s → %s\n", filepath.Base(secondmateHome), before[:8], defaultCommit[:8])

	_, err = gitRun("-C", secondmateHome, "merge", "--ff-only", defaultCommit)
	if err != nil {
		return "", "", fmt.Errorf("git merge --ff-only failed: %w", err)
	}

	after, err = gitRun("-C", secondmateHome, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("reading secondmate HEAD after ff: %w", err)
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

// Converge performs a locked convergence sweep over registered secondmates.
// Order: lock, validate registry/provenance, retry pending sends, safe ff,
// inheritance push, ownership-backed backend Alive check, and reread nudge
// only if instruction surface advanced.
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

// checkAliveViaBackend checks if a secondmate is alive using the session backend.
// It reads task meta, validates kind/sm_id/home before use, and uses backend.Alive.
func checkAliveViaBackend(parentHome string, sm Info) (bool, error) {
	taskID := taskIDForSecondmate(sm.ID)
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		return false, nil // not yet launched
	}

	if meta["kind"] != "secondmate" {
		return false, nil
	}
	if meta["sm_id"] != sm.ID {
		return false, nil
	}
	canonSM, err := canonicalHome(sm.Home)
	if err != nil {
		return false, fmt.Errorf("canonicalizing secondmate home: %w", err)
	}
	if meta["home"] != canonSM {
		return false, nil
	}

	windowID := meta["window"]
	if windowID == "" {
		return false, nil
	}

	bk, _, bkErr := session.BackendForTask(parentHome, meta)
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

// sendNudge sends a short re-read message to a secondmate via its
// session-backed endpoint. It reads task meta, validates endpoint
// identity, sends the message, removes the pending marker, and
// updates applied instruction identity only after success.
// On failure, the marker remains.
func sendNudge(parentHome string, sm Info) error {
	taskID := taskIDForSecondmate(sm.ID)
	meta, err := task.ReadMeta(parentHome, taskID)
	if err != nil {
		return fmt.Errorf("%s: no task meta — marker remains", sm.ID)
	}

	// Validate endpoint meta before use.
	if meta["kind"] != "secondmate" {
		return fmt.Errorf("%s: meta kind=%q, expected secondmate — marker remains", sm.ID, meta["kind"])
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
		return fmt.Errorf("%s: marker commit %q is not a valid commit in secondmate repo — marker remains", sm.ID, marker["commit"])
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
