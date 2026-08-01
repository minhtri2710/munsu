package taskauthorityfs

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// On-disk layout. All v2 documents live under the versioned v2 namespace;
// the legacy v1 aggregate layout in "state/.task-authority/aggregates" is a
// sibling that this package detects but never writes or migrates.
const (
	authorityRoot      = "state/.task-authority/v2"
	aggregatesDir      = authorityRoot + "/aggregates"
	holdsDir           = authorityRoot + "/holds"
	interpretationsDir = authorityRoot + "/interpretations"
	decisionsDir       = authorityRoot + "/decisions"
	auditDir           = authorityRoot + "/audit"
	receiptsDir        = authorityRoot + "/receipts"
	transactionsDir    = authorityRoot + "/transactions"
	currentFileName    = "current"
	documentExt        = ".json"
)

// DirPerm is the private mode for all authority directories, matching
// internal/home (0700).
const DirPerm = 0o700

// FilePerm is the private mode for all authority documents, matching
// internal/home (0600).
const FilePerm = 0o600

// pathError wraps a detail in the ErrInvalidPath sentinel.
func pathError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPath, fmt.Sprintf(format, args...))
}

// validateTaskID accepts safe non-empty slug identities, mirroring the
// internal/home task ID rules (no path separators, no traversal).
func validateTaskID(id string) error {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return pathError("invalid task id %q", id)
	}
	return nil
}

// safeFileID validates a free-form record identity (operation ID, hold ID,
// interpretation ID, decision key) and renders a filesystem-safe filename.
// It strengthens the home rules by rejecting hidden leading-dot identities.
// The filename is the full raw ID hex-encoded, so the mapping from identities
// to filenames is injective (no collisions) and reversible.
func safeFileID(id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") || strings.HasPrefix(id, ".") {
		return "", pathError("invalid record id %q", id)
	}
	return fileIDEncode(id), nil
}

// fileIDEncode renders a raw record identity as an injective, reversible
// filesystem-safe filename: the complete raw ID is hex-encoded byte-for-byte,
// so distinct identities can never collide and the raw ID is recoverable.
func fileIDEncode(id string) string {
	return hex.EncodeToString([]byte(id))
}

// fileIDDecode recovers the raw record identity from a fileIDEncode filename,
// failing closed on malformed or non-canonical filenames: the re-encoding of
// the decoded raw ID must reproduce the filename byte-for-byte.
func fileIDDecode(name string) (string, error) {
	raw, err := hex.DecodeString(name)
	if err != nil || hex.EncodeToString(raw) != name {
		return "", pathError("malformed record id filename %q", name)
	}
	return string(raw), nil
}

// AggregateRelPath returns the versioned rel path of one aggregate document.
func AggregateRelPath(taskID string, generation taskauthority.Generation) (string, error) {
	if err := validateTaskID(taskID); err != nil {
		return "", err
	}
	if err := generation.Validate(); err != nil {
		return "", pathError("invalid generation %s: %v", generation, err)
	}
	return filepath.Join(aggregatesDir, taskID, generation.String()+documentExt), nil
}

// CurrentPointerRelPath returns the versioned rel path of a task's current
// generation pointer file.
func CurrentPointerRelPath(taskID string) (string, error) {
	if err := validateTaskID(taskID); err != nil {
		return "", err
	}
	return filepath.Join(aggregatesDir, taskID, currentFileName), nil
}

// HoldRelPath returns the versioned rel path of one dispatch hold document.
func HoldRelPath(id string) (string, error) {
	name, err := safeFileID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(holdsDir, name+documentExt), nil
}

// InterpretationRelPath returns the versioned rel path of one dispatch
// interpretation document.
func InterpretationRelPath(id string) (string, error) {
	name, err := safeFileID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(interpretationsDir, name+documentExt), nil
}

// DecisionRelPath returns the versioned rel path of one dispatch decision
// document.
func DecisionRelPath(key string) (string, error) {
	name, err := safeFileID(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(decisionsDir, name+documentExt), nil
}

// AuditRelPath returns the versioned rel path of one typed audit event
// document, keyed by the Task Operation that committed it.
func AuditRelPath(operationID string) (string, error) {
	name, err := safeFileID(operationID)
	if err != nil {
		return "", err
	}
	return filepath.Join(auditDir, name+documentExt), nil
}

// ReceiptRelPath returns the versioned rel path of one Task Operation receipt
// document, keyed by the operation ID.
func ReceiptRelPath(operationID string) (string, error) {
	name, err := safeFileID(operationID)
	if err != nil {
		return "", err
	}
	return filepath.Join(receiptsDir, name+documentExt), nil
}

// TransactionManifestRelPath returns the versioned rel path of one pending
// transaction manifest document, keyed by the operation ID.
func TransactionManifestRelPath(operationID string) (string, error) {
	name, err := safeFileID(operationID)
	if err != nil {
		return "", err
	}
	return filepath.Join(transactionsDir, name+documentExt), nil
}

// EncodeCurrentPointer renders the current generation pointer file content
// for one generation.
func EncodeCurrentPointer(generation taskauthority.Generation) ([]byte, error) {
	if err := generation.Validate(); err != nil {
		return nil, pathError("invalid generation %s: %v", generation, err)
	}
	return []byte(generation.String() + "\n"), nil
}

// DecodeCurrentPointer parses the current generation pointer file content,
// failing closed on empty, non-numeric, or traversal-shaped content.
func DecodeCurrentPointer(data []byte) (taskauthority.Generation, error) {
	generation, err := taskauthority.ParseGeneration(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, corruptDocument("current", "generation", "invalid current generation %q", strings.TrimSpace(string(data)))
	}
	return generation, nil
}

// EnsureDir creates dir with DirPerm and re-secures it if it already exists,
// matching internal/home.EnsureDirTree behavior.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return fmt.Errorf("cannot create authority directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, DirPerm); err != nil {
		return fmt.Errorf("cannot secure authority directory %s: %w", dir, err)
	}
	return nil
}

// v1 record locations under a legacy home. Detection is strictly read-only:
// v1 state is never silently migrated or mutated by this package.
const (
	v1AggregatesRelPath      = "state/.task-authority/aggregates"
	v1WorktreeLeasesRelPath  = "state/.task-authority/worktree-leases"
	v1DispatchControlRelPath = "state/.dispatch"
)

// HasV1Records reports whether legacy v1 task-authority records exist under
// the home: legacy task aggregates, worktree leases, or dispatch-control
// records. Detection is strictly read-only: v1 state is never silently
// migrated or mutated by this package.
func HasV1Records(homeDir string) (bool, error) {
	for _, rel := range []string{v1AggregatesRelPath, v1WorktreeLeasesRelPath, v1DispatchControlRelPath} {
		has, err := hasVisibleEntries(filepath.Join(homeDir, rel))
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	return false, nil
}

// hasVisibleEntries reports whether dir contains at least one non-hidden
// entry, treating a missing dir as no records.
func hasVisibleEntries(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			return true, nil
		}
	}
	return false, nil
}
