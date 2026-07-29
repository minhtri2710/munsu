package home

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	BackupManifestName = "manifest.json"
	BackupPayloadDir   = "payload"
)

type BackupEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Size   int64  `json:"size,omitempty"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256,omitempty"`
}

type BackupManifest struct {
	SchemaVersion  int           `json:"schema_version"`
	CreatedAt      time.Time     `json:"created_at"`
	SourceIdentity string        `json:"source_identity"`
	Entries        []BackupEntry `json:"entries"`
	ManifestSHA256 string        `json:"manifest_sha256"`
}

func CreateBackup(sourceHome, backupDir string) (BackupManifest, error) {
	source, err := filepath.Abs(sourceHome)
	if err != nil {
		return BackupManifest{}, err
	}
	destination, err := filepath.Abs(backupDir)
	if err != nil {
		return BackupManifest{}, err
	}
	if destination == source || strings.HasPrefix(destination, source+string(os.PathSeparator)) {
		return BackupManifest{}, fmt.Errorf("backup destination must be outside source home")
	}
	if _, err := os.Lstat(destination); err == nil {
		return BackupManifest{}, fmt.Errorf("backup destination already exists")
	} else if !os.IsNotExist(err) {
		return BackupManifest{}, err
	}
	payload := filepath.Join(destination, BackupPayloadDir)
	if err := os.MkdirAll(payload, 0700); err != nil {
		return BackupManifest{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(destination)
		}
	}()

	manifest := BackupManifest{SchemaVersion: 1, CreatedAt: time.Now().UTC(), SourceIdentity: source}
	if err := collectBackupEntries(source, payload, &manifest); err != nil {
		return BackupManifest{}, err
	}
	manifest.ManifestSHA256, err = backupManifestDigest(manifest)
	if err != nil {
		return BackupManifest{}, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupManifest{}, err
	}
	if err := publishExclusive(filepath.Join(destination, BackupManifestName), append(data, '\n')); err != nil {
		return BackupManifest{}, err
	}
	if err := VerifyBackup(destination); err != nil {
		return BackupManifest{}, err
	}
	cleanup = false
	return manifest, nil
}

func VerifyBackup(backupDir string) error {
	manifest, err := readBackupManifest(backupDir)
	if err != nil {
		return err
	}
	digest, err := backupManifestDigest(manifest)
	if err != nil || digest != manifest.ManifestSHA256 {
		return fmt.Errorf("backup manifest digest mismatch")
	}
	actual, err := scanBackupPayload(filepath.Join(backupDir, BackupPayloadDir))
	if err != nil {
		return err
	}
	if len(actual) != len(manifest.Entries) {
		return fmt.Errorf("backup entry count mismatch: got %d want %d", len(actual), len(manifest.Entries))
	}
	for i, expected := range manifest.Entries {
		if i > 0 && manifest.Entries[i-1].Path >= expected.Path {
			return fmt.Errorf("backup manifest paths are duplicate or unsorted")
		}
		got := actual[i]
		if got.Path != expected.Path || got.Type != expected.Type || got.Size != expected.Size || got.SHA256 != expected.SHA256 {
			return fmt.Errorf("backup entry mismatch: %s", expected.Path)
		}
		if os.PathSeparator != '\\' && got.Mode != expected.Mode {
			return fmt.Errorf("backup mode mismatch: %s", expected.Path)
		}
	}
	return nil
}

func RestoreSmoke(backupDir string) error {
	if err := VerifyBackup(backupDir); err != nil {
		return err
	}
	manifest, err := readBackupManifest(backupDir)
	if err != nil {
		return err
	}
	restoreDir, err := os.MkdirTemp("", "munsu-restore-smoke-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(restoreDir)
	for _, entry := range manifest.Entries {
		target := filepath.Join(restoreDir, filepath.FromSlash(entry.Path))
		switch entry.Type {
		case "directory":
			if err := os.MkdirAll(target, fs.FileMode(entry.Mode)); err != nil {
				return err
			}
		case "file":
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			source, err := backupPayloadPath(backupDir, entry.Path)
			if err != nil {
				return err
			}
			if _, err := copyAndDigest(source, target, fs.FileMode(entry.Mode)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported backup entry type %q", entry.Type)
		}
	}
	actual, err := scanBackupPayload(restoreDir)
	if err != nil {
		return err
	}
	if len(actual) != len(manifest.Entries) {
		return fmt.Errorf("restore entry count mismatch")
	}
	for i := range actual {
		if actual[i] != manifest.Entries[i] {
			return fmt.Errorf("restore mismatch: %s", manifest.Entries[i].Path)
		}
	}
	return nil
}

func collectBackupEntries(source, payload string, manifest *BackupManifest) error {
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		target := filepath.Join(payload, rel)
		if info.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			manifest.Entries = append(manifest.Entries, BackupEntry{Path: filepath.ToSlash(rel), Type: "directory", Mode: uint32(info.Mode().Perm())})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported backup entry %s with mode %s", rel, info.Mode())
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		digest, err := copyAndDigest(path, target, info.Mode().Perm())
		if err != nil {
			return err
		}
		manifest.Entries = append(manifest.Entries, BackupEntry{Path: filepath.ToSlash(rel), Type: "file", Size: info.Size(), Mode: uint32(info.Mode().Perm()), SHA256: digest})
		return nil
	})
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	return err
}

func scanBackupPayload(root string) ([]BackupEntry, error) {
	var entries []BackupEntry
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
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
		item := BackupEntry{Path: filepath.ToSlash(rel), Mode: uint32(info.Mode().Perm())}
		if info.IsDir() {
			item.Type = "directory"
		} else if info.Mode().IsRegular() {
			item.Type = "file"
			item.Size = info.Size()
			item.SHA256, err = digestFile(path)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("unsupported backup entry %s", rel)
		}
		entries = append(entries, item)
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, err
}

func readBackupManifest(backupDir string) (BackupManifest, error) {
	data, err := os.ReadFile(filepath.Join(backupDir, BackupManifestName))
	if err != nil {
		return BackupManifest{}, err
	}
	var manifest BackupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return BackupManifest{}, err
	}
	if manifest.SchemaVersion != 1 || manifest.SourceIdentity == "" || manifest.ManifestSHA256 == "" {
		return BackupManifest{}, fmt.Errorf("invalid backup manifest")
	}
	return manifest, nil
}

func backupManifestDigest(manifest BackupManifest) (string, error) {
	manifest.ManifestSHA256 = ""
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func backupPayloadPath(backupDir, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe backup path %q", relative)
	}
	return filepath.Join(backupDir, BackupPayloadDir, clean), nil
}

func copyAndDigest(source, target string, mode fs.FileMode) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hash), in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if syncErr != nil {
		return "", syncErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
