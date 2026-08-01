package taskauthorityfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Durable migration document locations, relative to the canonical home.
// Everything below the home is untrusted: every component of every path is
// Lstat-verified below the trust boundary (canon) before create, write,
// rename, or removal.
const (
	migrationJournalRel  = migrationDir + "/journal.json"
	migrationReceiptRel  = migrationDir + "/receipt.json"
	migrationManifestRel = migrationDir + "/manifest.json"
)

// migrationArchiveRelPath is the deterministic archive location for one plan
// digest. It is derived from the digest alone so a retry after a crash
// discovers the same path even without the journal, and it is persisted in
// the journal before the first rename.
func migrationArchiveRelPath(digest string) string {
	return migrationDir + "/archive-" + digest
}

// migrationStageRelPath is the deterministic staging location for one plan
// digest.
func migrationStageRelPath(digest string) string {
	return migrationDir + "/stage/" + digest
}

// migrationSyncDir is the injectable directory-sync used after every
// migration rename so a crash after a move but before the sync still resumes
// deterministically. Tests record and fail the hook; production uses syncDir.
var migrationSyncDir = syncDir

// ensureRelPathSafe verifies every component of rel below canon is a real
// directory (never a symlink), creating missing components one at a time
// with DirPerm. canon is the trust boundary and may itself pass through an
// OS-resolved symlink exactly like the store's ensureDirSafe; every component
// below it stays link-checked. Returns the absolute path of rel.
func ensureRelPathSafe(canon, rel string) (string, error) {
	rel = filepath.Clean(filepath.FromSlash(rel))
	abs := canon
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("migration path %s escapes the home", rel)
		}
		abs = filepath.Join(abs, part)
		info, err := os.Lstat(abs)
		if err != nil {
			if !os.IsNotExist(err) {
				return "", fmt.Errorf("inspecting migration path %s: %w", abs, err)
			}
			// The parent already exists (canon or the previous iteration), so
			// a single-component Mkdir never traverses a missing parent.
			if err := os.Mkdir(abs, DirPerm); err != nil {
				return "", fmt.Errorf("creating migration path %s: %w", abs, err)
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("migration path component %s is a symlink or not a directory", abs)
		}
	}
	return abs, nil
}

// checkRelPathSafe verifies every existing component of rel below canon is a
// real directory (never a symlink), creating nothing. A missing component
// surfaces as its Lstat error.
func checkRelPathSafe(canon, rel string) (string, error) {
	rel = filepath.Clean(filepath.FromSlash(rel))
	abs := canon
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("migration path %s escapes the home", rel)
		}
		abs = filepath.Join(abs, part)
		info, err := os.Lstat(abs)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("migration path component %s is a symlink or not a directory", abs)
		}
	}
	return abs, nil
}

// readMigrationDoc reads one migration-owned document below canon, rejecting
// symlinks and non-regular files so journal, receipt, and manifest reads
// never follow a link. A missing document returns ok=false; a linked or
// corrupt document fails closed.
func readMigrationDoc(canon, rel string) ([]byte, bool, error) {
	rel = filepath.Clean(filepath.FromSlash(rel))
	dirRel := filepath.Dir(rel)
	dirAbs, err := checkRelPathSafe(canon, dirRel)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	abs := filepath.Join(dirAbs, filepath.Base(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("migration document %s is not a regular file", rel)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// writeMigrationDoc writes one migration-owned document below canon after
// verifying every parent component below canon is a real directory.
func writeMigrationDoc(canon, rel string, data []byte) error {
	rel = filepath.Clean(filepath.FromSlash(rel))
	dirRel := filepath.Dir(rel)
	dirAbs, err := ensureRelPathSafe(canon, dirRel)
	if err != nil {
		return err
	}
	return writeMigrationFile(filepath.Join(dirAbs, filepath.Base(rel)), data)
}

// removeMigrationRel removes one migration-owned directory below canon after
// verifying every component below canon is a real directory (never a
// symlink): RemoveAll is never reached through a linked or non-directory
// component.
func removeMigrationRel(canon, rel string) error {
	rel = filepath.Clean(filepath.FromSlash(rel))
	dirRel := filepath.Dir(rel)
	dirAbs, err := ensureRelPathSafe(canon, dirRel)
	if err != nil {
		return err
	}
	return removeCleanupPath(filepath.Join(dirAbs, filepath.Base(rel)))
}

// removeCleanupPath removes one migration-owned directory whose parent chain
// is already verified, failing closed on a symlink or non-directory at the
// final component instead of letting RemoveAll delete through a link.
func removeCleanupPath(abs string) error {
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("migration cleanup path %s is a symlink or not a directory", abs)
	}
	return os.RemoveAll(abs)
}

// pathIsRealDir reports whether abs exists and is a real directory, rejecting
// symlinks and non-directories.
func pathIsRealDir(abs string) (bool, error) {
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("path %s is a symlink or not a directory", abs)
	}
	return true, nil
}

// archiveHomeRel maps an archive-relative path back to its home-relative v1
// source path, the inverse of archiveRelFor.
func archiveHomeRel(archiveRel string) (string, bool) {
	switch {
	case strings.HasPrefix(archiveRel, "aggregates/"):
		return v1AggregatesRelPath + "/" + strings.TrimPrefix(archiveRel, "aggregates/"), true
	case strings.HasPrefix(archiveRel, "worktree-leases/"):
		return v1WorktreeLeasesRelPath + "/" + strings.TrimPrefix(archiveRel, "worktree-leases/"), true
	case strings.HasPrefix(archiveRel, "dispatch/"):
		return v1DispatchControlRelPath + "/" + strings.TrimPrefix(archiveRel, "dispatch/"), true
	}
	return "", false
}
