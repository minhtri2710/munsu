package home

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ImportManifestName = "import-manifest.json"

type ImportRequest struct {
	HomeDir              string
	Generation           string
	BuildIdentity        string
	BackupDir            string
	BackupManifestSHA256 string
}

type QuarantinedEntry struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type ImportedEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"`
}

type ImportResult struct {
	Generation           string             `json:"generation"`
	BuildIdentity        string             `json:"build_identity"`
	BackupManifestSHA256 string             `json:"backup_manifest_sha256"`
	ImportedAt           time.Time          `json:"imported_at"`
	Entries              []ImportedEntry    `json:"entries"`
	Quarantined          []QuarantinedEntry `json:"quarantined,omitempty"`
	ManifestSHA256       string             `json:"manifest_sha256"`
	AlreadyImported      bool               `json:"-"`
}

func (r ImportResult) ImportedFiles() int { return len(r.Entries) }

func ImportLegacy(request ImportRequest) (ImportResult, error) {
	if _, err := ReadActivation(request.HomeDir); err == nil {
		return ImportResult{}, errors.New("legacy import is forbidden after activation")
	} else if !errors.Is(err, ErrNotActivated) {
		return ImportResult{}, fmt.Errorf("checking activation: %w", err)
	}
	if strings.TrimSpace(request.BuildIdentity) == "" {
		return ImportResult{}, errors.New("import build identity is required")
	}
	backup, err := requireVerifiedBackup(request)
	if err != nil {
		return ImportResult{}, err
	}
	root, err := GenerationRoot(request.HomeDir, request.Generation)
	if err != nil {
		return ImportResult{}, err
	}
	manifestPath := filepath.Join(root, ImportManifestName)
	if _, err := os.Stat(manifestPath); err == nil {
		existing, err := verifyImportedGeneration(root)
		if err != nil {
			return ImportResult{}, err
		}
		if existing.Generation != request.Generation || existing.BuildIdentity != request.BuildIdentity || existing.BackupManifestSHA256 != backup.ManifestSHA256 {
			return ImportResult{}, errors.New("generation already imported by a different request")
		}
		existing.AlreadyImported = true
		return existing, nil
	} else if !os.IsNotExist(err) {
		return ImportResult{}, err
	}
	if _, err := os.Lstat(root); err == nil {
		return ImportResult{}, fmt.Errorf("generation root already exists without a completed import manifest")
	} else if !os.IsNotExist(err) {
		return ImportResult{}, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return ImportResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(root)
		}
	}()

	result := ImportResult{Generation: request.Generation, BuildIdentity: request.BuildIdentity, BackupManifestSHA256: backup.ManifestSHA256, ImportedAt: time.Now().UTC()}
	for _, top := range HomeDirNames {
		if err := importLegacyTree(request.HomeDir, root, top, &result); err != nil {
			return ImportResult{}, err
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Path < result.Entries[j].Path })
	sort.Slice(result.Quarantined, func(i, j int) bool { return result.Quarantined[i].Path < result.Quarantined[j].Path })
	result.ManifestSHA256, err = importManifestDigest(result)
	if err != nil {
		return ImportResult{}, err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ImportResult{}, err
	}
	if err := publishExclusive(manifestPath, append(data, '\n')); err != nil {
		return ImportResult{}, err
	}
	if _, err := verifyImportedGeneration(root); err != nil {
		return ImportResult{}, err
	}
	cleanup = false
	return result, nil
}

func requireVerifiedBackup(request ImportRequest) (BackupManifest, error) {
	if request.BackupDir == "" || request.BackupManifestSHA256 == "" {
		return BackupManifest{}, errors.New("verified backup identity is required")
	}
	if err := VerifyBackup(request.BackupDir); err != nil {
		return BackupManifest{}, fmt.Errorf("verifying backup: %w", err)
	}
	if err := RestoreSmoke(request.BackupDir); err != nil {
		return BackupManifest{}, fmt.Errorf("backup restore smoke: %w", err)
	}
	manifest, err := readBackupManifest(request.BackupDir)
	if err != nil {
		return BackupManifest{}, err
	}
	if manifest.ManifestSHA256 != request.BackupManifestSHA256 {
		return BackupManifest{}, errors.New("backup manifest identity mismatch")
	}
	source, err := filepath.Abs(request.HomeDir)
	if err != nil {
		return BackupManifest{}, err
	}
	if source != manifest.SourceIdentity {
		return BackupManifest{}, errors.New("backup source identity mismatch")
	}
	current, err := scanLegacySource(source)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("scanning legacy source: %w", err)
	}
	expected := legacyBackupEntries(manifest.Entries)
	if len(current) != len(expected) {
		return BackupManifest{}, errors.New("legacy source changed after backup")
	}
	for i := range current {
		if current[i] != expected[i] {
			return BackupManifest{}, fmt.Errorf("legacy source changed after backup: %s", expected[i].Path)
		}
	}
	return manifest, nil
}

func scanLegacySource(homeDir string) ([]BackupEntry, error) {
	var entries []BackupEntry
	for _, top := range HomeDirNames {
		root := filepath.Join(homeDir, top)
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		scanned, err := scanBackupPayload(root)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(root)
		if err != nil {
			return nil, err
		}
		entries = append(entries, BackupEntry{Path: top, Type: "directory", Mode: uint32(info.Mode().Perm())})
		for _, entry := range scanned {
			entry.Path = filepath.ToSlash(filepath.Join(top, filepath.FromSlash(entry.Path)))
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func legacyBackupEntries(entries []BackupEntry) []BackupEntry {
	var legacy []BackupEntry
	for _, entry := range entries {
		for _, top := range HomeDirNames {
			if entry.Path == top || strings.HasPrefix(entry.Path, top+"/") {
				legacy = append(legacy, entry)
				break
			}
		}
	}
	return legacy
}

func importLegacyTree(homeDir, root, top string, result *ImportResult) error {
	sourceRoot := filepath.Join(homeDir, top)
	if _, err := os.Lstat(sourceRoot); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(homeDir, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			result.Quarantined = append(result.Quarantined, QuarantinedEntry{Path: filepath.ToSlash(rel), Reason: "unsupported non-regular legacy entry"})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		digest, err := copyAndDigest(path, target, info.Mode().Perm())
		if err != nil {
			return err
		}
		result.Entries = append(result.Entries, ImportedEntry{Path: filepath.ToSlash(rel), Size: info.Size(), Mode: uint32(info.Mode().Perm()), SHA256: digest})
		return nil
	})
}

func verifyImportedGeneration(root string) (ImportResult, error) {
	data, err := os.ReadFile(filepath.Join(root, ImportManifestName))
	if err != nil {
		return ImportResult{}, err
	}
	var result ImportResult
	if err := json.Unmarshal(data, &result); err != nil {
		return ImportResult{}, err
	}
	digest, err := importManifestDigest(result)
	if err != nil || digest != result.ManifestSHA256 {
		return ImportResult{}, errors.New("import manifest digest mismatch")
	}
	if result.Generation == "" || result.BuildIdentity == "" || result.BackupManifestSHA256 == "" {
		return ImportResult{}, errors.New("import manifest missing identity")
	}
	if !sort.SliceIsSorted(result.Entries, func(i, j int) bool { return result.Entries[i].Path < result.Entries[j].Path }) {
		return ImportResult{}, errors.New("import entries are not sorted")
	}
	seen := map[string]bool{}
	for _, entry := range result.Entries {
		if !safeGenerationEntry(entry.Path) {
			return ImportResult{}, fmt.Errorf("unsafe imported entry %s", entry.Path)
		}
		if seen[entry.Path] {
			return ImportResult{}, fmt.Errorf("duplicate imported entry %s", entry.Path)
		}
		seen[entry.Path] = true
		path := filepath.Join(root, filepath.FromSlash(entry.Path))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
			return ImportResult{}, fmt.Errorf("imported entry mismatch: %s", entry.Path)
		}
		actual, err := digestFile(path)
		if err != nil || actual != entry.SHA256 {
			return ImportResult{}, fmt.Errorf("imported checksum mismatch: %s", entry.Path)
		}
		if os.PathSeparator != '\\' && uint32(info.Mode().Perm()) != entry.Mode {
			return ImportResult{}, fmt.Errorf("imported mode mismatch: %s", entry.Path)
		}
	}
	actual, err := scanGenerationFiles(root)
	if err != nil {
		return ImportResult{}, err
	}
	expected := make(map[string]bool, len(result.Entries)+1)
	expected[ImportManifestName] = true
	for _, entry := range result.Entries {
		expected[entry.Path] = true
	}
	for _, path := range actual {
		if !expected[path] {
			return ImportResult{}, fmt.Errorf("unexpected generation entry %s", path)
		}
	}
	return result, nil
}

func safeGenerationEntry(path string) bool {
	clean := filepath.Clean(filepath.FromSlash(path))
	return path != "" && clean == filepath.FromSlash(path) && !filepath.IsAbs(clean) && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(os.PathSeparator))
}

func scanGenerationFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported generation entry %s", rel)
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func importManifestDigest(result ImportResult) (string, error) {
	result.ManifestSHA256 = ""
	result.AlreadyImported = false
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
