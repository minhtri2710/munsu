// Package task manages task lifecycle data, including reading and writing
// task meta files (key=value lines stored at $MUNSU_HOME/state/<durable-stem>.meta).
// Durable stems are platform-specific filename projections of logical task IDs;
// callers pass logical IDs and discovery boundaries reverse-decode persisted stems.
package home

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// StateDir returns the path to the state directory under the given homeDir.
func StateDir(homeDir string) string {
	return filepath.Join(homeDir, "state")
}

// DurableFilePath returns a path whose filename starts with the platform
// durable key for the logical task ID. It is for filename-backed artifacts;
// logical IDs stored as journal keys are not transformed.
func DurableFilePath(dir, id, suffix string) (string, error) {
	stem, err := DurableKey(id)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(suffix, `/\\`) || strings.ContainsRune(suffix, 0) {
		return "", fmt.Errorf("invalid durable file suffix %q", suffix)
	}
	return filepath.Join(dir, stem+suffix), nil
}

// MetaFilePath returns the path to the meta file for the given task ID. The
// persisted file stem is the platform durable key for the logical id (see
// DurableKey), so the same logical id always resolves to the one persisted
// key for the platform.
func MetaFilePath(homeDir string, id string) (string, error) {
	return DurableFilePath(StateDir(homeDir), id, ".meta")
}

// StatusFilePath returns the path to the status file for the given task ID.
// The persisted file stem is the platform durable key for the logical id.
func StatusFilePath(homeDir string, id string) (string, error) {
	return DurableFilePath(StateDir(homeDir), id, ".status")
}

func validateTaskID(id string) error {
	if id == "" || id == "." || id == ".." || path.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("invalid task ID %q", id)
	}
	return nil
}

// AmbiguousTaskIDError reports that one requested task ID resolves to more
// than one munsu home (the primary home or a captain sub-home). It is shared
// by the CLI task lookup and the fleet handoff saga, which both collect
// canonical owners per home; CorrectionCommands renders the --home-pinned
// commands an operator can run against each owner.
type AmbiguousTaskIDError struct {
	Requested string
	Matches   []string
}

func (e *AmbiguousTaskIDError) Error() string {
	return fmt.Sprintf("task ID %q is ambiguous; use one of: %s", e.Requested, strings.Join(e.Matches, ", "))
}

func (e *AmbiguousTaskIDError) CorrectionCommands(command string) []string {
	commands := make([]string, 0, len(e.Matches))
	for _, match := range e.Matches {
		commands = append(commands, command+" "+e.Requested+" --home "+match)
	}
	return commands
}

func ensurePrivateStateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("creating state directory: not a directory: %s", path)
	}
	return restrictDir(path)
}

func validateStatePath(homeDir, target string, create bool) error {
	stateDir := StateDir(homeDir)
	info, err := os.Lstat(stateDir)
	if os.IsNotExist(err) {
		if !create {
			return nil
		}
		if err := ensurePrivateStateDir(stateDir); err != nil {
			return err
		}
		info, err = os.Lstat(stateDir)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("state path is not a directory: %s", stateDir)
	}
	canonicalState, err := filepath.EvalSymlinks(stateDir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(stateDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ErrKeyEscapes
	}
	canonicalTarget := filepath.Join(canonicalState, rel)
	return verifyNoFollow(canonicalState, canonicalTarget)
}

func validateMetaFields(meta map[string]string) error {
	for key, value := range meta {
		if key == "" || strings.ContainsRune(key, '=') || strings.IndexFunc(key, isMetaControl) >= 0 {
			return fmt.Errorf("invalid task meta key %q", key)
		}
		if strings.IndexFunc(value, isMetaControl) >= 0 {
			return fmt.Errorf("invalid task meta value for %q", key)
		}
	}
	return nil
}

func isMetaControl(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// WriteMeta writes a task meta file at $MUNSU_HOME/state/<durable-stem>.meta.
// The map is serialized as key=value lines, one per field.
// Uses atomic write: unique temp file + rename to prevent partial writes.
// Acquires the advisory lock to serialize concurrent writers.
func WriteMeta(homeDir string, id string, meta map[string]string) error {
	if err := validateMetaFields(meta); err != nil {
		return err
	}
	_, unlock, err := acquireMetaLock(homeDir, id)
	if err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	defer unlock()

	return writeMetaLocked(homeDir, id, meta)
}

// ErrMetaUnchanged is returned by an UpdateMeta callback that decided, from the
// meta it observed under the lock, that nothing needs writing. UpdateMeta
// abandons the update and reports no error. It is the only way a callback can
// decline the write without also declaring a failure.
var ErrMetaUnchanged = errors.New("task meta unchanged")

// UpdateMeta applies mutate to the current task meta and persists the result as
// one atomic cycle: the advisory lock is held across the read, the mutation and
// the write, so a concurrent projection writer cannot interleave. An absent meta
// file is an empty map, which is the first write for a task; every other read
// failure refuses the write, because writeMetaLocked replaces the whole file and
// a swallowed read error would erase every field it could not read.
//
// mutate observes the meta under the lock and decides the outcome: nil writes
// the mutated map, ErrMetaUnchanged abandons the update without writing, and
// any other error refuses the update and propagates the reason. A callback that
// requires the meta to already exist checks the field that proves it rather
// than the emptiness of the map.
func UpdateMeta(homeDir string, id string, mutate func(map[string]string) error) error {
	_, unlock, err := acquireMetaLock(homeDir, id)
	if err != nil {
		return fmt.Errorf("update meta: %w", err)
	}
	defer unlock()

	meta, err := ReadMeta(homeDir, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("update meta: %w", err)
	}
	if meta == nil {
		meta = make(map[string]string)
	}
	if err := mutate(meta); err != nil {
		if errors.Is(err, ErrMetaUnchanged) {
			return nil
		}
		return fmt.Errorf("update meta: %w", err)
	}
	return writeMetaLocked(homeDir, id, meta)
}

// writeMetaLocked writes a task meta file while the lock is already held.
// Uses a unique temp file (os.CreateTemp) for safe atomic writes.
func writeMetaLocked(homeDir string, id string, meta map[string]string) error {
	if err := validateMetaFields(meta); err != nil {
		return err
	}
	p, err := MetaFilePath(homeDir, id)
	if err != nil {
		return err
	}
	if err := ensurePrivateStateDir(filepath.Dir(p)); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	if err := validateStatePath(homeDir, p, true); err != nil {
		return err
	}
	var b strings.Builder
	for k, v := range meta {
		b.WriteString(fmt.Sprintf("%s=%s\n", k, v))
	}
	// Use os.CreateTemp for a unique temp file in the same directory. The temp
	// name is derived from the persisted stem (the durable key), never the raw
	// logical id, so it is a safe filename on every platform.
	stem := strings.TrimSuffix(filepath.Base(p), ".meta")
	tmpF, err := os.CreateTemp(filepath.Dir(p), stem+".meta.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp meta file: %w", err)
	}
	tmpPath := tmpF.Name()
	if err := secureFile(tmpPath); err != nil {
		tmpF.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("securing temp meta file: %w", err)
	}
	if _, err := tmpF.WriteString(b.String()); err != nil {
		tmpF.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp meta file: %w", err)
	}
	if err := tmpF.Sync(); err != nil {
		tmpF.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing temp meta file: %w", err)
	}
	if err := tmpF.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp meta file: %w", err)
	}
	if err := RenameDurable(tmpPath, p); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp meta file: %w", err)
	}
	return nil
}

// ReadMeta reads a task meta file at $MUNSU_HOME/state/<durable-stem>.meta.
// Each key=value line is parsed into the returned map.
// Returns an error if the file does not exist.
//
// This is the read half of the read-modify-write cycle in UpdateMeta, which
// rewrites the whole file, so it fails closed on an oversized line rather than
// silently dropping content: a line beyond bufio.Scanner's token limit surfaces
// as a scan error instead of being skipped, and UpdateMeta then refuses the
// write. This is a deliberately stricter contract than ReadMetaFile's tolerant
// directory scan; the two must not be merged.
func ReadMeta(homeDir string, id string) (map[string]string, error) {
	p, err := MetaFilePath(homeDir, id)
	if err != nil {
		return nil, err
	}
	if err := validateStatePath(homeDir, p, false); err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("reading task meta %s: %w", id, err)
	}
	defer f.Close()

	meta := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning task meta %s: %w", id, err)
	}
	return meta, nil
}

// ReadMetaFile parses a flat "key = value" meta file at path, skipping blank
// lines and "#" comments. Read-only state-directory scanners that already hold
// a resolved path use this (prune's live-workspace sweep, the observation event
// port); it reads the whole file with no per-line size limit so an oversized
// line never drops the file from the scan — the fail-open opposite of that
// would let prune close a still-referenced workspace. Callers that write the
// file back must use ReadMeta instead, whose stricter fail-closed contract
// guards the read-modify-write cycle; do not collapse the two.
func ReadMetaFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	meta := make(map[string]string)
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return meta, nil
}

// AppendStatus appends a status line to $MUNSU_HOME/state/<durable-stem>.status.
func AppendStatus(homeDir string, id, line string) error {
	_, unlock, err := acquireMetaLock(homeDir, id)
	if err != nil {
		return err
	}
	defer unlock()

	p, err := StatusFilePath(homeDir, id)
	if err != nil {
		return err
	}
	if err := validateStatePath(homeDir, p, true); err != nil {
		return err
	}
	if err := ensurePrivateStateDir(filepath.Dir(p)); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("opening status file: %w", err)
	}
	if err := secureFile(p); err != nil {
		f.Close()
		return fmt.Errorf("securing status file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("writing status line: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("syncing status file: %w", err)
	}
	return nil
}

// ReadStatus reads all status lines from $MUNSU_HOME/state/<durable-stem>.status.
func ReadStatus(homeDir string, id string) ([]string, error) {
	p, err := StatusFilePath(homeDir, id)
	if err != nil {
		return nil, err
	}
	if err := validateStatePath(homeDir, p, false); err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no status yet
		}
		return nil, fmt.Errorf("reading status %s: %w", id, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// ValidStatusStates lists the recognized status states.
var ValidStatusStates = []string{
	"working", "review-ready", "amending", "needs-decision", "blocked", "paused",
	"awaiting_approval", "resolved", "done", "failed", "delivered",
}

// IsValidStatusState checks whether the given state is recognized.
func IsValidStatusState(state string) bool {
	for _, s := range ValidStatusStates {
		if s == state {
			return true
		}
	}
	return false
}

// ParseStatusKey extracts an optional [key=<slug>] annotation from a status message.
// Returns the cleaned message and the key (empty if none).
func ParseStatusKey(line string) (message, key string) {
	// Format: state: message [key=<slug>]
	// Or just [key=<slug>] at the end.
	// Try with space prefix first: " [key="
	startMarker := " [key="
	idx := strings.LastIndex(line, startMarker)
	if idx < 0 {
		// Try without space: "[key=" at start
		startMarker = "[key="
		idx = strings.LastIndex(line, startMarker)
	}
	if idx >= 0 {
		end := strings.Index(line[idx+len(startMarker):], "]")
		if end >= 0 {
			keyVal := line[idx+len(startMarker) : idx+len(startMarker)+end]
			if keyVal != "" {
				key = keyVal
				message = strings.TrimSpace(line[:idx])
				return
			}
		}
	}
	return line, ""
}

// ValidMetaFields lists the recognized fields in a task meta file.
var ValidMetaFields = []string{
	"window", "worktree", "project", "harness",
	"model", "effort", "kind", "mode", "yolo",
	"backend", "herdr_session", "herdr_workspace_id", "herdr_tab_id", "herdr_pane_id",
	"pr_provider", "pr_owner", "pr_repo", "pr_number", "pr_url",
	"pr_base_ref", "pr_head_ref", "pr_head_sha", "pr_timestamp",
	"pr_identity_revision",
	"amend_expected_head", "amend_started_at",
	"amendment_history",
}

// MetaEntry represents a single task entry from state meta files.
type MetaEntry struct {
	ID         string
	Kind       string
	Project    string
	LastStatus string // last line from .status file, key stripped
}

// ListMeta reads all meta files from the state directory and returns them.
// It reads *.meta files, extracts key fields, and reads the last status line
// from the corresponding .status file for each task.
// ListMetaIDs returns the logical task IDs of every .meta file in the state
// directory, reversing the durable key so enumeration and the write path agree
// on the logical id. Stems that are not a key this home persisted are skipped.
// It reads no meta contents, so it fails only on a directory-level error; a
// caller that must not miss a task can therefore ReadMeta each id and decide
// for itself whether an unreadable projection is fatal. ListMeta layers the
// display-tolerant read on top of this.
func ListMetaIDs(homeDir string) ([]string, error) {
	sd := StateDir(homeDir)
	if err := validateStatePath(homeDir, sd, false); err != nil {
		return nil, err
	}
	dir, err := os.Open(sd)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening state dir: %w", err)
	}
	defer dir.Close()

	entries, err := dir.Readdir(-1)
	if err != nil {
		return nil, fmt.Errorf("reading state dir: %w", err)
	}

	var taskIDs []string
	seen := make(map[string]bool)
	for _, fi := range entries {
		name := fi.Name()
		if !strings.HasSuffix(name, ".meta") {
			continue
		}
		id, err := ReverseDurableKey(strings.TrimSuffix(name, ".meta"))
		if err != nil {
			continue
		}
		if !seen[id] {
			seen[id] = true
			taskIDs = append(taskIDs, id)
		}
	}
	return taskIDs, nil
}

func ListMeta(homeDir string) ([]MetaEntry, error) {
	taskIDs, err := ListMetaIDs(homeDir)
	if err != nil {
		return nil, err
	}

	var result []MetaEntry
	for _, id := range taskIDs {
		meta, err := ReadMeta(homeDir, id)
		if err != nil {
			continue // skip unreadable meta
		}

		// Read last status line
		lastStatus := ""
		if statusLines, err := ReadStatus(homeDir, id); err == nil && len(statusLines) > 0 {
			lastLine := statusLines[len(statusLines)-1]
			msg, _ := ParseStatusKey(lastLine)
			lastStatus = msg
		}

		kind := meta["kind"]
		project := pickProject(meta)
		result = append(result, MetaEntry{
			ID:         id,
			Kind:       kind,
			Project:    project,
			LastStatus: lastStatus,
		})
	}

	return result, nil
}

// pickProject returns the project from meta, falling back to repo if project is unset.
func pickProject(meta map[string]string) string {
	if p := meta["project"]; p != "" {
		return p
	}
	return meta["repo"]
}

// lockPath returns the path to the advisory lock file for a meta file.
func lockPath(homeDir, id string) (string, error) {
	p, err := MetaFilePath(homeDir, id)
	if err != nil {
		return "", err
	}
	return p + ".lock", nil
}

// acquireMetaLock acquires an exclusive advisory lock on the meta lock file.
// Uses flock(2) which is automatically released when the process exits.
// Returns the locked file and a cleanup function.
func acquireMetaLock(homeDir, id string) (*os.File, func(), error) {
	lp, err := lockPath(homeDir, id)
	if err != nil {
		return nil, nil, err
	}
	if err := ensurePrivateStateDir(filepath.Dir(lp)); err != nil {
		return nil, nil, fmt.Errorf("creating state directory for lock: %w", err)
	}
	if err := validateStatePath(homeDir, lp, true); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(lp, os.O_RDONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("opening lock file: %w", err)
	}
	if err := secureFile(lp); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("securing lock file: %w", err)
	}
	if err := lockExclusive(f); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("acquiring flock: %w", err)
	}
	return f, func() {
		unlockFile(f)
		f.Close()
	}, nil
}
