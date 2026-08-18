package home

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const watcherLeaseFile = "state/.watcher-lease"

// WatcherLease represents a watcher's exclusive claim on a home directory.
// Only one watcher may hold the lease for a given home at a time.
// The lease is claimed on watcher startup and released on shutdown.
type WatcherLease struct {
	Home      string `json:"home"`       // canonical home directory
	PID       int    `json:"pid"`        // watcher process PID
	StartedAt int64  `json:"started_at"` // unix timestamp (seconds) when the lease was claimed
	UpdatedAt int64  `json:"updated_at"` // unix timestamp (nanoseconds) of last update
}

// WatcherLeasePath returns the path to the watcher lease file for the given home.
func WatcherLeasePath(homeDir string) string {
	return filepath.Join(homeDir, watcherLeaseFile)
}

// ClaimWatcherLease attempts to claim the watcher lease for the given home.
// Returns true if the lease was claimed. Returns false with an error describing
// the conflicting lease if another watcher already holds the lease.
// The lease is claimed by writing the lease file atomically.
func ClaimWatcherLease(homeDir string, pid int) (bool, error) {
	path := WatcherLeasePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("creating lease directory: %w", err)
	}

	now := time.Now()

	// Check if a lease already exists.
	if existing, err := ReadWatcherLease(homeDir); err == nil && existing != nil {
		if existing.PID == pid {
			// Same PID — update the lease timestamp.
			existing.UpdatedAt = now.UnixNano()
			return writeLeaseFile(path, existing)
		}
		if isProcessAlive(existing.PID) {
			return false, fmt.Errorf("watcher lease held by pid %d", existing.PID)
		}
		// PID is dead — reclaim the lease.
	}

	lease := &WatcherLease{
		Home:      Canonical(homeDir),
		PID:       pid,
		StartedAt: now.Unix(),
		UpdatedAt: now.UnixNano(),
	}
	return writeLeaseFile(path, lease)
}

// ReleaseWatcherLeaseIfMatches releases the watcher lease for the given home,
// but only when the lease on disk is the one pid claimed. A watcher that is
// still exiting must not delete the lease its successor already claimed, so
// the release is guarded the same way the watcher identity is
// (RemoveWriterIdentityIfMatches).
// Idempotent: reports (false, nil) when no lease file exists or the lease
// belongs to another PID.
func ReleaseWatcherLeaseIfMatches(homeDir string, pid int) (bool, error) {
	lease, err := ReadWatcherLease(homeDir)
	if err != nil {
		return false, err
	}
	if lease == nil || lease.PID != pid {
		return false, nil
	}
	if err := os.Remove(WatcherLeasePath(homeDir)); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

// ReadWatcherLease reads the current watcher lease for the given home.
// Returns nil if no lease file exists.
func ReadWatcherLease(homeDir string) (*WatcherLease, error) {
	path := WatcherLeasePath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading watcher lease: %w", err)
	}
	return parseLease(data)
}

// IsWatcherLeaseHealthy checks whether the watcher lease is healthy.
// A lease is healthy when:
//   - The lease file exists
//   - The lease holder's PID is alive
//   - The watcher beat is fresh (not stale)
func IsWatcherLeaseHealthy(homeDir string) bool {
	lease, err := ReadWatcherLease(homeDir)
	if err != nil || lease == nil {
		return false
	}
	if !isProcessAlive(lease.PID) {
		return false
	}
	status := ReadWatcherBeatStatus(homeDir, time.Now())
	return status.Exists && !status.Stale
}

// writeLeaseFile writes the lease file atomically.
func writeLeaseFile(path string, lease *WatcherLease) (bool, error) {
	data, err := json.Marshal(lease)
	if err != nil {
		return false, fmt.Errorf("marshaling watcher lease: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("creating lease directory: %w", err)
	}
	if err := atomicWrite(path, data); err != nil {
		return false, fmt.Errorf("writing watcher lease: %w", err)
	}
	return true, nil
}

// parseLease parses a WatcherLease from JSON data.
func parseLease(data []byte) (*WatcherLease, error) {
	var lease WatcherLease
	if err := json.Unmarshal(data, &lease); err != nil {
		return nil, fmt.Errorf("parsing watcher lease: %w", err)
	}
	if lease.Home == "" || lease.PID <= 0 {
		return nil, fmt.Errorf("invalid watcher lease: missing home or PID")
	}
	return &lease, nil
}
