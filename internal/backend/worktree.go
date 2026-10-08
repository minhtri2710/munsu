// Package worktree manages pooled (treehouse) and fallback (git worktree) worktree acquisition.
//
// When treehouse is on PATH, all operations delegate to the treehouse CLI for pooled
// worktree management. When treehouse is absent, a bare git worktree fallback is used
// with a one-time stderr note.
//
// Lease hygiene: worktree returns are permitted only after the owning
// authority proves release and the worktree is clean. The backend provider is
// a mechanism; CLI and Fleet callers own release authorization.
//
// IMPORTANT: provider Return always passes --force to treehouse to avoid an
// interactive prompt, so callers must complete dirty/unlisted-content checks
// before invoking it. Provider force is never data-loss authority.
//
// The git worktree fallback uses stable hashed paths under <homeDir>/.worktrees.
// homeDir must be non-empty when treehouse is absent; it is passed by callers
// that have already resolved the munsu home (e.g. via home.Resolve).
package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Provider is the interface for acquiring, returning, and querying worktrees.
// treehouseProvider implements it via the treehouse CLI; gitWorktreeProvider
// implements it via bare git worktree commands.
type Provider interface {
	Get(repoPath string, lease bool) (string, error)
	GetReserved(repoPath string, lease bool, reservationID string, recovery bool) (string, error)
	ReservedPath(repoPath, reservationID string) (path string, ok bool)
	Return(path string) error
	Status() (string, error)
	StatusWorktrees() ([]WorktreeEntry, error)
}

// WorktreeEntry is one pooled worktree, with the launch reservation that holds
// it when one does. LeaseHolder is the treehouse lease holder — the
// reservationID munsu passes on `get --lease --lease-holder` — and is empty for
// the git fallback (which owns no holder record and derives paths from the
// reservation instead). Path is absolute so reclaim can compare it against the
// absolute paths in task meta and task authority bindings.
type WorktreeEntry struct {
	Path        string
	LeaseHolder string
}

// ErrWorktreeReservationRecoveryUnsupported is the typed fail-closed outcome
// when a worktree provider cannot recover a worktree by its durable launch
// reservation identity: a recovery must never silently allocate a
// replacement, and the provider reports the limitation instead.
var ErrWorktreeReservationRecoveryUnsupported = errors.New("worktree: reservation recovery unsupported by provider")

// IsWorktreeReservationRecoveryUnsupported reports whether the error
// indicates a provider that cannot recover a reservation-owned worktree.
func IsWorktreeReservationRecoveryUnsupported(err error) bool {
	return errors.Is(err, ErrWorktreeReservationRecoveryUnsupported)
}

var printFallbackNote sync.Once

// selectProvider chooses treehouseProvider when treehouse is on PATH,
// otherwise falls back to gitWorktreeProvider using the given homeDir.
// homeDir must be non-empty when treehouse is absent.
func selectProvider(homeDir string) (Provider, error) {
	if _, err := exec.LookPath("treehouse"); err == nil {
		return &treehouseProvider{}, nil
	}
	if homeDir == "" {
		return nil, fmt.Errorf("worktree: homeDir is required for git worktree fallback (resolve munsu home before calling)")
	}
	printFallbackNote.Do(func() {
		fmt.Fprintf(os.Stderr, "munsu: treehouse not found, using git worktree fallback (not pooled)\n")
	})
	return &gitWorktreeProvider{homeDir: homeDir}, nil
}

// GetWorktree acquires a worktree for the given repo path within the given munsu home.
// If lease is true and treehouse is the active provider, the --lease flag is
// passed for a durable hold.
func GetWorktree(homeDir, repoPath string, lease bool) (string, error) {
	p, err := selectProvider(homeDir)
	if err != nil {
		return "", err
	}
	return p.Get(repoPath, lease)
}

// GetWorktreeReserved acquires the worktree owned by ONE durable launch
// reservation identity (the launch intent's WorktreeReservationID). Every
// canonical launch acquisition consumes the reservation from the FIRST
// attempt; recovery passes recovery=true so the provider must either return
// the SAME reservation-owned worktree or fail closed with
// ErrWorktreeReservationRecoveryUnsupported — it never allocates a
// replacement for an already-attempted reservation.
//
// git fallback: the worktree path is derived deterministically from the
// reservation (stableHash(repoPath + reservation)), so the same reservation
// always returns/recreates the SAME worktree — recovery is idempotent and
// never duplicates a lease or path.
//
// treehouse: the first acquisition passes `get --lease --lease-holder
// <reservationID>` (the reservation is recorded as the lease holder), but the
// treehouse CLI cannot re-acquire a worktree by holder (`get` always allocates
// from the pool, `return` is by path), so a recovery fails closed instead of
// allocating a replacement (DEPENDENCY_REQUEST evidence; owner-clean
// alternative is operator reconciliation of the orphan lease before re-running
// the launch). The holder is readable the other direction via `status --json`
// lease_holder, which `worktree reclaim` uses to spare a reserved-but-unbound
// treehouse worktree.
func GetWorktreeReserved(homeDir, repoPath string, lease bool, reservationID string, recovery bool) (string, error) {
	if strings.TrimSpace(reservationID) == "" {
		return "", fmt.Errorf("worktree: reservation-aware acquisition requires a reservation identity")
	}
	p, err := selectProvider(homeDir)
	if err != nil {
		return "", err
	}
	return p.GetReserved(repoPath, lease, reservationID, recovery)
}

// ReservedWorktreePath returns the path reserved for a launch reservation when
// the active provider can derive it without querying or creating anything.
func ReservedWorktreePath(homeDir, repoPath, reservationID string) (path string, ok bool, err error) {
	p, err := selectProvider(homeDir)
	if err != nil {
		return "", false, err
	}
	path, ok = p.ReservedPath(repoPath, reservationID)
	return path, ok, nil
}

// Return returns a worktree path within the given munsu home.
// When treehouse is active, --force is always passed to prevent interactive prompts.
func ReturnWorktree(homeDir, path string) error {
	p, err := selectProvider(homeDir)
	if err != nil {
		return err
	}
	return p.Return(path)
}

// Status returns worktree status within the given munsu home.
// With treehouse, this shows pool status. With the git fallback, it lists
// managed worktree directories.
func WorktreeStatus(homeDir string) (string, error) {
	p, err := selectProvider(homeDir)
	if err != nil {
		return "", err
	}
	return p.Status()
}

// StatusWorktrees returns the pooled worktrees with their lease holders, the
// structured form `worktree reclaim` needs to compare absolute paths and to
// spare a treehouse worktree still held by a live launch reservation.
func StatusWorktrees(homeDir string) ([]WorktreeEntry, error) {
	p, err := selectProvider(homeDir)
	if err != nil {
		return nil, err
	}
	return p.StatusWorktrees()
}

// --- treehouse provider ---

type treehouseProvider struct{}

func runWorktreeCommand(bin, dir string, args ...string) ([]byte, []byte, error) {
	return runBackendCommand(bin, args, dir, nil)
}

func runWorktreeMutationCommand(bin, dir string, args ...string) ([]byte, []byte, error) {
	return runBackendCommandClass(context.Background(), backendCommandWorktree, bin, args, dir, nil)
}

// ReservedPath cannot derive a treehouse worktree path from a reservation
// without I/O: treehouse allocates pool paths, they are not a pure function of
// the reservation the way the git fallback's are. The reverse mapping —
// worktree path to its lease holder — does exist, via StatusWorktrees
// (`treehouse status --json` lease_holder), which is how reclaim spares a
// reserved-but-unbound treehouse worktree.
func (p *treehouseProvider) ReservedPath(repoPath, reservationID string) (string, bool) {
	return "", false
}

// GetReserved acquires a worktree owned by one launch reservation. On the
// FIRST acquisition the reservation is passed as the treehouse lease holder
// (--lease-holder <reservationID>) so the lease is durably labeled. The
// treehouse CLI cannot re-acquire a worktree by holder — `get` always allocates
// from the pool (there is no get-by-holder) and `return` is by path — so a
// recovery fails closed with ErrWorktreeReservationRecoveryUnsupported instead
// of allocating a replacement (DEPENDENCY_REQUEST evidence). Reclaim reads the
// holder the other direction, via `status --json` lease_holder (StatusWorktrees),
// to spare a reserved-but-unbound worktree; re-adopting one on recovery is a
// separate concern not built here.
func (p *treehouseProvider) GetReserved(repoPath string, lease bool, reservationID string, recovery bool) (string, error) {
	if recovery {
		return "", fmt.Errorf("%w: treehouse CLI has no get-by-holder to re-acquire the reserved worktree (get allocates from the pool; return is by path); the launch reservation %q cannot be recovered without allocating a replacement — owner-clean recovery requires operator reconciliation of the orphan lease", ErrWorktreeReservationRecoveryUnsupported, reservationID)
	}
	bin, err := treehouseBin()
	if err != nil {
		return "", err
	}
	absRepo, absErr := filepath.Abs(repoPath)
	if absErr != nil {
		return "", fmt.Errorf("resolving repo path: %w", absErr)
	}
	args := []string{"get", absRepo}
	if lease {
		args = append(args, "--lease")
	}
	args = append(args, "--lease-holder", reservationID)
	out, stderr, err := runWorktreeMutationCommand(bin, absRepo, args...)
	if err != nil {
		return "", wrapBackendCommandError("treehouse get", out, stderr, err)
	}
	wtPath := strings.TrimSpace(string(out))
	if wtPath == "" {
		return "", fmt.Errorf("treehouse get returned empty path: use --lease for a durable worktree (non-lease is interactive-only)")
	}
	return wtPath, nil
}

func (p *treehouseProvider) Get(repoPath string, lease bool) (string, error) {
	bin, err := treehouseBin()
	if err != nil {
		return "", err
	}
	absRepo, absErr := filepath.Abs(repoPath)
	if absErr != nil {
		return "", fmt.Errorf("resolving repo path: %w", absErr)
	}
	args := []string{"get", absRepo}
	if lease {
		args = append(args, "--lease")
	}
	out, stderr, err := runWorktreeMutationCommand(bin, absRepo, args...)
	if err != nil {
		return "", wrapBackendCommandError("treehouse get", out, stderr, err)
	}
	wtPath := strings.TrimSpace(string(out))
	if wtPath == "" {
		return "", fmt.Errorf("treehouse get returned empty path: use --lease for a durable worktree (non-lease is interactive-only)")
	}
	return wtPath, nil
}

func (p *treehouseProvider) Return(path string) error {
	bin, err := treehouseBin()
	if err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("refusing to return worktree with a non-absolute path: %q", path)
	}
	markerPath := filepath.Join(path, ".git")
	marker, err := os.Lstat(markerPath)
	if err != nil {
		return fmt.Errorf("verifying worktree .git marker: %w", err)
	}
	if !marker.Mode().IsRegular() {
		return fmt.Errorf("refusing to return worktree with a non-regular .git marker")
	}
	markerBytes, err := os.ReadFile(markerPath)
	if err != nil {
		return fmt.Errorf("reading worktree .git marker: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(markerBytes)), "gitdir: ") {
		return fmt.Errorf("unexpected .git file format: %s", strings.TrimSpace(string(markerBytes)))
	}
	status := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "-z")
	status.Dir = path
	out, err := status.Output()
	if err != nil {
		return fmt.Errorf("checking worktree cleanliness before return: %w", err)
	}
	if len(out) != 0 {
		return fmt.Errorf("refusing to return worktree with dirty, untracked, or ignored content")
	}
	stdout, stderr, err := runWorktreeMutationCommand(bin, "", "return", "--force", path)
	output := commandOutput(stdout, stderr)
	// Even if exit code is 0, check for "Aborted" which means treehouse
	// prompted interactively and was aborted (e.g. stdin closed).
	// This produces a false "worktree returned to pool" without --force.
	if strings.Contains(output, "Aborted") {
		return fmt.Errorf("treehouse return: %s", output)
	}
	if err != nil {
		return wrapBackendCommandError("treehouse return", stdout, stderr, err)
	}
	return nil
}

func (p *treehouseProvider) Status() (string, error) {
	bin, err := treehouseBin()
	if err != nil {
		return "", err
	}
	out, stderr, err := runBackendCommand(bin, []string{"status"}, "", nil)
	if err != nil {
		return "", wrapBackendCommandError("treehouse status", out, stderr, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// StatusWorktrees lists the pool via `treehouse status --json`, which reports an
// absolute path and the lease_holder (the --lease-holder reservationID) for each
// worktree. The text `status` is unusable for reclaim: it abbreviates paths to
// ~ and appends "(held by <holder>)" to leased lines, so a field-split lands on
// the holder token rather than the path.
func (p *treehouseProvider) StatusWorktrees() ([]WorktreeEntry, error) {
	bin, err := treehouseBin()
	if err != nil {
		return nil, err
	}
	out, stderr, err := runBackendCommand(bin, []string{"status", "--json"}, "", nil)
	if err != nil {
		return nil, wrapBackendCommandError("treehouse status --json", out, stderr, err)
	}
	var raw []struct {
		Path        string `json:"path"`
		LeaseHolder string `json:"lease_holder"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing treehouse status --json: %w", err)
	}
	entries := make([]WorktreeEntry, 0, len(raw))
	for _, r := range raw {
		entries = append(entries, WorktreeEntry{Path: r.Path, LeaseHolder: r.LeaseHolder})
	}
	return entries, nil
}

// treehouseBin returns the path to the treehouse binary, or an error if not found.
func treehouseBin() (string, error) {
	path, err := exec.LookPath("treehouse")
	if err != nil {
		return "", fmt.Errorf("treehouse: not found on PATH — install treehouse or verify your PATH")
	}
	return path, nil
}

// --- git worktree fallback provider ---

type gitWorktreeProvider struct {
	homeDir string
}

// getWorktreeBase returns the directory under which git worktrees are created.
// Always <homeDir>/.worktrees — no env fallback.
func (p *gitWorktreeProvider) getWorktreeBase() string {
	return filepath.Join(p.homeDir, ".worktrees")
}

// ReservedPath returns the deterministic path for a launch reservation without
// creating directories or invoking git.
func (p *gitWorktreeProvider) ReservedPath(repoPath, reservationID string) (string, bool) {
	if reservationID == "" {
		return "", false
	}
	return filepath.Join(p.getWorktreeBase(), stableHash(repoPath+"\x00"+reservationID)), true
}

// GetReserved acquires the worktree owned by one launch reservation. The
// path is derived deterministically from the repository AND the reservation
// identity (stableHash(repoPath + reservation)), so the same reservation
// always returns the SAME worktree: a first acquisition creates it and a
// recovery re-adopts the identical path — never a duplicate lease or path.
// recovery is therefore idempotent and needs no provider-side distinction.
func (p *gitWorktreeProvider) GetReserved(repoPath string, lease bool, reservationID string, recovery bool) (string, error) {
	hash := stableHash(repoPath + "\x00" + reservationID)
	base := p.getWorktreeBase()
	wtDir := filepath.Join(base, hash)

	// Ensure base directory exists.
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", fmt.Errorf("creating worktree base: %w", err)
	}

	// If worktree already exists, return it (idempotent; recovery re-adopts
	// the reservation-owned path).
	if fi, err := os.Stat(wtDir); err == nil && fi.IsDir() {
		return wtDir, nil
	}

	// Create new worktree with --detach.
	out, stderr, err := runWorktreeMutationCommand("git", repoPath, "worktree", "add", "--detach", wtDir)
	if err != nil {
		return "", wrapBackendCommandError("git worktree add", out, stderr, err)
	}
	return wtDir, nil
}

func (p *gitWorktreeProvider) Get(repoPath string, lease bool) (string, error) {
	hash := stableHash(repoPath)
	base := p.getWorktreeBase()
	wtDir := filepath.Join(base, hash)

	// Ensure base directory exists.
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", fmt.Errorf("creating worktree base: %w", err)
	}

	// If worktree already exists, return it (idempotent).
	if fi, err := os.Stat(wtDir); err == nil && fi.IsDir() {
		return wtDir, nil
	}

	// Create new worktree with --detach.
	out, stderr, err := runWorktreeMutationCommand("git", repoPath, "worktree", "add", "--detach", wtDir)
	if err != nil {
		return "", wrapBackendCommandError("git worktree add", out, stderr, err)
	}
	return wtDir, nil
}

func (p *gitWorktreeProvider) Return(path string) error {
	gitFile := filepath.Join(path, ".git")
	if !filepath.IsAbs(path) {
		return fmt.Errorf("refusing to remove worktree with a non-absolute path: %q", path)
	}
	marker, err := os.Lstat(gitFile)
	if err != nil {
		return fmt.Errorf("checking worktree .git marker: %w", err)
	}
	if !marker.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove worktree with a non-regular .git marker")
	}
	// Read the .git file to find the owning repo, since git worktree remove
	// must be run from within a git repository.
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return fmt.Errorf("reading worktree .git file: %w", err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return fmt.Errorf("unexpected .git file format: %s", line)
	}
	repoGitDir := strings.TrimPrefix(line, "gitdir: ")
	// Resolve relative paths against the worktree parent.
	if !filepath.IsAbs(repoGitDir) {
		repoGitDir = filepath.Join(filepath.Dir(path), repoGitDir)
	}
	// The repo root is three levels above .git/worktrees/<name>.
	repoDir := filepath.Dir(filepath.Dir(filepath.Dir(repoGitDir)))

	status := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching", "-z")
	status.Dir = path
	out, err := status.Output()
	if err != nil {
		return fmt.Errorf("checking worktree cleanliness before return: %w", err)
	}
	if len(out) != 0 {
		return fmt.Errorf("refusing to remove worktree with dirty, untracked, or ignored content")
	}
	out, stderr, err := runWorktreeMutationCommand("git", repoDir, "worktree", "remove", "--force", path)
	if err != nil {
		return wrapBackendCommandError("git worktree remove", out, stderr, err)
	}
	return nil
}

func (p *gitWorktreeProvider) Status() (string, error) {
	base := p.getWorktreeBase()
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", base, err)
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() {
			wtDir := filepath.Join(base, e.Name())
			// Check it looks like a valid worktree (has .git file).
			if _, err := os.Stat(filepath.Join(wtDir, ".git")); err == nil {
				lines = append(lines, wtDir)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// StatusWorktrees lists the fallback worktree directories. The git fallback owns
// no lease holder record — it derives worktree paths from the reservation, so
// reclaim spares reserved-but-unbound git worktrees via ReservedPath, not the
// holder — so every entry's LeaseHolder is empty.
func (p *gitWorktreeProvider) StatusWorktrees() ([]WorktreeEntry, error) {
	base := p.getWorktreeBase()
	dirents, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", base, err)
	}
	var out []WorktreeEntry
	for _, e := range dirents {
		if e.IsDir() {
			wtDir := filepath.Join(base, e.Name())
			if _, err := os.Stat(filepath.Join(wtDir, ".git")); err == nil {
				out = append(out, WorktreeEntry{Path: wtDir})
			}
		}
	}
	return out, nil
}

// stableHash returns a deterministic short hex string from a path, used
// as the directory name for git fallback worktrees.
func stableHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

// AssertNotTangled checks if the project's primary checkout at projectDir is on a
// non-default branch. Returns nil if HEAD is detached or on the default branch.
func AssertNotTangled(projectDir, projectName string) error {
	// Check current HEAD state
	out, _, err := runWorktreeCommand("git", projectDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		// Can't determine branch state; skip tangle check
		return nil
	}
	branch := strings.TrimSpace(string(out))

	// Detached HEAD is the normal/expected state for worktree usage
	if branch == "HEAD" {
		return nil
	}

	// Get the default branch from origin/HEAD, with main/master fallback
	out, _, err = runWorktreeCommand("git", projectDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		defaultRef := strings.TrimSpace(string(out))
		defaultBranch := strings.TrimPrefix(defaultRef, "origin/")

		// On the default branch = no tangle
		if branch == defaultBranch {
			return nil
		}
	} else {
		// Fall back to common default branch names when origin/HEAD unavailable
		foundDefault := false
		for _, candidate := range []string{"main", "master"} {
			_, _, err := runWorktreeCommand("git", projectDir, "rev-parse", "--verify", candidate)
			if err == nil {
				foundDefault = true
				// On the default branch = no tangle
				if branch == candidate {
					return nil
				}
				break
			}
		}
		if !foundDefault {
			// Can't determine default branch; skip tangle check
			return nil
		}
	}
	// Tangle detected: on a non-default branch in the primary checkout
	return fmt.Errorf("cannot spawn: %s is on branch %s, not an isolated worktree. Use a detached HEAD or a worktree",
		projectName, branch)
}
