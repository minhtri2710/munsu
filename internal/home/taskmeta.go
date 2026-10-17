// Task meta files are key=value lines stored at $MUNSU_HOME/state/<durable-stem>.meta.
// Durable stems are platform-specific filename projections of logical task IDs;
// callers pass logical IDs and discovery boundaries reverse-decode persisted stems.

package home

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
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

// writeMetaLocked durably writes a task meta file while the lock is already held.
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
	if err := atomicWrite(p, []byte(b.String()), secureFile); err != nil {
		return fmt.Errorf("writing meta file: %w", err)
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
// The line is stored EscapeLine-encoded, so a line holding a newline stays one
// record; ReadStatus decodes it.
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
	// A soldier writes its own status file and so can replace it. An unfenced
	// append must not follow what it put there into another task's documents
	// or the fleet registries. Human words (domain.Words): grantor the Human,
	// channel supervisor-relay:dialog, words=selected, G1058: "A soldier's
	// write fence lets it write only its own task's status, report and request
	// files; it can never write another task's documents, any review verdict,
	// delivery authorization or gate record, or the fleet registries
	// (projects.json, captains.json)."
	f, err := openRegular(p, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("opening status file: %w", err)
	}
	defer f.Close()
	if err := secureOpened(f); err != nil {
		return fmt.Errorf("securing status file: %w", err)
	}
	// A torn last line (a write cut off before its newline) is ended first, so
	// the new line is never glued onto it.
	record := EscapeLine(line) + "\n"
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("reading status file: %w", err)
	}
	if fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err != nil {
			return fmt.Errorf("reading status file: %w", err)
		}
		if last[0] != '\n' {
			record = "\n" + record
		}
	}
	if _, err := f.WriteString(record); err != nil {
		return fmt.Errorf("writing status line: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("syncing status file: %w", err)
	}
	return nil
}

// ReadStatus reads all status lines from $MUNSU_HOME/state/<durable-stem>.status,
// decoded as AppendStatus was given them. Only a regular file is read, as
// ReadRegularFile reads (Human words G1086, quoted there).
func ReadStatus(homeDir string, id string) ([]string, error) {
	lines, _, err := readStatus(homeDir, id)
	return lines, err
}

// readStatus is ReadStatus that also reports whether the last line is torn:
// not yet ended by its newline, so its writer may still be writing it.
func readStatus(homeDir string, id string) ([]string, bool, error) {
	p, err := StatusFilePath(homeDir, id)
	if err != nil {
		return nil, false, err
	}
	if err := validateStatePath(homeDir, p, false); err != nil {
		return nil, false, err
	}
	data, err := ReadRegularFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // no status yet
		}
		return nil, false, fmt.Errorf("reading status %s: %w", id, err)
	}
	var lines []string
	err = EachLine(bytes.NewReader(data), func(line string) { lines = append(lines, UnescapeLine(line)) })
	return lines, len(data) > 0 && data[len(data)-1] != '\n', err
}

// ReadRegularFile reads a file a fenced soldier can write (its status, ack log
// or scout report) for an unfenced reader: only a regular file, never through
// a symlink, never blocking on a FIFO. Human words (domain.Words): grantor
// the Human, channel supervisor-relay:dialog, words=selected, G1086, whose
// section 1 conditions include "PR-3 O_NOFOLLOW+regular-file": "Duyệt, có R3
// (Recommended): option 1 + 3 PRs with section 1 conditions; R3 worded as an
// accident guard (not a boundary), error names munsu report <state>; [...]"
func ReadRegularFile(path string) ([]byte, error) {
	f, err := openRegular(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// ListMetaIDs returns the logical task IDs of every .meta file in the state
// directory, reversing the durable key so enumeration and the write path agree
// on the logical id. Stems that are not a key this home persisted are skipped.
// It reads no meta contents, so it fails only on a directory-level error; a
// caller that must not miss a task can therefore ReadMeta each id and decide
// for itself whether an unreadable projection is fatal.
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

// lockPath returns the path to the advisory lock file for a meta file.
func lockPath(homeDir, id string) (string, error) {
	p, err := MetaFilePath(homeDir, id)
	if err != nil {
		return "", err
	}
	return p + ".lock", nil
}

// SoldierFiles returns the only files a soldier of task id writes in this
// home: its status file and its ack log (see Store.ackLogPath). It first
// creates the task's meta lock if it is missing: the soldier's status append
// takes that lock, and its fence lets it lock the file but not create it.
func SoldierFiles(homeDir, id string) ([]string, error) {
	_, unlock, err := acquireMetaLock(homeDir, id)
	if err != nil {
		return nil, err
	}
	unlock()
	// The lock path above already validated id, so these cannot fail.
	status, _ := StatusFilePath(homeDir, id)
	acks, _ := DurableFilePath(StateDir(homeDir), id, ".acks")
	return []string{status, acks}, nil
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
	if err := lockFile(f, false); err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("acquiring flock: %w", err)
	}
	return f, func() {
		unlockFile(f)
		f.Close()
	}, nil
}
