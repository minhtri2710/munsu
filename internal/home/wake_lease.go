package home

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	wakeLeaseDir      = "state/.wake-leases"
	defaultLeaseGrace = 30 * time.Second // grace period before expired leases are reclaimable
)

// LeasePath returns the directory for wake lease files.
func LeaseDir(homeDir string) string {
	return filepath.Join(homeDir, wakeLeaseDir)
}

// LeaseFilePath returns the path for a specific lease file.
func LeaseFilePath(homeDir, leaseID string) string {
	return filepath.Join(LeaseDir(homeDir), leaseID)
}

// ClaimedWakeRecord represents a wake record claimed under a lease.
type ClaimedWakeRecord struct {
	Epoch   string
	Seq     string
	Kind    string
	Key     string
	Payload string
}

// ClaimResult holds a set of claimed wakes and the lease that owns them.
type ClaimResult struct {
	LeaseID   string
	Consumer  string
	ExpiresAt int64 // unix captains
	Wakes     []ClaimedWakeRecord
	Reclaimed int // count of expired-lease wakes that were reclaimed

	// WakeToClaimLatencies is the typed internal wake-to-claim latency per
	// claimed wake, aligned by index with Wakes. Each value is measured as time
	// since the record's LATEST enqueue Epoch: ReclaimExpiredLeases re-enqueues
	// reclaimed wakes under a fresh Epoch before they are claimed again, so a
	// reclaimed wake reports the short time since its latest enqueue, never its
	// original emission age. It is an internal observation and is never surfaced
	// through the CLI response contract.
	WakeToClaimLatencies []time.Duration
}

// WakeAgeSinceEnqueue returns the latency between a wake record's latest
// enqueue Epoch and now. Because a newly claimed record is read after
// ReclaimExpiredLeases has re-enqueued reclaimed wakes, the Epoch on the
// record IS the latest enqueue (reclaim creates a new Epoch), so this reports
// time since the latest enqueue rather than original emission age. A malformed
// Epoch measures as zero latency.
func WakeAgeSinceEnqueue(epoch string, now time.Time) time.Duration {
	secs, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return 0
	}
	age := now.Sub(time.Unix(secs, 0))
	if age < 0 {
		return 0
	}
	return age
}

// ClaimWakes claims up to limit wake records from the queue under a lease.
// Unacked wakes that have expired leases are reclaimed (re-enqueued then claimed).
// Returns the claim result or an error.
func ClaimWakes(homeDir, consumer string, leaseCaptains, limit int) (*ClaimResult, error) {
	return claimWakesAt(homeDir, consumer, leaseCaptains, limit, time.Now)
}

func claimWakesAt(homeDir, consumer string, leaseCaptains, limit int, now func() time.Time) (*ClaimResult, error) {
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, ".wake-claim.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening wake claim lock: %w", err)
	}
	defer lock.Close()
	if err := lockWakeFile(lock); err != nil {
		return nil, fmt.Errorf("locking wake claims: %w", err)
	}
	defer unlockWakeFile(lock)

	if leaseCaptains < 0 {
		leaseCaptains = 0
	}
	if limit < 1 {
		limit = 10
	}

	// Reclaim expired leases first — re-enqueue their wakes
	claimNow := now()
	reclaimed, err := reclaimExpiredLeasesAt(homeDir, claimNow)
	if err != nil {
		return nil, err
	}

	// Read the current wake queue
	qPath := WakeQueuePath(homeDir)
	queueFile, err := os.Open(qPath)
	var queueRecords []WakeRecord
	if err == nil {
		scanner := bufio.NewScanner(queueFile)
		for scanner.Scan() {
			parts := strings.SplitN(scanner.Text(), "\t", 5)
			if len(parts) < 5 {
				continue
			}
			queueRecords = append(queueRecords, WakeRecord{
				Epoch:   parts[0],
				Seq:     parts[1],
				Kind:    parts[2],
				Key:     parts[3],
				Payload: parts[4],
			})
		}
		// Close the read handle before any rewrite or removal below. Windows
		// refuses to unlink (and misbehaves on truncate of) a file that is
		// still open, so a deferred close would leave the stale queue behind
		// on every drain and re-deliver the claimed wakes (#549).
		_ = queueFile.Close()
	}

	// Take up to limit records
	take := limit
	if take > len(queueRecords) {
		take = len(queueRecords)
	}

	claimed := queueRecords[:take]
	remaining := queueRecords[take:]
	if len(claimed) == 0 {
		return &ClaimResult{Consumer: consumer, Reclaimed: reclaimed}, nil
	}

	leaseDir := LeaseDir(homeDir)
	if err := os.MkdirAll(leaseDir, 0755); err != nil {
		return nil, fmt.Errorf("creating lease directory: %w", err)
	}
	leaseID := fmt.Sprintf("lease-%d", claimNow.UnixNano())
	expiresAt := claimNow.Unix() + int64(leaseCaptains)

	// Write lease file
	leasePath := LeaseFilePath(homeDir, leaseID)
	f, err := os.Create(leasePath)
	if err != nil {
		return nil, fmt.Errorf("creating lease file: %w", err)
	}
	defer f.Close()

	// Header: leaseID, consumer, expiresAt, claimedAt
	header := fmt.Sprintf("%s\t%s\t%d\t%d\n", leaseID, consumer, expiresAt, claimNow.Unix())
	if _, err := f.WriteString(header); err != nil {
		return nil, fmt.Errorf("writing lease header: %w", err)
	}

	var resultWakes []ClaimedWakeRecord
	var claimLatencies []time.Duration
	for _, r := range claimed {
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\n", r.Epoch, r.Seq, r.Kind, r.Key, r.Payload)
		if _, err := f.WriteString(line); err != nil {
			return nil, fmt.Errorf("writing lease wake: %w", err)
		}
		resultWakes = append(resultWakes, ClaimedWakeRecord{
			Epoch:   r.Epoch,
			Seq:     r.Seq,
			Kind:    r.Kind,
			Key:     r.Key,
			Payload: r.Payload,
		})
		claimLatencies = append(claimLatencies, WakeAgeSinceEnqueue(r.Epoch, claimNow))
	}

	// Rewrite the queue file with remaining records
	if len(remaining) > 0 {
		var b strings.Builder
		for _, r := range remaining {
			b.WriteString(fmt.Sprintf("%s	%s	%s	%s	%s\n", r.Epoch, r.Seq, r.Kind, r.Key, r.Payload))
		}
		if err := os.WriteFile(qPath, []byte(b.String()), 0644); err != nil {
			return nil, fmt.Errorf("rewriting wake queue: %w", err)
		}
	} else {
		if err := os.Remove(qPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing claimed wake queue: %w", err)
		}
	}

	return &ClaimResult{
		LeaseID:              leaseID,
		Consumer:             consumer,
		ExpiresAt:            expiresAt,
		Wakes:                resultWakes,
		Reclaimed:            reclaimed,
		WakeToClaimLatencies: claimLatencies,
	}, nil
}

// AckWakes acknowledges specific claimed wakes, removing them from the lease.
// eventIDs are the epoch+seq pairs (format: "epoch:seq") of claimed records to ack.
func AckWakes(homeDir, leaseID string, eventIDs []string) error {
	leasePath := LeaseFilePath(homeDir, leaseID)
	f, err := os.Open(leasePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("lease %q not found or expired", leaseID)
		}
		return fmt.Errorf("opening lease: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// Read header
	if !scanner.Scan() {
		return fmt.Errorf("lease %q is empty", leaseID)
	}
	header := scanner.Text()

	// Build ack set
	ackSet := make(map[string]bool)
	for _, id := range eventIDs {
		ackSet[id] = true
	}

	// Read remaining lines, skip acked ones
	var remainingLines []string
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 2 {
			continue
		}
		eventKey := parts[0] + ":" + parts[1]
		if ackSet[eventKey] {
			continue
		}
		remainingLines = append(remainingLines, line)
	}

	// If no remaining wakes, remove the lease file
	if len(remainingLines) == 0 {
		f.Close()
		os.Remove(leasePath)
		return nil
	}

	// Rewrite lease with remaining (unacked) wakes
	f.Close()
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	for _, line := range remainingLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(leasePath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("rewriting lease file: %w", err)
	}

	return nil
}

// reclaimExpiredLeases finds expired lease files and re-enqueues their wakes.
// Returns the count of re-enqueued wakes.
func ReclaimExpiredLeases(homeDir string) (int, error) {
	return reclaimExpiredLeasesAt(homeDir, time.Now())
}

func reclaimExpiredLeasesAt(homeDir string, claimNow time.Time) (int, error) {
	leaseDir := LeaseDir(homeDir)
	entries, err := os.ReadDir(leaseDir)
	if err != nil {
		return 0, nil
	}

	now := claimNow.Unix()
	reclaimed := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		leasePath := filepath.Join(leaseDir, entry.Name())

		f, err := os.Open(leasePath)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		if !scanner.Scan() {
			f.Close()
			os.Remove(leasePath)
			continue
		}
		header := scanner.Text()
		parts := strings.SplitN(header, "\t", 4)
		if len(parts) < 3 {
			f.Close()
			os.Remove(leasePath)
			continue
		}

		expiresAtStr := parts[2]
		var expiresAt int64
		if _, err := fmt.Sscanf(expiresAtStr, "%d", &expiresAt); err != nil {
			f.Close()
			os.Remove(leasePath)
			continue
		}

		if now < expiresAt {
			f.Close()
			continue // not expired yet
		}

		// Re-enqueue the wakes
		var enqueued int
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			wakeParts := strings.SplitN(line, "\t", 5)
			if len(wakeParts) < 5 {
				continue
			}
			if wakeResolutionCompleted(homeDir, wakeParts[0]+":"+wakeParts[1]) {
				continue
			}
			if err := enqueueWakeAt(homeDir, wakeParts[2], wakeParts[3], wakeParts[4], claimNow); err == nil {
				enqueued++
			}
		}
		f.Close()
		os.Remove(leasePath)
		reclaimed += enqueued
	}

	return reclaimed, nil
}
