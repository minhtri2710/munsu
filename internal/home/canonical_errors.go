package home

import (
	"errors"
	"fmt"
)

// Canonical home typed errors. These are the fail-closed outcomes of the
// owner-clean durable-mechanics foundation. Owning modules can distinguish
// them with errors.Is/As; there is no migration, upgrade, salvage, or reset
// guidance exposed anywhere in this module.
var (
	// ErrNotInitialized reports an Open on a home that has no identity file.
	ErrNotInitialized = errors.New("home: not initialized")

	// ErrNonEmptyHome reports an Init against a directory that exists but is
	// not a recognized munsu home (no identity file). It fails closed.
	ErrNonEmptyHome = errors.New("home: directory exists but is not a munsu home")

	// ErrMalformedIdentity reports an identity file that cannot be decoded or
	// is incomplete.
	ErrMalformedIdentity = errors.New("home: identity file is malformed")

	// ErrUnsupportedSchema reports an identity with an unsupported schema
	// version.
	ErrUnsupportedSchema = errors.New("home: unsupported identity schema version")

	// ErrUnsupportedLayout reports an identity whose layout version is not the
	// current v1 layout.
	ErrUnsupportedLayout = errors.New("home: unsupported layout version")

	// ErrIdentityMismatch reports an identity whose canonical root does not
	// match the physical root it was opened at.
	ErrIdentityMismatch = errors.New("home: identity root mismatch")

	// ErrMalformedLayout reports an initialized home whose on-disk layout is
	// missing or not a directory.
	ErrMalformedLayout = errors.New("home: layout is malformed")

	// ErrEmptyRoot reports an Init or Open with an empty root path.
	ErrEmptyRoot = errors.New("home: empty root path")

	// ErrNotDirectory reports an Init or Open against a non-directory root.
	ErrNotDirectory = errors.New("home: root is not a directory")

	// ErrEmptyKey reports a blank logical key.
	ErrEmptyKey = errors.New("home: empty key")

	// ErrAbsoluteKey reports an absolute, leading-separator, or
	// volume-qualified logical key.
	ErrAbsoluteKey = errors.New("home: absolute key")

	// ErrKeyEscapes reports a logical key that uses native backslashes or escapes
	// its root via ".." or invalid slash-separated components.
	ErrKeyEscapes = errors.New("home: key escapes root")

	// ErrSymlinkEscapes reports a logical key whose parent path traverses a
	// symlink that resolves outside the root.
	ErrSymlinkEscapes = errors.New("home: symlink escapes root")

	// ErrUnknownRoot reports an unknown logical root name.
	ErrUnknownRoot = errors.New("home: unknown logical root")

	// ErrInvalidScope reports a blank or unsafe lock/lease scope.
	ErrInvalidScope = errors.New("home: invalid scope")

	// ErrLockTimeout reports a scoped lock that could not be acquired within
	// the bounded retry budget.
	ErrLockTimeout = errors.New("home: lock acquisition timed out")

	// ErrFenced reports a commit whose fencing token is stale (the holder lost
	// its fencing generation).
	ErrFenced = errors.New("home: fencing token is stale")

	// ErrConflict reports a commit whose expected revision does not match the
	// current revision.
	ErrConflict = errors.New("home: expected revision mismatch")

	// ErrLeaseHeld reports a lease that is still held by another owner.
	ErrLeaseHeld = errors.New("home: lease is held by another owner")

	// ErrLeaseExpired reports an operation on a lease that has expired.
	ErrLeaseExpired = errors.New("home: lease expired")

	// ErrEmptyChangeset reports a commit with no change items.
	ErrEmptyChangeset = errors.New("home: empty changeset")

	// ErrInvalidTxnID reports a blank or unsafe transaction identity.
	ErrInvalidTxnID = errors.New("home: invalid transaction id")
)

// incompatibleError wraps a typed error for existing state that cannot be
// adopted. It is never converted into a migration path.
type incompatibleError struct {
	kind string
	path string
	err  error
}

func (e *incompatibleError) Error() string {
	return fmt.Sprintf("home: %s at %s: %v", e.kind, e.path, e.err)
}

func (e *incompatibleError) Unwrap() error { return e.err }
