package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	PublishedSnapshotSchemaVersion = "munsu.config.snapshot/v1"
	PublishedSnapshotPath          = "config/resolved-project.json"
)

type PublishedSnapshotDocument struct {
	SchemaVersion string                `json:"schemaVersion"`
	Config        ResolvedProjectConfig `json:"config"`
}

func (d PublishedSnapshotDocument) Validate() error {
	if err := validateSchema("published config snapshot", d.SchemaVersion, PublishedSnapshotSchemaVersion); err != nil {
		return err
	}
	if d.Config.Project == "" {
		return fmt.Errorf("published config snapshot project is required")
	}
	if d.Config.ProjectPath == "" {
		return fmt.Errorf("published config snapshot project path is required")
	}
	if d.Config.Digest == "" {
		return fmt.Errorf("published config snapshot digest is required")
	}
	return nil
}

func StorePublishedSnapshot(home string, resolved ResolvedProjectConfig) error {
	document := PublishedSnapshotDocument{
		SchemaVersion: PublishedSnapshotSchemaVersion,
		Config:        cloneResolvedProjectConfig(resolved),
	}
	if err := document.Validate(); err != nil {
		return err
	}
	return storeDocument(filepath.Join(home, PublishedSnapshotPath), document)
}

func LoadPublishedSnapshot(home string) (ResolvedSnapshot, error) {
	var document PublishedSnapshotDocument
	if err := loadDocument(filepath.Join(home, PublishedSnapshotPath), &document); err != nil {
		return ResolvedSnapshot{}, err
	}
	if err := document.Validate(); err != nil {
		return ResolvedSnapshot{}, err
	}
	return ResolvedSnapshot{loadedAt: time.Now().UTC(), config: cloneResolvedProjectConfig(document.Config)}, nil
}

func PublishedSnapshotAvailable(home string) bool {
	_, err := os.Stat(filepath.Join(home, PublishedSnapshotPath))
	return err == nil || !os.IsNotExist(err)
}
