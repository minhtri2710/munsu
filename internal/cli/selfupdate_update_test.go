//go:build integration

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// TestVersionString verifies that VersionString produces the expected label.
func TestVersionString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"abc1234", "0.1.0-dev+abc1234"},
		{"", "0.1.0-dev+"},
		{"deadbeef", "0.1.0-dev+deadbeef"},
	}
	for _, tc := range tests {
		got := VersionString(tc.input)
		if got != tc.expected {
			t.Errorf("VersionString(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// TestShortHEAD_Isolated verifies that ShortHEAD(root) always returns the
// commit of root, regardless of the process CWD.
func TestShortHEAD_Isolated(t *testing.T) {
	// Create repo A and make an initial commit.
	repoA := t.TempDir()
	initRepo(t, repoA, "first commit in A")
	shaA := shortHEADFromCWD(t, repoA)

	// Create repo B, make a different commit, and chdir there.
	repoB := t.TempDir()
	initRepo(t, repoB, "first commit in B")
	shaB := shortHEADFromCWD(t, repoB)

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	if err := os.Chdir(repoB); err != nil {
		t.Fatal(err)
	}

	// ShortHEAD(repoA) must return repoA's commit, not the CWD repo's.
	gotA, err := ShortHEAD(repoA)
	if err != nil {
		t.Fatalf("ShortHEAD(repoA): %v", err)
	}
	if gotA != shaA {
		t.Errorf("ShortHEAD(repoA) = %q, want %q (repo A commit)", gotA, shaA)
	}

	// ShortHEAD(repoB) must still work.
	gotB, err := ShortHEAD(repoB)
	if err != nil {
		t.Fatalf("ShortHEAD(repoB): %v", err)
	}
	if gotB != shaB {
		t.Errorf("ShortHEAD(repoB) = %q, want %q (repo B commit)", gotB, shaB)
	}
}

// TestGitDir_AlwaysSetsDir verifies that gitDir sets Dir and reads the
// correct repository.
func TestGitDir_AlwaysSetsDir(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo, "test commit")

	out, err := gitDir(repo, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatalf("gitDir: %v", err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		t.Errorf("expected gitDir to detect repo, got: %s", string(out))
	}
}

// --- snapshotWatcher tests ---

func TestSnapshotWatcher_NoIdentity(t *testing.T) {
	home := t.TempDir()
	snap := snapshotWatcher(home)
	if snap.Active {
		t.Error("expected Active=false for empty home")
	}
	if snap.OldVersion != "" {
		t.Errorf("OldVersion = %q, want empty", snap.OldVersion)
	}
	if snap.OldPID != 0 {
		t.Errorf("OldPID = %d, want 0", snap.OldPID)
	}
}

func TestSnapshotWatcher_NoBeat(t *testing.T) {
	home := t.TempDir()
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)

	snap := snapshotWatcher(home)
	if snap.Active {
		t.Error("expected Active=false when no beat exists")
	}
}

func TestSnapshotWatcher_IdentityPIDMismatch(t *testing.T) {
	home := t.TempDir()
	id := orchestrator.NewIdentity(home)
	id.PID = 99999
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	snap := snapshotWatcher(home)
	if snap.Active {
		t.Error("expected Active=false when identity PID != beat PID")
	}
}

func TestSnapshotWatcher_Active(t *testing.T) {
	home := t.TempDir()
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	snap := snapshotWatcher(home)
	if !snap.Active {
		t.Error("expected Active=true for valid identity+beat")
	}
	if snap.OldVersion != orchestrator.BuildVersion {
		t.Errorf("OldVersion = %q, want %q", snap.OldVersion, orchestrator.BuildVersion)
	}
	if snap.OldPID != os.Getpid() {
		t.Errorf("OldPID = %d, want %d", snap.OldPID, os.Getpid())
	}
	if snap.OldCommitSHA != id.CommitSHA {
		t.Errorf("OldCommitSHA = %q, want %q", snap.OldCommitSHA, id.CommitSHA)
	}
}

// --- waitForNewWatcher tests ---

func TestWaitForNewWatcher_Timeout(t *testing.T) {
	defer setHandshakeTimeout(50 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()
	snap := &WatcherSnapshot{
		Active:             true,
		OldVersion:         "0.1.0-dev+oldcommit",
		OldPID:             99999,
		OldCommitSHA:       "oldcommit",
		InstalledVersion:   "0.1.0-dev+newcommit",
		InstalledCommitSHA: "newcommit",
		InstalledPath:      "/tmp/fake-munsu",
	}

	err := waitForNewWatcher(home, snap)
	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}

	he, ok := err.(*HandshakeError)
	if !ok {
		t.Fatalf("expected *HandshakeError, got %T: %v", err, err)
	}
	if he.DesiredVersion != "0.1.0-dev+newcommit" {
		t.Errorf("DesiredVersion = %q, want %q", he.DesiredVersion, "0.1.0-dev+newcommit")
	}
	if he.DesiredCommitSHA != "newcommit" {
		t.Errorf("DesiredCommitSHA = %q, want %q", he.DesiredCommitSHA, "newcommit")
	}
	if he.OldVersion != "0.1.0-dev+oldcommit" {
		t.Errorf("OldVersion = %q, want %q", he.OldVersion, "0.1.0-dev+oldcommit")
	}
	if he.OldCommitSHA != "oldcommit" {
		t.Errorf("OldCommitSHA = %q, want %q", he.OldCommitSHA, "oldcommit")
	}
	if he.OldPID != "99999" {
		t.Errorf("OldPID = %q, want 99999", he.OldPID)
	}
}

func TestWaitForNewWatcher_IdentityAppears(t *testing.T) {
	defer setHandshakeTimeout(2 * time.Second)()
	defer setHeartBeatPoll(20 * time.Millisecond)()

	home := t.TempDir()
	snap := &WatcherSnapshot{
		Active:             true,
		OldVersion:         "0.1.0-dev+oldcommit",
		OldCommitSHA:       "oldcommit",
		OldPID:             77777,
		InstalledVersion:   "0.1.0-dev+newcommit",
		InstalledCommitSHA: "newcommit",
		InstalledPath:      "/tmp/fake-munsu",
	}

	// Simulate watcher starting in background after a short delay.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		// Write identity with new commit SHA.
		id := orchestrator.NewIdentity(home)
		id.CommitSHA = "newcommit"
		id.BuildVersion = "0.1.0-dev+newcommit"
		orchestrator.WriteIdentity(home, id)
		// Write beat.
		orchestrator.WriteBeat(home)
	}()

	err := waitForNewWatcher(home, snap)
	wg.Wait()

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestWaitForNewWatcher_OnlyBeatWithoutIdentity(t *testing.T) {
	defer setHandshakeTimeout(50 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()
	snap := &WatcherSnapshot{
		Active:             true,
		OldVersion:         "0.1.0-dev+old",
		OldCommitSHA:       "oldcommit",
		OldPID:             77777,
		InstalledVersion:   "0.1.0-dev+new",
		InstalledCommitSHA: "newcommit",
		InstalledPath:      "/tmp/fake",
	}

	// Write beat but no identity — should still time out.
	orchestrator.WriteBeat(home)

	err := waitForNewWatcher(home, snap)
	if err == nil {
		t.Fatal("expected error when identity never arrives")
	}

	he, ok := err.(*HandshakeError)
	if !ok {
		t.Fatalf("expected *HandshakeError, got %T", err)
	}
	if he.IdentityOK {
		t.Error("IdentityOK should be false")
	}
}

// --- waitForNewWatcher spoofed-identity / stale-beat tests ---

func TestWaitForNewWatcher_SpoofedIdentity(t *testing.T) {
	defer setHandshakeTimeout(50 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()
	snap := &WatcherSnapshot{
		Active:             true,
		OldVersion:         "0.1.0-dev+old",
		OldCommitSHA:       "oldcommit",
		OldPID:             77777,
		InstalledVersion:   "0.1.0-dev+new",
		InstalledCommitSHA: "newcommit",
		InstalledPath:      "/tmp/fake",
	}

	// Write identity with matching CommitSHA but dead PID.
	spoofedID := orchestrator.NewIdentity(home)
	spoofedID.PID = 99999
	spoofedID.CommitSHA = "newcommit"
	spoofedID.BuildVersion = "0.1.0-dev+new"
	orchestrator.WriteIdentity(home, spoofedID)

	// Write beat matching the spoofed PID with fresh timestamp.
	beatContent := fmt.Sprintf("%d %d", time.Now().Unix(), 99999)
	os.WriteFile(orchestrator.BeatPath(home), []byte(beatContent), 0644)

	err := waitForNewWatcher(home, snap)
	if err == nil {
		t.Fatal("expected timeout error for spoofed identity")
	}
	he, ok := err.(*HandshakeError)
	if !ok {
		t.Fatalf("expected *HandshakeError, got %T", err)
	}
	if he.OwnershipOK {
		t.Error("OwnershipOK should be false for spoofed PID")
	}
}

func TestWaitForNewWatcher_StaleBeat(t *testing.T) {
	defer setHandshakeTimeout(50 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()
	snap := &WatcherSnapshot{
		Active:             true,
		OldVersion:         "0.1.0-dev+old",
		OldCommitSHA:       "oldcommit",
		OldPID:             77777,
		InstalledVersion:   "0.1.0-dev+new",
		InstalledCommitSHA: "newcommit",
		InstalledPath:      "/tmp/fake",
	}

	// Identity with real PID and matching CommitSHA.
	id := orchestrator.NewIdentity(home)
	id.CommitSHA = "newcommit"
	id.BuildVersion = "0.1.0-dev+new"
	orchestrator.WriteIdentity(home, id)

	// Beat with stale timestamp.
	beatContent := fmt.Sprintf("%d %d", time.Now().Add(-10*time.Minute).Unix(), os.Getpid())
	os.WriteFile(orchestrator.BeatPath(home), []byte(beatContent), 0644)

	err := waitForNewWatcher(home, snap)
	if err == nil {
		t.Fatal("expected timeout error for stale beat")
	}
	he, ok := err.(*HandshakeError)
	if !ok {
		t.Fatalf("expected *HandshakeError, got %T", err)
	}
	if he.BeatFresh {
		t.Error("BeatFresh should be false for stale beat")
	}
}

func TestWaitForNewWatcher_FutureBeat(t *testing.T) {
	defer setHandshakeTimeout(50 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()
	snap := &WatcherSnapshot{
		Active:             true,
		OldVersion:         "0.1.0-dev+old",
		OldCommitSHA:       "oldcommit",
		OldPID:             77777,
		InstalledVersion:   "0.1.0-dev+new",
		InstalledCommitSHA: "newcommit",
		InstalledPath:      "/tmp/fake",
	}

	// Identity with real PID and matching CommitSHA.
	id := orchestrator.NewIdentity(home)
	id.CommitSHA = "newcommit"
	id.BuildVersion = "0.1.0-dev+new"
	orchestrator.WriteIdentity(home, id)

	// Beat with future timestamp.
	beatContent := fmt.Sprintf("%d %d", time.Now().Add(1*time.Hour).Unix(), os.Getpid())
	os.WriteFile(orchestrator.BeatPath(home), []byte(beatContent), 0644)

	err := waitForNewWatcher(home, snap)
	if err == nil {
		t.Fatal("expected timeout error for future beat")
	}
	he, ok := err.(*HandshakeError)
	if !ok {
		t.Fatalf("expected *HandshakeError, got %T", err)
	}
	if he.BeatFresh {
		t.Error("BeatFresh should be false for future beat")
	}
}

func TestHandshakeError_Format(t *testing.T) {
	err := &HandshakeError{
		OldVersion:       "0.1.0-dev+abc",
		OldPID:           "12345",
		OldCommitSHA:     "abc",
		DesiredVersion:   "0.1.0-dev+xyz",
		DesiredCommitSHA: "xyz",
		IdentityVersion:  "",
		IdentityPID:      "",
		BeatPID:          "",
		BeatTimestamp:    0,
		BeatOK:           false,
		IdentityOK:       false,
	}
	msg := err.Error()
	if !strings.Contains(msg, "old=0.1.0-dev+abc") {
		t.Errorf("message should contain old version, got: %s", msg)
	}
	if !strings.Contains(msg, "commit=abc") {
		t.Errorf("message should contain old commit SHA, got: %s", msg)
	}
	if !strings.Contains(msg, "desired=0.1.0-dev+xyz") {
		t.Errorf("message should contain desired version, got: %s", msg)
	}
	if !strings.Contains(msg, "commit=xyz") {
		t.Errorf("message should contain desired commit SHA, got: %s", msg)
	}
	if !strings.Contains(msg, "pid=12345") {
		t.Errorf("message should contain old PID, got: %s", msg)
	}
}

func TestHandshakeError_PartialSuccess(t *testing.T) {
	err := &HandshakeError{
		OldVersion:       "0.1.0-dev+abc",
		OldPID:           "12345",
		OldCommitSHA:     "abc",
		DesiredVersion:   "0.1.0-dev+xyz",
		DesiredCommitSHA: "xyz",
		IdentityVersion:  "0.1.0-dev+xyz",
		IdentityPID:      "99999",
		BeatPID:          "88888",
		BeatTimestamp:    1000000,
		BeatOK:           false,
		IdentityOK:       true,
	}
	msg := err.Error()
	if !strings.Contains(msg, "identity=ok") {
		t.Errorf("message should say identity=ok, got: %s", msg)
	}
	if !strings.Contains(msg, "beat=88888") {
		t.Errorf("message should contain beat evidence, got: %s", msg)
	}
}

// --- Integration: UpdateWithHandshake orchestration tests ---

// TestUpdateWithHandshake_NoActiveWatcher verifies that when no watcher is
// running, UpdateWithHandshake runs the update, skips restart, and returns
// a snapshot with Active=false.
func TestUpdateWithHandshake_NoActiveWatcher(t *testing.T) {
	defer setHandshakeTimeout(100 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()
	// Deliberately no identity or beat — watcher is not active.

	calledUpdate := false
	savedUpdate := doUpdate
	doUpdate = func() error {
		calledUpdate = true
		return nil
	}
	defer func() { doUpdate = savedUpdate }()

	snap, err := UpdateWithHandshake(home)
	if err != nil {
		t.Fatalf("UpdateWithHandshake: %v", err)
	}
	if !calledUpdate {
		t.Error("Update() was not called")
	}
	if snap.Active {
		t.Error("expected Active=false for empty home")
	}
}

// TestUpdateWithHandshake_ActiveWatcherRestarts verifies that when a watcher
// is active, UpdateWithHandshake restarts it and waits for the new build
// identity to appear.
func TestUpdateWithHandshake_ActiveWatcherRestarts(t *testing.T) {
	defer setHandshakeTimeout(2 * time.Second)()
	defer setHeartBeatPoll(20 * time.Millisecond)()

	home := t.TempDir()

	// Set up a fake active watcher: identity matches beat.
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	installedVersion := "0.1.0-dev+newcommit"
	installedCommitSHA := "newcommit"

	// Override version resolution so the snapshot carries the test version.
	savedResolve := resolveInstalledVersion
	resolveInstalledVersion = func(snap *WatcherSnapshot) {
		snap.InstalledPath = "/tmp/fake-munsu"
		snap.InstalledVersion = installedVersion
		snap.InstalledCommitSHA = installedCommitSHA
	}
	defer func() { resolveInstalledVersion = savedResolve }()

	// Replace doUpdate with a no-op.
	savedUpdate := doUpdate
	doUpdate = func() error { return nil }
	defer func() { doUpdate = savedUpdate }()

	// Start a subprocess helper to simulate a new watcher with a real PID.
	// This is required because waitForNewWatcher now validates process
	// ownership (ValidatePIDOwnership), which needs a real process PID.
	savedArm := doArmBackground
	doArmBackground = func(dir string, restart bool) error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperNewWatcher$")
		cmd.Env = append(os.Environ(),
			"GO_TEST_HELPER_NEW_WATCHER=1",
			"GO_TEST_HELPER_HOME="+home,
			"GO_TEST_HELPER_VERSION="+installedVersion,
			"GO_TEST_HELPER_COMMIT="+installedCommitSHA,
		)
		if err := cmd.Start(); err != nil {
			return err
		}
		t.Cleanup(func() { cmd.Process.Kill() })
		return nil
	}
	defer func() { doArmBackground = savedArm }()

	snap, err := UpdateWithHandshake(home)
	if err != nil {
		t.Fatalf("UpdateWithHandshake: %v", err)
	}
	if !snap.Active {
		t.Error("expected Active=true")
	}
	if snap.InstalledVersion != installedVersion {
		t.Errorf("InstalledVersion = %q, want %q", snap.InstalledVersion, installedVersion)
	}
	if snap.InstalledCommitSHA != installedCommitSHA {
		t.Errorf("InstalledCommitSHA = %q, want %q", snap.InstalledCommitSHA, installedCommitSHA)
	}
	if snap.OldPID != os.Getpid() {
		t.Errorf("OldPID = %d, want %d", snap.OldPID, os.Getpid())
	}
}

// TestUpdateWithHandshake_TimeoutCarriesEvidence verifies that when the
// watcher does not appear after the update, the error is a HandshakeError
// with old/new evidence, and the message includes "binary updated but watcher
// convergence failed".
func TestUpdateWithHandshake_TimeoutCarriesEvidence(t *testing.T) {
	defer setHandshakeTimeout(50 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()

	// Set up a fake active watcher with known CommitSHA.
	id := orchestrator.NewIdentity(home)
	id.BuildVersion = "0.1.0-dev+oldcommit"
	id.CommitSHA = "oldcommit"
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	installedVersion := "0.1.0-dev+newcommit"
	installedCommitSHA := "newcommit"

	// Override version resolution so the snapshot carries a known InstalledVersion.
	savedResolve := resolveInstalledVersion
	resolveInstalledVersion = func(snap *WatcherSnapshot) {
		snap.InstalledPath = "/tmp/fake-munsu"
		snap.InstalledVersion = installedVersion
		snap.InstalledCommitSHA = installedCommitSHA
	}
	defer func() { resolveInstalledVersion = savedResolve }()

	// Track that old version was captured in snapshot.
	oldPID := os.Getpid()

	// Replace doUpdate with a no-op.
	savedUpdate := doUpdate
	doUpdate = func() error {
		return nil
	}
	defer func() { doUpdate = savedUpdate }()

	// Replace doArmBackground to NOT start a new watcher (simulate hang).
	savedArm := doArmBackground
	doArmBackground = func(dir string, restart bool) error {
		return nil // arm succeeds but watcher never starts
	}
	defer func() { doArmBackground = savedArm }()

	_, err := UpdateWithHandshake(home)
	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}

	// Error message must clearly state binary was updated but convergence failed.
	msg := err.Error()
	if !strings.Contains(msg, "binary updated but watcher convergence failed") {
		t.Errorf("error message should mention binary-update success: %s", msg)
	}

	// Must be a HandshakeError with evidence.
	var he *HandshakeError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HandshakeError, got %T: %v", err, err)
	}
	if he.OldVersion != "0.1.0-dev+oldcommit" {
		t.Errorf("OldVersion = %q, want 0.1.0-dev+oldcommit", he.OldVersion)
	}
	if he.OldCommitSHA != "oldcommit" {
		t.Errorf("OldCommitSHA = %q, want oldcommit", he.OldCommitSHA)
	}
	if he.OldPID != fmt.Sprintf("%d", oldPID) {
		t.Errorf("OldPID = %q, want %d", he.OldPID, oldPID)
	}
	if he.IdentityOK {
		t.Error("IdentityOK should be false on timeout")
	}
}

// TestUpdateWithHandshake_UpdateFails verifies that when Update() returns an
// error, it is propagated directly without wrapping.
func TestUpdateWithHandshake_UpdateFails(t *testing.T) {
	home := t.TempDir()

	savedUpdate := doUpdate
	doUpdate = func() error {
		return fmt.Errorf("git fetch failed: test error")
	}
	defer func() { doUpdate = savedUpdate }()

	snap, err := UpdateWithHandshake(home)
	if err == nil {
		t.Fatal("expected error from Update()")
	}
	if !strings.Contains(err.Error(), "git fetch failed") {
		t.Errorf("error should contain Update() error: %v", err)
	}
	if snap.Active {
		t.Error("expected Active=false when no watcher was set up")
	}
}

// TestUpdateWithHandshake_ArmBackgroundFails verifies that when ArmBackground
// errors, the message includes "binary updated but watcher convergence failed".
func TestUpdateWithHandshake_ArmBackgroundFails(t *testing.T) {
	defer setHandshakeTimeout(100 * time.Millisecond)()

	home := t.TempDir()

	// Set up a fake active watcher.
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	savedUpdate := doUpdate
	doUpdate = func() error {
		return nil
	}
	defer func() { doUpdate = savedUpdate }()

	savedArm := doArmBackground
	doArmBackground = func(dir string, restart bool) error {
		return fmt.Errorf("starting watcher: exec format error")
	}
	defer func() { doArmBackground = savedArm }()

	_, err := UpdateWithHandshake(home)
	if err == nil {
		t.Fatal("expected error from ArmBackground")
	}
	msg := err.Error()
	if !strings.Contains(msg, "binary updated but watcher convergence failed") {
		t.Errorf("error should mention binary-update success: %s", msg)
	}
	if !strings.Contains(msg, "starting watcher: exec format error") {
		t.Errorf("error should contain underlying error: %s", msg)
	}
}

// --- UpdateWithHandshakeEx tests ---

// TestResolveBuildIdentity_PopulatesBoth verifies that resolveBuildIdentity
// sets both InstalledVersion and InstalledCommitSHA from a commit string.
func TestResolveBuildIdentity_PopulatesBoth(t *testing.T) {
	snap := &WatcherSnapshot{}
	resolveBuildIdentity(snap, "abc1234")
	if snap.InstalledVersion != "0.1.0-dev+abc1234" {
		t.Errorf("InstalledVersion = %q, want 0.1.0-dev+abc1234", snap.InstalledVersion)
	}
	if snap.InstalledCommitSHA != "abc1234" {
		t.Errorf("InstalledCommitSHA = %q, want abc1234", snap.InstalledCommitSHA)
	}
}

// TestResolveBuildIdentity_EmptyCommit verifies that an empty commit produces
// an empty version string and empty commit SHA.
func TestResolveBuildIdentity_EmptyCommit(t *testing.T) {
	snap := &WatcherSnapshot{}
	resolveBuildIdentity(snap, "")
	if snap.InstalledVersion != "0.1.0-dev+" {
		t.Errorf("InstalledVersion = %q, want 0.1.0-dev+", snap.InstalledVersion)
	}
	if snap.InstalledCommitSHA != "" {
		t.Errorf("InstalledCommitSHA = %q, want empty", snap.InstalledCommitSHA)
	}
}

// TestUpdateWithHandshakeEx_PopulatesInstalledCommitSHA verifies that when
// UpdateWithHandshakeEx runs with a valid repo, both InstalledVersion and
// InstalledCommitSHA are populated from the commit SHA.
func TestUpdateWithHandshakeEx_PopulatesInstalledCommitSHA(t *testing.T) {
	defer setHandshakeTimeout(100 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	// Capture the expected commit.
	expectedCommit := shortHEADFromCWD(t, repo)

	// Override doUpdateIn to be a no-op (skip actual git fetch/build).
	savedUpdateIn := doUpdateIn
	doUpdateIn = func(string) error { return nil }
	defer func() { doUpdateIn = savedUpdateIn }()

	// No active watcher, so handshake will skip convergence.
	snap, err := UpdateWithHandshakeEx(home, repo)
	if err != nil {
		t.Fatalf("UpdateWithHandshakeEx: %v", err)
	}
	if snap.Active {
		t.Error("expected Active=false for empty home")
	}
	if snap.InstalledVersion != "0.1.0-dev+"+expectedCommit {
		t.Errorf("InstalledVersion = %q, want 0.1.0-dev+%s", snap.InstalledVersion, expectedCommit)
	}
	if snap.InstalledCommitSHA != expectedCommit {
		t.Errorf("InstalledCommitSHA = %q, want %q", snap.InstalledCommitSHA, expectedCommit)
	}
}

// TestUpdateWithHandshakeEx_UpdateFailureNoConvergence verifies that when the
// update step fails, no watcher convergence is attempted and the error is
// propagated directly.
func TestUpdateWithHandshakeEx_UpdateFailureNoConvergence(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	armCalled := false
	savedUpdateIn := doUpdateIn
	doUpdateIn = func(string) error {
		return fmt.Errorf("build failed: test error")
	}
	defer func() { doUpdateIn = savedUpdateIn }()

	savedArm := doArmBackground
	doArmBackground = func(dir string, restart bool) error {
		armCalled = true
		return nil
	}
	defer func() { doArmBackground = savedArm }()

	snap, err := UpdateWithHandshakeEx(home, repo)
	if err == nil {
		t.Fatal("expected error from UpdateWithHandshakeEx")
	}
	if !strings.Contains(err.Error(), "build failed: test error") {
		t.Errorf("error should contain injected error: %v", err)
	}
	if armCalled {
		t.Error("ArmBackground should not be called after update failure")
	}
	if snap.Active {
		t.Error("expected Active=false when no watcher was set up")
	}
}

// TestUpdateWithHandshakeEx_EmptyCommitFailsClosed verifies that an empty
// desired commit SHA causes the handshake to fail closed even if a new
// watcher identity appears.
func TestUpdateWithHandshakeEx_EmptyCommitFailsClosed(t *testing.T) {
	defer setHandshakeTimeout(50 * time.Millisecond)()
	defer setHeartBeatPoll(10 * time.Millisecond)()

	home := t.TempDir()
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	// Set up a fake active watcher.
	id := orchestrator.NewIdentity(home)
	id.BuildVersion = "0.1.0-dev+oldcommit"
	id.CommitSHA = "oldcommit"
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	savedUpdateIn := doUpdateIn
	doUpdateIn = func(string) error { return nil }
	defer func() { doUpdateIn = savedUpdateIn }()

	// Override doArmBackground to synchronously overwrite identity with
	// an empty commit and write the beat, then return success.
	// This lets the real waitForNewWatcher poll and discover the empty
	// commit identity, exercising the real handshake evidence path.
	savedArm := doArmBackground
	doArmBackground = func(dir string, restart bool) error {
		emptyID := orchestrator.NewIdentity(dir)
		emptyID.BuildVersion = "0.1.0-dev+newcommit"
		emptyID.CommitSHA = ""
		if err := orchestrator.WriteIdentity(dir, emptyID); err != nil {
			return err
		}
		orchestrator.WriteBeat(dir)
		return nil
	}
	defer func() { doArmBackground = savedArm }()

	_, err := UpdateWithHandshakeEx(home, repo)
	if err == nil {
		t.Fatal("expected error for empty desired commit, got nil")
	}
	// Must include convergence-failed message, not update error.
	if !strings.Contains(err.Error(), "binary updated but watcher convergence failed") {
		t.Errorf("error should mention convergence failure: %s", err.Error())
	}
	// The underlying handshake error should show identity with empty commit.
	var he *HandshakeError
	if errors.As(err, &he) {
		if he.IdentityCommitSHA != "" {
			t.Errorf("IdentityCommitSHA should be empty, got %q", he.IdentityCommitSHA)
		}
	}
}

// TestUpdateWithHandshakeEx_ActiveWatcherHandshake verifies that when a
// watcher is active, UpdateWithHandshakeEx restarts it and waits for the
// new build identity. Uses a real subprocess for PID ownership validation,
// mirroring the --repo path.
func TestUpdateWithHandshakeEx_ActiveWatcherHandshake(t *testing.T) {
	defer setHandshakeTimeout(2 * time.Second)()
	defer setHeartBeatPoll(20 * time.Millisecond)()

	home := t.TempDir()
	repo := t.TempDir()
	initMunsuRepo(t, repo, "main")

	// Set up a fake active watcher.
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	expectedCommit := shortHEADFromCWD(t, repo)

	savedUpdateIn := doUpdateIn
	doUpdateIn = func(string) error { return nil }
	defer func() { doUpdateIn = savedUpdateIn }()

	// Start subprocess helper for real PID ownership validation.
	savedArm := doArmBackground
	doArmBackground = func(dir string, restart bool) error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperNewWatcher$")
		cmd.Env = append(os.Environ(),
			"GO_TEST_HELPER_NEW_WATCHER=1",
			"GO_TEST_HELPER_HOME="+home,
			"GO_TEST_HELPER_VERSION=0.1.0-dev+"+expectedCommit,
			"GO_TEST_HELPER_COMMIT="+expectedCommit,
		)
		if err := cmd.Start(); err != nil {
			return err
		}
		t.Cleanup(func() { cmd.Process.Kill() })
		return nil
	}
	defer func() { doArmBackground = savedArm }()

	snap, err := UpdateWithHandshakeEx(home, repo)
	if err != nil {
		t.Fatalf("UpdateWithHandshakeEx: %v", err)
	}
	if !snap.Active {
		t.Error("expected Active=true")
	}
	if snap.InstalledCommitSHA != expectedCommit {
		t.Errorf("InstalledCommitSHA = %q, want %q", snap.InstalledCommitSHA, expectedCommit)
	}
	if snap.InstalledVersion != "0.1.0-dev+"+expectedCommit {
		t.Errorf("InstalledVersion = %q, want 0.1.0-dev+%s", snap.InstalledVersion, expectedCommit)
	}
	if snap.OldPID != os.Getpid() {
		t.Errorf("OldPID = %d, want %d", snap.OldPID, os.Getpid())
	}
}

// TestHelperNewWatcher is a subprocess helper that writes a valid watcher
// identity and beat from a separate process. Used by integration tests
// that need a real PID for ownership validation. Stays alive via
// time.Sleep so the parent can validate PID ownership until cleanup
// kills it.
// kills it.
func TestHelperNewWatcher(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_NEW_WATCHER") != "1" {
		t.Skip("not a helper invocation")
	}
	home := os.Getenv("GO_TEST_HELPER_HOME")
	version := os.Getenv("GO_TEST_HELPER_VERSION")
	commitSHA := os.Getenv("GO_TEST_HELPER_COMMIT")
	if home == "" || version == "" {
		t.Fatal("GO_TEST_HELPER_HOME and GO_TEST_HELPER_VERSION required")
	}
	id := orchestrator.NewIdentity(home)
	id.BuildVersion = version
	id.CommitSHA = commitSHA
	if err := orchestrator.WriteIdentity(home, id); err != nil {
		t.Fatal(err)
	}
	orchestrator.WriteBeat(home)
	// Keep process alive so parent can validate PID ownership.
	time.Sleep(10 * time.Second)
}

// custom build version and commit SHA written into the identity.
func TestSnapshotWatcher_WithCustomVersion(t *testing.T) {
	home := t.TempDir()
	origVersion := orchestrator.BuildVersion
	origCommitSHA := orchestrator.CommitSHA
	orchestrator.BuildVersion = "0.2.0-test+abc1234"
	orchestrator.CommitSHA = "abc1234"
	t.Cleanup(func() {
		orchestrator.BuildVersion = origVersion
		orchestrator.CommitSHA = origCommitSHA
	})

	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	snap := snapshotWatcher(home)
	if !snap.Active {
		t.Fatal("expected active watcher")
	}
	if snap.OldVersion != "0.2.0-test+abc1234" {
		t.Errorf("OldVersion = %q, want %q", snap.OldVersion, "0.2.0-test+abc1234")
	}
	if snap.OldCommitSHA != "abc1234" {
		t.Errorf("OldCommitSHA = %q, want abc1234", snap.OldCommitSHA)
	}
}

// --- snapshotWatcher beat freshness tests ---

func TestSnapshotWatcher_StaleBeat(t *testing.T) {
	home := t.TempDir()
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)

	// Write a beat with a stale timestamp (beyond StaleThreshold).
	beatContent := fmt.Sprintf("%d %d", time.Now().Add(-10*time.Minute).Unix(), os.Getpid())
	os.WriteFile(orchestrator.BeatPath(home), []byte(beatContent), 0644)

	snap := snapshotWatcher(home)
	if snap.Active {
		t.Error("expected Active=false for stale beat")
	}
}

func TestSnapshotWatcher_FutureBeat(t *testing.T) {
	home := t.TempDir()
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)

	// Write a beat with a future timestamp (>5s skew).
	beatContent := fmt.Sprintf("%d %d", time.Now().Add(1*time.Hour).Unix(), os.Getpid())
	os.WriteFile(orchestrator.BeatPath(home), []byte(beatContent), 0644)

	snap := snapshotWatcher(home)
	if snap.Active {
		t.Error("expected Active=false for future beat")
	}
}

// TestHandshakeError_ImplementsError verifies the interface.
func TestHandshakeError_ImplementsError(t *testing.T) {
	var err error = &HandshakeError{DesiredVersion: "test"}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
}

// TestBuildHandshakeError verifies that the error builder collects evidence
// correctly from the filesystem.
func TestBuildHandshakeError(t *testing.T) {
	home := t.TempDir()
	id := orchestrator.NewIdentity(home)
	id.BuildVersion = "0.1.0-dev+stale"
	id.CommitSHA = "stalecommit"
	orchestrator.WriteIdentity(home, id)

	snap := &WatcherSnapshot{
		Active:             true,
		OldVersion:         "0.1.0-dev+old",
		OldPID:             12345,
		OldCommitSHA:       "oldcommit",
		InstalledVersion:   "0.1.0-dev+new",
		InstalledCommitSHA: "newcommit",
	}

	err := buildHandshakeError(home, snap, false, false)
	he, ok := err.(*HandshakeError)
	if !ok {
		t.Fatalf("expected *HandshakeError, got %T", err)
	}
	if he.OldVersion != "0.1.0-dev+old" {
		t.Errorf("OldVersion = %q", he.OldVersion)
	}
	if he.OldCommitSHA != "oldcommit" {
		t.Errorf("OldCommitSHA = %q, want oldcommit", he.OldCommitSHA)
	}
	if he.OldPID != "12345" {
		t.Errorf("OldPID = %q", he.OldPID)
	}
	if he.DesiredVersion != "0.1.0-dev+new" {
		t.Errorf("DesiredVersion = %q", he.DesiredVersion)
	}
	if he.DesiredCommitSHA != "newcommit" {
		t.Errorf("DesiredCommitSHA = %q, want newcommit", he.DesiredCommitSHA)
	}
	// Identity file exists (stale), so we should see it.
	if he.IdentityVersion != "0.1.0-dev+stale" {
		t.Errorf("IdentityVersion = %q, want stale version", he.IdentityVersion)
	}
	// Identity matches the real current process — ownership should pass.
	if !he.OwnershipOK {
		t.Error("OwnershipOK should be true for identity matching current process")
	}
	// No beat file exists — BeatFresh must be false.
	if he.BeatFresh {
		t.Error("BeatFresh should be false when no beat exists")
	}
}

// TestHandshakeError_AllFieldsSet validates that all fields are surfaced.
func TestHandshakeError_AllFieldsSet_IdentityOK(t *testing.T) {
	err := &HandshakeError{
		OldVersion:      "v1",
		OldPID:          "100",
		DesiredVersion:  "v2",
		IdentityVersion: "v2",
		IdentityPID:     "200",
		BeatPID:         "200",
		BeatTimestamp:   5000000,
		BeatOK:          true,
		IdentityOK:      true,
		OwnershipOK:     true,
		BeatFresh:       true,
	}
	msg := err.Error()
	if !strings.Contains(msg, "identity=ok") {
		t.Errorf("should say identity=ok, got: %s", msg)
	}
	if !strings.Contains(msg, "beat=ok") {
		t.Errorf("should say beat=ok, got: %s", msg)
	}
	if strings.Contains(msg, "ownership=fail") {
		t.Errorf("should not include ownership=fail for valid ownership, got: %s", msg)
	}
	if strings.Contains(msg, "beatfresh=fail") {
		t.Errorf("should not include beatfresh=fail for fresh beat, got: %s", msg)
	}
}

// TestHandshakeError_FailedWithCommitSHA verifies that the error includes
// commit SHA information when available.
func TestHandshakeError_FailedWithCommitSHA(t *testing.T) {
	err := &HandshakeError{
		OldVersion:        "0.1.0-dev+abc",
		OldPID:            "12345",
		OldCommitSHA:      "abc",
		DesiredVersion:    "0.1.0-dev+xyz",
		DesiredCommitSHA:  "xyz",
		IdentityVersion:   "0.1.0-dev+other",
		IdentityPID:       "99999",
		IdentityCommitSHA: "different_sha",
		BeatPID:           "88888",
		BeatTimestamp:     1000000,
		BeatOK:            false,
		IdentityOK:        false,
		OwnershipOK:       false,
		BeatFresh:         false,
	}
	msg := err.Error()
	if !strings.Contains(msg, "commit=abc") {
		t.Errorf("message should contain old commit SHA, got: %s", msg)
	}
	if !strings.Contains(msg, "commit=xyz") {
		t.Errorf("message should contain desired commit SHA, got: %s", msg)
	}
	if !strings.Contains(msg, "commit=different_sha") {
		t.Errorf("message should contain identity commit SHA, got: %s", msg)
	}
}

// TestConcurrentSnapshotAccess verifies that snapshot creation is safe.
func TestConcurrentSnapshotAccess(t *testing.T) {
	home := t.TempDir()
	id := orchestrator.NewIdentity(home)
	orchestrator.WriteIdentity(home, id)
	orchestrator.WriteBeat(home)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := snapshotWatcher(home)
			if snap == nil {
				t.Error("snapshot should not be nil")
			}
		}()
	}
	wg.Wait()
}

// --- helpers ---

// initRepo creates a git repo at root and makes an initial commit.
func initRepo(t *testing.T, root, msg string) {
	t.Helper()
	for _, cmd := range []struct {
		args []string
		desc string
	}{
		{[]string{"init"}, "git init"},
		{[]string{"config", "user.email", "test@test"}, "set email"},
		{[]string{"config", "user.name", "test"}, "set name"},
		{[]string{"commit", "--allow-empty", "-m", msg}, "first commit"},
	} {
		c := exec.Command("git", cmd.args...)
		c.Dir = root
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			t.Fatalf("%s: %v", cmd.desc, err)
		}
	}
}

// shortHEADFromCWD returns the short commit at root by running git directly
// from the CWD package (not via ShortHEAD), for use as the expected value.
func shortHEADFromCWD(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("shortHEADFromCWD: %v", err)
	}
	return strings.TrimSpace(string(out))
}
