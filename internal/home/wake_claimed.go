package home

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Derived states of one claimed wake, see ClaimState.
const (
	ClaimHeld      = "held"
	ClaimExpired   = "expired"
	ClaimResolving = "resolving"
	ClaimStalled   = "stalled"
	ClaimResolved  = "resolved"
)

const (
	resolutionPrepared  = "prepared"
	resolutionCompleted = "completed"
)

// ClaimedLease is one lease file read without the wake lock.
type ClaimedLease struct {
	LeaseID   string
	Consumer  string
	ClaimedAt int64     // unix seconds; 0 when the header has no claim time
	ExpiresAt int64     // unix seconds
	ModTime   time.Time // lease file mtime: moves on each partial ack
	Wakes     []ClaimedLeaseWake
}

// ClaimedLeaseWake is one wake still held in a lease, with the state of its
// resolution record when one exists.
type ClaimedLeaseWake struct {
	ClaimedWakeRecord
	Resolution   string // "", "prepared" or "completed"
	ResolutionAt int64  // resolution updated_at in unix seconds; 0 when none
}

// readClaimedLeaseDir and readClaimedLeaseFile are the lister's only
// filesystem reads past directory validation; the file read returns the
// content and the mtime of the same open handle.
var (
	readClaimedLeaseDir  = os.ReadDir
	readClaimedLeaseFile = func(path string) ([]byte, time.Time, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, time.Time{}, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, time.Time{}, err
		}
		data, err := io.ReadAll(f)
		return data, info.ModTime(), err
	}
)

// ListClaimedLeases returns the leases that currently hold wakes, ordered by
// claim time then lease ID. It is a read-only view: it takes no wake lock,
// runs no recovery and creates nothing, so it never blocks a claimant and a
// home without a lease directory stays untouched.
//
// A lease directory or entry that is absent, a temp file, a non-regular entry,
// a lease with an unreadable header (a tombstone has none) or no decodable
// wake, and an unreadable resolution record are skipped. A lease directory that is
// not a contained directory, or a read error other than absence, is returned:
// callers must show it as unknown, never as none.
//
// A wake mid-claim is in neither the queue nor a lease, and a wake mid-reclaim
// is in both; callers that count unacked wakes combine this list with the
// queue by Epoch:Seq.
func ListClaimedLeases(homeDir string) ([]ClaimedLease, error) {
	leaseRoot, err := validatedLeaseDir(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing claimed leases: %w", err)
	}
	entries, err := readClaimedLeaseDir(leaseRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading lease directory: %w", err)
	}

	var leases []ClaimedLease
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.Type().IsRegular() || validateLeaseID(name) != nil {
			continue
		}
		data, modTime, err := readClaimedLeaseFile(filepath.Join(leaseRoot, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("reading lease %q: %w", name, err)
		}
		if lease, ok := parseClaimedLease(homeDir, name, string(data), modTime); ok {
			leases = append(leases, lease)
		}
	}
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].ClaimedAt != leases[j].ClaimedAt {
			return leases[i].ClaimedAt < leases[j].ClaimedAt
		}
		return leases[i].LeaseID < leases[j].LeaseID
	})
	return leases, nil
}

// parseClaimedLease decodes one lease file; ok is false for every shape the
// lister skips.
func parseClaimedLease(homeDir, leaseID, content string, modTime time.Time) (ClaimedLease, bool) {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	header, ok := parseLeaseHeader(lines[0])
	if !ok {
		return ClaimedLease{}, false
	}
	lease := ClaimedLease{LeaseID: leaseID, Consumer: header.consumer, ExpiresAt: header.expiresAt, ClaimedAt: header.claimedAt, ModTime: modTime}
	for _, line := range lines[1:] {
		record, ok := decodeWakeRecordLine(line)
		if !ok {
			continue
		}
		wake := ClaimedLeaseWake{ClaimedWakeRecord: ClaimedWakeRecord(record)}
		if res, err := readWakeResolution(homeDir, leaseID, record.Epoch+":"+record.Seq); err == nil {
			wake.Resolution, wake.ResolutionAt = res.State, res.UpdatedAt
		}
		lease.Wakes = append(lease.Wakes, wake)
	}
	if len(lease.Wakes) == 0 {
		return ClaimedLease{}, false
	}
	return lease, true
}

// ClaimState derives, from stored facts only, what a wake held in a lease is
// doing. There is no timer and no writer behind it. In precedence order:
//
//   - resolved: a completed resolution exists (the ack did not finish removing
//     the wake); the wake is not unacked.
//   - stalled: the latest of claim time (Epoch when the header has none), lease
//     mtime and resolution time is more than stallAfter old.
//   - resolving: a resolution was prepared and not completed.
//   - expired: the lease is past ExpiresAt and nobody has reclaimed it.
//   - held: otherwise.
//
// The evidence names what is missing ("no ack or resolve since"); age alone
// never says the claimant is dead or idle. stallAfter is a parameter because
// the threshold constant lives in internal/orchestrator, which imports this
// package.
func ClaimState(l ClaimedLease, w ClaimedLeaseWake, now time.Time, stallAfter time.Duration) (state, evidence string) {
	if w.Resolution == resolutionCompleted {
		return ClaimResolved, "resolution completed, wake still listed in the lease"
	}
	var claimedAt time.Time
	if l.ClaimedAt > 0 {
		claimedAt = time.Unix(l.ClaimedAt, 0)
	} else if epoch, err := strconv.ParseInt(w.Epoch, 10, 64); err == nil {
		claimedAt = time.Unix(epoch, 0)
	}
	anchor := claimedAt
	if l.ModTime.After(anchor) {
		anchor = l.ModTime
	}
	if w.ResolutionAt > 0 && time.Unix(w.ResolutionAt, 0).After(anchor) {
		anchor = time.Unix(w.ResolutionAt, 0)
	}
	if !anchor.IsZero() && now.Sub(anchor) > stallAfter {
		since := claimedAt
		if since.IsZero() {
			since = anchor
		}
		return ClaimStalled, fmt.Sprintf("claimed %s ago by %s, no ack or resolve since", now.Sub(since).Round(time.Second), l.Consumer)
	}
	if w.Resolution == resolutionPrepared {
		return ClaimResolving, "wake resolve began and did not complete"
	}
	if now.Unix() >= l.ExpiresAt {
		return ClaimExpired, fmt.Sprintf("lease %s held by %s %s past expiry", l.LeaseID, l.Consumer, now.Sub(time.Unix(l.ExpiresAt, 0)).Round(time.Second))
	}
	return ClaimHeld, ""
}
