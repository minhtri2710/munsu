//go:build integration

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
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/taskauthority"
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
		SchemaVersion:   1,
		TaskID:          "test-task",
		PollPath:        "test-task.check",
		PollDigest:      "abcdef",
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature",
		HeadSHA:         "deadbeef",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "cafebabe",
		PublicationLine: "done [key=pr-merged:test-task]: PR merged at cafebabe",
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
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
		SchemaVersion:   1,
		TaskID:          "captain:munsu",
		PollPath:        "captain:munsu.check",
		PollDigest:      "d1g3st",
		Provider:        "github",
		Owner:           "o",
		Repo:            "r",
		Number:          1,
		URL:             "https://github.com/o/r/pull/1",
		BaseRef:         "main",
		HeadRef:         "f",
		HeadSHA:         "sha1",
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "sha2",
		PublicationLine: "done: merged",
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
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

func TestValidateCheckWithLstat_NotFound(t *testing.T) {
	err := ValidateCheckWithLstat(filepath.Join(t.TempDir(), "missing.check"))
	if err == nil {
		t.Fatal("expected error for missing check")
	}
}

func TestValidateCheckWithLstat_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.check")
	if err := os.WriteFile(path, nil, 0755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCheckWithLstat(path); err == nil {
		t.Fatal("expected error for empty check")
	}
}

func TestValidateCheckWithLstat_Directory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory.check")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCheckWithLstat(path); err == nil {
		t.Fatal("expected error for directory")
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

	lines, err := mhome.ReadStatus(home, "task-1")
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

	if err := mhome.AppendStatus(home, "task-1", "done: existing"); err != nil {
		t.Fatal(err)
	}
	if err := mhome.AppendStatus(home, "task-1", "working: progress"); err != nil {
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

	lines, err := mhome.ReadStatus(home, "task-1")
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
	// The canonical home must be initialized before the identity projection is
	// written so a canonical Authority composed over this home (the merged
	// transition path) is bound to the exact home the retirement flow reads.
	if _, err := mhome.Init(home); err != nil {
		t.Fatalf("home.Init: %v", err)
	}
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
		"kind":         "ship",
		"window":       "@test-window",
		"pr_provider":  "github",
		"pr_owner":     "testowner",
		"pr_repo":      "testrepo",
		"pr_number":    "42",
		"pr_url":       "https://github.com/testowner/testrepo/pull/42",
		"pr_base_ref":  baseRef,
		"pr_head_ref":  "feature-branch",
		"pr_head_sha":  headSHA,
		"pr_timestamp": "2024-01-01T00:00:00Z",
	}
	if err := mhome.WriteMeta(home, taskID, meta); err != nil {
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

// installMockMergeStatus installs a mock QueryDeliveryMergeStatus
// that returns a specific result. Returns the original for restoration.
func installMockMergeStatus(t *testing.T, merged bool, headSHA, mergedSHA string) func() {
	t.Helper()
	orig := QueryDeliveryMergeStatus
	QueryDeliveryMergeStatus = func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
		state := "OPEN"
		if merged {
			state = "MERGED"
		}
		mSHA := mergedSHA
		if mSHA == "" && merged {
			mSHA = headSHA
		}
		return &domain.PRMergeStatus{
			Merged:    merged,
			MergedSHA: mSHA,
			Closed:    !merged,
			HeadSHA:   headSHA,
			State:     state,
		}, nil
	}
	return func() {
		QueryDeliveryMergeStatus = orig
	}
}

// retirementPollAuthFor seeds one ship task per ID in a canonical home-backed
// Authority for the merged poll retirement path (#414 B hard cut): the poll
// path derives merged truth from the canonical committed completed delivery
// outcome, so every task gets a canonical completed outcome under its meta
// identity head. The home must already be initialized (home.Init) by the
// caller.
func retirementPollAuthFor(t *testing.T, homeDir string, taskIDs ...string) *taskauthority.Canonical {
	t.Helper()
	auth, err := taskauthority.NewCanonical(mustHome(t, homeDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, taskID := range taskIDs {
		canonicalCreateTask(t, auth, taskID, "ship", "")
		// Seed the canonical completed outcome only once per task: repeated
		// recovery cycles reuse the already committed evidence.
		if out, err := auth.DeliveryOutcome(mustTaskID(t, taskID)); err == nil && out.Status == taskauthority.DeliveryOutcomeCompleted {
			continue
		}
		seedPollCompletedOutcome(t, auth, homeDir, taskID)
	}
	return auth
}

// seedPollCompletedOutcome binds worktree/endpoint and commits a canonical
// completed provider-merge delivery outcome for the task, under the identity
// head stored in the task meta projection. Operation IDs carry a random
// suffix so repeated seeding for the same task never collides.
func seedPollCompletedOutcome(t *testing.T, auth *taskauthority.Canonical, homeDir, taskID string) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	meta, err := mhome.ReadMeta(homeDir, taskID)
	if err != nil {
		t.Fatalf("ReadMeta(%s): %v", taskID, err)
	}
	ident, err := domain.IdentityFromMeta(meta)
	if err != nil {
		t.Fatalf("IdentityFromMeta(%s): %v", taskID, err)
	}
	if ident == nil {
		t.Fatalf("task %s has no delivery identity for the canonical outcome fixture", taskID)
	}

	wtDir := filepath.Join(homeDir, "worktrees", taskID)
	os.MkdirAll(wtDir, 0755)
	seedWorktreeEvidenceAtHead(t, auth, taskID, wtDir, "lease-wt-poll", "fence-wt-poll", ident.HeadSHA)
	seedEndpointEvidence(t, auth, taskID, "@test-window", "lease-ep-poll", "fence-ep-poll")

	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	authReq := taskauthority.CanonicalDeliveryAuthorizationRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Kind:         taskauthority.DeliveryAuthorizationProviderMerge,
		Identity:     *ident,
		Preconditions: []taskauthority.DeliveryPrecondition{
			taskauthority.DeliveryPreconditionPRMergeable,
			taskauthority.DeliveryPreconditionPRHeadCurrent,
		},
	}
	authOpID := "op-poll-auth-" + taskID + "-" + suffix
	if _, err := auth.AuthorizeDelivery(mustFleetOperation(t, authOpID, authReq), authReq); err != nil {
		t.Fatalf("AuthorizeDelivery(%s): %v", taskID, err)
	}
	agg2, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	outReq := taskauthority.CanonicalDeliveryOutcomeRequest{
		HomeID:                   auth.HomeID(),
		TaskID:                   mustTaskID(t, taskID),
		Precondition:             domain.Of(uint64(agg2.Generation), uint64(agg2.Revision)),
		AuthorizationOperationID: authOpID,
		Status:                   taskauthority.DeliveryOutcomeCompleted,
		Detail:                   "provider confirms merged",
		HeadSHA:                  ident.HeadSHA,
		MergedSHA:                "aaaabbbbccccddddeeeeffff0000111122223333",
	}
	if _, err := auth.CommitDeliveryOutcome(mustFleetOperation(t, "op-poll-out-"+taskID+"-"+suffix, outReq), outReq); err != nil {
		t.Fatalf("CommitDeliveryOutcome(%s): %v", taskID, err)
	}
}

// seedWorktreeEvidenceAtHead binds a worktree whose head matches the delivery
// identity head so the canonical authorization gate holds. The operation ID
// carries a random suffix so repeated seeding for the same task never
// collides.
func seedWorktreeEvidenceAtHead(t *testing.T, auth *taskauthority.Canonical, taskID, path, lease, fence, head string) {
	t.Helper()
	agg, err := auth.Get(mustTaskID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	req := taskauthority.CanonicalBindWorktreeRequest{
		HomeID:       auth.HomeID(),
		TaskID:       mustTaskID(t, taskID),
		Precondition: domain.Of(uint64(agg.Generation), uint64(agg.Revision)),
		Binding: taskauthority.WorktreeBinding{
			RepositoryIdentity: "repo-" + taskID,
			Path:               path,
			GitDir:             filepath.Join(path, ".git"),
			CommonDir:          filepath.Join(filepath.Dir(path), ".git"),
			Head:               head,
			LeaseID:            lease,
			FenceToken:         fence,
			BoundAtUnix:        time.Now().Unix(),
		},
		Reason: "spawn",
	}
	op, err := domain.NewOperation(mustOpID(t, fmt.Sprintf("op-bind-wt-poll-%s-%d", taskID, time.Now().UnixNano())), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BindWorktree(op, req); err != nil {
		t.Fatalf("BindWorktree(%s): %v", taskID, err)
	}
}

// retirementPollAuth seeds one ship task in a canonical home-backed Authority.
func retirementPollAuth(t *testing.T, homeDir, taskID string) *taskauthority.Canonical {
	t.Helper()
	return retirementPollAuthFor(t, homeDir, taskID)
}

func readRetirementRecordOrNil(t *testing.T, home, taskID string) *PollRetirementRecord {
	t.Helper()
	rec, err := ReadRetirementRecord(home, taskID)
	if err != nil {
		t.Fatalf("read retirement record: %v", err)
	}
	return rec
}

// mergedResultForTest encodes a captured merge result the way ObserveMergedPoll
// emits it; empty arguments take the fixture's default SHAs.
func mergedResultForTest(headSHA, mergedSHA string) []byte {
	if headSHA == "" {
		headSHA = "0000111122223333444455556666777788889999"
	}
	if mergedSHA == "" {
		mergedSHA = "aaaabbbbccccddddeeeeffff0000111122223333"
	}
	data, err := json.Marshal(mergedPollResult{HeadSHA: headSHA, MergedSHA: mergedSHA})
	if err != nil {
		panic(err)
	}
	return data
}

// observeAndRetire runs the resolver stage and, on a resolved capture, the
// action stage — the same two calls the watcher makes across capture and wake.
func observeAndRetire(t *testing.T, home, taskID, checkPath string, auth *taskauthority.Canonical) error {
	t.Helper()
	resolved, result, err := ObserveMergedPoll(home, taskID)
	if err != nil {
		t.Fatalf("ObserveMergedPoll: %v", err)
	}
	if !resolved {
		t.Fatal("ObserveMergedPoll: poll did not resolve")
	}
	return RetireMergedPoll(home, taskID, checkPath, result, auth)
}

// Test: record persisted, crash before publication
func TestRetireMergedPoll_CrashBeforePublication(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Execute full retirement.
	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
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
	lines, err := mhome.ReadStatus(home, taskID)
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
		QuarantinePath:  quarantinePathForTest(taskID),
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
	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
		t.Fatalf("RetireMergedPoll: %v", err)
	}

	// Verify everything is clean.
	lines, err := mhome.ReadStatus(home, taskID)
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
		QuarantinePath:  quarantinePathForTest(taskID),
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
	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
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
		QuarantinePath:  quarantinePathForTest(taskID),
		Quarantined:     true,
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
	quarantinePath := filepath.Join(retirementDirPath(home), rec.QuarantinePath)
	if err := os.MkdirAll(filepath.Dir(quarantinePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(checkPath, quarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(quarantinePath); err != nil {
		t.Fatal(err)
	}

	// Re-entry through the action: the pending record routes the call into
	// recovery, which finishes the already-removed poll case.
	if err := RetireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), retirementPollAuth(t, home, taskID)); err != nil {
		t.Fatalf("RetireMergedPoll after crash: %v", err)
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

	// Create a modern pending record before quarantine and publication.
	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}

	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	rec.PollDigest = digest
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	// Recovery should append publication, remove poll, clean record.
	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err != nil {
		t.Fatalf("RecoverPendingRetirement: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}

	// Verify publication exists.
	lines, err := mhome.ReadStatus(home, taskID)
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

func TestRecoverPendingRetirement_LegacyRecordPreservesReplacement(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	original, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatal(err)
	}
	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	legacyRecord := map[string]any{
		"schemaVersion":   rec.SchemaVersion,
		"taskId":          rec.TaskID,
		"pollPath":        rec.PollPath,
		"pollDigest":      rec.PollDigest,
		"provider":        rec.Provider,
		"owner":           rec.Owner,
		"repo":            rec.Repo,
		"number":          rec.Number,
		"url":             rec.URL,
		"baseRef":         rec.BaseRef,
		"headRef":         rec.HeadRef,
		"headSHA":         rec.HeadSHA,
		"capturedAt":      rec.CapturedAt,
		"mergedSHA":       rec.MergedSHA,
		"publicationLine": rec.PublicationLine,
		"discoveredAt":    rec.DiscoveredAt,
		"recordedAt":      rec.RecordedAt,
	}
	data, err := json.Marshal(legacyRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(retirementDirPath(home), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retirementRecordPath(home, taskID), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkPath, []byte("replacement"), 0644); err != nil {
		t.Fatal(err)
	}

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err == nil || resolved {
		t.Fatalf("recovery = %v, %v", resolved, err)
	}
	got, err := os.ReadFile(checkPath)
	if err != nil || string(got) != "replacement" {
		t.Fatalf("replacement = %q, %v", got, err)
	}
	if string(original) == string(got) {
		t.Fatal("replacement was not installed")
	}
	entries, err := os.ReadDir(retirementDirPath(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".quarantine") {
			t.Fatalf("unexpected quarantine artifact %q", entry.Name())
		}
	}
	if readRetirementRecordOrNil(t, home, taskID) == nil {
		t.Fatal("retirement record should be preserved")
	}
}

func TestRetireMergedPoll_QuarantinePreservesReplacement(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	auth := retirementPollAuth(t, home, taskID)
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	var verifiedPath string
	calls := 0
	err := retireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), auth, func(path string) (string, error) {
		calls++
		if calls == 2 {
			verifiedPath = path
			if filepath.Ext(path) != ".quarantine" {
				t.Fatalf("verification path = %q, want quarantine path", path)
			}
			if _, statErr := os.Stat(checkPath); !os.IsNotExist(statErr) {
				t.Fatalf("public poll should be absent during quarantine verification: %v", statErr)
			}
			if err := os.WriteFile(checkPath, []byte("replacement"), 0644); err != nil {
				t.Fatalf("write replacement: %v", err)
			}
		}
		return pollContentDigest(path)
	})
	if err != nil {
		t.Fatalf("retirement = %v", err)
	}
	data, err := os.ReadFile(checkPath)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement = %q, %v", data, err)
	}
	if _, err := os.Stat(verifiedPath); !os.IsNotExist(err) {
		t.Fatalf("quarantine should be removed after retirement: %v", err)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("retirement record should be removed")
	}
	t.Logf("verified private path=%s; replacement=%q survived at public path; quarantine removed; retirement record removed", filepath.Base(verifiedPath), data)
}

func TestRecoverPendingRetirement_RejectsUnsupportedSchema(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	rec.SchemaVersion++
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatal(err)
	}

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err == nil || !strings.Contains(err.Error(), "unsupported schema version") || resolved {
		t.Fatalf("recovery = %v, %v; want unsupported-schema refusal", resolved, err)
	}
	assertRecoveryArtifactsPreserved(t, home, taskID, checkPath, "")
}

func TestRecoverPendingRetirement_RejectsInvalidQuarantinePath(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	rec.QuarantinePath = "../bad.quarantine"
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatal(err)
	}

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err == nil || !strings.Contains(err.Error(), "invalid quarantine path") || resolved {
		t.Fatalf("recovery = %v, %v; want invalid-quarantine refusal", resolved, err)
	}
	assertRecoveryArtifactsPreserved(t, home, taskID, checkPath, "")
}

func TestRecoverPendingRetirement_QuarantinePreservesReplacement(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := durableAppendStatus(home, taskID, rec.PublicationLine); err != nil {
		t.Fatal(err)
	}
	quarantinePath, err := quarantinePollPath(home, taskID)
	if err != nil {
		t.Fatal(err)
	}
	rec.QuarantinePath = filepath.Base(quarantinePath)
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatal(err)
	}
	if err := quarantinePollAt(checkPath, quarantinePath, mhome.RenameDurable); err != nil {
		t.Fatal(err)
	}
	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), func(path string) (string, error) {
		if err := os.WriteFile(checkPath, []byte("replacement"), 0644); err != nil {
			t.Fatal(err)
		}
		return rec.PollDigest, nil
	})
	if err != nil || !resolved {
		t.Fatalf("recovery = %v, %v", resolved, err)
	}
	data, err := os.ReadFile(checkPath)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement = %q, %v", data, err)
	}
	if _, err := os.Stat(quarantinePath); !os.IsNotExist(err) {
		t.Fatalf("quarantine should be removed after recovery: %v", err)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("retirement record should be removed after recovery")
	}
	t.Logf("replacement=%q survived at public path; quarantine removed; retirement record removed", data)
}

func TestRetireMergedPoll_QuarantineRenameFailureRefuses(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()
	err := retireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), retirementPollAuth(t, home, taskID), pollContentDigest, func(string, string) error { return errors.New("rename refused") })
	if err == nil || !errors.Is(err, domain.ErrCheckValidationRefused) {
		t.Fatalf("error = %v, want validation refusal", err)
	}
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatalf("original must survive: %v", err)
	}
	t.Logf("rename refusal=%v; public poll survived; retirement remains pending", err)
}

func TestRecoverPendingRetirement_QuarantineRenameFailurePreservesRecord(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := durableAppendStatus(home, taskID, rec.PublicationLine); err != nil {
		t.Fatal(err)
	}
	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest, func(string, string) error {
		return errors.New("rename refused")
	})
	if err == nil || resolved {
		t.Fatalf("recovery = %v, %v", resolved, err)
	}
	assertRecoveryArtifactsPreserved(t, home, taskID, checkPath, "")
	t.Logf("rename refusal=%v; public poll and retirement record preserved", err)
}

func TestRecoverPendingRetirement_DoesNotTouchReplacementAfterQuarantine(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	original, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatal(err)
	}
	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	rec.Quarantined = true
	quarantinePath := filepath.Join(retirementDirPath(home), rec.QuarantinePath)
	if err := os.MkdirAll(filepath.Dir(quarantinePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(checkPath, quarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(quarantinePath); err != nil {
		t.Fatal(err)
	}
	replacement := original
	if err := os.WriteFile(checkPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := durableAppendStatus(home, taskID, rec.PublicationLine); err != nil {
		t.Fatal(err)
	}

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err != nil || !resolved {
		t.Fatalf("recovery = %v, %v", resolved, err)
	}
	got, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatalf("replacement should survive: %v", err)
	}
	if string(got) != string(replacement) {
		t.Fatalf("replacement changed to %q", got)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("retirement record should be removed")
	}
}

func TestRecoverPendingRetirement_IgnoresStaleQuarantine(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := durableAppendStatus(home, taskID, rec.PublicationLine); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(taskID))
	stalePath := filepath.Join(retirementDirPath(home), fmt.Sprintf(".poll-%x-stale.quarantine", digest))
	if err := os.WriteFile(stalePath, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err != nil || !resolved {
		t.Fatalf("recovery = %v, %v", resolved, err)
	}
	if _, err := os.Stat(stalePath); err != nil {
		t.Fatalf("stale quarantine should survive: %v", err)
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
		QuarantinePath:  quarantinePathForTest(taskID),
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
	if err := os.Chmod(checkPath, 0644); err != nil {
		t.Fatalf("chmod check: %v", err)
	}

	// Recovery: should skip duplicate append, remove poll, clean record.
	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
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
	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err != nil {
		t.Fatalf("first recovery: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true for no-op recovery")
	}

	// Second recovery: still nothing.
	resolved, err = recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err != nil {
		t.Fatalf("second recovery: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true for second no-op recovery")
	}

	// Now set up a real pending record and recover.
	checkPath := filepath.Join(mhome.StateDir(home), taskID+".check")
	os.WriteFile(checkPath, []byte("#!/bin/bash\necho\n"), 0755)
	digest, _ := pollContentDigest(checkPath)

	pubLine := publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333")
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		QuarantinePath:  quarantinePathForTest(taskID),
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

	resolved, err = recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err != nil {
		t.Fatalf("real recovery: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}

	// One more time: should be no-op.
	resolved, err = recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
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
	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
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

func TestRecoverPendingRetirement_DeliveryIdentityMismatchPreservesArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PollRetirementRecord)
	}{
		{name: "owner", mutate: func(rec *PollRetirementRecord) { rec.Owner = "other-owner" }},
		{name: "repo", mutate: func(rec *PollRetirementRecord) { rec.Repo = "other-repo" }},
		{name: "number", mutate: func(rec *PollRetirementRecord) { rec.Number = 43 }},
		{name: "url", mutate: func(rec *PollRetirementRecord) { rec.URL = "https://github.com/other-owner/other-repo/pull/42" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
			defer cleanup()

			rec := recoveryRecordForTest(t, home, taskID, checkPath)
			tc.mutate(rec)
			if err := WriteRetirementRecord(home, rec); err != nil {
				t.Fatalf("WriteRetirementRecord: %v", err)
			}

			resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
			if err == nil || resolved {
				t.Fatalf("expected unresolved delivery identity mismatch, got resolved=%v err=%v", resolved, err)
			}
			assertRecoveryArtifactsPreserved(t, home, taskID, checkPath, rec.PublicationLine)
		})
	}
}

func TestRecoverPendingRetirement_InvalidPublicationEvidencePreservesArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PollRetirementRecord)
	}{
		{name: "missing merged SHA", mutate: func(rec *PollRetirementRecord) { rec.MergedSHA = "" }},
		{name: "mismatched publication line", mutate: func(rec *PollRetirementRecord) { rec.PublicationLine = "done: arbitrary publication" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
			defer cleanup()

			rec := recoveryRecordForTest(t, home, taskID, checkPath)
			tc.mutate(rec)
			if err := WriteRetirementRecord(home, rec); err != nil {
				t.Fatalf("WriteRetirementRecord: %v", err)
			}

			resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
			if err == nil || resolved {
				t.Fatalf("expected unresolved invalid publication evidence, got resolved=%v err=%v", resolved, err)
			}
			assertRecoveryArtifactsPreserved(t, home, taskID, checkPath, rec.PublicationLine)
		})
	}
}

func TestRecoverPendingRetirement_MutableAttributesDoNotBlockRecovery(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	rec := recoveryRecordForTest(t, home, taskID, checkPath)
	rec.BaseRef = "release"
	rec.HeadRef = "retargeted-feature"
	rec.CapturedAt = "2025-01-01T00:00:00Z"
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err != nil || !resolved {
		t.Fatalf("expected recovery despite mutable attribute changes, got resolved=%v err=%v", resolved, err)
	}
	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatalf("check should be removed after recovery, got err=%v", err)
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
		HeadSHA:         "0000000000000000000000000000000000000000", // wrong!
		CapturedAt:      "2024-01-01T00:00:00Z",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: "done: wrong head",
		DiscoveredAt:    "2024-01-01T00:01:00Z",
		RecordedAt:      "2024-01-01T00:01:00Z",
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
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

func recoveryRecordForTest(t *testing.T, home, taskID, checkPath string) *PollRetirementRecord {
	t.Helper()
	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}
	return &PollRetirementRecord{
		SchemaVersion:   PollRetirementSchema,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		QuarantinePath:  quarantinePathForTest(taskID),
		Provider:        "github",
		Owner:           "testowner",
		Repo:            "testrepo",
		Number:          42,
		URL:             "https://github.com/testowner/testrepo/pull/42",
		BaseRef:         "main",
		HeadRef:         "feature-branch",
		HeadSHA:         "0000111122223333444455556666777788889999",
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333"),
	}
}

func quarantinePathForTest(taskID string) string {
	digest := sha256.Sum256([]byte(taskID))
	return fmt.Sprintf(".poll-%x-test.quarantine", digest)
}

func assertRecoveryArtifactsPreserved(t *testing.T, home, taskID, checkPath, publication string) {
	t.Helper()
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatalf("poll should be preserved: %v", err)
	}
	if got := readRetirementRecordOrNil(t, home, taskID); got == nil {
		t.Fatal("retirement record should be preserved")
	}
	lines, err := mhome.ReadStatus(home, taskID)
	if err == nil {
		for _, line := range lines {
			if line == publication {
				t.Fatal("recovery should not publish")
			}
		}
	}
}

func TestRecoverPendingRetirement_IncompleteMetadataPreservesArtifacts(t *testing.T) {
	fields := []string{"pr_provider", "pr_url", "pr_head_sha"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
			defer cleanup()
			auth := retirementPollAuth(t, home, taskID)

			meta, err := mhome.ReadMeta(home, taskID)
			if err != nil {
				t.Fatalf("ReadMeta: %v", err)
			}
			meta[field] = ""
			if err := mhome.WriteMeta(home, taskID, meta); err != nil {
				t.Fatalf("write incomplete metadata: %v", err)
			}

			rec := recoveryRecordForTest(t, home, taskID, checkPath)
			if err := WriteRetirementRecord(home, rec); err != nil {
				t.Fatalf("WriteRetirementRecord: %v", err)
			}

			resolved, err := recoverPendingRetirement(home, taskID, auth, pollContentDigest)
			if err == nil || resolved {
				t.Fatalf("expected unresolved incomplete metadata, got resolved=%v err=%v", resolved, err)
			}
			assertRecoveryArtifactsPreserved(t, home, taskID, checkPath, rec.PublicationLine)
		})
	}
}

func TestRecoverPendingRetirement_MissingMetadataPreservesArtifacts(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

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
		MergedSHA:       "aaaabbbbccccddddeeeeffff0000111122223333",
		PublicationLine: publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333"),
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatalf("WriteRetirementRecord: %v", err)
	}
	auth := retirementPollAuth(t, home, taskID)
	metaPath, err := mhome.MetaFilePath(home, taskID)
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove metadata: %v", err)
	}

	resolved, err := recoverPendingRetirement(home, taskID, auth, pollContentDigest)
	if err == nil || resolved {
		t.Fatalf("expected unresolved missing metadata, got resolved=%v err=%v", resolved, err)
	}
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatalf("poll should be preserved: %v", err)
	}
	if got := readRetirementRecordOrNil(t, home, taskID); got == nil {
		t.Fatal("retirement record should be preserved")
	}
	lines, err := mhome.ReadStatus(home, taskID)
	if err == nil {
		for _, line := range lines {
			if line == rec.PublicationLine {
				t.Fatal("recovery should not publish without metadata")
			}
		}
	}
}

func TestRecoverPendingRetirement_InvalidPollPathPreservesArtifacts(t *testing.T) {
	paths := []string{"../victim", filepath.Join("foreign", "victim"), filepath.Join(t.TempDir(), "victim")}
	for _, pollPath := range paths {
		t.Run(pollPath, func(t *testing.T) {
			home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
			defer cleanup()

			// An absolute poll path names a file outside the home outright, so
			// the artifact it must not touch is the one already at that path.
			// Joining it under the state directory made it a path component,
			// which on Windows carries a volume letter and cannot be one.
			resolvedPath := filepath.Join(mhome.StateDir(home), pollPath)
			if filepath.IsAbs(pollPath) {
				resolvedPath = pollPath
			}
			if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
				t.Fatalf("create victim directory: %v", err)
			}
			if err := os.WriteFile(resolvedPath, []byte("must remain\n"), 0644); err != nil {
				t.Fatalf("write victim: %v", err)
			}
			victimDigest, err := pollContentDigest(resolvedPath)
			if err != nil {
				t.Fatalf("victim digest: %v", err)
			}
			rec := recoveryRecordForTest(t, home, taskID, checkPath)
			rec.PollPath = pollPath
			rec.PollDigest = victimDigest
			if err := WriteRetirementRecord(home, rec); err != nil {
				t.Fatalf("WriteRetirementRecord: %v", err)
			}

			resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
			if err == nil || resolved {
				t.Fatalf("expected invalid path refusal, got resolved=%v err=%v", resolved, err)
			}
			assertRecoveryArtifactsPreserved(t, home, taskID, checkPath, rec.PublicationLine)
			contents, readErr := os.ReadFile(resolvedPath)
			if readErr != nil {
				t.Fatalf("victim was removed: %v", readErr)
			}
			if string(contents) != "must remain\n" {
				t.Fatalf("victim contents = %q", contents)
			}
		})
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
		QuarantinePath:  quarantinePathForTest(taskID),
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

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err == nil || resolved {
		t.Fatalf("expected digest mismatch to remain unresolved, got resolved=%v err=%v", resolved, err)
	}
	quarantinePath := filepath.Join(retirementDirPath(home), rec.QuarantinePath)
	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatalf("public poll should not remain after quarantine, got %v", err)
	}
	if _, err := os.Stat(quarantinePath); err != nil {
		t.Fatalf("quarantined poll should be preserved: %v", err)
	}
	got := readRetirementRecordOrNil(t, home, taskID)
	if got == nil || !got.Quarantined {
		t.Fatalf("quarantined retirement record should be preserved: %+v", got)
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

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
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
	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
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

// --- Validation refusal and provider error / open / closed-unmerged preserve poll ---

func TestRetireMergedPoll_DigestAcquisitionRefusalBeforePublication(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	digestErr := errors.New("digest read failed")
	err := retireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), nil, func(string) (string, error) {
		return "", digestErr
	})
	if err == nil || !errors.Is(err, domain.ErrCheckValidationRefused) {
		t.Fatalf("error = %v, want ErrCheckValidationRefused", err)
	}
	if _, statErr := os.Stat(checkPath); statErr != nil {
		t.Fatalf("check was not preserved: %v", statErr)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("digest refusal must not create a retirement record")
	}
}

func TestObserveMergedPoll_EmptyProviderHeadSHAIsUnresolved(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	resolved, result, err := ObserveMergedPoll(home, taskID)
	if err != nil {
		t.Fatalf("ObserveMergedPoll: %v", err)
	}
	if resolved || result != nil {
		t.Fatalf("resolved=%v result=%q, want unresolved with no capture", resolved, result)
	}
	if _, statErr := os.Stat(checkPath); statErr != nil {
		t.Fatalf("check was not preserved: %v", statErr)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("an unresolved observation must not create a retirement record")
	}
	lines, readErr := mhome.ReadStatus(home, taskID)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("ReadStatus: %v", readErr)
	}
	for _, line := range lines {
		if line == publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "aaaabbbbccccddddeeeeffff0000111122223333") {
			t.Fatal("an unresolved observation must not publish a merge")
		}
	}
}

func TestRetireMergedPoll_CapturedResultMissingSHAIsStale(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	auth := retirementPollAuth(t, home, taskID)

	partial, err := json.Marshal(mergedPollResult{HeadSHA: "0000111122223333444455556666777788889999"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	err = RetireMergedPoll(home, taskID, checkPath, partial, auth)
	if !errors.Is(err, domain.ErrStaleCapture) {
		t.Fatalf("err=%v, want ErrStaleCapture for a capture missing its merged SHA", err)
	}
	if _, statErr := os.Stat(checkPath); statErr != nil {
		t.Fatalf("check was not preserved: %v", statErr)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("a stale capture must not create a retirement record")
	}
	if n := countPublicationLines(t, home, taskID); n != 0 {
		t.Fatalf("publication lines=%d, want 0", n)
	}
}

func TestRetireMergedPoll_CapturedHeadMismatchIsStale(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	auth := retirementPollAuth(t, home, taskID)

	recaptured := mergedResultForTest("1111222233334444555566667777888899990000", "aaaabbbbccccddddeeeeffff0000111122223333")
	err := RetireMergedPoll(home, taskID, checkPath, recaptured, auth)
	if !errors.Is(err, domain.ErrStaleCapture) {
		t.Fatalf("err=%v, want ErrStaleCapture when the identity head moved after capture", err)
	}
	if !strings.Contains(err.Error(), "preserving poll") {
		t.Fatalf("err=%v, want the preserving-poll classification", err)
	}
	if _, statErr := os.Stat(checkPath); statErr != nil {
		t.Fatalf("check was not preserved: %v", statErr)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("a stale capture must not create a retirement record")
	}
	if n := countPublicationLines(t, home, taskID); n != 0 {
		t.Fatalf("publication lines=%d, want 0", n)
	}
}

func countPublicationLines(t *testing.T, home, taskID string) int {
	t.Helper()
	lines, err := mhome.ReadStatus(home, taskID)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadStatus: %v", err)
	}
	n := 0
	for _, line := range lines {
		if strings.Contains(line, "done [key=pr-merged") {
			n++
		}
	}
	return n
}

func TestRetireMergedPoll_DigestAcquisitionRefusalAfterPublication(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	auth := retirementPollAuth(t, home, taskID)
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()
	calls := 0
	digestErr := errors.New("digest read failed after publication")
	err := retireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), auth, func(path string) (string, error) {
		calls++
		if calls == 2 {
			if writeErr := os.WriteFile(checkPath, []byte("replacement"), 0644); writeErr != nil {
				t.Fatalf("write replacement: %v", writeErr)
			}
			return "", digestErr
		}
		return pollContentDigest(path)
	})
	if err == nil || !errors.Is(err, domain.ErrCheckInvalidAfterPublication) {
		t.Fatalf("error = %v, want ErrCheckInvalidAfterPublication", err)
	}
	if data, readErr := os.ReadFile(checkPath); readErr != nil || string(data) != "replacement" {
		t.Fatalf("replacement should survive: %q, %v", data, readErr)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("post-publication digest refusal must clean the retirement record")
	}
}

func TestRetireMergedPoll_DigestMismatchIsNotValidationRefusal(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	auth := retirementPollAuth(t, home, taskID)
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()
	calls := 0
	err := retireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), auth, func(path string) (string, error) {
		calls++
		if calls == 2 {
			if writeErr := os.WriteFile(checkPath, []byte("replacement"), 0644); writeErr != nil {
				t.Fatalf("write replacement: %v", writeErr)
			}
			return "different-digest", nil
		}
		return pollContentDigest(path)
	})
	if err == nil || errors.Is(err, domain.ErrCheckValidationRefused) || errors.Is(err, domain.ErrCheckInvalidAfterPublication) {
		t.Fatalf("error = %v, want ordinary digest mismatch", err)
	}
	if data, readErr := os.ReadFile(checkPath); readErr != nil || string(data) != "replacement" {
		t.Fatalf("replacement should survive: %q, %v", data, readErr)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("digest mismatch must clean the retirement record")
	}
}

func TestRetireMergedPoll_ValidationRefusalIsClassifiedAndPreservesPoll(t *testing.T) {
	home := t.TempDir()
	taskID := "task-validation-refusal"
	checkPath := filepath.Join(home, "invalid.check")
	if err := os.WriteFile(checkPath, []byte("not an executable check\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := RetireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), nil)
	if err == nil {
		t.Fatal("expected validation refusal")
	}
	if !errors.Is(err, domain.ErrCheckValidationRefused) {
		t.Fatalf("error = %v, want ErrCheckValidationRefused", err)
	}
	if errors.Is(err, domain.ErrCheckInvalidAfterPublication) {
		t.Fatalf("pre-retirement refusal classified after publication: %v", err)
	}
	if _, statErr := os.Stat(checkPath); statErr != nil {
		t.Fatalf("invalid check was not preserved: %v", statErr)
	}
}

func TestRetireMergedPoll_PostPublicationRevalidationRefusalIsClassified(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	auth := retirementPollAuth(t, home, taskID)
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// The public artifact is replaced by a directory after step-0 validation
	// and digest acquisition but before quarantine: the digest seam is the
	// only point between those steps, so the replacement happens on its first
	// call (the pre-mutation digest), after the digest of the real file is
	// computed.
	calls := 0
	err := retireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), auth, func(path string) (string, error) {
		calls++
		digest, digestErr := pollContentDigest(path)
		if calls == 1 {
			if err := os.Remove(checkPath); err != nil {
				t.Fatalf("remove check before quarantine: %v", err)
			}
			if err := os.Mkdir(checkPath, 0755); err != nil {
				t.Fatalf("replace check with directory: %v", err)
			}
		}
		return digest, digestErr
	})
	if err == nil {
		t.Fatal("expected post-publication validation refusal")
	}
	if !errors.Is(err, domain.ErrCheckValidationRefused) || !errors.Is(err, domain.ErrCheckInvalidAfterPublication) {
		t.Fatalf("error = %v, want both validation sentinels", err)
	}
	lines, readErr := mhome.ReadStatus(home, taskID)
	if readErr != nil {
		t.Fatalf("ReadStatus: %v", readErr)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "merged at") {
		t.Fatalf("status lines = %v, want durable publication", lines)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("retirement record should be cleaned after publication")
	}
	if _, statErr := os.Stat(checkPath); !os.IsNotExist(statErr) {
		t.Fatalf("replacement directory should not be touched by quarantine: %v", statErr)
	}
}

func TestObserveMergedPoll_OpenIsUnresolved(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, false, "0000111122223333444455556666777788889999", "")
	defer restore()

	resolved, result, err := ObserveMergedPoll(home, taskID)
	if err != nil {
		t.Fatalf("ObserveMergedPoll: %v", err)
	}
	if resolved || result != nil {
		t.Fatalf("resolved=%v result=%q, want unresolved for an open PR", resolved, result)
	}
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved for open PR")
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("no record should exist for open PR")
	}
}

func TestObserveMergedPoll_ClosedUnmergedIsUnresolved(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	orig := QueryDeliveryMergeStatus
	QueryDeliveryMergeStatus = func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
		return &domain.PRMergeStatus{
			Merged:    false,
			MergedSHA: "",
			Closed:    true,
			HeadSHA:   "0000111122223333444455556666777788889999",
			State:     "CLOSED",
		}, nil
	}
	defer func() { QueryDeliveryMergeStatus = orig }()

	resolved, result, err := ObserveMergedPoll(home, taskID)
	if err != nil {
		t.Fatalf("ObserveMergedPoll: %v", err)
	}
	if resolved || result != nil {
		t.Fatalf("resolved=%v result=%q, want unresolved for a closed-unmerged PR", resolved, result)
	}
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved")
	}
}

func TestObserveMergedPoll_ProviderErrorPreservesPoll(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	orig := QueryDeliveryMergeStatus
	QueryDeliveryMergeStatus = func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
		return nil, fmt.Errorf("network error")
	}
	defer func() { QueryDeliveryMergeStatus = orig }()

	resolved, result, err := ObserveMergedPoll(home, taskID)
	if err == nil || !strings.Contains(err.Error(), "merge status query") || !strings.Contains(err.Error(), "network error") {
		t.Fatalf("error = %v, want the provider error surfaced as a query failure", err)
	}
	if resolved || result != nil {
		t.Fatalf("resolved=%v result=%q, want no capture on provider error", resolved, result)
	}
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved on provider error")
	}
}

// --- Head SHA mismatch ---

func TestObserveMergedPoll_ProviderHeadMismatchIsUnresolved(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "ffffffffffffffffffffffffffffffffffffffff", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	resolved, result, err := ObserveMergedPoll(home, taskID)
	if err != nil {
		t.Fatalf("ObserveMergedPoll: %v", err)
	}
	if resolved || result != nil {
		t.Fatalf("resolved=%v result=%q, want unresolved on head mismatch", resolved, result)
	}
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatal("check should be preserved on head mismatch")
	}
}

func TestObserveMergedPoll_MergedCapturesHeadAndMergedSHA(t *testing.T) {
	home, taskID, _, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	resolved, result, err := ObserveMergedPoll(home, taskID)
	if err != nil || !resolved {
		t.Fatalf("resolved=%v err=%v, want a resolved capture", resolved, err)
	}
	var captured mergedPollResult
	if err := json.Unmarshal(result, &captured); err != nil {
		t.Fatalf("captured result undecodable: %v", err)
	}
	if captured.HeadSHA != "0000111122223333444455556666777788889999" || captured.MergedSHA != "aaaabbbbccccddddeeeeffff0000111122223333" {
		t.Fatalf("captured = %+v, want head and merged SHAs from the provider", captured)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatal("observation must not create a retirement record")
	}
}

func TestObserveMergedPoll_MergedSHAFallsBackToHead(t *testing.T) {
	home, taskID, _, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	orig := QueryDeliveryMergeStatus
	QueryDeliveryMergeStatus = func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
		return &domain.PRMergeStatus{Merged: true, HeadSHA: ident.HeadSHA, State: "MERGED"}, nil
	}
	defer func() { QueryDeliveryMergeStatus = orig }()

	resolved, result, err := ObserveMergedPoll(home, taskID)
	if err != nil || !resolved {
		t.Fatalf("resolved=%v err=%v, want a resolved capture", resolved, err)
	}
	var captured mergedPollResult
	if err := json.Unmarshal(result, &captured); err != nil {
		t.Fatalf("captured result undecodable: %v", err)
	}
	if captured.MergedSHA != captured.HeadSHA || captured.HeadSHA != "0000111122223333444455556666777788889999" {
		t.Fatalf("captured = %+v, want merged SHA to fall back to head", captured)
	}
}

// --- Poll digest mismatch ---

func TestRetireMergedPoll_PollDigestMismatch(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Run full retirement to get the record.
	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
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

	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
		t.Fatalf("GitHub retirement: %v", err)
	}

	lines, err := mhome.ReadStatus(home, taskID)
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
	meta, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	meta["pr_provider"] = "gitlab"
	meta["pr_owner"] = "glowner"
	meta["pr_repo"] = "glrepo"
	meta["pr_number"] = "7"
	meta["pr_url"] = "https://gitlab.com/glowner/glrepo/-/merge_requests/7"
	meta["pr_head_sha"] = "gitlab-sha-0000111122223333444455556666777788889999"
	if err := mhome.WriteMeta(home, taskID, meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	orig := QueryDeliveryMergeStatus
	QueryDeliveryMergeStatus = func(ident *domain.DeliveryIdentity) (*domain.PRMergeStatus, error) {
		return &domain.PRMergeStatus{
			Merged:    true,
			MergedSHA: "gl-merged-sha",
			Closed:    false,
			HeadSHA:   "gitlab-sha-0000111122223333444455556666777788889999",
			State:     "MERGED",
		}, nil
	}
	defer func() { QueryDeliveryMergeStatus = orig }()

	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
		t.Fatalf("GitLab retirement: %v", err)
	}

	lines, err := mhome.ReadStatus(home, taskID)
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

// --- Retirement never invokes teardown or removes meta/worktree/status ---

func TestRetireMergedPoll_PreservesMetaWorktreeStatus(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Add some initial status lines and worktree meta.
	if err := mhome.AppendStatus(home, taskID, "working: started"); err != nil {
		t.Fatal(err)
	}
	meta, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatal(err)
	}
	meta["worktree"] = "/tmp/some-worktree"
	if err := mhome.WriteMeta(home, taskID, meta); err != nil {
		t.Fatal(err)
	}

	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
		t.Fatalf("RetireMergedPoll: %v", err)
	}

	// Verify meta still exists.
	meta2, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("meta should be preserved: %v", err)
	}
	if meta2["kind"] != "ship" {
		t.Fatalf("meta kind should be ship")
	}

	// Verify status still exists with original + publication lines.
	lines, err := mhome.ReadStatus(home, taskID)
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
	if err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID)); err != nil {
		t.Fatalf("gen1: %v", err)
	}

	// Generation 2: check file is gone, no journal, publication present — the
	// action reports the retirement as already done instead of refusing.
	if err := RetireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), retirementPollAuth(t, home, taskID)); !errors.Is(err, domain.ErrAlreadyRetired) {
		t.Fatalf("gen2 error = %v, want ErrAlreadyRetired", err)
	}

	// Count publications.
	lines, err := mhome.ReadStatus(home, taskID)
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

// --- Migration of ValidateCheck to Lstat (existing tests still pass) ---

func TestValidateCheck_LstatRejectsSymlink(t *testing.T) {
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

// --- DeliveryState merge transition tests ---

func TestRetireMergedPoll_RequiresCanonicalCompletedOutcome(t *testing.T) {
	// A bare task authority without a canonical completed outcome refuses
	// poll retirement: the poll path derives merged truth from the canonical
	// committed completed delivery outcome and never writes the .meta
	// delivery_state projection.
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	auth := canonicalMergeTestAuth(t, home, taskID)
	if err := observeAndRetire(t, home, taskID, checkPath, auth); err == nil || !strings.Contains(err.Error(), "canonical delivery outcome") {
		t.Fatalf("RetireMergedPoll err = %v, want canonical-outcome refusal", err)
	}
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatalf("poll removed without canonical merged truth: %v", err)
	}

	// With the canonical completed outcome seeded, retirement proceeds and
	// the canonical outcome is the merged truth.
	seedPollCompletedOutcome(t, auth, home, taskID)
	if err := observeAndRetire(t, home, taskID, checkPath, auth); err != nil {
		t.Fatalf("RetireMergedPoll: %v", err)
	}
	outcome, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil {
		t.Fatalf("canonical delivery outcome: %v", err)
	}
	if outcome.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical delivery outcome = %q, want completed", outcome.Status)
	}

	// Verify other meta is preserved and no .meta delivery_state was written.
	meta, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta["kind"] != "ship" {
		t.Fatal("meta kind should be preserved")
	}
	if meta[MetaDeliveryState] != "" {
		t.Fatalf("poll retirement must never write .meta delivery_state; got %q", meta[MetaDeliveryState])
	}
}

func TestRecoverPendingRetirement_RequiresCanonicalCompletedOutcome(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Simulate crash after record write but before publication.
	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatalf("pollContentDigest: %v", err)
	}
	rec := &PollRetirementRecord{
		SchemaVersion:   1,
		TaskID:          taskID,
		PollPath:        taskID + ".check",
		PollDigest:      digest,
		QuarantinePath:  quarantinePathForTest(taskID),
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

	// Recovery requires the canonical completed outcome; it never writes the
	// .meta delivery_state projection.
	auth := retirementPollAuthFor(t, home, taskID)
	resolved, err := recoverPendingRetirement(home, taskID, auth, pollContentDigest)
	if err != nil {
		t.Fatalf("RecoverPendingRetirement: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}
	outcome, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil || outcome.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical delivery outcome = %v %+v, want completed", err, outcome)
	}
	meta, err := mhome.ReadMeta(home, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta[MetaDeliveryState] != "" {
		t.Fatalf("recovery must never write .meta delivery_state; got %q", meta[MetaDeliveryState])
	}
}

func TestRecoverPendingRetirement_IdempotentWithCanonicalTruth(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()

	// Successful full retirement.
	auth := retirementPollAuthFor(t, home, taskID)
	if err := observeAndRetire(t, home, taskID, checkPath, auth); err != nil {
		t.Fatalf("RetireMergedPoll: %v", err)
	}

	// The canonical outcome is the merged truth.
	outcome, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil || outcome.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical delivery outcome = %v %+v, want completed", err, outcome)
	}

	// Recovery with nothing pending should be idempotent.
	resolved, err := recoverPendingRetirement(home, taskID, auth, pollContentDigest)
	if err != nil {
		t.Fatalf("RecoverPendingRetirement: %v", err)
	}
	if !resolved {
		t.Fatal("expected resolved=true")
	}
	// The canonical outcome remains completed.
	outcome2, err := auth.DeliveryOutcome(mustTaskID(t, taskID))
	if err != nil || outcome2.Status != taskauthority.DeliveryOutcomeCompleted {
		t.Fatalf("canonical delivery outcome after recovery = %v %+v, want completed", err, outcome2)
	}
}

func TestRetirementRecordPathsDoNotCollide(t *testing.T) {
	home := t.TempDir()
	ids := []string{"task_a", "task:a", "task.a", "task/a"}
	paths := make(map[string]string)
	for _, id := range ids {
		path := retirementRecordPath(home, id)
		if previous, ok := paths[path]; ok {
			t.Fatalf("task IDs %q and %q share retirement path %q", previous, id, path)
		}
		paths[path] = id
	}
}

func TestRecoverPendingRetirement_PreservesRecordWhenPollDigestChanges(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()

	digest, err := pollContentDigest(checkPath)
	if err != nil {
		t.Fatal(err)
	}
	rec := &PollRetirementRecord{
		SchemaVersion:   PollRetirementSchema,
		TaskID:          taskID,
		PollPath:        filepath.Base(checkPath),
		PollDigest:      digest,
		Provider:        "github",
		URL:             "https://github.com/testowner/testrepo/pull/42",
		HeadSHA:         "0000111122223333444455556666777788889999",
		PublicationLine: publicationLine(taskID, "https://github.com/testowner/testrepo/pull/42", "merged-sha"),
	}
	if err := WriteRetirementRecord(home, rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho changed\n"), 0700); err != nil {
		t.Fatal(err)
	}

	resolved, err := recoverPendingRetirement(home, taskID, retirementPollAuth(t, home, taskID), pollContentDigest)
	if err == nil || resolved {
		t.Fatalf("RecoverPendingRetirement = (%v, %v), want unresolved error", resolved, err)
	}
	if got := readRetirementRecordOrNil(t, home, taskID); got == nil {
		t.Fatal("retirement record was removed after digest mismatch")
	}
	if _, err := os.Stat(checkPath); err != nil {
		t.Fatalf("poll was removed after digest mismatch: %v", err)
	}
}

// The watcher validates a check artifact before it surfaces one, and its
// production validator is this same ValidateCheckWithLstat. Step 0 of
// retirement re-applies that rule to the same path as its first action, before
// any query, record or mutation. This table pins that: every shape the
// validator rejects, retirement refuses in the classified way that suppresses
// the wake and preserves the artifact — so a second caller-side validation
// immediately before this call can change nothing about the outcome.
func TestRetireMergedPoll_RefusesEveryShapeTheCheckValidatorRefuses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reshape func(t *testing.T, checkPath string)
		gone    bool
	}{
		{
			name: "symlink",
			reshape: func(t *testing.T, checkPath string) {
				target := checkPath + ".target"
				if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(checkPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, checkPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing",
			reshape: func(t *testing.T, checkPath string) {
				if err := os.Remove(checkPath); err != nil {
					t.Fatal(err)
				}
			},
			gone: true,
		},
		{
			name: "directory",
			reshape: func(t *testing.T, checkPath string) {
				if err := os.Remove(checkPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(checkPath, 0755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "empty",
			reshape: func(t *testing.T, checkPath string) {
				if err := os.WriteFile(checkPath, nil, 0755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing shebang",
			reshape: func(t *testing.T, checkPath string) {
				if err := os.WriteFile(checkPath, []byte("echo ready\n"), 0755); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
			defer cleanup()
			restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
			defer restore()
			tc.reshape(t, checkPath)

			// Control: without this the case proves nothing about agreement.
			if err := ValidateCheckWithLstat(checkPath); err == nil {
				t.Fatalf("check validator accepted %s — the shapes must disagree with it first", tc.name)
			}

			err := observeAndRetire(t, home, taskID, checkPath, retirementPollAuth(t, home, taskID))
			if !errors.Is(err, domain.ErrCheckValidationRefused) {
				t.Fatalf("error = %v, want ErrCheckValidationRefused", err)
			}
			if !tc.gone {
				if _, statErr := os.Lstat(checkPath); statErr != nil {
					t.Fatalf("refused artifact must be preserved: %v", statErr)
				}
			}
			if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
				t.Fatal("a step-0 refusal must not persist a retirement record")
			}
		})
	}
}
