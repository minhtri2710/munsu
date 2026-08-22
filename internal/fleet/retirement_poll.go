// Package supervision provides watcher check plugin infrastructure including
// crash-safe retirement of merged PR poll artifacts.
package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// PollRetirementSchema is the schema version for PollRetirementRecord.
const PollRetirementSchema = 1

// ErrCheckValidationRefused indicates that a check artifact failed validation
// during retirement.
var ErrCheckValidationRefused = errors.New("check validation refused")

// ErrCheckInvalidAfterPublication indicates that post-publication
// revalidation refused the check artifact. It also matches
// ErrCheckValidationRefused because this is a validation refusal.
var ErrCheckInvalidAfterPublication = fmt.Errorf("%w: check invalid after publication", ErrCheckValidationRefused)

// retirementDir is the private state directory for pending retirement records.
const retirementDir = "state/.poll-retirements"

// PollRetirementRecord captures a durable write-ahead record for merged PR
// poll retirement. It is persisted BEFORE publication and removed only AFTER
// the poll artifact is gone, making crashes recoverable.
type PollRetirementRecord struct {
	SchemaVersion int `json:"schemaVersion"`

	// Task identity
	TaskID string `json:"taskId"`

	// Poll identity: state-relative path + SHA-256 content digest
	PollPath   string `json:"pollPath"`   // relative to state dir (e.g. "<id>.check")
	PollDigest string `json:"pollDigest"` // hex-encoded SHA-256 of poll content at discovery

	// Delivery identity (complete, validated at capture)
	Provider   string `json:"provider"`
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	BaseRef    string `json:"baseRef"`
	HeadRef    string `json:"headRef"`
	HeadSHA    string `json:"headSHA"`
	CapturedAt string `json:"capturedAt"`

	// Provider-confirmed merged result
	MergedSHA string `json:"mergedSHA"` // merge commit SHA from provider

	// Publication evidence: the exact status line that was durably written
	PublicationLine string `json:"publicationLine"` // e.g. "done [key=pr-merged:<stable-id>]: PR merged"

	// Timestamps
	DiscoveredAt string `json:"discoveredAt"` // when the merged PR was detected
	RecordedAt   string `json:"recordedAt"`   // when this record was persisted
}

// stableMergeKey returns a deterministic key for the publication status line.
func stableMergeKey(taskID string) string {
	// Use a prefix that the classify package can match for general-relevance.
	return fmt.Sprintf("pr-merged:%s", taskID)
}

// publicationLine builds the deterministic status line for a merged PR.
func publicationLine(taskID, prURL, mergedSHA string) string {
	return fmt.Sprintf("done [key=%s]: PR %s merged at %s",
		stableMergeKey(taskID), prURL, mergedSHA)
}

// retirementRecordPath returns the path for a pending retirement record.
// Uses a collision-safe filename based on schema + task ID.
func retirementRecordPath(homeDir, taskID string) string {
	digest := sha256.Sum256([]byte(taskID))
	return filepath.Join(homeDir, retirementDir, fmt.Sprintf("v%d-%s.json", PollRetirementSchema, hex.EncodeToString(digest[:])))
}

// retirementDirPath returns the retirement state directory path.
func retirementDirPath(homeDir string) string {
	return filepath.Join(homeDir, retirementDir)
}

// pollContentDigest computes a SHA-256 digest of the poll file content.
func pollContentDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading poll for digest: %w", err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// durableAppendStatus appends a status line with fsync durability.
// Scans existing lines first to avoid duplicate publication.
func durableAppendStatus(homeDir, taskID, line string) (bool, error) {
	// Scan existing lines for exact match.
	lines, err := home.ReadStatus(homeDir, taskID)
	if err != nil {
		// Status file may not exist yet; that's fine.
		lines = nil
	}
	for _, existing := range lines {
		if existing == line {
			return false, nil // already present; no-op
		}
	}

	// Open file for append with fsync.
	statusPath, err := home.StatusFilePath(homeDir, taskID)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(statusPath), 0755); err != nil {
		return false, fmt.Errorf("creating state dir: %w", err)
	}
	f, err := os.OpenFile(statusPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false, fmt.Errorf("opening status file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(line + "\n"); err != nil {
		return false, fmt.Errorf("writing status line: %w", err)
	}
	if err := f.Sync(); err != nil {
		return false, fmt.Errorf("fsync status file: %w", err)
	}
	return true, nil
}

// WriteRetirementRecord atomically writes a pending retirement record.
// Uses temp file + Sync + rename + directory Sync for durability.
func WriteRetirementRecord(homeDir string, rec *PollRetirementRecord) error {
	dir := retirementDirPath(homeDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating retirement dir: %w", err)
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshaling retirement record: %w", err)
	}

	path := retirementRecordPath(homeDir, rec.TaskID)
	tmpPath := path + ".tmp"

	// Write and fsync the temp through a single write-capable handle.
	// On Windows, FlushFileBuffers (os.File.Sync) requires a handle opened
	// for write, so a read-only reopen would fail; writing and syncing one
	// write handle preserves the durable-before-rename atomic contract on
	// both platforms.
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("writing retirement temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing retirement temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing retirement temp: %w", err)
	}

	// Atomic rename with directory sync for crash safety.
	if err := home.RenameDurable(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming retirement record: %w", err)
	}

	return nil
}

// ReadRetirementRecord reads a pending retirement record.
// Returns nil, nil if the file does not exist.
func ReadRetirementRecord(homeDir, taskID string) (*PollRetirementRecord, error) {
	path := retirementRecordPath(homeDir, taskID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading retirement record: %w", err)
	}

	var rec PollRetirementRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parsing retirement record: %w", err)
	}
	return &rec, nil
}

// RemoveRetirementRecord removes a completed retirement record atomically.
func RemoveRetirementRecord(homeDir, taskID string) error {
	path := retirementRecordPath(homeDir, taskID)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // idempotent
		}
		return fmt.Errorf("removing retirement record: %w", err)
	}
	// Directory sync after removal.
	dir := retirementDirPath(homeDir)
	dirF, err := os.Open(dir)
	if err != nil {
		return nil // best-effort
	}
	defer dirF.Close()
	dirF.Sync()
	return nil
}

// ListPendingRetirements returns all pending retirement record task IDs.
func ListPendingRetirements(homeDir string) ([]string, error) {
	dir := retirementDirPath(homeDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading retirement dir: %w", err)
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading retirement record %s: %w", entry.Name(), err)
		}
		var rec PollRetirementRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("parsing retirement record %s: %w", entry.Name(), err)
		}
		if rec.TaskID == "" || retirementRecordPath(homeDir, rec.TaskID) != filepath.Join(dir, entry.Name()) {
			return nil, fmt.Errorf("retirement record %s has invalid task identity", entry.Name())
		}
		ids = append(ids, rec.TaskID)
	}
	return ids, nil
}

// ValidateRetirementPath checks that the record path is safe and well-formed.
// Returns an error for symlinks, non-regular files, or path escape attempts.
func ValidateRetirementPath(homeDir, taskID string) error {
	path := retirementRecordPath(homeDir, taskID)
	dir := retirementDirPath(homeDir)

	// Check path stays within retirement directory.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving retirement dir: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving record path: %w", err)
	}
	if !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) &&
		absPath != absDir {
		return fmt.Errorf("retirement record path escapes retirement directory: %s", path)
	}

	// Use Lstat to detect symlinks and non-regular files.
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // not yet created; path is valid
		}
		return fmt.Errorf("lstat retirement record: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("retirement record is a symlink: %s", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("retirement record is not a regular file: %s", path)
	}
	return nil
}

// ValidateCheckWithLstat validates a check plugin with Lstat to reject
// symlinks and non-regular files. This is the crash-safe variant.
func ValidateCheckWithLstat(path string) error {
	// Use Lstat to detect symlinks.
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("check not found: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("check is a symlink (refused for crash safety): %s", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("check is not a regular file: %s", path)
	}
	// Must be executable. Recognition differs by platform: Unix requires the
	// owner-execute mode bit; Windows, where Go never reports exec bits, keys
	// executability off the shebang (see check_exec_windows.go). The fail-closed
	// rejections above (symlink, non-regular) and below (empty, missing shebang)
	// are shared.
	if err := checkExecutable(path, fi); err != nil {
		return err
	}
	// Read first line to verify shebang.
	data := make([]byte, 2)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening check: %w", err)
	}
	defer f.Close()
	n, err := f.Read(data)
	if err != nil || n < 2 {
		return fmt.Errorf("check is empty or unreadable: %s", path)
	}
	if data[0] != '#' || data[1] != '!' {
		return fmt.Errorf("check is missing shebang (#!): %s", path)
	}
	return nil
}

// RetireMergedPoll performs the full crash-safe poll retirement sequence for
// one per-task check whose poll script reports a merged PR. It returns nil only
// when retirement is complete; validation failures before retirement preserve
// the poll, while post-publication invalidation preserves the durable outcome
// and reports the artifact state.
//
// Sequence:
//  1. Validate task identity and poll digest against delivery meta.
//  2. Query provider merge status via QueryDeliveryMergeStatus.
//  3. Require Merged == true, nonempty provider head, provider-head == stored HeadSHA.
//  4. Persist the pending retirement record BEFORE publication.
//  5. Derive merged truth from the canonical committed delivery outcome:
//     a committed completed outcome is required; the .meta delivery_state
//     projection never authorizes merged truth and no parallel delivery
//     state is written here.
//  6. Durably publish one deterministic keyed status line.
//  7. Remove the exact poll artifact (with digest revalidation).
//  8. Remove the pending retirement record.
//
// Fail-closed on acceptance: validation refusal before publication preserves
// the externally authored poll. After publication, revalidation refusal cleans
// the record because the durable outcome already exists. Recovery completion
// is separate: it removes a previously committed poll only after publication
// evidence and the recorded content digest both match.
//
// The normal removal site requires successful pre-publication identity and
// canonical-outcome checks, confirmed publication, and a matching digest after
// publication. The recovery removal site requires the durable record, the
// expected safe poll basename, current delivery identity, canonical completion,
// deterministic recorded publication evidence, confirmed publication, and the
// recorded digest. Both sites retain the known verification-to-remove pathname
// replacement window tracked by #602; a replacement is not repaired here.
func RetireMergedPoll(homeDir, taskID, checkPath string, auth *taskauthority.Canonical) error {
	// Step 0: Lstat validation on check path for crash safety.
	if err := ValidateCheckWithLstat(checkPath); err != nil {
		return fmt.Errorf("%w: poll validation failed: %w", ErrCheckValidationRefused, err)
	}

	// Compute poll digest BEFORE any mutation.
	pollDigest, err := pollContentDigest(checkPath)
	if err != nil {
		return fmt.Errorf("poll digest: %w", err)
	}

	// Read task delivery identity.
	ident, err := requireRetirementIdentity(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("delivery identity: %w", err)
	}

	// Step 1: Query provider merge status.
	status, err := QueryDeliveryMergeStatus(ident)
	if err != nil {
		// Provider unavailable / query error: preserve poll, do not fail fatal.
		return fmt.Errorf("merge status query (preserving poll): %w", err)
	}

	// Step 2: Require merged with recognized state.
	if !status.Merged {
		if status.Closed {
			return fmt.Errorf("PR #%d is closed but not merged (preserving poll)", ident.Number)
		}
		return fmt.Errorf("PR #%d is not merged (state=%s, preserving poll)", ident.Number, status.State)
	}
	if status.HeadSHA == "" {
		return fmt.Errorf("provider returned empty head SHA for merged PR #%d (preserving poll)", ident.Number)
	}

	// Step 3: Verify provider head matches stored identity head.
	// Exact match required for crash safety.
	if ident.HeadSHA != "" && status.HeadSHA != ident.HeadSHA {
		return fmt.Errorf("head SHA mismatch: stored=%q provider=%q (preserving poll)", ident.HeadSHA, status.HeadSHA)
	}

	// Resolve merged SHA.
	mergedSHA := status.MergedSHA
	if mergedSHA == "" {
		mergedSHA = status.HeadSHA
	}

	// Build publication evidence.
	pubLine := publicationLine(taskID, ident.URL, mergedSHA)

	// Build poll path relative to state dir.
	pollRel := filepath.Base(checkPath) // "<id>.check" in state dir

	// Step 4: Persist pending retirement record BEFORE publication.
	now := time.Now().UTC().Format(time.RFC3339)
	rec := &PollRetirementRecord{
		SchemaVersion:   PollRetirementSchema,
		TaskID:          taskID,
		PollPath:        pollRel,
		PollDigest:      pollDigest,
		Provider:        ident.Provider,
		Owner:           ident.Owner,
		Repo:            ident.Repo,
		Number:          ident.Number,
		URL:             ident.URL,
		BaseRef:         ident.BaseRef,
		HeadRef:         ident.HeadRef,
		HeadSHA:         ident.HeadSHA,
		CapturedAt:      ident.CapturedAt,
		MergedSHA:       mergedSHA,
		PublicationLine: pubLine,
		DiscoveredAt:    now,
		RecordedAt:      now,
	}

	if err := WriteRetirementRecord(homeDir, rec); err != nil {
		return fmt.Errorf("persisting retirement record: %w", err)
	}

	// Step 5: Derive merged truth from the canonical committed delivery
	// outcome. A committed completed outcome is required before publication
	// and poll removal; the .meta delivery_state projection never authorizes
	// merged truth and no parallel delivery state is written here. Fail
	// closed: if the canonical outcome is missing or not completed, the
	// retirement record stays pending and the poll is preserved.
	if err := requireCanonicalCompletedOutcome(auth, taskID); err != nil {
		return fmt.Errorf("canonical merged truth required (pending record exists): %w", err)
	}

	// Step 6: Durable publication. Only appends if exact evidence is absent.
	appended, err := durableAppendStatus(homeDir, taskID, pubLine)
	if err != nil {
		// Publication failed; record is pending for recovery.
		return fmt.Errorf("publication failed (pending record exists): %w", err)
	}
	if !appended {
		// Already published (recovery from crash-after-publication).
		// Continue to poll removal.
	}

	// Step 7: Revalidate poll path and digest, then remove.
	if err := ValidateCheckWithLstat(checkPath); err != nil {
		// Poll was removed or became invalid after publication. The record is
		// cleaned because publication evidence is already durable.
		if cleanupErr := RemoveRetirementRecord(homeDir, taskID); cleanupErr != nil {
			return fmt.Errorf("%w: poll disappeared or became invalid after publication; cleanup failed: %v: %w", ErrCheckInvalidAfterPublication, cleanupErr, err)
		}
		return fmt.Errorf("%w: poll disappeared or became invalid after publication (record cleaned): %w", ErrCheckInvalidAfterPublication, err)
	}

	currentDigest, err := pollContentDigest(checkPath)
	if err != nil {
		// Poll disappeared between validation and digest.
		RemoveRetirementRecord(homeDir, taskID)
		return fmt.Errorf("poll disappeared after publication (record cleaned): %w", err)
	}

	// Digest must still match the record.
	if currentDigest != pollDigest {
		RemoveRetirementRecord(homeDir, taskID)
		// Return error but record is cleaned — publication evidence exists.
		return fmt.Errorf("poll digest changed between discovery and removal (record cleaned): old=%q new=%q",
			pollDigest, currentDigest)
	}

	// Remove the poll artifact.
	if err := os.Remove(checkPath); err != nil {
		if os.IsNotExist(err) {
			// Already removed; continue cleanup.
		} else {
			// Leave record pending for recovery on restart.
			return fmt.Errorf("removing poll artifact (pending record exists): %w", err)
		}
	}

	// Step 8: Remove the pending retirement record.
	if err := RemoveRetirementRecord(homeDir, taskID); err != nil {
		return fmt.Errorf("removing retirement record: %w", err)
	}

	return nil
}

func requireRetirementIdentity(homeDir, id string) (*domain.DeliveryIdentity, error) {
	meta, err := home.ReadMeta(homeDir, id)
	if err != nil {
		return nil, fmt.Errorf("reading task meta for identity: %w", err)
	}
	for _, key := range []string{"pr_provider", "pr_url", "pr_head_sha"} {
		if meta[key] == "" {
			return nil, fmt.Errorf("required identity field %q is missing", key)
		}
	}
	ident, err := domain.IdentityFromMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("parsing delivery identity: %w", err)
	}
	if ident == nil {
		return nil, fmt.Errorf("no delivery identity found for task %s", id)
	}
	if err := domain.ValidateIdentity(ident); err != nil {
		return nil, fmt.Errorf("incomplete delivery identity for task %s: %w", id, err)
	}
	return ident, nil
}

// requireCanonicalCompletedOutcome fails closed unless the task's canonical
// committed delivery outcome is completed. It is the single merged-truth
// derivation for poll retirement: no .meta delivery_state projection
// authorizes merged truth.
func requireCanonicalCompletedOutcome(auth *taskauthority.Canonical, taskID string) error {
	if auth == nil {
		return fmt.Errorf("canonical merged truth requires a composed task authority")
	}
	tid, err := domain.NewTaskID(taskID)
	if err != nil {
		return err
	}
	out, err := auth.DeliveryOutcome(tid)
	if err != nil {
		if errors.Is(err, taskauthority.ErrNotFound) {
			return fmt.Errorf("task %s has no canonical delivery outcome; a committed completed outcome is required", taskID)
		}
		return fmt.Errorf("resolving canonical delivery outcome: %w", err)
	}
	if out.Status != taskauthority.DeliveryOutcomeCompleted {
		return fmt.Errorf("task %s canonical delivery outcome is %q; a committed completed outcome is required", taskID, out.Status)
	}
	return nil
}

// RecoverPendingRetirement completes a crashed retirement sequence for one
// pending record. Returns true if the record was fully resolved (cleanup done
// or nothing left to do). Returns an error for unresolvable corruption.
//
// Recovery logic:
//  1. Validate record filename, schema, and file integrity.
//  2. Validate current task identity and canonical completion when available.
//  3. Validate deterministic recorded publication evidence, then append
//     publication only if exact evidence is absent.
//  4. Remove poll only after publication is confirmed and its content digest
//     matches the committed record (or accept an already-missing poll); a
//     digest mismatch preserves the poll and record.
//  5. Remove completed record.
//
// Validation refusal governs acceptance of a newly offered operator artifact;
// recovery instead requires durable retirement intent, canonical completion,
// confirmed publication, and this exact content identity. Current executable
// mode is not recovery identity.
func RecoverPendingRetirement(homeDir, taskID string, auth *taskauthority.Canonical) (bool, error) {
	// Validate record path.
	if err := ValidateRetirementPath(homeDir, taskID); err != nil {
		return false, fmt.Errorf("invalid retirement path: %w", err)
	}

	// Read record.
	rec, err := ReadRetirementRecord(homeDir, taskID)
	if err != nil {
		return false, fmt.Errorf("reading record: %w", err)
	}
	if rec == nil {
		return true, nil // nothing to recover
	}

	// Validate schema.
	if rec.SchemaVersion != PollRetirementSchema {
		return false, fmt.Errorf("unsupported schema version %d (expected %d)",
			rec.SchemaVersion, PollRetirementSchema)
	}

	// Recovery accepts only the canonical task check basename.
	expectedPollPath := taskID + ".check"
	if filepath.IsAbs(rec.PollPath) || strings.ContainsAny(rec.PollPath, `/\\`) || rec.PollPath != expectedPollPath {
		return false, fmt.Errorf("recovery: invalid poll path %q (preserving poll and retirement record)", rec.PollPath)
	}
	checkPath := filepath.Join(home.StateDir(homeDir), rec.PollPath)

	// Task metadata and canonical completion are required before recovery can
	// publish or remove anything. Missing or incomplete metadata preserves the
	// poll and record for the next recovery cycle.
	ident, identityErr := requireRetirementIdentity(homeDir, taskID)
	if identityErr != nil {
		return false, fmt.Errorf("recovery: task identity required (preserving poll and record): %w", identityErr)
	}
	// Delivery identity names one pull request at one commit: Provider,
	// Owner, Repo, Number, URL, and HeadSHA. BaseRef and HeadRef are mutable
	// PR attributes, and CapturedAt is observation time, so neither is part
	// of the identity comparison.
	if ident.Provider != rec.Provider {
		return false, fmt.Errorf("stale retirement: current provider=%q, record provider=%q", ident.Provider, rec.Provider)
	}
	if ident.Owner != rec.Owner {
		return false, fmt.Errorf("stale retirement: current owner=%q, record owner=%q", ident.Owner, rec.Owner)
	}
	if ident.Repo != rec.Repo {
		return false, fmt.Errorf("stale retirement: current repo=%q, record repo=%q", ident.Repo, rec.Repo)
	}
	if ident.Number != rec.Number {
		return false, fmt.Errorf("stale retirement: current number=%d, record number=%d", ident.Number, rec.Number)
	}
	if ident.URL != rec.URL {
		return false, fmt.Errorf("stale retirement: current pr_url=%q, record pr_url=%q", ident.URL, rec.URL)
	}
	if ident.HeadSHA != rec.HeadSHA {
		return false, fmt.Errorf("stale retirement: current head SHA=%q, record head SHA=%q", ident.HeadSHA, rec.HeadSHA)
	}

	if rec.MergedSHA == "" || rec.PublicationLine != publicationLine(taskID, rec.URL, rec.MergedSHA) {
		return false, fmt.Errorf("recovery: invalid publication evidence (preserving poll and retirement record)")
	}

	// Derive merged truth from the canonical committed delivery outcome:
	// a committed completed outcome is required before any poll artifact
	// is removed. The .meta delivery_state projection never authorizes
	// merged truth and no parallel delivery state is written here.
	if err := requireCanonicalCompletedOutcome(auth, taskID); err != nil {
		return false, fmt.Errorf("recovery: canonical merged truth required: %w", err)
	}

	// Check if poll still exists.
	pollExists := false
	pollMatches := false
	if fi, statErr := os.Lstat(checkPath); statErr == nil && fi.Mode().IsRegular() && fi.Mode()&os.ModeSymlink == 0 {
		pollExists = true
		if digest, digestErr := pollContentDigest(checkPath); digestErr == nil && digest == rec.PollDigest {
			pollMatches = true
		}
	}

	// Check if publication evidence exists.
	hasPublication := false
	lines, statusErr := home.ReadStatus(homeDir, taskID)
	if statusErr == nil {
		for _, line := range lines {
			if line == rec.PublicationLine {
				hasPublication = true
				break
			}
		}
	} else if !os.IsNotExist(statusErr.(*os.PathError).Err) && statusErr != nil {
		// status file missing is fine
	}

	// Append publication if absent.
	if !hasPublication {
		appended, err := durableAppendStatus(homeDir, taskID, rec.PublicationLine)
		if err != nil {
			return false, fmt.Errorf("recovery: durable append failed: %w", err)
		}
		if appended {
			hasPublication = true
		}
	}

	// If publication evidence does not exist after append attempt, we cannot
	// safely remove the poll. Preserve everything.
	if !hasPublication {
		return false, fmt.Errorf("recovery: publication could not be confirmed; preserving poll and record")
	}

	// Publication exists. Recovery deletes only after this confirmed
	// publication and an exact content-digest match. A changed poll is a
	// different artifact and must preserve the recovery record for operator
	// attention; current validation state does not change that identity check.
	if pollExists && !pollMatches {
		return false, fmt.Errorf("recovery: poll digest changed; preserving poll and retirement record")
	}
	if pollExists {
		if err := os.Remove(checkPath); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("recovery: removing poll: %w", err)
		}
	}

	// Remove the completed record.
	if err := RemoveRetirementRecord(homeDir, taskID); err != nil {
		return false, fmt.Errorf("recovery: removing record: %w", err)
	}

	return true, nil
}

// RecoverAllPendingRetirements scans all pending retirement records and
// completes each. Returns the count of fully resolved records and any
// non-fatal errors encountered. Records that fail recovery are preserved.
func RecoverAllPendingRetirements(homeDir string, auth *taskauthority.Canonical) (int, []error) {
	ids, err := ListPendingRetirements(homeDir)
	if err != nil {
		return 0, []error{fmt.Errorf("listing pending retirements: %w", err)}
	}

	var errors []error
	resolved := 0
	for _, id := range ids {
		if _, recErr := RecoverPendingRetirement(homeDir, id, auth); recErr != nil {
			errors = append(errors, fmt.Errorf("task %s: %w", id, recErr))
		} else {
			resolved++
		}
	}
	return resolved, errors
}
