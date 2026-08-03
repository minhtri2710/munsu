package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInvalidOperation reports a malformed cross-module operation (bad ID or
// digest).
var ErrInvalidOperation = errors.New("munsu: invalid operation")

// Operation is the stable identity of one cross-module mutation intent. It
// carries a stable Operation ID and a request digest. Repeating the same ID
// with the same digest is the same intent (replay); reusing the ID with a
// different digest is a conflicting intent that must fail closed.
type Operation struct {
	ID     ScopedID // KindOperation
	Digest string
}

// Validate checks that the operation carries an operation-kind identity and a
// well-formed sha256 digest.
func (op Operation) Validate() error {
	if op.ID.Kind != KindOperation {
		return fmt.Errorf("%w: operation id must be operation-kind", ErrInvalidOperation)
	}
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

// Digest computes the deterministic sha256 digest of an intent's canonical
// JSON form. The digest is stable across identical intents, so the same
// Operation ID with the same digest is a replay and with a different digest is
// a conflicting intent.
func Digest(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("munsu: encode intent digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Replay reports whether incoming is the same intent as existing: same
// Operation ID and same digest.
func Replay(existing, incoming Operation) bool {
	return existing.ID == incoming.ID && existing.Digest == incoming.Digest
}

// ReusedWithDifferentIntent reports whether incoming reuses an existing
// Operation ID with a different digest, which is a conflicting intent that
// must fail closed rather than be replayed against newer state.
func ReusedWithDifferentIntent(existing, incoming Operation) bool {
	return existing.ID == incoming.ID && existing.Digest != incoming.Digest
}
