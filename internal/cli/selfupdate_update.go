// Package selfupdate provides fast-forward-only self-update for munsu.
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/orchestrator"
)

// WatcherSnapshot captures whether a watcher was active before an update.
// Fields carry evidence for reporting partial or failed restarts.
type WatcherSnapshot struct {
	Active             bool   // true if an identity and beat were found
	OldVersion         string // build version of the watcher before update
	OldPID             int    // PID of the watcher before update
	OldCommitSHA       string // commit SHA of the old watcher
	InstalledVersion   string // new build version after update
	InstalledCommitSHA string // verified commit SHA of the installed build
	InstalledPath      string // path to the installed binary
}

// handshakeTimeout is the maximum time to wait for a restarted watcher
// to write its identity with the new build version.
var handshakeTimeout = 15 * time.Second

// heartBeatPoll is the interval between polls while waiting for the watcher.
var heartBeatPoll = 200 * time.Millisecond

// doUpdateIn is an injectable seam for tests with explicit install root.
var doUpdateIn = UpdateIn

// doArmBackground is an injectable seam for tests. Production code must not replace it.
var doArmBackground = orchestrator.ArmBackground

// resolveBuildIdentity sets InstalledVersion and InstalledCommitSHA on the
// snapshot from the given commit SHA. Shared by UpdateWithHandshake and
// UpdateWithHandshakeEx to keep version and commit identity in sync.
func resolveBuildIdentity(snap *WatcherSnapshot, commit string) {
	snap.InstalledCommitSHA = commit
	snap.InstalledVersion = VersionString(commit)
}

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
	resolveBuildIdentity(snap, commit)
}

// UpdateIn performs a fast-forward-only git pull + rebuild on the given
// install root. The root must be a verified munsu git repository.
func UpdateIn(root string) error {
	realPath, err := execRealPath()
	if err != nil {
		return err
	}
	return updateIn(root, realPath)
}

// execRealPath returns the real (symlink-resolved) path of the running binary.
func execRealPath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding munsu binary: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", fmt.Errorf("resolving munsu path: %w", err)
	}
	return realPath, nil
}

// updateIn is the core update logic: git fetch, ff-merge, rebuild, atomic install.
func updateIn(installRoot, realPath string) error {
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

	// Check worktree is clean before mutation
	if err := requireCleanWorktree(installRoot); err != nil {
		return err
	}

	// Fetch and fast-forward
	cmd := exec.Command("git", "fetch", "origin")
	cmd.Dir = installRoot
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}

	// Resolve default branch from remote HEAD
	defaultBranch, err := resolveDefaultBranch(installRoot)
	if err != nil {
		return err
	}

	// Verify on default branch (not detached)
	currentBranch, err := currentBranch(installRoot)
	if err != nil {
		return err
	}
	if currentBranch != defaultBranch {
		return fmt.Errorf("checked out branch %q does not match default branch %q; only default branch can be updated", currentBranch, defaultBranch)
	}

	// Fast-forward merge
	mergeCmd := exec.Command("git", "merge", "--ff-only", "origin/"+defaultBranch)
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
	ldflags := fmt.Sprintf("-X github.com/minhtri2710/munsu/internal/cli.Version=%s -X github.com/minhtri2710/munsu/internal/orchestrator.CommitSHA=%s", version, commit)
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

// requireCleanWorktree checks that the git worktree has no uncommitted changes.
func requireCleanWorktree(root string) error {
	out, err := gitDir(root, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("checking worktree status: %w", err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		return fmt.Errorf("worktree at %s has uncommitted changes; refusing to update", root)
	}
	return nil
}

// resolveDefaultBranch resolves the default branch name from
// refs/remotes/origin/HEAD. Requires a fetch to have been done.
func resolveDefaultBranch(root string) (string, error) {
	out, err := gitDir(root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		// Fallback: try rev-parse --abbrev-ref, then hardcode main/master
		// as last resort.
		ref, refErr := gitDir(root, "rev-parse", "--abbrev-ref", "HEAD")
		if refErr == nil {
			branch := strings.TrimSpace(string(ref))
			if branch != "" && branch != "HEAD" {
				return branch, nil
			}
		}
		// Try common defaults as fallback
		for _, candidate := range []string{"main", "master"} {
			if _, err := gitDir(root, "rev-parse", "--verify", candidate); err == nil {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("cannot resolve default branch for %s: %w", root, err)
	}
	ref := strings.TrimSpace(string(out))
	// ref is like "refs/remotes/origin/main" — extract the branch name.
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1], nil
}

// currentBranch returns the checked-out branch name.
func currentBranch(root string) (string, error) {
	out, err := gitDir(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("determining current branch: %w", err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "", fmt.Errorf("not on a branch (detached HEAD)")
	}
	return branch, nil
}

// snapshotWatcher checks whether a watcher is active and returns a snapshot.
// Returns a snapshot with Active=false if no watcher is clearly running.
func snapshotWatcher(homeDir string) *WatcherSnapshot {
	id := orchestrator.ReadIdentity(homeDir)
	_, pid, ok := orchestrator.ReadBeat(homeDir)
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
	if !orchestrator.ValidatePIDOwnership(homeDir, pid) {
		return &WatcherSnapshot{Active: false}
	}
	// Reject stale or future heartbeats using canonical lifecycle staleness.
	bt := orchestrator.ReadBeatStatus(homeDir, time.Now())
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

// UpdateWithHandshakeEx resolves the install root using the 6-step resolver,
// persists it, performs the update, and handles the watcher handshake.
// Returns the snapshot with evidence on success or partial/failure.
func UpdateWithHandshakeEx(homeDir, repoOpt string) (*WatcherSnapshot, error) {
	// Step 1: Resolve the install root.
	root, err := ResolveInstallRoot(homeDir, repoOpt)
	if err != nil {
		return nil, err
	}

	// Step 2: Persist after successful resolution.
	if persistErr := PersistInstallRoot(homeDir, root); persistErr != nil {
		// Non-fatal: resolution succeeded, persistence is secondary.
		fmt.Fprintf(os.Stderr, "warning: failed to persist install root: %v\n", persistErr)
	}

	snap := snapshotWatcher(homeDir)

	// Step 3: Perform update in the resolved root.
	if err := doUpdateIn(root); err != nil {
		return snap, err
	}

	// Step 4: Set installed version and commit identity from known root.
	execPath, _ := os.Executable()
	realPath, _ := filepath.EvalSymlinks(execPath)
	if realPath == "" {
		realPath = execPath
	}
	snap.InstalledPath = realPath
	if commit, err := ShortHEAD(root); err == nil {
		resolveBuildIdentity(snap, commit)
	}

	return completeHandshake(homeDir, snap)
}

// completeHandshake runs the post-update handshake: arm background and wait
// for the new build version if the watcher was active.
func completeHandshake(homeDir string, snap *WatcherSnapshot) (*WatcherSnapshot, error) {
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
		_, pid, ok := orchestrator.ReadBeat(homeDir)
		if ok && pid > 0 && pid != snap.OldPID {
			bt := orchestrator.ReadBeatStatus(homeDir, time.Now())
			if bt.Exists && !bt.Stale && bt.Age >= -5*time.Second {
				beatOK = true
			} else {
				beatOK = false
			}
		} else {
			beatOK = false
		}

		// Check identity: should have the new build version (verified via CommitSHA).
		if id := orchestrator.ReadIdentity(homeDir); id != nil {
			if orchestrator.NewBuildIdentity(id.CommitSHA).Matches(orchestrator.NewBuildIdentity(snap.InstalledCommitSHA)) && id.PID > 0 {
				identityOK = true
				_, beatPID, beatOK2 := orchestrator.ReadBeat(homeDir)
				if beatOK2 && beatPID == id.PID && beatPID != snap.OldPID {
					// Verify beat freshness and process ownership before
					// declaring success. This rejects spoofed identity+beat
					// files and stale/future heartbeats.
					bt := orchestrator.ReadBeatStatus(homeDir, time.Now())
					if bt.Exists && !bt.Stale && bt.Age >= -5*time.Second &&
						orchestrator.ValidatePIDOwnership(homeDir, id.PID) {
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

	newID := orchestrator.ReadIdentity(homeDir)
	newVersion := ""
	newPID := ""
	newCommitSHA := ""
	ownershipOK := false
	if newID != nil {
		newVersion = newID.BuildVersion
		newPID = fmt.Sprintf("%d", newID.PID)
		newCommitSHA = newID.CommitSHA
		ownershipOK = orchestrator.ValidatePIDOwnership(homeDir, newID.PID)
	}

	beatTS, beatPID, beatOKLive := orchestrator.ReadBeat(homeDir)
	beatPIDStr := ""
	beatFresh := false
	if beatOKLive {
		beatPIDStr = fmt.Sprintf("%d", beatPID)
		bt := orchestrator.ReadBeatStatus(homeDir, time.Now())
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
	OldVersion        string
	OldPID            string
	OldCommitSHA      string
	DesiredVersion    string
	DesiredCommitSHA  string
	IdentityVersion   string
	IdentityPID       string
	IdentityCommitSHA string
	BeatPID           string
	BeatTimestamp     int64
	BeatOK            bool
	IdentityOK        bool
	OwnershipOK       bool
	BeatFresh         bool
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
