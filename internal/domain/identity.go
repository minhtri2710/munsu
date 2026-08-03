package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Kind is the explicit kind of a scoped identity. A scoped identity is never
// constructed from an ambiguous bare value: the kind is always named. This is
// the fail-closed boundary between CLI shorthand resolution and core modules.
type Kind string

const (
	// KindTask is the scoped identity kind for a Task mutation target.
	KindTask Kind = "task"
	// KindCaptain is the scoped identity kind for a Captain mutation target.
	KindCaptain Kind = "captain"
	// KindOperation is the scoped identity kind for a cross-module mutation
	// operation.
	KindOperation Kind = "operation"
	// KindResource is the scoped identity kind for a resource-binding
	// mutation (endpoint, worktree, lease, ...).
	KindResource Kind = "resource"
	// KindHome is the scoped identity kind for a canonical home.
	KindHome Kind = "home"
)

// Valid reports whether the kind is a known scoped identity kind.
func (k Kind) Valid() bool {
	switch k {
	case KindTask, KindCaptain, KindOperation, KindResource, KindHome:
		return true
	}
	return false
}

// Scoped identity errors. These are the fail-closed outcomes when an identity
// is missing, ambiguous, or cannot be resolved.
var (
	// ErrAmbiguousIdentity reports a bare or otherwise ambiguous identity that
	// cannot be resolved to exactly one scoped identity.
	ErrAmbiguousIdentity = errors.New("munsu: ambiguous identity; a kind is required")

	// ErrInvalidIdentity reports a malformed or unsupported scoped identity.
	ErrInvalidIdentity = errors.New("munsu: invalid scoped identity")

	// ErrUnknownKind reports an unknown scoped identity kind.
	ErrUnknownKind = errors.New("munsu: unknown scoped identity kind")
)

// ScopedID is a typed, scoped identity with a stable canonical form. It is
// the only accepted way to name a cross-module mutation target. A ScopedID is
// constructed with an explicit Kind and a validated value; it never accepts an
// ambiguous bare string.
type ScopedID struct {
	Kind  Kind
	Value string
}

// TaskID returns a scoped identity for a Task value.
func TaskID(value string) ScopedID { return ScopedID{Kind: KindTask, Value: value} }

// CaptainID returns a scoped identity for a Captain value.
func CaptainID(value string) ScopedID { return ScopedID{Kind: KindCaptain, Value: value} }

// OperationID returns a scoped identity for an operation value.
func OperationID(value string) ScopedID { return ScopedID{Kind: KindOperation, Value: value} }

// ResourceID returns a scoped identity for a resource-binding value.
func ResourceID(value string) ScopedID { return ScopedID{Kind: KindResource, Value: value} }

// HomeID returns a scoped identity for a canonical home value.
func HomeID(value string) ScopedID { return ScopedID{Kind: KindHome, Value: value} }

// Canonical returns the stable canonical form of the identity: "kind:value".
// This is the durable spelling used for keys, logs, and cross-module
// references.
func (id ScopedID) Canonical() string { return string(id.Kind) + ":" + id.Value }

// Validate checks the identity kind and value and rejects ambiguous or
// malformed identities.
func (id ScopedID) Validate() error {
	if !id.Kind.Valid() {
		return fmt.Errorf("%w: %q", ErrUnknownKind, id.Kind)
	}
	if err := validateScopedValue(id.Value); err != nil {
		return err
	}
	return nil
}

func validateScopedValue(value string) error {
	if value == "" || value == "." || value == ".." || path.Base(value) != value || strings.ContainsAny(value, `/\\:`) || strings.IndexFunc(value, isSpace) >= 0 {
		return fmt.Errorf("%w: %q", ErrInvalidIdentity, value)
	}
	return nil
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }

// Resolve converts an explicit kind and value into a ScopedID. It refuses to
// infer the kind from a bare value: callers must always name the kind. This is
// the fail-closed boundary between CLI shorthand resolution and core modules.
func Resolve(kind Kind, value string) (ScopedID, error) {
	id := ScopedID{Kind: kind, Value: value}
	if err := id.Validate(); err != nil {
		return ScopedID{}, err
	}
	return id, nil
}

// ParseCanonical parses a canonical "kind:value" identity. A bare value
// without a kind is ambiguous and fails closed; a value with more than one
// colon is malformed.
func ParseCanonical(raw string) (ScopedID, error) {
	if raw == "" {
		return ScopedID{}, fmt.Errorf("%w: empty identity", ErrAmbiguousIdentity)
	}
	idx := strings.Index(raw, ":")
	if idx <= 0 {
		return ScopedID{}, fmt.Errorf("%w: %q (a kind:value form is required)", ErrAmbiguousIdentity, raw)
	}
	if strings.Index(raw[idx+1:], ":") >= 0 {
		return ScopedID{}, fmt.Errorf("%w: %q", ErrInvalidIdentity, raw)
	}
	id := ScopedID{Kind: Kind(raw[:idx]), Value: raw[idx+1:]}
	if err := id.Validate(); err != nil {
		return ScopedID{}, err
	}
	return id, nil
}
