// Package selfupdate provides fast-forward-only self-update for munsu.
package selfupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/lifecycle"
	"github.com/minhtri2710/munsu/internal/supervision"
)

// WatcherSnapshot captures whether a watcher was active before an update.
// Fields carry evidence for reporting partial or failed restarts.
type WatcherSnapshot struct {
	Active              bool   // true if an identity and beat were found
	OldVersion          string // build version of the watcher before update
	OldPID              int    // PID of the watcher before update
	OldCommitSHA        string // commit SHA of the old watcher
	InstalledVersion    string // new build version after update
	InstalledCommitSHA  string // verified commit SHA of the installed build
	InstalledPath       string // path to the installed binary
}

// handshakeTimeout is the maximum time to wait for a restarted watcher
// to write its identity with the new build version.
var handshakeTimeout = 15 * time.Second

// heartBeatPoll is the interval between polls while waiting for the watcher.
var heartBeatPoll = 200 * time.Millisecond

// doUpdate is an injectable seam for tests. Production code must not replace it.
var doUpdate = Update

// doArmBackground is an injectable seam for tests. Production code must not replace it.
var doArmBackground = supervision.ArmBackground

// resolveInstalledVersion populates InstalledPath, InstalledVersion, and
// InstalledCommitSHA on the snapshot by resolving the binary's real path
// and inspecting the git HEAD.
// Made injectable for tests so they can set known values without a real git repo.
var resolveInstalledVersion = func(snap *WatcherSnapshot) {
	execPath, err := os.Executable()
	if err != nil {
		return
	}
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}
	snap.InstalledPath = realPath

	installRoot, err := findGitRoot(filepath.Dir(realPath))
	if err != nil {
		return
	}
	commit, err := ShortHEAD(installRoot)
	if err != nil {
		return
	}
	snap.InstalledVersion = VersionString(commit)
	snap.InstalledCommitSHA = commit
}

// Update performs a fast-forward-only git pull on the munsu installation.
// It determines the install root by resolving the munsu binary's real path,
// then walking up to find the git repository root.
func Update() error {
	// Find the munsu binary path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding munsu binary: %w", err)
	}

	// Resolve symlinks to get the real path
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving munsu path: %w", err)
	}

	// Walk up to find the git repo root
	installRoot, err := findGitRoot(filepath.Dir(realPath))
	if err != nil {
		return fmt.Errorf("finding munsu repository: %w", err)
	}

	// Verify it's a git repo we can update
	gitMeta, err := os.ReadFile(filepath.Join(installRoot, ".git"))
	if err != nil {
		// Could be a bare .git directory
		gitDirPath := filepath.Join(installRoot, ".git")
		if fi, statErr := os.Stat(gitDirPath); statErr != nil || !fi.IsDir() {
			return fmt.Errorf("%s is not a git repository", installRoot)
		}
	} else {
		// .git is a file pointing to a worktree gitdir — follow it
		content := strings.TrimSpace(string(gitMeta))
		if !strings.HasPrefix(content, "gitdir: ") {
			return fmt.Errorf("unexpected .git format in %s", installRoot)
		}
	}

	// Fetch and fast-forward
	cmd := exec.Command("git", "fetch", "origin")
	cmd.Dir = installRoot
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}

	branchBytes, err := gitDir(installRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("determining current branch: %w", err)
	}
	branch := strings.TrimSpace(string(branchBytes))
	if branch == "" || branch == "HEAD" {
		return fmt.Errorf("not on a branch (detached HEAD)")
	}

	// Fast-forward merge
	mergeCmd := exec.Command("git", "merge", "--ff-only", "origin/"+branch)
	mergeCmd.Dir = installRoot
	mergeCmd.Stderr = os.Stderr
	out, err := mergeCmd.Output()
	if err != nil {
		return fmt.Errorf("ff-merge failed (is the tree dirty or diverged?): %w", err)
	}

	fmt.Printf("Updated %s to %s\n", installRoot, strings.TrimSpace(string(out)))

	// Determine commit hash for version stamping
	commit, err := ShortHEAD(installRoot)
	if err != nil {
		return fmt.Errorf("determining commit hash: %w", err)
	}

	// Rebuild binary with version/commit ldflags
	version := VersionString(commit)
	tmpPath := realPath + ".tmp"
	ldflags := fmt.Sprintf("-X github.com/minhtri2710/munsu/internal/cli.Version=%s -X github.com/minhtri2710/munsu/internal/supervision.CommitSHA=%s", version, commit)
	buildCmd := exec.Command("go", "build",
		"-ldflags", ldflags,
		"-o", tmpPath,
		"./cmd/munsu",
	)
	buildCmd.Dir = installRoot
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rebuild failed after update: %w", err)
	}

	// Atomic install: rename temp file over current binary
	if err := os.Rename(tmpPath, realPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("installing updated binary: %w", err)
	}

	fmt.Printf("Rebuilt binary at %s (version %s)\n", realPath, version)
	return nil
}

// snapshotWatcher checks whether a watcher is active and returns a snapshot.
// Returns a snapshot with Active=false if no watcher is clearly running.
func snapshotWatcher(homeDir string) *WatcherSnapshot {
	id := supervision.ReadIdentity(homeDir)
	_, pid, ok := lifecycle.ReadBeat(homeDir)
	if !ok || pid <= 0 || id == nil {
		return &WatcherSnapshot{Active: false}
	}
	// Identity PID must match beat PID (fresh heartbeat).
	if id.PID != pid {
		return &WatcherSnapshot{Active: false}
	}
	// OS-backed process ownership validation: PID must provably belong to
	// the process that wrote the identity (start time + executable match).
	// This rejects stale or reused PIDs.
	if !supervision.ValidatePIDOwnership(homeDir, pid) {
		return &WatcherSnapshot{Active: false}
	}
	// Reject stale or future heartbeats using canonical lifecycle staleness.
	bt := lifecycle.ReadBeatStatus(homeDir, time.Now())
	if !bt.Exists || bt.Stale {
		return &WatcherSnapshot{Active: false}
	}
	if bt.Age < -5*time.Second {
		return &WatcherSnapshot{Active: false}
	}
	return &WatcherSnapshot{
		Active:       true,
		OldVersion:   id.BuildVersion,
		OldPID:       id.PID,
		OldCommitSHA: id.CommitSHA,
	}
}

// UpdateWithHandshake performs an update and, if a watcher was active,
// restarts it and waits for the new binary to claim the watcher role.
// Returns the snapshot with evidence on success or partial/failure.
func UpdateWithHandshake(homeDir string) (*WatcherSnapshot, error) {
	snap := snapshotWatcher(homeDir)

	// Run the standard update (ff-only pull + rebuild + atomic install).
	if err := doUpdate(); err != nil {
		return snap, err
	}

	// Resolve the install root for evidence.
	resolveInstalledVersion(snap)

	// No active watcher: skip restart entirely (no unsolicited start).
	if !snap.Active {
		return snap, nil
	}

	// Binary was swapped successfully. Attempt to converge watcher.
	if err := doArmBackground(homeDir, true); err != nil {
		return snap, fmt.Errorf("binary updated but watcher convergence failed: %w", err)
	}

	// Wait boundedly for heartbeat carrying the new build identity.
	if err := waitForNewWatcher(homeDir, snap); err != nil {
		return snap, fmt.Errorf("binary updated but watcher convergence failed: %w", err)
	}

	return snap, nil
}

// waitForNewWatcher polls the watcher identity and beat until the new build
// version is confirmed or the timeout expires. Uses the snapshot to detect PID
// change and to report evidence on failure.
func waitForNewWatcher(homeDir string, snap *WatcherSnapshot) error {
	deadline := time.Now().Add(handshakeTimeout)
	beatOK := false
	identityOK := false

	for time.Now().Before(deadline) {
		// Check beat: watcher should be writing beats with a new PID
		// and the beat content-timestamp must be fresh (not stale or future).
		_, pid, ok := lifecycle.ReadBeat(homeDir)
		if ok && pid > 0 && pid != snap.OldPID {
			bt := lifecycle.ReadBeatStatus(homeDir, time.Now())
			if bt.Exists && !bt.Stale && bt.Age >= -5*time.Second {
				beatOK = true
			} else {
				beatOK = false
			}
		} else {
			beatOK = false
		}

		// Check identity: should have the new build version (verified via CommitSHA).
		if id := supervision.ReadIdentity(homeDir); id != nil {
			if supervision.NewBuildIdentity(id.CommitSHA).Matches(supervision.NewBuildIdentity(snap.InstalledCommitSHA)) && id.PID > 0 {
				identityOK = true
				_, beatPID, beatOK2 := lifecycle.ReadBeat(homeDir)
				if beatOK2 && beatPID == id.PID && beatPID != snap.OldPID {
					// Verify beat freshness and process ownership before
					// declaring success. This rejects spoofed identity+beat
					// files and stale/future heartbeats.
					bt := lifecycle.ReadBeatStatus(homeDir, time.Now())
					if bt.Exists && !bt.Stale && bt.Age >= -5*time.Second &&
						supervision.ValidatePIDOwnership(homeDir, id.PID) {
						return nil
					}
				}
			}
		}

		time.Sleep(heartBeatPoll)
	}

	// Timeout — build detailed evidence.
	return buildHandshakeError(homeDir, snap, beatOK, identityOK)
}

// buildHandshakeError collects evidence from the filesystem and returns
// a structured HandshakeError for the timeout outcome.
func buildHandshakeError(homeDir string, snap *WatcherSnapshot, beatOK, identityOK bool) error {
	oldPID := ""
	if snap.OldPID > 0 {
		oldPID = fmt.Sprintf("%d", snap.OldPID)
	}

	newID := supervision.ReadIdentity(homeDir)
	newVersion := ""
	newPID := ""
	newCommitSHA := ""
	ownershipOK := false
	if newID != nil {
		newVersion = newID.BuildVersion
		newPID = fmt.Sprintf("%d", newID.PID)
		newCommitSHA = newID.CommitSHA
		ownershipOK = supervision.ValidatePIDOwnership(homeDir, newID.PID)
	}

	beatTS, beatPID, beatOKLive := lifecycle.ReadBeat(homeDir)
	beatPIDStr := ""
	beatFresh := false
	if beatOKLive {
		beatPIDStr = fmt.Sprintf("%d", beatPID)
		bt := lifecycle.ReadBeatStatus(homeDir, time.Now())
		beatFresh = bt.Exists && !bt.Stale && bt.Age >= -5*time.Second
	}

	return &HandshakeError{
		OldVersion:        snap.OldVersion,
		OldPID:            oldPID,
		OldCommitSHA:      snap.OldCommitSHA,
		DesiredVersion:    snap.InstalledVersion,
		DesiredCommitSHA:  snap.InstalledCommitSHA,
		IdentityVersion:   newVersion,
		IdentityPID:       newPID,
		IdentityCommitSHA: newCommitSHA,
		BeatPID:           beatPIDStr,
		BeatTimestamp:     beatTS,
		BeatOK:            beatOK,
		IdentityOK:        identityOK,
		OwnershipOK:       ownershipOK,
		BeatFresh:         beatFresh,
	}
}

// HandshakeError carries detailed evidence when the watcher handshake fails.
type HandshakeError struct {
	OldVersion      string
	OldPID          string
	OldCommitSHA    string
	DesiredVersion  string
	DesiredCommitSHA string
	IdentityVersion string
	IdentityPID     string
	IdentityCommitSHA string
	BeatPID         string
	BeatTimestamp   int64
	BeatOK          bool
	IdentityOK      bool
	OwnershipOK     bool
	BeatFresh       bool
}

func (e *HandshakeError) Error() string {
	var b strings.Builder
	b.WriteString("watcher handshake failed: ")
	if e.OldVersion != "" {
		fmt.Fprintf(&b, "old=%s pid=%s commit=%s ", e.OldVersion, e.OldPID, e.OldCommitSHA)
	}
	fmt.Fprintf(&b, "desired=%s", e.DesiredVersion)
	if e.DesiredCommitSHA != "" {
		fmt.Fprintf(&b, " commit=%s", e.DesiredCommitSHA)
	}
	if e.IdentityOK {
		b.WriteString(" identity=ok")
	} else {
		fmt.Fprintf(&b, " identity=%s pid=%s", e.IdentityVersion, e.IdentityPID)
		if e.IdentityCommitSHA != "" {
			fmt.Fprintf(&b, " commit=%s", e.IdentityCommitSHA)
		}
	}
	if e.BeatOK {
		b.WriteString(" beat=ok")
	} else {
		fmt.Fprintf(&b, " beat=%s ts=%d", e.BeatPID, e.BeatTimestamp)
	}
	if !e.OwnershipOK {
		b.WriteString(" ownership=fail")
	}
	if !e.BeatFresh {
		b.WriteString(" beatfresh=fail")
	}
	return b.String()
}

// findGitRoot walks up from dir to find a git repository root.
func findGitRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	current := abs
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no git repository found from %s", dir)
		}
		current = parent
	}
}

// gitDir runs git with Dir fixed to root. All repo-scoped git goes through this.
func gitDir(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	return cmd.Output()
}

// ShortHEAD returns the short commit SHA at root (never process CWD).
func ShortHEAD(root string) (string, error) {
	out, err := gitDir(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// VersionString builds "0.1.0-dev+<short>" (pure).
func VersionString(shortCommit string) string {
	return fmt.Sprintf("0.1.0-dev+%s", shortCommit)
}

// setHandshakeTimeout overrides the handshake timeout for testing.
// Returns a cleanup function that restores the previous value.
func setHandshakeTimeout(d time.Duration) func() {
	prev := handshakeTimeout
	handshakeTimeout = d
	return func() { handshakeTimeout = prev }
}

// setHeartBeatPoll overrides the heartbeat poll interval for testing.
// Returns a cleanup function that restores the previous value.
func setHeartBeatPoll(d time.Duration) func() {
	prev := heartBeatPoll
	heartBeatPoll = d
	return func() { heartBeatPoll = prev }
}
