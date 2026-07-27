package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WriterIdentity struct {
	SchemaVersion   int    `json:"schema_version"`
	Kind            string `json:"kind"`
	PID             int    `json:"pid"`
	StartToken      string `json:"start_token"`
	ExecutablePath  string `json:"executable_path"`
	CanonicalHome   string `json:"canonical_home"`
	Endpoint        string `json:"endpoint,omitempty"`
	SessionOwner    string `json:"session_owner,omitempty"`
	BuildVersion    string `json:"build_version,omitempty"`
	ProtocolVersion int    `json:"protocol_version,omitempty"`
	StartedAt       int64  `json:"started_at,omitempty"`
	CommitSHA       string `json:"commit_sha,omitempty"`
}

func CanonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func WriterIdentityPath(homeDir, kind string) string {
	return filepath.Join(homeDir, "state", "."+kind+"-identity")
}

func PublishWriterIdentity(homeDir, kind string, identity WriterIdentity) error {
	if err := validateWriterIdentity(homeDir, kind, identity); err != nil {
		return err
	}
	path := WriterIdentityPath(homeDir, kind)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating identity directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("securing identity directory: %w", err)
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "."+kind+"-identity.tmp-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	return nil
}

func ReadWriterIdentity(homeDir, kind string) (WriterIdentity, error) {
	if err := validateWriterKind(kind); err != nil {
		return WriterIdentity{}, err
	}
	data, err := os.ReadFile(WriterIdentityPath(homeDir, kind))
	if err != nil {
		return WriterIdentity{}, err
	}
	var identity WriterIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return WriterIdentity{}, fmt.Errorf("decoding writer identity: %w", err)
	}
	if err := validateWriterIdentity(homeDir, kind, identity); err != nil {
		return WriterIdentity{}, err
	}
	canonical, err := CanonicalPath(homeDir)
	if err != nil || canonical != identity.CanonicalHome {
		return WriterIdentity{}, errors.New("writer identity home mismatch")
	}
	return identity, nil
}

func RemoveWriterIdentityIfMatches(homeDir, kind string, expected WriterIdentity) (bool, error) {
	current, err := ReadWriterIdentity(homeDir, kind)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if current.PID != expected.PID || current.StartToken != expected.StartToken || current.ExecutablePath != expected.ExecutablePath || current.CanonicalHome != expected.CanonicalHome {
		return false, nil
	}
	if err := os.Remove(WriterIdentityPath(homeDir, kind)); err != nil {
		return false, err
	}
	return true, nil
}

func validateWriterIdentity(homeDir, kind string, identity WriterIdentity) error {
	if err := validateWriterKind(kind); err != nil {
		return err
	}
	if identity.SchemaVersion != 1 {
		return fmt.Errorf("unsupported writer identity schema %d", identity.SchemaVersion)
	}
	if identity.Kind != kind {
		return errors.New("writer identity kind mismatch")
	}
	if identity.PID <= 0 || strings.TrimSpace(identity.StartToken) == "" || strings.TrimSpace(identity.ExecutablePath) == "" || strings.TrimSpace(identity.CanonicalHome) == "" {
		return errors.New("writer identity is incomplete")
	}
	return nil
}
func validateWriterKind(kind string) error {
	if kind == "" || filepath.Base(kind) != kind || strings.ContainsAny(kind, `/\\.`) {
		return fmt.Errorf("invalid writer kind %q", kind)
	}
	return nil
}
