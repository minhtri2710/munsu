package fleet

import "path/filepath"

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
type ProcessVerifier interface {
	VerifyDead(WriterArtifact) (bool, error)
}

// CompositeWriterFence carries the evidence sources behind InspectOrphans: the
// report-only scan over this home's writer identities and over processes that
// carry an ownership marker. It never terminates or disposes anything —
// quiescence enforcement before a destructive operation has one owner, Task
// Authority (ADR-0012).
type CompositeWriterFence struct {
	Artifacts ArtifactScanner
	Processes ProcessInventory
	Verifier  ProcessVerifier
	Marked    MarkerInventory
	Oracle    RunOracle
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
