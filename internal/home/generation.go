package home

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	GenerationsDirName   = "generations"
	ActivationRecordName = "activation.json"
)

var ErrNotActivated = errors.New("munsu home is not activated")

type ActivationRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Generation    string `json:"generation"`
	BuildIdentity string `json:"build_identity"`
}

func GenerationRoot(homeDir, generation string) (string, error) {
	if generation == "" || generation == "." || generation == ".." || filepath.Base(generation) != generation || strings.ContainsAny(generation, `/\\`) {
		return "", fmt.Errorf("invalid generation %q", generation)
	}
	return filepath.Join(homeDir, GenerationsDirName, generation), nil
}

func ReadActivation(homeDir string) (ActivationRecord, error) {
	data, err := os.ReadFile(filepath.Join(homeDir, ActivationRecordName))
	if err != nil {
		if os.IsNotExist(err) {
			return ActivationRecord{}, ErrNotActivated
		}
		return ActivationRecord{}, fmt.Errorf("reading activation record: %w", err)
	}
	var record ActivationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return ActivationRecord{}, fmt.Errorf("decoding activation record: %w", err)
	}
	if err := validateActivation(record); err != nil {
		return ActivationRecord{}, err
	}
	return record, nil
}

func ResolveActiveRoot(homeDir string) (string, error) {
	record, err := ReadActivation(homeDir)
	if err != nil {
		return "", err
	}
	root, err := GenerationRoot(homeDir, record.Generation)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("stat active generation root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("active generation root is not a directory: %s", root)
	}
	return root, nil
}

func PublishActivation(homeDir string, record ActivationRecord) error {
	if err := validateActivation(record); err != nil {
		return err
	}
	root, err := GenerationRoot(homeDir, record.Generation)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat generation root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("generation root is not a directory: %s", root)
	}
	if err := os.MkdirAll(homeDir, 0700); err != nil {
		return fmt.Errorf("creating home directory: %w", err)
	}
	path := filepath.Join(homeDir, ActivationRecordName)
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encoding activation record: %w", err)
	}
	if err := publishExclusive(path, append(data, '\n')); err != nil {
		return fmt.Errorf("publishing activation record: %w", err)
	}
	return nil
}

func validateActivation(record ActivationRecord) error {
	if record.SchemaVersion <= 0 {
		return fmt.Errorf("invalid activation schema version %d", record.SchemaVersion)
	}
	if _, err := GenerationRoot(".", record.Generation); err != nil {
		return err
	}
	if strings.TrimSpace(record.BuildIdentity) == "" {
		return errors.New("activation build identity is required")
	}
	return nil
}
