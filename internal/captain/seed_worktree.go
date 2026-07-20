package captain

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// CaptainProvenanceName is the provenance metadata file for managed
	// worktree captain homes.
	CaptainProvenanceName = ".captain-provenance"

	// WorktreeGitignoreName is the gitignore file in a managed worktree
	// captain home.
	WorktreeGitignoreName = ".gitignore"
)

// worktreeGitignoreContent lists the operational dirs and files that are
// gitignored in a managed worktree captain home so they never pollute
// the host project's index.
var worktreeGitignoreContent = []string{
	"state/",
	"tmp/",
	"sessions/",
	".captain-launch.sh",
	".captain-provenance",
}

// SeedFromWorktree provisions a managed git-worktree captain home.
//
// It creates a detached worktree at homePath from repoPath's default branch,
// writes .gitignore, writes provenance metadata, then runs the standard seed
// setup (charter, state/data/config dirs, registration, config push, pi
// extensions).
//
// Idempotent: if homePath is already a managed worktree with matching
// provenance, SeedFromWorktree is a no-op (returns nil). Legacy state-only
// homes at homePath cause an error — use the worktree-free Seed/SeedWithParent
// for those.
//
// repoPath must be the root of a local git clone (typically a project repo).
// The default branch is resolved from origin/HEAD.
func SeedFromWorktree(id, homePath, repoPath, parentHome, charter string, force bool, ref string) (err error) {
	var absRepo string
	absRepo, err = filepath.Abs(repoPath)
	if err != nil {
		err = fmt.Errorf("resolving repo path: %w", err)
		return
	}

	// Verify sourceRepo is a git repo.
	if _, stErr := os.Stat(filepath.Join(absRepo, ".git")); stErr != nil {
		err = fmt.Errorf("source repo %s is not a git repository: %w", absRepo, stErr)
		return
	}

	// Abs home path.
	var absHome string
	absHome, err = filepath.Abs(homePath)
	if err != nil {
		err = fmt.Errorf("resolving home path: %w", err)
		return
	}

	// Track created artifacts for rollback.
	var worktreeCreated bool
	var registered bool
	defer func() {
		if err != nil {
			rollbackWorktree(worktreeCreated, absHome, absRepo, registered, parentHome, id)
		}
	}()

	// Idempotency check — already managed worktree with matching provenance.
	managed, mErr := isManagedWorktree(absHome)
	if mErr != nil {
		err = fmt.Errorf("checking if %s is already managed: %w", absHome, mErr)
		return
	}
	if managed && !force {
		// Already managed — safe no-op.
		return nil
	}

	// Reject existing state-only captain homes (cannot be --force replaced).
	if isStateOnlyHome(absHome) {
		err = fmt.Errorf("path %s is an existing state-only captain home; use 'munsu captain seed' (without --repo) or migrate manually", absHome)
		return
	}

	// Remote validation: verify source repo remote matches parent remote.
	if parentHome != "" {
		if err = validateWorktreeRemote(absRepo, parentHome); err != nil {
			return
		}
	}

	// Determine target ref (default branch or explicit --ref).
	checkoutRef := ref
	if checkoutRef == "" {
		var defaultBranch string
		defaultBranch, err = resolveDefaultBranch(absRepo)
		if err != nil {
			err = fmt.Errorf("resolving default branch for %s: %w", absRepo, err)
			return
		}
		checkoutRef = "origin/" + defaultBranch
	}

	// Verify the tracking ref exists (for branch refs, not raw commits).
	if ref == "" {
		if _, verr := gitRun("-C", absRepo, "rev-parse", "--verify", checkoutRef); verr != nil {
			err = fmt.Errorf("remote tracking ref %q does not exist in %s — fetch origin first", checkoutRef, absRepo)
			return
		}
	}

	// If force, remove existing worktree before recreating.
	if force {
		removeExistingWorktree(absHome, absRepo)
	}

	// Create the worktree — git worktree add --detach <homePath> <ref>.
	if _, wtErr := gitRun("-C", absRepo, "worktree", "add", "--detach", "--force", absHome, checkoutRef); wtErr != nil {
		err = fmt.Errorf("creating git worktree at %s: %w", absHome, wtErr)
		return
	}
	worktreeCreated = true

	// Write .gitignore for operational dirs.
	if err = writeWorktreeGitignore(absHome); err != nil {
		err = fmt.Errorf("writing worktree .gitignore: %w", err)
		return
	}

	// Write provenance metadata.
	if err = writeCaptainProvenance(absHome, absRepo); err != nil {
		err = fmt.Errorf("writing captain provenance: %w", err)
		return
	}

	// Create required captain home directories.
	for _, dir := range []string{"state", "data", "config", "projects"} {
		if err = os.MkdirAll(filepath.Join(absHome, dir), 0755); err != nil {
			err = fmt.Errorf("creating %s/%s: %w", absHome, dir, err)
			return
		}
	}

	// Write charter / AGENTS.md.
	if strings.TrimSpace(charter) == "" {
		if parentHome == "" {
			err = fmt.Errorf("seeding captain %s: empty charter requires parent home for return-channel path", id)
			return
		}
		charter = DefaultCharter(id, parentHome)
	}
	if err = os.WriteFile(filepath.Join(absHome, "AGENTS.md"), []byte(charter), 0644); err != nil {
		err = fmt.Errorf("writing AGENTS.md: %w", err)
		return
	}

	// Write the .munsu-captain-home provenance marker (same as regular seed).
	if err = SeedProvenance(absHome, id); err != nil {
		err = fmt.Errorf("seeding provenance marker: %w", err)
		return
	}

	// Register with parent home.
	if parentHome != "" {
		if err = Register(parentHome, id, absHome, "", ""); err != nil {
			err = fmt.Errorf("registering captain %s: %w", id, err)
			return
		}
		registered = true
		if err = ConfigPush(parentHome, absHome); err != nil {
			err = fmt.Errorf("seed inherit: %w", err)
			return
		}
	}

	// Install Pi extensions.
	if err = EnsureCaptainPiExtensions(absHome); err != nil {
		err = fmt.Errorf("installing captain pi extensions: %w", err)
		return
	}

	fmt.Printf("Seeded worktree captain %s at %s (from %s, %s)\n", id, absHome, absRepo, checkoutRef)
	return
}

// isManagedWorktree checks whether homePath is a managed git-worktree
// captain home. Returns true when all of these hold:
//   - homePath exists
//   - homePath/.git exists and is a file (git worktree marker)
//   - homePath/.captain-provenance exists
func isManagedWorktree(homePath string) (bool, error) {
	fi, err := os.Stat(homePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !fi.IsDir() {
		return false, fmt.Errorf("%s exists but is not a directory", homePath)
	}

	// Git worktrees use a .git FILE (not directory) pointing to the main repo.
	gitFi, err := os.Stat(filepath.Join(homePath, ".git"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if gitFi.IsDir() {
		// Regular clone, not a worktree — treat as unmanaged.
		return false, nil
	}

	// Check provenance file.
	if _, err := os.Stat(filepath.Join(homePath, CaptainProvenanceName)); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// isStateOnlyHome checks whether homePath is an existing captain home
// without a git worktree (legacy state-only format).
func isStateOnlyHome(homePath string) bool {
	fi, err := os.Stat(homePath)
	if err != nil || !fi.IsDir() {
		return false
	}
	// Has .munsu-captain-home marker but no .git worktree file.
	_, markerErr := os.Stat(filepath.Join(homePath, ProvenanceMarkerName))
	if markerErr != nil {
		return false
	}
	gitFi, gitErr := os.Stat(filepath.Join(homePath, ".git"))
	return gitErr != nil || gitFi.IsDir()
}

// writeWorktreeGitignore writes the .gitignore file for a managed worktree
// captain home, ensuring operational dirs never pollute the index.
func writeWorktreeGitignore(homePath string) error {
	content := "# Captain home operational dirs and runtime artifacts\n"
	for _, entry := range worktreeGitignoreContent {
		content += entry + "\n"
	}
	content += "# Inherited config and data must also be gitignored\n"
	content += "config/\n"
	content += "data/\n"
	return os.WriteFile(filepath.Join(homePath, WorktreeGitignoreName), []byte(content), 0644)
}

// writeCaptainProvenance writes the .captain-provenance metadata file
// recording the source repo, current commit hash, origin remote, and
// creation timestamp.
func writeCaptainProvenance(homePath, repoPath string) error {
	commit, err := gitRun("-C", repoPath, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolving HEAD commit in %s: %w", repoPath, err)
	}

	origin, err := gitRun("-C", repoPath, "remote", "get-url", "origin")
	if err != nil {
		origin = "" // optional — non-fatal
	}

	content := fmt.Sprintf("source-repo: %s\ncommit: %s\ncreated: %s\n",
		repoPath, commit, time.Now().UTC().Format(time.RFC3339))
	if origin != "" {
		content += fmt.Sprintf("origin: %s\n", origin)
	}

	return os.WriteFile(filepath.Join(homePath, CaptainProvenanceName), []byte(content), 0644)
}

// resolveDefaultBranch returns the default branch name for repoPath
// by reading origin/HEAD's symbolic ref.
func resolveDefaultBranch(repoPath string) (string, error) {
	symRef, err := gitRun("-C", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		// Fallback: try origin/main directly.
		if _, fallbackErr := gitRun("-C", repoPath, "rev-parse", "--verify", "origin/main"); fallbackErr == nil {
			return "main", nil
		}
		// Try origin/master as last resort.
		if _, fallbackErr := gitRun("-C", repoPath, "rev-parse", "--verify", "origin/master"); fallbackErr == nil {
			return "master", nil
		}
		// Fallback: try local main/master branches.
		for _, candidate := range []string{"main", "master"} {
			if _, fbErr := gitRun("-C", repoPath, "rev-parse", "--verify", candidate); fbErr == nil {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("cannot resolve default branch — set origin/HEAD or ensure origin/main exists: %w", err)
	}

	parts := strings.SplitN(symRef, "/", 4)
	if len(parts) < 4 || parts[0] != "refs" || parts[1] != "remotes" || parts[2] != "origin" {
		return "", fmt.Errorf("unexpected origin/HEAD format: %q", symRef)
	}
	return parts[3], nil
}

// rollbackMarkerName is the marker file left on migration failure.
const rollbackMarkerName = ".migration-rollback"

// operationalAllowlist lists the runtime operational dirs that must be
// preserved when migrating a state-only captain home to a managed worktree.
var operationalAllowlist = []string{
	"state",
	"config",
	"data",
	"tmp",
	"sessions",
	"holds",
}

// copyDir recursively copies src directory contents into dst.
// recursiveCopy recursively copies src directory contents into dst.
func recursiveCopy(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return fmt.Errorf("creating dst dir %s: %w", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // source dir missing is not a failure
		}
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := recursiveCopy(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			// Skip symlinks.
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			in, err := os.Open(srcPath)
			if err != nil {
				return err
			}
			out, err := os.Create(dstPath)
			if err != nil {
				in.Close()
				return err
			}
			_, err = io.Copy(out, in)
			in.Close()
			if err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

// copyAllowlistedDirs copies each operational directory from src to dst.
// Missing source dirs are silently skipped.
func copyAllowlistedDirs(src, dst string) error {
	for _, dir := range operationalAllowlist {
		srcDir := filepath.Join(src, dir)
		dstDir := filepath.Join(dst, dir)
		if fi, err := os.Stat(srcDir); err == nil && fi.IsDir() {
			if err := recursiveCopy(srcDir, dstDir); err != nil {
				return fmt.Errorf("copying %s: %w", dir, err)
			}
		}
	}
	return nil
}

// writeRollbackMarker writes a marker file at the captain home's parent
// directory indicating a failed migration.
func writeRollbackMarker(backupPath, captainHome string) error {
	markerDir := filepath.Dir(captainHome)
	markerPath := filepath.Join(markerDir, rollbackMarkerName)
	content := fmt.Sprintf("migration-rollback\nbackup: %s\nhome: %s\ntimestamp: %s\n",
		backupPath, captainHome, time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(markerPath, []byte(content), 0644)
}

// MigrateToWorktree migrates a legacy state-only captain home to a managed
// git-worktree captain home transactionally.
//
// The migration is atomic: if any step fails, the original state-only home
// is restored and a rollback marker is written. After successful migration,
// the old home is backed up at <homePath>.backup-<timestamp>.
//
// Requirements:
//   - captainHome must be a valid state-only captain home
//   - repoPath must be a local git clone whose origin matches parentHome's
//   - id must match the home's provenance id
//   - parentHome is the fleet General home (for registration, config push)
func MigrateToWorktree(captainHome, repoPath, id, parentHome string) (err error) {
	// 1. Validate captain home is a state-only home.
	if !isStateOnlyHome(captainHome) {
		// Refuse if it's already a managed worktree.
		if managed, mErr := isManagedWorktree(captainHome); mErr == nil && managed {
			return fmt.Errorf("captain home %s is already a managed worktree — no migration needed", captainHome)
		}
		return fmt.Errorf("captain home %s is not a state-only home; validate its structure first", captainHome)
	}

	// 2. Validate home structure (dirs + AGENTS.md).
	if err = validateStructure(captainHome); err != nil {
		return fmt.Errorf("migrate pre-check failed: %w", err)
	}

	// 3. Resolve absolute paths.
	var absHome, absRepo string
	absHome, err = filepath.Abs(captainHome)
	if err != nil {
		return fmt.Errorf("resolving home path: %w", err)
	}
	absRepo, err = filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolving repo path: %w", err)
	}

	// 4. Verify source repo is a git repo.
	if _, stErr := os.Stat(filepath.Join(absRepo, ".git")); stErr != nil {
		return fmt.Errorf("source repo %s is not a git repository: %w", absRepo, stErr)
	}

	// 5. Remote validation: verify source repo remote matches parent remote.
	if parentHome != "" {
		if err = validateWorktreeRemote(absRepo, parentHome); err != nil {
			return
		}
	}

	// 6. Determine default branch.
	var defaultBranch string
	defaultBranch, err = resolveDefaultBranch(absRepo)
	if err != nil {
		return fmt.Errorf("resolving default branch for %s: %w", absRepo, err)
	}
	checkoutRef := "origin/" + defaultBranch
	if _, verr := gitRun("-C", absRepo, "rev-parse", "--verify", checkoutRef); verr != nil {
		return fmt.Errorf("remote tracking ref %q does not exist in %s — fetch origin first", checkoutRef, absRepo)
	}

	// 7. Create temp worktree directory alongside the home.
	ts := time.Now().UTC().Format("150405.000000000")
	tmpWorktree := absHome + ".worktree-" + ts

	// Track created artifacts for cleanup on failure.
	var worktreeCreated bool
	var swapped bool
	defer func() {
		if err != nil {
			if swapped {
				// Try to restore backup.
				backupPath := absHome + ".backup-" + ts
				if _, stErr := os.Stat(backupPath); stErr == nil {
					os.RemoveAll(absHome) // remove failed worktree
					os.Rename(backupPath, absHome)
					fmt.Fprintf(os.Stderr, "munsu: restored original captain home from backup\n")
				}
				writeRollbackMarker(backupPath, absHome)
			} else if worktreeCreated {
				removeExistingWorktree(tmpWorktree, absRepo)
				fmt.Fprintf(os.Stderr, "munsu: removed incomplete worktree at %s\n", tmpWorktree)
			}
		}
	}()

	// 8. Create the worktree at temp location.
	if _, wtErr := gitRun("-C", absRepo, "worktree", "add", "--detach", "--force", tmpWorktree, checkoutRef); wtErr != nil {
		return fmt.Errorf("creating git worktree at %s: %w", tmpWorktree, wtErr)
	}
	worktreeCreated = true

	// 9. Write .gitignore for operational dirs.
	if err = writeWorktreeGitignore(tmpWorktree); err != nil {
		return
	}

	// 10. Write captain provenance metadata.
	if err = writeCaptainProvenance(tmpWorktree, absRepo); err != nil {
		return
	}

	// 11. Write the .munsu-captain-home provenance marker.
	if err = SeedProvenance(tmpWorktree, id); err != nil {
		return
	}

	// 12. Copy AGENTS.md from old home.
	srcAgents := filepath.Join(absHome, "AGENTS.md")
	agentsData, rErr := os.ReadFile(srcAgents)
	if rErr != nil {
		return fmt.Errorf("reading AGENTS.md from old home: %w", rErr)
	}
	if err = os.WriteFile(filepath.Join(tmpWorktree, "AGENTS.md"), agentsData, 0644); err != nil {
		return fmt.Errorf("writing AGENTS.md to worktree: %w", err)
	}

	// 13. Copy allowlisted operational dirs from old home.
	if err = copyAllowlistedDirs(absHome, tmpWorktree); err != nil {
		return fmt.Errorf("copying operational dirs: %w", err)
	}

	// 14. Atomic swap: backup old home, rename worktree to home path.
	backupPath := absHome + ".backup-" + ts
	if err = os.Rename(absHome, backupPath); err != nil {
		return fmt.Errorf("backing up old captain home: %w", err)
	}
	if err = os.Rename(tmpWorktree, absHome); err != nil {
		// Failed to rename — try to restore backup.
		os.Rename(backupPath, absHome)
		return fmt.Errorf("atomic swap failed: %w", err)
	}
	swapped = true
	worktreeCreated = false // worktree is now at the real location

	// Re-seed provenance marker so its canonical path points to the real home.
	if err = SeedProvenance(absHome, id); err != nil {
		return fmt.Errorf("re-seeding provenance marker after swap: %w", err)
	}

	// 15. Register with parent home.
	if parentHome != "" {
		if err = Register(parentHome, id, absHome, "", ""); err != nil {
			return fmt.Errorf("registering captain after migration: %w", err)
		}
		if err = ConfigPush(parentHome, absHome); err != nil {
			return fmt.Errorf("config push after migration: %w", err)
		}
	}

	// 16. Install Pi extensions.
	if err = EnsureCaptainPiExtensions(absHome); err != nil {
		return fmt.Errorf("installing captain pi extensions: %w", err)
	}

	fmt.Printf("Migrated captain %s to managed worktree at %s (from %s, %s)\n", id, absHome, absRepo, checkoutRef)
	fmt.Printf("  backup preserved at %s\n", backupPath)
	return nil
}

