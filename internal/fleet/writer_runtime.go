package fleet

import (
	"errors"
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/home"
)

var ErrProcessInventoryUnsupported = errors.New("OS process inventory is unsupported on this platform")

type DurableArtifactScanner struct{ Kinds []string }

func (s DurableArtifactScanner) Scan(canonicalHome string) ([]WriterArtifact, error) {
	artifacts := make([]WriterArtifact, 0, len(s.Kinds))
	for _, kind := range s.Kinds {
		identity, err := home.ReadWriterIdentity(canonicalHome, kind)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s writer identity: %w", kind, err)
		}
		artifacts = append(artifacts, WriterArtifact{Path: home.WriterIdentityPath(canonicalHome, kind), Kind: identity.Kind, PID: identity.PID, StartToken: StartToken(identity.StartToken), ExecutablePath: identity.ExecutablePath, CanonicalHome: identity.CanonicalHome, Endpoint: identity.Endpoint, SessionOwner: identity.SessionOwner})
	}
	return artifacts, nil
}

type OSProcessInventory struct{}

func (OSProcessInventory) List(canonicalHome string) ([]WriterProcess, error) {
	return listWriterProcesses(canonicalHome)
}

type OSProcessVerifier struct{}

func (OSProcessVerifier) VerifyDead(artifact WriterArtifact) (bool, error) {
	current, err := inspectProcess(artifact.PID)
	if isProcessMissing(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if current.StartToken != artifact.StartToken {
		return true, nil
	}
	return false, nil
}

func NewRuntimeWriterFence() CompositeWriterFence {
	return CompositeWriterFence{Artifacts: DurableArtifactScanner{Kinds: []string{"watcher", "afk"}}, Processes: OSProcessInventory{}, Verifier: OSProcessVerifier{}, Marked: OSMarkerInventory{}, Oracle: OSRunOracle{}}
}
