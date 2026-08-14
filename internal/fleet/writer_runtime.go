package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

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

type NoEndpointDrainer struct{}

func (NoEndpointDrainer) Drain(process WriterProcess) error {
	if process.Endpoint != "" {
		return fmt.Errorf("no endpoint drainer configured for %s", process.Kind)
	}
	return nil
}

type OSProcessController struct{ ExitTimeout time.Duration }

func (c OSProcessController) TerminateExact(process WriterProcess) error {
	current, err := inspectProcess(process.PID)
	if err != nil {
		return fmt.Errorf("revalidating PID %d: %w", process.PID, err)
	}
	if current.StartToken != process.StartToken || current.ExecutablePath != process.ExecutablePath {
		return fmt.Errorf("PID %d identity changed before termination", process.PID)
	}
	if err := terminateProcess(process.PID); err != nil {
		return err
	}
	timeout := c.ExitTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := inspectProcess(process.PID)
		if isProcessMissing(err) {
			return nil
		}
		if err != nil {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("PID %d did not exit before timeout", process.PID)
}
func (OSProcessController) RetireArtifact(artifact WriterArtifact) error {
	expectedPath := home.WriterIdentityPath(artifact.CanonicalHome, artifact.Kind)
	if filepath.Clean(artifact.Path) != filepath.Clean(expectedPath) {
		return fmt.Errorf("writer artifact path %q does not match expected %q", artifact.Path, expectedPath)
	}
	removed, err := home.RemoveWriterIdentityIfMatches(artifact.CanonicalHome, artifact.Kind, home.WriterIdentity{
		SchemaVersion: 1, Kind: artifact.Kind, PID: artifact.PID, StartToken: string(artifact.StartToken),
		ExecutablePath: artifact.ExecutablePath, CanonicalHome: artifact.CanonicalHome,
	})
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("writer artifact changed before retirement")
	}
	return nil
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
	return CompositeWriterFence{Artifacts: DurableArtifactScanner{Kinds: []string{"watcher", "afk"}}, Processes: OSProcessInventory{}, Endpoints: NoEndpointDrainer{}, Controller: OSProcessController{}, Verifier: OSProcessVerifier{}, Marked: OSMarkerInventory{}, Oracle: OSRunOracle{}}
}

func FenceBoundEndpoints(homeDir string, scanner EndpointScanner, controller EndpointController) ([]string, error) {
	canonical, err := canonicalHome(homeDir)
	if err != nil {
		return nil, err
	}
	return fenceEndpoints(canonical, scanner, controller)
}
