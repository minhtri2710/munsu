package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Kind is the explicit scope of a typed identity. Every typed identity is a
// distinct nominal type carrying a fixed Kind; the compiler prevents
// substituting one identity for another at an owner boundary. A Kind is never
// inferred from a bare value.
type Kind string

const (
	// KindTask is the identity kind for a Task mutation target.
	KindTask Kind = "task"
	// KindCaptain is the identity kind for a Captain mutation target.
	KindCaptain Kind = "captain"
	// KindProject is the identity kind for a Project mutation target.
	KindProject Kind = "project"
	// KindHome is the identity kind for a canonical home.
	KindHome Kind = "home"
	// KindOperation is the identity kind for a cross-module mutation operation.
	KindOperation Kind = "operation"
)

// Valid reports whether the kind is a known typed identity kind.
func (k Kind) Valid() bool {
	switch k {
	case KindTask, KindCaptain, KindProject, KindHome, KindOperation:
		return true
	}
	return false
}

// Scoped identity errors. These are the fail-closed outcomes when an identity
// is missing, ambiguous, or cannot be resolved.
var (
	// ErrInvalidIdentity reports a malformed or unsupported typed identity.
	ErrInvalidIdentity = errors.New("munsu: invalid typed identity")
)

// Scoped is the common surface of every typed scoped identity. The concrete
// identity types (TaskID, CaptainID, ProjectID, HomeID, OperationID) are distinct
// nominal types, so the compiler rejects passing one
// where another is expected. Scoped is used only for serialization and
// diagnostics; typed owner operations always take the concrete type.
type Scoped interface {
	// Kind returns the fixed identity kind.
	Kind() Kind
	// Value returns the raw identity value.
	Value() string
	// Canonical returns the stable "kind:value" canonical form.
	Canonical() string
	// Validate rejects malformed or unsafe identities.
	Validate() error
}

// scoped is the shared storage of a typed identity. It is embedded unexported
// so each concrete type is a distinct nominal type whose methods are promoted
// from scoped; external code can only construct a concrete identity through
// its validated constructor.
type scoped struct {
	kind  Kind
	value string
}

func (s scoped) Kind() Kind        { return s.kind }
func (s scoped) Value() string     { return s.value }
func (s scoped) Canonical() string { return string(s.kind) + ":" + s.value }
func (s scoped) Validate() error   { return validateScopedValue(s.value) }

// The concrete typed identity types. Each is a distinct nominal type, so
// taskID, captainID, projectID, homeID, operationID, and resourceID are never
// interchangeable at a typed boundary.
type (
	TaskID      struct{ scoped }
	CaptainID   struct{ scoped }
	ProjectID   struct{ scoped }
	HomeID      struct{ scoped }
	OperationID struct{ scoped }
)

// NewTaskID returns a validated Task identity, failing closed on an unsafe
// value.
func NewTaskID(value string) (TaskID, error) {
	if err := validateScopedValue(value); err != nil {
		return TaskID{}, err
	}
	return TaskID{scoped{kind: KindTask, value: value}}, nil
}

// NewCaptainID returns a validated Captain identity, failing closed on an
// unsafe value.
func NewCaptainID(value string) (CaptainID, error) {
	if err := validateScopedValue(value); err != nil {
		return CaptainID{}, err
	}
	return CaptainID{scoped{kind: KindCaptain, value: value}}, nil
}

// NewProjectID returns a validated Project identity, failing closed on an
// unsafe value.
func NewProjectID(value string) (ProjectID, error) {
	if err := validateScopedValue(value); err != nil {
		return ProjectID{}, err
	}
	return ProjectID{scoped{kind: KindProject, value: value}}, nil
}

// NewHomeID returns a validated Home identity, failing closed on an unsafe
// value.
func NewHomeID(value string) (HomeID, error) {
	if err := validateScopedValue(value); err != nil {
		return HomeID{}, err
	}
	return HomeID{scoped{kind: KindHome, value: value}}, nil
}

// NewOperationID returns a validated operation identity, failing closed on an
// unsafe value.
func NewOperationID(value string) (OperationID, error) {
	if err := validateScopedValue(value); err != nil {
		return OperationID{}, err
	}
	return OperationID{scoped{kind: KindOperation, value: value}}, nil
}

func validateScopedValue(value string) error {
	if value == "" || value == "." || value == ".." || path.Base(value) != value || strings.ContainsAny(value, `/\\:`) || strings.IndexFunc(value, isSpace) >= 0 {
		return fmt.Errorf("%w: %q", ErrInvalidIdentity, value)
	}
	return nil
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }
