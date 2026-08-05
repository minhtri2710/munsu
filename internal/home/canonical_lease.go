package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// leaseRecord is the durable content of one scoped lease file.
type leaseRecord struct {
	Owner         string `json:"owner"`
	FenceToken    uint64 `json:"fence_token"`
	ExpiresAtUnix int64  `json:"expires_at_unix"`
}

// Lease is a scoped fenced lease. A lease grants exclusive ownership of a
// scope for a bounded duration; expired leases are reclaimable by another
// owner, and a stale owner's fencing token can no longer commit.
type Lease struct {
	h        *Home
	scope    string
	path     string
	owner    string
	token    FenceToken
	expires  time.Time
	released bool
}

// FenceToken returns the fencing generation held by this lease.
func (l *Lease) FenceToken() FenceToken { return l.token }

// ExpiresAt returns the current expiry of this lease.
func (l *Lease) ExpiresAt() time.Time { return l.expires }

// Renew extends the lease for another ttl, failing if the lease was released,
// expired, or fenced by a newer owner.
func (l *Lease) Renew(ttl time.Duration) error {
	if l.released {
		return ErrLeaseExpired
	}
	if ttl <= 0 {
		return ErrInvalidScope
	}
	lk, err := l.h.Lock(l.scope)
	if err != nil {
		return err
	}
	defer lk.Release()

	rec, err := readLeaseRecord(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrLeaseExpired
		}
		return err
	}
	if rec.FenceToken != uint64(l.token) {
		return ErrFenced
	}
	if rec.ExpiresAtUnix <= time.Now().Unix() {
		return ErrLeaseExpired
	}
	rec.ExpiresAtUnix = time.Now().Add(ttl).Unix()
	if err := writeLeaseRecord(l.path, rec); err != nil {
		return err
	}
	l.expires = time.Unix(rec.ExpiresAtUnix, 0)
	return nil
}

// Release releases the lease, removing its file. Releasing a lease that has
// been fenced by a newer owner fails closed.
func (l *Lease) Release() error {
	if l.released {
		return nil
	}
	l.released = true
	lk, err := l.h.Lock(l.scope)
	if err != nil {
		return err
	}
	defer lk.Release()

	rec, err := readLeaseRecord(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if rec.FenceToken != uint64(l.token) {
		return ErrFenced
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("home: remove lease: %w", err)
	}
	return nil
}

// AcquireLease acquires a scoped fenced lease for ttl. The underlying scoped
// lock applies bounded retry; if another owner holds the lease it fails closed
// immediately with ErrLeaseHeld rather than spinning until the retry budget is
// exhausted.
func (h *Home) AcquireLease(scope string, ttl time.Duration, owner string) (*Lease, error) {
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, ErrInvalidScope
	}
	if owner == "" {
		return nil, ErrInvalidScope
	}
	path := h.leasePath(scope)
	if err := privateDir(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("home: lease dir: %w", err)
	}
	return h.tryAcquireLease(path, scope, ttl, owner)
}

func (h *Home) tryAcquireLease(path, scope string, ttl time.Duration, owner string) (*Lease, error) {
	lk, err := h.Lock(scope)
	if err != nil {
		return nil, err
	}
	defer lk.Release()

	rec, err := readLeaseRecord(path)
	switch {
	case err == nil:
		if rec.ExpiresAtUnix > time.Now().Unix() {
			return nil, ErrLeaseHeld
		}
		// Expired: reclaim with an advanced fencing token.
	case os.IsNotExist(err):
		// Fresh lease.
	default:
		return nil, err
	}
	rec.Owner = owner
	// The fencing token is the persisted per-scope lock counter, so it is
	// monotonic across lease releases and reclaims.
	rec.FenceToken = uint64(lk.FenceToken())
	rec.ExpiresAtUnix = time.Now().Add(ttl).Unix()
	if err := writeLeaseRecord(path, rec); err != nil {
		return nil, err
	}
	return &Lease{
		h:       h,
		scope:   scope,
		path:    path,
		owner:   owner,
		token:   lk.FenceToken(),
		expires: time.Unix(rec.ExpiresAtUnix, 0),
	}, nil
}

func (h *Home) leasePath(scope string) string {
	return filepath.Join(h.root, LeaseDirName, scope+".lease")
}

func readLeaseRecord(path string) (leaseRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return leaseRecord{}, err
	}
	var rec leaseRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return leaseRecord{}, fmt.Errorf("home: decode lease: %w", err)
	}
	return rec, nil
}

func writeLeaseRecord(path string, rec leaseRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("home: encode lease: %w", err)
	}
	return canonicalAtomicWrite(path, append(data, '\n'))
}
