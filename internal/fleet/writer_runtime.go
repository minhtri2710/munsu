package fleet

import (
	"fmt"
	"os"

	"github.com/minhtri2710/munsu/internal/home"
)

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

func NewRuntimeWriterFence() CompositeWriterFence {
	return CompositeWriterFence{Artifacts: DurableArtifactScanner{Kinds: []string{"watcher", "afk"}}, Processes: OSProcessInventory{}, Verifier: OSProcessVerifier{}, Marked: OSMarkerInventory{}, Oracle: OSRunOracle{}}
}
