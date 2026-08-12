package fleet

import (
	"crypto/rand"
	"encoding/hex"
)

// IncarnationMintFunc returns an opaque, generation-bound identity for one
// endpoint launch/binding (BEO-16/P1a). The value must be opaque to the
// backend (Fleet mints it, taskauthority persists it, backend only cross-checks
// it) and stable across retries of the SAME launch operation: a retry reuses
// the persisted value rather than minting a new one.
type IncarnationMintFunc func() (string, error)

// defaultIncarnationMint produces a 16-byte cryptographically random opaque
// identity. Production uses this; tests may inject a deterministic func.
func defaultIncarnationMint() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "inc-" + hex.EncodeToString(b), nil
}
