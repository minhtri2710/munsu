package taskauthorityfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// recoverLocked completes every interrupted transaction under the held
// dispatch lock. Manifests are processed in deterministic filename order;
// each is applied idempotently, durably marked committed when still pending,
// and removed. Corrupt, identity-mismatched, or diverged manifests fail
// closed so no canonical read or write ever observes partial state. Fault
// hooks never fire here: recovery itself must be deterministic.
func (s *Store) recoverLocked() error {
	dir := filepath.Join(s.homeDir, filepath.FromSlash(transactionsDir))
	// The transactions directory is part of every manifest path: recovery
	// must never read manifests through a symlinked or non-directory
	// component, or an outside manifest could be applied as this home's own.
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return corruptDocument("transaction_manifest", "", "transactions path %s is a symlink or not a directory", dir)
	}
	files, err := recordFiles("transaction_manifest", dir)
	if err != nil {
		return err
	}
	for _, name := range files {
		if err := s.recoverManifest(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// recoverManifest converges one transaction manifest to the fully committed
// view and receipt, then removes it. Every after entry is applied
// idempotently (already-applied files are verified and skipped), a pending
// manifest is durably flipped to committed, and only then is the manifest
// retired. Any of these steps failing closes the home to canonical access.
func (s *Store) recoverManifest(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	manifest, err := DecodeTransactionManifest(data)
	if err != nil {
		return err
	}
	name := filepath.Base(path)
	rawID, err := fileIDDecode(strings.TrimSuffix(name, documentExt))
	if err != nil {
		return corruptDocument("transaction_manifest", "operation_id", "invalid manifest filename %q: %v", name, err)
	}
	if manifest.OperationID != rawID {
		return corruptDocument("transaction_manifest", "operation_id", "manifest operation id %q does not match filename %q", manifest.OperationID, name)
	}
	rel := filepath.ToSlash(filepath.Join(transactionsDir, name))
	before := indexBeforeEntries(manifest.Before)
	for _, entry := range manifest.After {
		if err := applyManifestEntry(s.homeDir, rel, entry, before); err != nil {
			return err
		}
	}
	if manifest.State != ManifestCommitted {
		committed := manifest
		committed.State = ManifestCommitted
		committedData, err := EncodeTransactionManifest(committed)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(s.homeDir, path, committedData); err != nil {
			return err
		}
	}
	return removeManifest(path)
}

// indexBeforeEntries indexes pinned pre-state digests by relative path.
func indexBeforeEntries(entries []ManifestEntry) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		out[entry.Path] = entry.Digest
	}
	return out
}

// applyManifestEntry applies one manifest after entry idempotently:
//   - a file already matching the after digest is already applied and skipped;
//   - a file matching its pinned before digest is replaced atomically;
//   - an absent file without a before pin is created;
//   - every other state — divergence, symlinks, corruption, or partial state
//     not explained by the manifest — fails closed.
func applyManifestEntry(homeDir, manifestPath string, entry ManifestEntry, before map[string]string) error {
	abs := filepath.Join(homeDir, filepath.FromSlash(entry.Path))
	current, exists, err := readDocument(abs)
	if err != nil {
		return err
	}
	switch {
	case exists && DigestHex(current) == entry.Digest:
		return nil // already applied
	case exists:
		beforeDigest, pinned := before[entry.Path]
		if !pinned || DigestHex(current) != beforeDigest {
			return recoveryDivergence(manifestPath, entry.Path, "file exists but matches neither the pinned before digest nor the after digest")
		}
		return writeFileAtomic(homeDir, abs, []byte(entry.Payload))
	default:
		if _, pinned := before[entry.Path]; pinned {
			return recoveryDivergence(manifestPath, entry.Path, "file is absent but the manifest pins a before digest")
		}
		return writeFileAtomic(homeDir, abs, []byte(entry.Payload))
	}
}

// recoveryDivergence builds the typed recovery-failure error for a manifest
// whose data files no longer match the pinned pre-state. The transaction
// cannot converge; reads and writes fail closed until a human repairs it.
func recoveryDivergence(manifestPath, entryPath, reason string) error {
	return &RecoveryRequiredError{
		ManifestPath: manifestPath,
		Reason:       fmt.Sprintf("entry %s: %s", entryPath, reason),
	}
}

// removeManifest retires a fully committed manifest and syncs its directory.
// A missing manifest is already retired and treated as success.
func removeManifest(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing transaction manifest %s: %w", path, err)
	}
	return syncDir(filepath.Dir(path))
}
