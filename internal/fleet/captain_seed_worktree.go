package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// CaptainProvenanceName is the provenance metadata file for managed
	// worktree captain homes.
	CaptainProvenanceName = ".captain-provenance"

	// CaptainCharterName is the untracked captain charter file written in a
	// managed worktree captain home. It is excluded via git info/exclude so the
	// worktree stays git-clean without modifying the tracked AGENTS.md.
	CaptainCharterName = ".captain-charter.md"
)

// worktreeExcludeContent lists the operational dirs and files that are
// excluded in a managed worktree captain home via git info/exclude so they
// never pollute the host project's index without modifying tracked .gitignore.
var worktreeExcludeContent = []string{
	"state/",
	"tmp/",
	"sessions/",
	"holds/",
	".captain-launch.sh",
	".captain-provenance",
	".munsu-captain-home",
	".captain-charter.md",
	"config/",
	"data/",
}

// seedFromWorktree provisions a managed git-worktree captain home.
//
// It creates a detached worktree at homePath from repoPath's default branch,
// writes git info/exclude for operational dirs, writes provenance metadata, then
// runs the standard seed setup (charter via untracked .captain-charter.md,
// state/data/config dirs, registration, config push, pi extensions).
//
// It never writes to the tracked .gitignore or AGENTS.md, keeping the worktree
// git-clean while still providing a runtime Captain charter file.
//
// Idempotent: if homePath is already a managed worktree with matching
// provenance, seedFromWorktree is a no-op (returns nil). An existing captain
// home at homePath that is not a managed worktree is refused, even with force.
//
// repoPath must be the root of a local git clone (typically a project repo).
// The default branch is resolved from origin/HEAD.
func seedFromWorktree(id, homePath, repoPath, parentHome, charter string, force bool, ref string, integration IntegrationPort) (err error) {
	if integration == nil {
		return fmt.Errorf("captain integration capability is required")
	}
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

	// Refuse to replace an existing unmanaged captain home (force would delete it).
	if isUnmanagedCaptainHome(absHome) {
		err = fmt.Errorf("path %s is an existing captain home that is not a managed worktree; it is unsupported and will not be replaced — remove it and reseed", absHome)
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

	// Write git info/exclude for operational dirs instead of tracked .gitignore.
	if err = writeWorktreeExcludes(absHome); err != nil {
		err = fmt.Errorf("writing worktree excludes: %w", err)
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

	// Write charter to untracked .captain-charter.md instead of tracked AGENTS.md.
	if strings.TrimSpace(charter) == "" {
		if parentHome == "" {
			err = fmt.Errorf("seeding captain %s: empty charter requires parent home for return-channel path", id)
			return
		}
		charter, err = DefaultCaptainCharter(id, parentHome)
		if err != nil {
			err = fmt.Errorf("creating default charter: %w", err)
			return
		}
	}
	if err = os.WriteFile(filepath.Join(absHome, CaptainCharterName), []byte(charter), 0644); err != nil {
		err = fmt.Errorf("writing %s: %w", CaptainCharterName, err)
		return
	}

	// Write the .munsu-captain-home provenance marker (same as regular seed).
	if err = SeedProvenance(absHome, id); err != nil {
		err = fmt.Errorf("seeding provenance marker: %w", err)
		return
	}

	// Ensure the parent has the typed base/config contract before registration;
	// otherwise the initial propagation cannot publish the captain snapshot.
	if parentHome != "" {
		if err = ensureParentTypedConfig(parentHome, absHome, id); err != nil {
			err = fmt.Errorf("ensuring parent typed config: %w", err)
			return
		}
		if err = Register(parentHome, id, absHome, "", ""); err != nil {
			err = fmt.Errorf("registering captain %s: %w", id, err)
			return
		}
		registered = true
		if _, pErr := PropagateConfig(PropagateConfigRequest{
			ParentHome:  parentHome,
			CaptainHome: absHome,
			Mailbox:     &noopBoundSender{},
		}); pErr != nil {
			err = fmt.Errorf("seed inherit: %w", pErr)
			return
		}
	}

	harnessName, resolveErr := resolveCaptainHarness(absHome)
	if resolveErr != nil {
		return fmt.Errorf("resolving captain integration harness: %w", resolveErr)
	}
	integrationPaths, pathsErr := integration.CaptainPaths(absHome, harnessName)
	if pathsErr != nil {
		err = fmt.Errorf("resolving captain integration paths: %w", pathsErr)
		return
	}
	if err = writeWorktreeExcludes(absHome, integrationPaths...); err != nil {
		err = fmt.Errorf("writing worktree integration excludes: %w", err)
		return
	}
	if err = ensureCaptainIntegration(absHome, harnessName, integration); err != nil {
		return fmt.Errorf("installing captain integration: %w", err)
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

// isUnmanagedCaptainHome checks whether homePath is an existing captain home
// without a git worktree.
func isUnmanagedCaptainHome(homePath string) bool {
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

// writeWorktreeExcludes writes operational dir excludes to the worktree's git
// info/exclude (via git common dir) instead of the tracked .gitignore, keeping
// the worktree git-clean without modifying source-tracked files.
// Git uses the common dir's info/exclude for per-worktree excludes,
// not the worktree-specific git dir.
func writeWorktreeExcludes(homePath string, integrationPaths ...string) error {
	commonDir, err := worktreeCommonDir(homePath)
	if err != nil {
		return fmt.Errorf("resolving worktree common dir: %w", err)
	}
	excludePath := filepath.Join(commonDir, "info", "exclude")

	// Ensure the info/ directory exists.
	if err := os.MkdirAll(filepath.Dir(excludePath), 0755); err != nil {
		return fmt.Errorf("creating info/ directory: %w", err)
	}

	content := "# Captain home operational dirs and runtime artifacts\n"
	for _, entry := range worktreeExcludeContent {
		content += entry + "\n"
	}
	if len(integrationPaths) > 0 {
		content += "# Captain harness integration (installed by munsu)\n"
		for _, path := range integrationPaths {
			content += "/" + path + "\n"
		}
	}
	return os.WriteFile(excludePath, []byte(content), 0644)
}

// worktreeCommonDir resolves the git common directory for a worktree captain
// home. For worktrees, the common dir is the parent repository's .git directory,
// accessible via the .git worktree pointer file.
func worktreeCommonDir(homePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(homePath, ".git"))
	if err != nil {
		return "", fmt.Errorf("reading .git worktree pointer: %w", err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return "", fmt.Errorf("unexpected .git format: %q", line)
	}
	gitDir := strings.TrimPrefix(line, "gitdir: ")
	if !filepath.IsAbs(gitDir) {
		return "", fmt.Errorf(".git gitdir is not absolute: %q", gitDir)
	}
	// The worktree git dir is at $GIT_COMMON_DIR/worktrees/<name>
	// So the common dir is two levels up from the worktree git dir.
	return filepath.Dir(filepath.Dir(gitDir)), nil
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

// readCaptainProvenance parses the .captain-provenance file and returns the
// source-repo path (the git repo the managed worktree was created from).
// Returns empty string if the file does not exist or source-repo is missing.
func readCaptainProvenance(homePath string) string {
	data, err := os.ReadFile(filepath.Join(homePath, CaptainProvenanceName))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "source-repo: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "source-repo: "))
		}
	}
	return ""
}

func SeedCaptain(opts CaptainSeedOptions) error {
	if strings.TrimSpace(opts.Repo) == "" {
		return fmt.Errorf("seeding captain %s: a project repo is required; captain homes are managed git worktrees", opts.ID)
	}
	return seedFromWorktree(opts.ID, opts.Home, opts.Repo, opts.ParentHome, opts.Charter, opts.Force, opts.Ref, opts.Integration)
}
