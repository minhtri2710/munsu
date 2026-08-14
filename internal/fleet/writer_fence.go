package fleet

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/minhtri2710/munsu/internal/home"
)

type StartToken string

type WriterArtifact struct {
	Path           string
	Kind           string
	PID            int
	StartToken     StartToken
	ExecutablePath string
	CanonicalHome  string
	Endpoint       string
	SessionOwner   string
}

type WriterProcess struct {
	PID            int
	StartToken     StartToken
	ExecutablePath string
	CanonicalHome  string
	Kind           string
	Endpoint       string
	SessionOwner   string
}

type ArtifactScanner interface {
	Scan(canonicalHome string) ([]WriterArtifact, error)
}
type ProcessInventory interface {
	List(canonicalHome string) ([]WriterProcess, error)
}
type EndpointDrainer interface{ Drain(WriterProcess) error }
type ExactProcessController interface {
	TerminateExact(WriterProcess) error
	RetireArtifact(WriterArtifact) error
}
type ProcessVerifier interface {
	VerifyDead(WriterArtifact) (bool, error)
}

type CompositeWriterFence struct {
	Artifacts          ArtifactScanner
	Processes          ProcessInventory
	Endpoints          EndpointDrainer
	Controller         ExactProcessController
	Verifier           ProcessVerifier
	EndpointArtifacts  EndpointScanner
	EndpointController EndpointController
	// Marked and Oracle serve InspectOrphans only: the report-only scan over
	// processes that carry an ownership marker. Neither is used by the fencing
	// paths below, and InspectOrphans never uses Controller.
	Marked MarkerInventory
	Oracle RunOracle
}

func canonicalHome(path string) (string, error) {
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

func (f CompositeWriterFence) FenceOSWriters(homeDir string) (home.WriterInventory, error) {
	if f.Artifacts == nil || f.Processes == nil || f.Endpoints == nil || f.Controller == nil || f.Verifier == nil {
		return home.WriterInventory{}, fmt.Errorf("writer process fence dependencies are required")
	}
	canonical, err := canonicalHome(homeDir)
	if err != nil {
		return home.WriterInventory{}, fmt.Errorf("canonicalizing home: %w", err)
	}
	artifacts, err := f.Artifacts.Scan(canonical)
	if err != nil {
		return home.WriterInventory{}, err
	}
	processes, err := f.Processes.List(canonical)
	if err != nil {
		return home.WriterInventory{}, err
	}
	inventory := home.WriterInventory{}
	pairs, stale, err := correlateWriters(canonical, artifacts, processes)
	if err != nil {
		return inventory, err
	}
	for _, artifact := range stale {
		dead, err := f.Verifier.VerifyDead(artifact)
		if err != nil || !dead {
			return inventory, fmt.Errorf("writer artifact %s is not verified dead: %w", artifact.Path, err)
		}
		if err := f.Controller.RetireArtifact(artifact); err != nil {
			return inventory, fmt.Errorf("retiring stale %s: %w", artifact.Path, err)
		}
		inventory.Writers = append(inventory.Writers, artifact.Path)
	}
	for _, pair := range pairs {
		p, a := pair.process, pair.artifact
		inventory.Writers = append(inventory.Writers, fmt.Sprintf("process:%s:%d", p.Kind, p.PID), a.Path)
		if p.Endpoint != "" {
			if err := f.Endpoints.Drain(p); err != nil {
				return inventory, fmt.Errorf("draining %s PID %d: %w", p.Kind, p.PID, err)
			}
		}
		if err := f.Controller.TerminateExact(p); err != nil {
			return inventory, fmt.Errorf("terminating exact %s PID %d: %w", p.Kind, p.PID, err)
		}
		dead, err := f.Verifier.VerifyDead(a)
		if err != nil || !dead {
			return inventory, fmt.Errorf("writer %s PID %d did not exit: %w", p.Kind, p.PID, err)
		}
		if err := f.Controller.RetireArtifact(a); err != nil {
			return inventory, fmt.Errorf("retiring %s: %w", a.Path, err)
		}
	}
	afterArtifacts, err := f.Artifacts.Scan(canonical)
	if err != nil {
		return inventory, err
	}
	afterProcesses, err := f.Processes.List(canonical)
	if err != nil {
		return inventory, err
	}
	if len(afterArtifacts) != 0 || len(afterProcesses) != 0 {
		return inventory, fmt.Errorf("writers remain after fencing: artifacts=%v processes=%v", afterArtifacts, afterProcesses)
	}
	sort.Strings(inventory.Writers)
	inventory.VerifiedQuiescent = true
	inventory.Evidence = append(inventory.Evidence, "typed artifact and independent OS process inventories empty after exact termination and rescan")
	return inventory, nil
}

func (f CompositeWriterFence) FenceWriters(homeDir string) (home.WriterInventory, error) {
	if f.EndpointArtifacts == nil || f.EndpointController == nil {
		return f.FenceOSWriters(homeDir)
	}
	canonical, err := canonicalHome(homeDir)
	if err != nil {
		return home.WriterInventory{}, fmt.Errorf("canonicalizing home: %w", err)
	}
	endpointEvidence, err := fenceEndpoints(canonical, f.EndpointArtifacts, f.EndpointController)
	if err != nil {
		return home.WriterInventory{}, err
	}
	result, err := f.FenceOSWriters(homeDir)
	if err != nil {
		return result, err
	}
	result.Writers = append(result.Writers, endpointEvidence...)
	sort.Strings(result.Writers)
	return result, nil
}

type writerPair struct {
	artifact WriterArtifact
	process  WriterProcess
}

func correlateWriters(canonical string, artifacts []WriterArtifact, processes []WriterProcess) ([]writerPair, []WriterArtifact, error) {
	byPID := make(map[int]WriterProcess, len(processes))
	for _, p := range processes {
		if p.PID <= 0 || p.StartToken == "" || p.ExecutablePath == "" || p.CanonicalHome != canonical || p.Kind == "" {
			return nil, nil, fmt.Errorf("unverified writer process: %+v", p)
		}
		if _, exists := byPID[p.PID]; exists {
			return nil, nil, fmt.Errorf("duplicate writer PID %d", p.PID)
		}
		byPID[p.PID] = p
	}
	pairs := make([]writerPair, 0, len(artifacts))
	stale := make([]WriterArtifact, 0)
	for _, a := range artifacts {
		if a.Path == "" || a.Kind == "" || a.PID <= 0 || a.StartToken == "" || a.ExecutablePath == "" || a.CanonicalHome != canonical {
			return nil, nil, fmt.Errorf("unverified writer artifact: %+v", a)
		}
		p, ok := byPID[a.PID]
		if !ok {
			stale = append(stale, a)
			continue
		}
		if a.StartToken != p.StartToken || a.ExecutablePath != p.ExecutablePath || a.Kind != p.Kind || a.Endpoint != p.Endpoint || a.SessionOwner != p.SessionOwner {
			return nil, nil, fmt.Errorf("writer ownership mismatch for %s", a.Path)
		}
		pairs = append(pairs, writerPair{artifact: a, process: p})
		delete(byPID, a.PID)
	}
	if len(byPID) != 0 {
		return nil, nil, fmt.Errorf("writer process has no matching artifact")
	}
	return pairs, stale, nil
}
