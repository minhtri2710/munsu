package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minhtri2710/munsu/internal/backend"
	"github.com/minhtri2710/munsu/internal/home"
)

const (
	// CaptainCharterName is the untracked captain charter file written in a
	// managed worktree captain home. It is excluded via the worktree-scoped excludes
	// file so the worktree stays git-clean without modifying the tracked AGENTS.md.
	CaptainCharterName = ".captain-charter.md"
)

// worktreeExcludeContent lists the operational dirs and files that are
// excluded in a managed worktree captain home via its worktree-scoped
// excludes file (see writeWorktreeExcludes) so they
// never pollute the host project's index without modifying tracked .gitignore.
var worktreeExcludeContent = []string{
	"state/",
	"git-shim/",
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
// writes worktree-scoped git excludes for operational dirs, writes provenance metadata, then
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
		// Already managed — a no-op, except that a missing or malformed
		// .munsu-captain-home marker is rewritten from the surviving
		// .captain-provenance. A marker naming another home is left alone.
		if _, verr := home.ValidateCaptainProvenance(absHome); errors.Is(verr, home.ErrCaptainMarkerUnusable) {
			if err = home.SeedCaptainProvenance(absHome, id); err != nil {
				err = fmt.Errorf("rewriting provenance marker: %w", err)
			}
		}
		return err
	}

	// Refuse to replace an existing captain home that is not a managed worktree
	// (force would delete it). A home whose captain files cannot be read cannot be
	// proven replaceable, so it is refused too: no lane runs this; isManagedWorktree
	// stats the same entries first, so only a race or EIO reaches it.
	if !managed {
		if captain, capErr := home.IsCaptainHome(absHome); capErr != nil || captain {
			err = fmt.Errorf("path %s is an existing captain home that is not a managed worktree; it is unsupported and will not be replaced — remove it and reseed", absHome)
			return
		}
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
		defaultBranch, err = backend.RemoteDefaultBranch(absRepo)
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

	// Write worktree-scoped git excludes for operational dirs instead of tracked .gitignore.
	if err = writeWorktreeExcludes(absHome, captainWorktreeExcludes()); err != nil {
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
	if err = home.SeedCaptainProvenance(absHome, id); err != nil {
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
	if err = writeWorktreeExcludes(absHome, captainWorktreeExcludes(integrationPaths...)); err != nil {
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
	if _, err := os.Stat(filepath.Join(homePath, home.CaptainProvenanceName)); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// worktreeExcludeFileName is the excludes file munsu writes inside the
// captain worktree's own git dir; it is bound to that worktree alone through
// a worktree-scoped core.excludesFile.
const worktreeExcludeFileName = "munsu-exclude"

// captainWorktreeExcludes is the excludes file content of a captain worktree:
// the operational dirs and the harness integration paths munsu installs.
func captainWorktreeExcludes(integrationPaths ...string) string {
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
	return content
}

// writeWorktreeExcludes writes content to an excludes file in the worktree's
// own git dir and points the worktree-scoped core.excludesFile at it, so the
// entries never reach the project repo's other worktrees and the shared
// info/exclude is never touched. Captain homes and soldier worktrees share it.
// It enables extensions.worktreeConfig on the project repo; a common config
// that sets core.bare=true or core.worktree is refused, because worktreeConfig
// would change how git reads it.
func writeWorktreeExcludes(homePath, content string) error {
	if out, err := gitRun("-C", homePath, "config", "--local", "--get", "core.bare"); err == nil && out == "true" {
		return fmt.Errorf("project repo common config sets core.bare=true; refusing to enable extensions.worktreeConfig")
	}
	if out, err := gitRun("-C", homePath, "config", "--local", "--get", "core.worktree"); err == nil {
		return fmt.Errorf("project repo common config sets core.worktree=%s; refusing to enable extensions.worktreeConfig", out)
	}
	if out, err := gitRun("-C", homePath, "config", "--local", "extensions.worktreeConfig", "true"); err != nil {
		return fmt.Errorf("enabling extensions.worktreeConfig: %w: %s", err, out)
	}
	gitDir, err := gitRun("-C", homePath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fmt.Errorf("resolving worktree git dir: %w: %s", err, gitDir)
	}

	excludePath := filepath.Join(gitDir, worktreeExcludeFileName)
	if err := atomicWriteFile(excludePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", excludePath, err)
	}
	if out, err := gitRun("-C", homePath, "config", "--worktree", "core.excludesFile", excludePath); err != nil {
		return fmt.Errorf("setting worktree core.excludesFile: %w: %s", err, out)
	}
	return nil
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

	return os.WriteFile(filepath.Join(homePath, home.CaptainProvenanceName), []byte(content), 0644)
}

// readCaptainProvenance parses the .captain-provenance file and returns the
// source-repo path (the git repo the managed worktree was created from).
// Returns empty string if the file does not exist or source-repo is missing.
func readCaptainProvenance(homePath string) string {
	data, err := os.ReadFile(filepath.Join(homePath, home.CaptainProvenanceName))
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
