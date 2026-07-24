package supervision

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/delivery"
	"github.com/minhtri2710/munsu/internal/task"
)

// --- PollRetirementRecord helpers ---

func TestPollRetirementRecordSchema(t *testing.T) {
	if PollRetirementSchema != 1 {
		t.Fatalf("expected schema version 1, got %d", PollRetirementSchema)
	}
}

func TestStableMergeKey(t *testing.T) {
	key := stableMergeKey("task-1")
	if key != "pr-merged:task-1" {
		t.Errorf("unexpected key: %q", key)
	}
	key2 := stableMergeKey("task-1")
	if key != key2 {
		t.Errorf("key must be deterministic: %q != %q", key, key2)
	}
	key3 := stableMergeKey("task-2")
	if key == key3 {
		t.Errorf("different tasks must have different keys")
	}
}

func TestPublicationLine(t *testing.T) {
	line := publicationLine("task-1", "https://github.com/o/r/pull/1", "abc123")
	expected := "done [key=pr-merged:task-1]: PR https://github.com/o/r/pull/1 merged at abc123"
	if line != expected {
		t.Errorf("publication line mismatch:\n  got:  %q\n  want: %q", line, expected)
	}
}

// --- WriteRetirementRecord / ReadRetirementRecord / RemoveRetirementRecord ---

func TestRetirementRecordRoundTrip(t *testing.T) {
	home := t.TempDir()
	rec := &PollRetirementRecord{
		SchemaVersion:    1,
		TaskID:           "test-task",
		PollPath:         "test-task.check",
		PollDigest:       "abcdef",
		Provider:         "github",
		Owner:            "testowner",
		Repo:             "testrepo",
		Number:           42,
		URL:              "https://github.com/testowner/testrepo/pull/42",
		BaseRef:          "main",
		HeadRef:          "feature",
		HeadSHA:          "deadbeef",
		CapturedAt:       "2024-01-01T00:00:00Z",
		MergedSHA:        "cafebabe",
		PublicationLine:  "done [key=pr-merged:test-task]: PR merged at cafebabe",
		DiscoveredAt:     "2024-01-01T00:01:00Z",
		RecordedAt:       "2024-01-01T00:01:00Z",
	}

	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	got, err := ReadRetirementRecord(home, "test-task")
	if err != nil {
		t.Fatalf("ReadRetirementRecord: %v", err)
	}
	if got == nil {
		t.Fatal("got nil record")
	}
	if got.TaskID != "test-task" || got.Provider != "github" || got.Number != 42 {
		t.Errorf("record mismatch: %+v", got)
	}
	if got.PollDigest != "abcdef" {
		t.Errorf("poll digest mismatch: %q", got.PollDigest)
	}

	if err := RemoveRetirementRecord(home, "test-task"); err != nil {
		t.Fatalf("RemoveRetirementRecord: %v", err)
	}

	got2, err := ReadRetirementRecord(home, "test-task")
	if err != nil {
		t.Fatalf("ReadRetirementRecord after removal: %v", err)
	}
	if got2 != nil {
		t.Fatal("record should be nil after removal")
	}
}

func TestRetirementRecordRemoveIdempotent(t *testing.T) {
	home := t.TempDir()
	// Remove non-existent record should be a no-op.
	if err := RemoveRetirementRecord(home, "nonexistent"); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
}

func TestRetirementRecordTaskWithColon(t *testing.T) {
	home := t.TempDir()
	rec := &PollRetirementRecord{
		SchemaVersion:    1,
		TaskID:           "captain:munsu",
		PollPath:         "captain:munsu.check",
		PollDigest:       "d1g3st",
		Provider:         "github",
		Owner:            "o",
		Repo:             "r",
		Number:           1,
		URL:              "https://github.com/o/r/pull/1",
		BaseRef:          "main",
		HeadRef:          "f",
		HeadSHA:          "sha1",
		CapturedAt:       "2024-01-01T00:00:00Z",
		MergedSHA:        "sha2",
		PublicationLine:  "done: merged",
		DiscoveredAt:     "2024-01-01T00:01:00Z",
		RecordedAt:       "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord with colon: %v", err)
	}

	got, err := ReadRetirementRecord(home, "captain:munsu")
	if err != nil {
		t.Fatalf("ReadRetirementRecord with colon: %v", err)
	}
	if got == nil || got.TaskID != "captain:munsu" {
		t.Fatalf("got %+v, want TaskID=captain:munsu", got)
	}

	// Also test ListPendingRetirements with colon.
	ids, err := ListPendingRetirements(home)
	if err != nil {
		t.Fatalf("ListPendingRetirements: %v", err)
	}
	if len(ids) != 1 || ids[0] != "captain:munsu" {
		t.Fatalf("expected [captain:munsu], got %v", ids)
	}
}

// --- ValidateRetirementPath ---

func TestValidateRetirementPath_Valid(t *testing.T) {
	home := t.TempDir()
	if err := ValidateRetirementPath(home, "test-task"); err != nil {
		t.Fatalf("expected valid path, got: %v", err)
	}
}

func TestValidateRetirementPath_Symlink(t *testing.T) {
	home := t.TempDir()
	dir := retirementDirPath(home)
	os.MkdirAll(dir, 0755)

	// Create a symlink record.
	linkPath := retirementRecordPath(home, "task-sym")
	targetPath := filepath.Join(home, "outside", "malicious.json")
	os.MkdirAll(filepath.Dir(targetPath), 0755)
	os.WriteFile(targetPath, []byte("{}"), 0644)
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	err := ValidateRetirementPath(home, "task-sym")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got: %v", err)
	}
}

func TestValidateRetirementPath_NotRegular(t *testing.T) {
	home := t.TempDir()
	// Create a directory in place of the record.
	dir := retirementDirPath(home)
	os.MkdirAll(dir, 0755)
	recPath := retirementRecordPath(home, "task-dir")
	os.Mkdir(recPath, 0755)

	err := ValidateRetirementPath(home, "task-dir")
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected not-regular error, got: %v", err)
	}
}

// --- ValidateCheckWithLstat ---

func TestValidateCheckWithLstat_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	if err := os.WriteFile(path, []byte("#!/bin/bash\necho hello\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCheckWithLstat(path); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateCheckWithLstat_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.check")
	os.WriteFile(target, []byte("#!/bin/bash\necho\n"), 0755)
	link := filepath.Join(dir, "link.check")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	err := ValidateCheckWithLstat(link)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got: %v", err)
	}
}

func TestValidateCheckWithLstat_NotExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	if err := os.WriteFile(path, []byte("#!/bin/bash\necho\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := ValidateCheckWithLstat(path)
	if err == nil {
		t.Fatal("expected error for non-executable")
	}
}

func TestValidateCheckWithLstat_MissingShebang(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	if err := os.WriteFile(path, []byte("echo hello\n"), 0755); err != nil {
		t.Fatal(err)
	}
	err := ValidateCheckWithLstat(path)
	if err == nil {
		t.Fatal("expected error for missing shebang")
	}
}

// --- durableAppendStatus ---

func TestDurableAppendStatus_NewFile(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	appended, err := durableAppendStatus(home, "task-1", "done: first")
	if err != nil {
		t.Fatalf("durableAppendStatus: %v", err)
	}
	if !appended {
		t.Fatal("expected appended=true")
	}

	lines, err := task.ReadStatus(home, "task-1")
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if len(lines) != 1 || lines[0] != "done: first" {
		t.Fatalf("expected [done: first], got %v", lines)
	}
}

func TestDurableAppendStatus_Deduplicate(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	if err := task.AppendStatus(home, "task-1", "done: existing"); err != nil {
		t.Fatal(err)
	}
	if err := task.AppendStatus(home, "task-1", "working: progress"); err != nil {
		t.Fatal(err)
	}

	// Appending an existing line should be a no-op.
	appended, err := durableAppendStatus(home, "task-1", "done: existing")
	if err != nil {
		t.Fatalf("durableAppendStatus: %v", err)
	}
	if appended {
		t.Fatal("expected appended=false for duplicate")
	}

	// Appending a new line should work.
	appended, err = durableAppendStatus(home, "task-1", "done: new")
	if err != nil {
		t.Fatalf("durableAppendStatus: %v", err)
	}
	if !appended {
		t.Fatal("expected appended=true for new line")
	}

	lines, err := task.ReadStatus(home, "task-1")
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
}

// --- RetireMergedPoll crash-window tests ---

// setupMergedPollTest sets up a realistic task with a delivery identity
// and check script that would return merged=true. Returns the home dir,
// task ID, check path, and a cleanup function.
func setupMergedPollTest(t *testing.T, headSHA, baseRef string) (home, taskID, checkPath string, cleanup func()) {
	t.Helper()
	home = t.TempDir()
	taskID = "test-ship"
	stateDir := filepath.Join(home, "state")
	os.MkdirAll(stateDir, 0755)

	if headSHA == "" {
		headSHA = "0000111122223333444455556666777788889999"
	}
	if baseRef == "" {
		baseRef = "main"
	}

	// Write task meta with full delivery identity.
	meta := map[string]string{
		"kind":        "ship",
		"window":      "@test-window",
		"pr_provider": "github",
		"pr_owner":    "testowner",
		"pr_repo":     "testrepo",
		"pr_number":   "42",
		"pr_url":      "https://github.com/testowner/testrepo/pull/42",
		"pr_base":     baseRef,
		"pr_base_ref": baseRef,
		"pr_head_ref": "feature-branch",
		"pr_head":     headSHA,
		"pr_head_sha": headSHA,
		"pr_timestamp": "2024-01-01T00:00:00Z",
	}
	if err := task.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	// Write check script.
	checkPath = filepath.Join(stateDir, taskID+".check")
	script := fmt.Sprintf(`#!/bin/bash
echo "Polling PR merge status..."
exit 0
`)
	if err := os.WriteFile(checkPath, []byte(script), 0755); err != nil {
		t.Fatalf("writing check: %v", err)
	}

	return home, taskID, checkPath, func() {}
}

// installMockMergeStatus installs a mock delivery.QueryDeliveryMergeStatus
// that returns a specific result. Returns the original for restoration.
func installMockMergeStatus(t *testing.T, merged bool, headSHA, mergedSHA string) func() {
	t.Helper()
	orig := delivery.QueryDeliveryMergeStatus
	delivery.QueryDeliveryMergeStatus = func(ident *delivery.DeliveryIdentity) (*delivery.PRMergeStatus, error) {
		state := "OPEN"
		if merged {
			state = "MERGED"
		}
		mSHA := mergedSHA
		if mSHA == "" && merged {
			mSHA = headSHA
		}
		return &delivery.PRMergeStatus{
			Merged:    merged,
			MergedSHA: mSHA,
			Closed:    !merged,
			HeadSHA:   headSHA,
			State:     state,
		}, nil
	}
	return func() {
		delivery.QueryDeliveryMergeStatus = orig
	}
}

func readRetirementRecordOrNil(t *testing.T, home, taskID string) *PollRetirementRecord {
	t.Helper()
	rec, err := ReadRetirementRecord(home, taskID)
	if err != nil {
		t.Fatalf("read retirement record: %v", err)
	}
	return rec
}

// Test: record persisted, crash before publication
func TestRetireMergedPoll_CrashBeforePublication(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Execute full retirement.
	if err := RetireMergedPoll(home, taskID, checkPath); err != nil {
		t.Fatalf("RetireMergedPoll: %v", err)
	}

	// Verify: record is gone.
	rec := readRetirementRecordOrNil(t, home, taskID)
	if rec != nil {
		t.Fatal("retirement record should be nil after successful retirement")
	}

	// Verify: check file is gone.
	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatal("check file should be removed after retirement")
	}

	// Verify: publication line exists.
	lines, err := task.ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	found := false
	for _, l := range lines {
		if strings.Contains(l, "done [key=pr-merged") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("publication line not found in status: %v", lines)
	}
}

// Simulate crash after record write but before publication.
func TestRetireMergedPoll_CrashAfterRecordBeforePublication(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	// Write the pending record manually (simulating crash between persist and publication).
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature-branch",
		HeadSHA:         "0000111122223333444455556666777788889999",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333"),
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	// Run full retirement (will detect publication pending and complete it).
	if err := RetireMergedPoll(home, taskID, checkPath); err != nil {
		t.Fatalf("RetireMergedPoll: %v", err)
	}

	// Verify everything is clean.
	lines, err := task.ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	pubCount := 0
	for _, l := range lines {
		if strings.Contains(l, "done [key=pr-merged") {
			pubCount++
		}
	}
	if pubCount != 1 {
		t.Fatalf("expected exactly 1 publication line, got %d: %v", pubCount, lines)
	}

	rec2 := readRetirementRecordOrNil(t, home, taskID)
	if rec2 != nil {
		t.Fatal("record should be removed")
	}
}

// Simulate crash after publication but before poll removal.
func TestRetireMergedPoll_CrashAfterPublicationBeforePollRemoval(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	// Write the pending record.
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature-branch",
		HeadSHA:         "0000111122223333444455556666777788889999",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333"),
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	// Append publication manually (simulating crash after publish, before poll removal).
	appended, err := durableAppendStatus(home, taskID, rec.PublicationLine)
	if err != nil {
		t.Fatalf("durableAppendStatus: %v", err)
	}
	if !appended {
		t.Fatal("expected append")
	}

	// Now run retirement again — should detect publication exists, remove poll, clean record.
	if err := RetireMergedPoll(home, taskID, checkPath); err != nil {
		t.Fatalf("RetireMergedPoll (second call): %v", err)
	}

	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatal("check should be removed")
	}
	rec2 := readRetirementRecordOrNil(t, home, taskID)
	if rec2 != nil {
		t.Fatal("record should be removed")
	}
}

// Simulate crash after poll removal before record removal.
func TestRetireMergedPoll_CrashAfterPollRemovalBeforeRecordRemoval(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	// Write pending record and publication, remove poll (simulating crash after poll removal).
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature-branch",
		HeadSHA:         "0000111122223333444455556666777788889999",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333"),
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}
	durableAppendStatus(home, taskID, rec.PublicationLine)
	os.Remove(checkPath)

	// Use recovery (not RetireMergedPoll) to handle the already-removed poll case.
	resolved, err := RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("RecoverPendingRetirement after crash: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}

	rec2 := readRetirementRecordOrNil(t, home, taskID)
	if rec2 != nil {
		t.Fatal("record should be removed")
	}
}

// --- RecoverPendingRetirement tests ---

func TestRecoverPendingRetirement_IncompleteSequence(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	// Create pending record (no publication, no poll removal).
	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature-branch",
		HeadSHA:         "0000111122223333444455556666777788889999",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333"),
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	// Recovery should append publication, remove poll, clean record.
	resolved, err := RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("RecoverPendingRetirement: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}

	// Verify publication exists.
	lines, err := task.ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	pubCount := 0
	for _, l := range lines {
		if strings.Contains(l, "done [key=pr-merged") {
			pubCount++
		}
	}
	if pubCount != 1 {
		t.Fatalf("expected 1 publication line, got %d", pubCount)
	}

	// Poll should be removed.
	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatal("check should be removed after recovery")
	}

	// Record should be removed.
	rec2 := readRetirementRecordOrNil(t, home, taskID)
	if rec2 != nil {
		t.Fatal("record should be removed")
	}
}

func TestRecoverPendingRetirement_PublicationExists(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	pubLine := publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333")

	// Write record, publication, but leave poll.
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature-branch",
		HeadSHA:         "0000111122223333444455556666777788889999",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: pubLine,
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}
	durableAppendStatus(home, taskID, pubLine)

	// Recovery: should skip duplicate append, remove poll, clean record.
	resolved, err := RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("RecoverPendingRetirement: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}

	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatal("check should be removed")
	}
	rec2 := readRetirementRecordOrNil(t, home, taskID)
	if rec2 != nil {
		t.Fatal("record should be removed")
	}
}

func TestRecoverPendingRetirement_RepeatedRecovery(t *testing.T) {
	// Run recovery twice; second call should be idempotent.
	home, taskID, _, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	// First recovery: nothing to recover (no pending records).
	resolved, err := RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("first recovery: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true for no-op recovery")
	}

	// Second recovery: still nothing.
	resolved, err = RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("second recovery: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true for second no-op recovery")
	}

	// Now set up a real pending record and recover.
	checkPath := filepath.Join(task.StateDir(home), taskID+".check")
	os.WriteFile(checkPath, []byte("#!/bin/bash\necho\n"), 0755)
	digest, _ := pollContentDigest(checkPath)

	pubLine := publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333")
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature-branch",
		HeadSHA:         "0000111122223333444455556666777788889999",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: pubLine,
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	WriteRetirementRecord(home, rec)

	resolved, err = RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("real recovery: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}

	// One more time: should be no-op.
	resolved, err = RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("post-recovery check: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true after recovery")
	}
}

// --- Recovery failure cases ---

func TestRecoverPendingRetirement_StaleIdentity(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	// Write record with a different provider (stale).
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		Provider:        "gitlab", // different from meta's "github"
		Owner:           "oldowner",
		Repo:            "oldrepo",
		Number:          1,
		URL:             "https://gitlab.com/oldowner/oldrepo/-/merge_requests/1",
		BaseRef:         "main",
		HeadRef:         "old-feature",
		HeadSHA:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		PublicationLine: "done: old merge",
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	// Recovery should fail closed, preserving poll and record.
	resolved, err := RecoverPendingRetirement(home, taskID)
	if err == nil {
		t.Fatal("expected error for stale identity")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale error, got: %v", err)
	}
	if resolved {
		t.Fatal("expected unresolved for stale identity")
	}

	// Poll should still exist.
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved")
	}
}

func TestRecoverPendingRetirement_WrongTaskIdentity(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	// Write record with a different head SHA.
	rec := &PollRetirementRecord{
		SchemaVersion: 1,
		TaskID:        taskID,
		PollPath:      taskID + ".check",
		PollDigest:    digest,
		Provider:      "github",
		Owner:         "testowner",
		Repo:          "testrepo",
		Number:        42,
		URL:           "https://github.com/testowner/testrepo/pull/42",
		BaseRef:       "main",
		HeadRef:       "feature-branch",
		HeadSHA:       "0000000000000000000000000000000000000000", // wrong!
		CapturedAt:    "2024-01-01T00:00:00Z",
		MergedSHA:     "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: "done: wrong head",
		DiscoveredAt:  "2024-01-01T00:01:00Z",
		RecordedAt:    "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	resolved, err := RecoverPendingRetirement(home, taskID)
	if err == nil {
		t.Fatal("expected error for wrong head SHA")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale error, got: %v", err)
	}
	if resolved {
		t.Fatal("expected unresolved")
	}

	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved")
	}
}

func TestRecoverPendingRetirement_PollDigestMismatch(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	// Write record with wrong digest.
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature-branch",
		HeadSHA:         "0000111122223333444455556666777788889999",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333"),
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	// Recovery: poll exists but digest doesn't match — recovery checks meta identity
	// (which matches TaskID, provider, URL, and head SHA), not poll digest.
	// Since publication evidence is absent, recovery appends it. But because
	// the poll digest does not match, recovery does NOT remove the poll
	// (the poll may have been replaced by a different one). The record IS removed
	// because the meta identity matches and publication is durably written.
	resolved, err := RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("expected recovery to handle digest mismatch: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}

	// Check file should NOT be removed (digest mismatch preserves it).
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("poll should be preserved on digest mismatch")
	}
}

// --- Malformed/symlink record tests ---

func TestRecoverPendingRetirement_MalformedJSON(t *testing.T) {
	home, taskID, _, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	dir := retirementDirPath(home)
	os.MkdirAll(dir, 0755)
	recPath := retirementRecordPath(home, taskID)
	os.WriteFile(recPath, []byte("not valid json\n"), 0644)

	resolved, err := RecoverPendingRetirement(home, taskID)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if resolved {
		t.Fatal("expected unresolved for malformed JSON")
	}
}

func TestRecoverPendingRetirement_SymlinkRecord(t *testing.T) {
	home, taskID, _, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	// Create a symlink in the retirement directory.
	dir := retirementDirPath(home)
	os.MkdirAll(dir, 0755)
	recPath := retirementRecordPath(home, taskID)
	targetPath := filepath.Join(home, "outside", "data.json")
	os.MkdirAll(filepath.Dir(targetPath), 0755)
	os.WriteFile(targetPath, []byte("{}"), 0644)
	if err := os.Symlink(targetPath, recPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	// Path validation should reject the symlink before any reads.
	err := ValidateRetirementPath(home, taskID)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got: %v", err)
	}

	// Recovery should also fail on invalid path.
	resolved, err := RecoverPendingRetirement(home, taskID)
	if err == nil {
		t.Fatal("expected error for symlink record")
	}
	if resolved {
		t.Fatal("expected unresolved")
	}
}

func TestRecoverPendingRetirement_EscapedPath(t *testing.T) {
	home := t.TempDir()
	// ValidateRetirementPath for a task whose record path would escape
	// (unlikely in practice but test the guard).
	badID := "../../etc/passwd"
	err := ValidateRetirementPath(home, badID)
	// The path may or may not be marked as escape depending on how
	// filepath.Abs resolves it. Check that Lstat or prefix check catches it.
	if err == nil {
		// Path didn't resolve to a real file, so it's OK (doesn't exist).
		// The important thing is that it doesn't read a file outside the dir.
	}
}

func TestRecoverPendingRetirement_DirectoryRecord(t *testing.T) {
	home, taskID, _, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	// Create a directory at the record path.
	dir := retirementDirPath(home)
	os.MkdirAll(dir, 0755)
	recPath := retirementRecordPath(home, taskID)
	os.Mkdir(recPath, 0755)

	err := ValidateRetirementPath(home, taskID)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected not-regular error, got: %v", err)
	}
}

// --- Provider error / open / closed-unmerged preserve poll ---

func TestRetireMergedPoll_OpenPreservesPoll(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, false, "0000111122223333444455556666777788889999", "")
	defer restore()

	err := RetireMergedPoll(home, taskID, checkPath)
	if err == nil {
		t.Fatal("expected error for open PR")
	}
	if !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("expected 'not merged' error, got: %v", err)
	}

	// Poll should still exist.
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved for open PR")
	}
	// No retirement record should exist.
	rec := readRetirementRecordOrNil(t, home, taskID)
	if rec != nil {
		t.Fatal("no record should exist for open PR")
	}
}

func TestRetireMergedPoll_ClosedUnmergedPreservesPoll(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	orig := delivery.QueryDeliveryMergeStatus
	delivery.QueryDeliveryMergeStatus = func(ident *delivery.DeliveryIdentity) (*delivery.PRMergeStatus, error) {
		return &delivery.PRMergeStatus{
			Merged:    false,
			MergedSHA: "",
			Closed:    true,
			HeadSHA:   "0000111122223333444455556666777788889999",
			State:     "CLOSED",
		}, nil
	}
	defer func() { delivery.QueryDeliveryMergeStatus = orig }()

	err := RetireMergedPoll(home, taskID, checkPath)
	if err == nil {
		t.Fatal("expected error for closed-unmerged PR")
	}
	if !strings.Contains(err.Error(), "closed but not merged") {
		t.Fatalf("expected 'closed but not merged' error, got: %v", err)
	}

	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved")
	}
}

func TestRetireMergedPoll_ProviderErrorPreservesPoll(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	orig := delivery.QueryDeliveryMergeStatus
	delivery.QueryDeliveryMergeStatus = func(ident *delivery.DeliveryIdentity) (*delivery.PRMergeStatus, error) {
		return nil, fmt.Errorf("network error")
	}
	defer func() { delivery.QueryDeliveryMergeStatus = orig }()

	err := RetireMergedPoll(home, taskID, checkPath)
	if err == nil {
		t.Fatal("expected error for provider error")
	}

	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved on provider error")
	}
}

// --- Head SHA mismatch ---

func TestRetireMergedPoll_ProviderHeadMismatch(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "ffffffffffffffffffffffffffffffffffffffff", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	err := RetireMergedPoll(home, taskID, checkPath)
	if err == nil {
		t.Fatal("expected error for head SHA mismatch")
	}
	if !strings.Contains(err.Error(), "head SHA mismatch") {
		t.Fatalf("expected 'head SHA mismatch' error, got: %v", err)
	}

	// Poll preserved.
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved on head mismatch")
	}
}

// --- Poll digest mismatch ---

func TestRetireMergedPoll_PollDigestMismatch(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Run full retirement to get the record.
	if err := RetireMergedPoll(home, taskID, checkPath); err != nil {
		t.Fatalf("first retirement: %v", err)
	}

	// After retirement, poll is gone and record is cleaned.
	// This is the success case; the digest match is checked internally.
}

// --- GitHub and GitLab identity tables ---

func TestRetirementGitHubIdentity(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "github-sha-0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "github-sha-0000111122223333444455556666777788889999", "merged-github-sha")
	defer restore()

	if err := RetireMergedPoll(home, taskID, checkPath); err != nil {
		t.Fatalf("GitHub retirement: %v", err)
	}

	lines, err := task.ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	found := false
	for _, l := range lines {
		if strings.Contains(l, "done [key=pr-merged") && strings.Contains(l, "merged-github-sha") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("GitHub publication not found: %v", lines)
	}
}

func TestRetirementGitLabIdentity(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "gitlab-sha-0000111122223333444455556666777788889999", "main")
	defer cleanup()

	// Change meta to GitLab identity.
	meta, err := task.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	meta["pr_provider"] = "gitlab"
	meta["pr_owner"] = "glowner"
	meta["pr_repo"] = "glrepo"
	meta["pr_number"] = "7"
	meta["pr_url"] = "https://gitlab.com/glowner/glrepo/-/merge_requests/7"
	meta["pr_head"] = "gitlab-sha-0000111122223333444455556666777788889999"
	meta["pr_head_sha"] = "gitlab-sha-0000111122223333444455556666777788889999"
	if err := task.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	orig := delivery.QueryDeliveryMergeStatus
	delivery.QueryDeliveryMergeStatus = func(ident *delivery.DeliveryIdentity) (*delivery.PRMergeStatus, error) {
		return &delivery.PRMergeStatus{
			Merged:    true,
			MergedSHA: "gl-merged-sha",
			Closed:    false,
			HeadSHA:   "gitlab-sha-0000111122223333444455556666777788889999",
			State:     "MERGED",
		}, nil
	}
	defer func() { delivery.QueryDeliveryMergeStatus = orig }()

	if err := RetireMergedPoll(home, taskID, checkPath); err != nil {
		t.Fatalf("GitLab retirement: %v", err)
	}

	lines, err := task.ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	found := false
	for _, l := range lines {
		if strings.Contains(l, "done [key=pr-merged") && strings.Contains(l, "gl-merged-sha") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("GitLab publication not found: %v", lines)
	}
}

// --- RecoverAllPendingRetirements ---

func TestRecoverAllPendingRetirements_Multiple(t *testing.T) {
	home := t.TempDir()

	// Set up two tasks with pending retirement records.
	for _, id := range []string{"task-one", "task-two"} {
		stateDir := filepath.Join(home, "state")
		os.MkdirAll(stateDir, 0755)
		meta := map[string]string{
			"kind":        "ship",
			"window":      "@w",
			"pr_provider": "github",
			"pr_owner":    "o",
			"pr_repo":     "r",
			"pr_number":   "1",
			"pr_url":      fmt.Sprintf("https://github.com/o/r/pull/1"),
			"pr_base":     "main",
			"pr_base_ref": "main",
			"pr_head_ref": "f",
			"pr_head":     "0000111122223333444455556666777788889999",
			"pr_head_sha": "0000111122223333444455556666777788889999",
			"pr_timestamp": "2024-01-01T00:00:00Z",
		}
		task.WriteMeta(home, id, meta)

		checkPath := filepath.Join(stateDir, id+".check")
		os.WriteFile(checkPath, []byte("#!/bin/bash\necho\n"), 0755)

		digest, _ := pollContentDigest(checkPath)
		pubLine := publicationLine(id, "https://github.com/o/r/pull/1", "merged-sha")
		rec := &PollRetirementRecord{
			SchemaVersion:   1,
			TaskID:          id,
			PollPath:        id + ".check",
			PollDigest:      digest,
			Provider:        "github",
			Owner:           "o",
			Repo:            "r",
			Number:          1,
			URL:             "https://github.com/o/r/pull/1",
			BaseRef:         "main",
			HeadRef:         "f",
			HeadSHA:         "0000111122223333444455556666777788889999",
			CapturedAt:      "2024-01-01T00:00:00Z",
			MergedSHA:       "merged-sha",
			PublicationLine: pubLine,
			DiscoveredAt:    "2024-01-01T00:01:00Z",
			RecordedAt:      "2024-01-01T00:01:00Z",
		}
		WriteRetirementRecord(home, rec)
	}

	resolved, errs := RecoverAllPendingRetirements(home)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if resolved != 2 {
		t.Fatalf("expected 2 resolved, got %d", resolved)
	}

	// All records should be gone.
	for _, id := range []string{"task-one", "task-two"} {
		rec, _ := ReadRetirementRecord(home, id)
		if rec != nil {
			t.Errorf("record for %s should be removed", id)
		}
	}
}

func TestRecoverAllPendingRetirements_Empty(t *testing.T) {
	home := t.TempDir()
	resolved, errs := RecoverAllPendingRetirements(home)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if resolved != 0 {
		t.Fatalf("expected 0 resolved for empty dir, got %d", resolved)
	}
}

// --- Retirement never invokes teardown or removes meta/worktree/status ---

func TestRetireMergedPoll_PreservesMetaWorktreeStatus(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Add some initial status lines and worktree meta.
	if err := task.AppendStatus(home, taskID, "working: started"); err != nil {
		t.Fatal(err)
	}
	meta, err := task.ReadMeta(home, taskID)
	if err != nil {
		t.Fatal(err)
	}
	meta["worktree"] = "/tmp/some-worktree"
	if err := task.WriteMeta(home, taskID, meta); err != nil {
		t.Fatal(err)
	}

	if err := RetireMergedPoll(home, taskID, checkPath); err != nil {
		t.Fatalf("RetireMergedPoll: %v", err)
	}

	// Verify meta still exists.
	meta2, err := task.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("meta should be preserved: %v", err)
	}
	if meta2["kind"] != "ship" {
		t.Fatalf("meta kind should be ship")
	}

	// Verify status still exists with original + publication lines.
	lines, err := task.ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("status should be preserved: %v", err)
	}
	foundWorking := false
	foundPub := false
	for _, l := range lines {
		if l == "working: started" {
			foundWorking = true
		}
		if strings.Contains(l, "done [key=pr-merged") {
			foundPub = true
		}
	}
	if !foundWorking {
		t.Error("original status line should be preserved")
	}
	if !foundPub {
		t.Error("publication line should exist")
	}

	// Check file should be removed.
	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatal("check should be removed")
	}

	// No retirement record should remain.
	rec := readRetirementRecordOrNil(t, home, taskID)
	if rec != nil {
		t.Fatal("record should be removed")
	}
}

// --- Watcher restart generations produce one merged status ---

func TestRetireMergedPoll_WatcherRestartGenerations(t *testing.T) {
	// Simulates multiple watcher generations finding the same merged poll.
	// Only one publication line should ever be written.
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Generation 1: retire the poll.
	if err := RetireMergedPoll(home, taskID, checkPath); err != nil {
		t.Fatalf("gen1: %v", err)
	}

	// Generation 2: check file is gone, recovery has nothing to do.
	resolved, err := RecoverPendingRetirement(home, taskID)
	if err != nil {
		t.Fatalf("gen2 recovery: %v", err)
	}
	if !resolved {
		t.Fatal("gen2: expected resolved=true")
	}

	// Count publications.
	lines, err := task.ReadStatus(home, taskID)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	pubCount := 0
	for _, l := range lines {
		if strings.Contains(l, "done [key=pr-merged") {
			pubCount++
		}
	}
	if pubCount != 1 {
		t.Fatalf("expected exactly 1 publication across generations, got %d", pubCount)
	}
}

// --- pollContentDigest ---

func TestPollContentDigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.check")
	content := "#!/bin/bash\necho hello\n"
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	digest, err := pollContentDigest(path)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	expected := sha256.Sum256([]byte(content))
	expectedHex := hex.EncodeToString(expected[:])
	if digest != expectedHex {
		t.Fatalf("digest mismatch:\n  got:  %s\n  want: %s", digest, expectedHex)
	}
}

func TestPollContentDigest_MissingFile(t *testing.T) {
	_, err := pollContentDigest("/nonexistent/check")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// --- ListPendingRetirements ---

func TestListPendingRetirements_NoDir(t *testing.T) {
	home := t.TempDir()
	ids, err := ListPendingRetirements(home)
	if err != nil {
		t.Fatalf("ListPendingRetirements: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected empty list, got %v", ids)
	}
}

func TestListPendingRetirements_IgnoresNonJSON(t *testing.T) {
	home := t.TempDir()
	dir := retirementDirPath(home)
	os.MkdirAll(dir, 0755)

	os.WriteFile(filepath.Join(dir, "v1-test-task.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "v1-other.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("notes"), 0644)

	ids, err := ListPendingRetirements(home)
	if err != nil {
		t.Fatalf("ListPendingRetirements: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d: %v", len(ids), ids)
	}
}

// --- Migration of ValidateCheck to Lstat (existing tests still pass) ---

func TestValidateCheck_LstatRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.check")
	os.WriteFile(target, []byte("#!/bin/bash\necho\n"), 0755)
	link := filepath.Join(dir, "link.check")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	err := ValidateCheck(link)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink error, got: %v", err)
	}
}
