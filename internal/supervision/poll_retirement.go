// Package supervision provides watcher check plugin infrastructure including
// crash-safe retirement of merged PR poll artifacts.
package supervision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/task"
)

// PollRetirementSchema is the schema version for PollRetirementRecord.
const PollRetirementSchema = 1

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
	// Safe filename: replace path separators and colons.
	safeID := strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(taskID)
	return filepath.Join(homeDir, retirementDir, fmt.Sprintf("v%d-%s.json", PollRetirementSchema, safeID))
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
	lines, err := task.ReadStatus(homeDir, taskID)
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
	statusPath := filepath.Join(task.StateDir(homeDir), taskID+".status")
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

	// Write temp file.
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("writing retirement temp: %w", err)
	}

	// Sync the temp file before rename.
	f, err := os.OpenFile(tmpPath, os.O_RDONLY, 0644)
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("opening temp for sync: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("fsync temp: %w", err)
	}
	f.Close()

	// Atomic rename.
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming retirement record: %w", err)
	}

	// Directory sync for crash safety.
	dirF, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening retirement dir for sync: %w", err)
	}
	defer dirF.Close()
	if err := dirF.Sync(); err != nil {
		return fmt.Errorf("fsync retirement dir: %w", err)
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
		// Parse safe-ID back from filename: "v1-<safeID>.json"
		name := entry.Name()
		trimmed := strings.TrimPrefix(name, fmt.Sprintf("v%d-", PollRetirementSchema))
		trimmed = strings.TrimSuffix(trimmed, ".json")
		if trimmed == "" || trimmed == name {
			continue
		}
		// Reverse the safe-ID encoding.
		id := strings.NewReplacer("_", ":").Replace(trimmed)
		ids = append(ids, id)
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
	// Must be executable (owner at minimum).
	if fi.Mode()&0100 == 0 {
		return fmt.Errorf("check is not executable: %s", path)
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
// one per-task check whose poll script reports a merged PR. Returns true if
// retirement was completed (record written, published, poll removed, record cleaned).
//
// Sequence:
//  1. Validate task identity and poll digest against delivery meta.
//  2. Query provider merge status via QueryDeliveryMergeStatus.
//  3. Require Merged == true, nonempty provider head, provider-head == stored HeadSHA.
//  4. Persist the pending retirement record BEFORE publication.
//  5. Atomically transition delivery_state to merged via MarkMerged.
//  6. Durably publish one deterministic keyed status line.
//  7. Remove the exact poll artifact (with digest revalidation).
//  8. Remove the pending retirement record.
//
// Fail-closed on any validation step: preserves poll and all artifacts.
func RetireMergedPoll(homeDir, taskID, checkPath string) error {
	// Step 0: Lstat validation on check path for crash safety.
	if err := ValidateCheckWithLstat(checkPath); err != nil {
		return fmt.Errorf("poll validation failed: %w", err)
	}

	// Compute poll digest BEFORE any mutation.
	pollDigest, err := pollContentDigest(checkPath)
	if err != nil {
		return fmt.Errorf("poll digest: %w", err)
	}

	// Read task delivery identity.
	ident, err := delivery.RequireIdentity(homeDir, taskID)
	if err != nil {
		return fmt.Errorf("delivery identity: %w", err)
	}

	// Step 1: Query provider merge status.
	status, err := delivery.QueryDeliveryMergeStatus(ident)
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

	// Step 5: Atomically transition delivery_state to merged.
	// This ensures teardown accepts without --force after external merge.
	// Fail-closed: if CAS fails, the retirement record stays pending and
	// the next cycle retries via recovery. The publication and poll removal
	// below do NOT proceed until meta is consistent.
	if err := markDeliveryMerged(homeDir, taskID, ident); err != nil {
		return fmt.Errorf("delivery_state CAS failed (pending record exists): %w", err)
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
		// Poll was removed or became invalid between discovery and retirement.
		// Check if publication evidence exists (recovery step handles this).
		// Clean up the record since evidence is durably published.
		RemoveRetirementRecord(homeDir, taskID)
		return fmt.Errorf("poll disappeared or became invalid after publication (record cleaned): %w", err)
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

// removePollWithValidation validates a poll path against an expected digest
// and removes it. Returns os.ErrNotExist if the file is already gone.
func removePollWithValidation(checkPath, expectedDigest string) error {
	if err := ValidateCheckWithLstat(checkPath); err != nil {
		return err
	}
	currentDigest, err := pollContentDigest(checkPath)
	if err != nil {
		return err
	}
	if currentDigest != expectedDigest {
		return fmt.Errorf("poll digest changed: old=%q new=%q", expectedDigest, currentDigest)
	}
	return os.Remove(checkPath)
}

// markDeliveryMerged is a seam over delivery.MarkMerged, replaceable in tests.
var markDeliveryMerged = delivery.MarkMerged

// recordToIdentity builds a DeliveryIdentity from a PollRetirementRecord for
// use in the recovery path's MarkMerged call.
func recordToIdentity(rec *PollRetirementRecord) *delivery.DeliveryIdentity {
	return &delivery.DeliveryIdentity{
		Provider: rec.Provider,
		Owner:    rec.Owner,
		Repo:     rec.Repo,
		Number:   rec.Number,
		URL:      rec.URL,
		BaseRef:  rec.BaseRef,
		HeadRef:  rec.HeadRef,
		HeadSHA:  rec.HeadSHA,
	}
}

// RecoverPendingRetirement completes a crashed retirement sequence for one
// pending record. Returns true if the record was fully resolved (cleanup done
// or nothing left to do). Returns an error for unresolvable corruption.
//
// Recovery logic:
//  1. Validate record filename, schema, and file integrity.
//  2. If poll still exists and matches digest, revalidate against current meta.
//  3. Append publication only if exact evidence is absent.
//  4. Remove poll only after evidence exists (or accept missing poll).
//  5. Remove completed record.
func RecoverPendingRetirement(homeDir, taskID string) (bool, error) {
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

	// Build the poll path.
	checkPath := filepath.Join(task.StateDir(homeDir), rec.PollPath)

	// Attempt to validate current task identity (best-effort; corruption
	// preserves everything).
	currentMeta, metaErr := task.ReadMeta(homeDir, taskID)
	if metaErr == nil {
		if currentProvider := currentMeta["pr_provider"]; currentProvider != "" && currentProvider != rec.Provider {
			return false, fmt.Errorf("stale retirement: current provider=%q, record provider=%q", currentProvider, rec.Provider)
		}
		if currentURL := currentMeta["pr_url"]; currentURL != "" && currentURL != rec.URL {
			return false, fmt.Errorf("stale retirement: current pr_url=%q, record pr_url=%q", currentURL, rec.URL)
		}
		if currentHead := currentMeta["pr_head_sha"]; currentHead != "" && currentHead != rec.HeadSHA {
			return false, fmt.Errorf("stale retirement: current head SHA=%q, record head SHA=%q", currentHead, rec.HeadSHA)
		}

		// Atomically transition delivery_state to merged.
		// This heals orphaned retirement records and is idempotent
		// (no-op if already merged). Fail-closed: preserves record
		// and poll for the next recovery cycle.
		if currentMeta[delivery.MetaDeliveryState] != string(delivery.DeliveryStateMerged) {
			if err := markDeliveryMerged(homeDir, taskID, recordToIdentity(rec)); err != nil {
				return false, fmt.Errorf("recovery: delivery_state CAS failed: %w", err)
			}
		}
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
	lines, statusErr := task.ReadStatus(homeDir, taskID)
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

	// Publication exists. Now remove poll if it still matches.
	if pollExists && pollMatches {
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
func RecoverAllPendingRetirements(homeDir string) (int, []error) {
	ids, err := ListPendingRetirements(homeDir)
	if err != nil {
		return 0, []error{fmt.Errorf("listing pending retirements: %w", err)}
	}

	var errors []error
	resolved := 0
	for _, id := range ids {
		if _, recErr := RecoverPendingRetirement(homeDir, id); recErr != nil {
			errors = append(errors, fmt.Errorf("task %s: %w", id, recErr))
		} else {
			resolved++
		}
	}
	return resolved, errors
}
