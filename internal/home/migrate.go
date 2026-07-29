package home

import (
	"errors"
	"fmt"
)

type WriterInventory struct {
	VerifiedQuiescent bool     `json:"verified_quiescent"`
	Writers           []string `json:"writers,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
}

type WriterFence interface {
	FenceWriters(homeDir string) (WriterInventory, error)
}

type MigrationRequest struct {
	HomeDir       string
	BackupDir     string
	Generation    string
	BuildIdentity string
	WriterFence   WriterFence
}

type MigrationResult struct {
	Backup     BackupManifest
	Writers    WriterInventory
	Import     ImportResult
	Activation ActivationRecord
}

func MigrateAndActivate(request MigrationRequest) (MigrationResult, error) {
	if request.WriterFence == nil {
		return MigrationResult{}, errors.New("writer fence is required")
	}
	backup, err := CreateBackup(request.HomeDir, request.BackupDir)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("creating backup: %w", err)
	}
	if err := RestoreSmoke(request.BackupDir); err != nil {
		return MigrationResult{}, fmt.Errorf("restore smoke: %w", err)
	}
	writers, err := request.WriterFence.FenceWriters(request.HomeDir)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("fencing writers: %w", err)
	}
	if !writers.VerifiedQuiescent {
		return MigrationResult{}, errors.New("writer inventory is not verified quiescent")
	}
	imported, err := ImportLegacy(ImportRequest{
		HomeDir:              request.HomeDir,
		Generation:           request.Generation,
		BuildIdentity:        request.BuildIdentity,
		BackupDir:            request.BackupDir,
		BackupManifestSHA256: backup.ManifestSHA256,
	})
	if err != nil {
		return MigrationResult{}, fmt.Errorf("importing legacy state: %w", err)
	}
	root, err := GenerationRoot(request.HomeDir, request.Generation)
	if err != nil {
		return MigrationResult{}, err
	}
	if _, err := verifyImportedGeneration(root); err != nil {
		return MigrationResult{}, fmt.Errorf("verifying generation: %w", err)
	}
	activation := ActivationRecord{SchemaVersion: 1, Generation: request.Generation, BuildIdentity: request.BuildIdentity}
	if err := PublishActivation(request.HomeDir, activation); err != nil {
		return MigrationResult{}, fmt.Errorf("publishing activation: %w", err)
	}
	return MigrationResult{Backup: backup, Writers: writers, Import: imported, Activation: activation}, nil
}
