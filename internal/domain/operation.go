package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrInvalidOperation reports a malformed cross-module operation (bad ID or
// digest).
var ErrInvalidOperation = errors.New("munsu: invalid operation")

// Intent is an owner-defined typed mutation intent. The owner supplies its
// canonical bytes; request digests are derived from this typed intent, never
// from a generic command/payload seam or an untyped map[string]any payload.
type Intent interface {
	// DigestBytes returns the canonical bytes that identify the intent.
	DigestBytes() ([]byte, error)
}

// Digest computes the deterministic sha256 digest of an owner-defined typed
// intent. The digest is stable across identical intents, so the same Operation
// ID with the same digest is a replay and with a different digest is a
// conflicting intent.
func Digest(intent Intent) (string, error) {
	b, err := intent.DigestBytes()
	if err != nil {
		return "", fmt.Errorf("munsu: encode intent digest: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Operation is the stable identity of one cross-module mutation intent. It
// carries a stable Operation ID and a request digest. Repeating the same ID
// with the same digest is the same intent (replay); reusing the ID with a
// different digest is a conflicting intent that must fail closed.
type Operation struct {
	ID     OperationID
	Digest string
}

// NewOperation builds a validated Operation from a typed Operation ID and an
// owner-defined typed intent. The digest is derived from the intent so there
// is no generic payload seam.
func NewOperation(id OperationID, intent Intent) (Operation, error) {
	digest, err := Digest(intent)
	if err != nil {
		return Operation{}, err
	}
	op := Operation{ID: id, Digest: digest}
	if err := op.Validate(); err != nil {
		return Operation{}, err
	}
	return op, nil
}

// Validate checks that the operation carries a validated operation ID and a
// well-formed sha256 digest.
func (op Operation) Validate() error {
	if err := op.ID.Validate(); err != nil {
		return err
	}
	if !IsSHA256(op.Digest) {
		return fmt.Errorf("%w: operation digest must be a 64-hex sha256", ErrInvalidOperation)
	}
	return nil
}

// IsSHA256 reports whether the value is a full 64-hex sha256 digest. The
// shape is enforced by length and hex validity, not length alone.
func IsSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
