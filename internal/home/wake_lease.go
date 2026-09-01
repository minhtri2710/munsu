package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const wakeLeaseDir = "state/.wake-leases"

// LeasePath returns the directory for wake lease files.
func LeaseDir(homeDir string) string {
	return filepath.Join(homeDir, wakeLeaseDir)
}

// LeaseFilePath returns the path for a specific lease file. Invalid lease IDs
// have no path; mutating operations use validatedLeasePath so they can return
// the validation error instead of accidentally joining caller input.
func LeaseFilePath(homeDir, leaseID string) string {
	if validateLeaseID(leaseID) != nil {
		return ""
	}
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
	ExpiresAt int64 // unix seconds
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
func ClaimWakes(homeDir, consumer string, leaseSeconds, limit int) (*ClaimResult, error) {
	return claimWakesAt(homeDir, consumer, leaseSeconds, limit, time.Now)
}

func claimWakesAt(homeDir, consumer string, leaseSeconds, limit int, now func() time.Time) (result *ClaimResult, err error) {
	lock, err := acquireWakeLock(homeDir)
	if err != nil {
		return nil, err
	}
	defer joinWakeLockError(&err, lock)
	if err := recoverWakeMutationLocked(homeDir); err != nil {
		return nil, err
	}
	return claimWakesLocked(homeDir, consumer, leaseSeconds, limit, now)
}

func claimWakesLocked(homeDir, consumer string, leaseSeconds, limit int, now func() time.Time) (*ClaimResult, error) {
	if leaseSeconds < 0 {
		leaseSeconds = 0
	}
	if limit < 1 {
		limit = 10
	}

	// Reclaim expired leases first — re-enqueue their wakes. Keep the clock
	// calls at the same behavioral boundary as the pre-observation code; the
	// injected clock only makes those calls deterministic in tests.
	reclaimed, err := reclaimExpiredLeasesLocked(homeDir, now)
	if err != nil {
		return nil, err
	}

	queueRecords, err := readWakeQueue(homeDir)
	if err != nil {
		return nil, err
	}

	// Process-event wakes are reserved for their in-process consumer
	// (DrainWakesOfKind): they are never leased, and every other record keeps
	// its queue order.
	var claimed, remaining []WakeRecord
	for _, record := range queueRecords {
		if record.Kind != ProcessEventWakeKind && len(claimed) < limit {
			claimed = append(claimed, record)
			continue
		}
		remaining = append(remaining, record)
	}
	if len(claimed) == 0 {
		return &ClaimResult{Consumer: consumer, Reclaimed: reclaimed}, nil
	}

	claimNow := now()
	leaseID := fmt.Sprintf("lease-%d", claimNow.UnixNano())
	expiresAt := now().Unix() + int64(leaseSeconds)
	header := fmt.Sprintf("%s\t%s\t%d\t%d\n", leaseID, consumer, expiresAt, now().Unix())
	var lease strings.Builder
	lease.WriteString(header)
	var resultWakes []ClaimedWakeRecord
	var claimLatencies []time.Duration
	for _, record := range claimed {
		fmt.Fprintf(&lease, "%s\t%s\t%s\t%s\t%s\n", record.Epoch, record.Seq, record.Kind, record.Key, record.Payload)
		resultWakes = append(resultWakes, ClaimedWakeRecord(record))
		claimLatencies = append(claimLatencies, WakeAgeSinceEnqueue(record.Epoch, claimNow))
	}

	if err := applyWakeMutationLocked(homeDir, wakeMutation{
		queueFirst:  true,
		queueSet:    true,
		queueData:   wakeQueueData(remaining),
		leaseAction: wakeLeaseActionWrite,
		leaseID:     leaseID,
		leaseData:   []byte(lease.String()),
	}); err != nil {
		return nil, err
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
func AckWakes(homeDir, leaseID string, eventIDs []string) (err error) {
	if err := validateLeaseID(leaseID); err != nil {
		return err
	}
	lock, err := acquireWakeLock(homeDir)
	if err != nil {
		return err
	}
	defer joinWakeLockError(&err, lock)
	if err := recoverWakeMutationLocked(homeDir); err != nil {
		return err
	}
	return ackWakesLocked(homeDir, leaseID, eventIDs)
}

func ackWakesLocked(homeDir, leaseID string, eventIDs []string) error {
	leasePath, err := validatedLeasePath(homeDir, leaseID)
	if err != nil {
		return err
	}
	header, remainingLines, err := readLease(homeDir, leaseID, leasePath)
	if err != nil {
		return err
	}

	ackSet := make(map[string]bool, len(eventIDs))
	for _, id := range eventIDs {
		ackSet[id] = true
	}
	var unacked []string
	for _, line := range remainingLines {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 2 {
			continue
		}
		if !ackSet[parts[0]+":"+parts[1]] {
			unacked = append(unacked, line)
		}
	}

	if len(unacked) == 0 {
		return applyWakeMutationLocked(homeDir, wakeMutation{
			leaseAction: wakeLeaseActionRemove,
			leaseID:     leaseID,
		})
	}

	var data strings.Builder
	data.WriteString(header)
	data.WriteByte('\n')
	for _, line := range unacked {
		data.WriteString(line)
		data.WriteByte('\n')
	}
	return applyWakeMutationLocked(homeDir, wakeMutation{
		leaseAction: wakeLeaseActionWrite,
		leaseID:     leaseID,
		leaseData:   []byte(data.String()),
	})
}

// reclaimExpiredLeases finds expired lease files and re-enqueues their wakes.
// Returns the count of re-enqueued wakes.
func ReclaimExpiredLeases(homeDir string) (int, error) {
	return reclaimExpiredLeasesAt(homeDir, time.Now)
}

func reclaimExpiredLeasesAt(homeDir string, now func() time.Time) (reclaimed int, err error) {
	lock, err := acquireWakeLock(homeDir)
	if err != nil {
		return 0, err
	}
	defer joinWakeLockError(&err, lock)
	if err := recoverWakeMutationLocked(homeDir); err != nil {
		return 0, err
	}
	return reclaimExpiredLeasesLocked(homeDir, now)
}

func reclaimExpiredLeasesLocked(homeDir string, now func() time.Time) (int, error) {
	leaseDir, err := validatedLeaseDir(homeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	entries, err := os.ReadDir(leaseDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading lease directory: %w", err)
	}

	reclaimNow := now().Unix()
	reclaimed := 0
	queueRecords, queueLoaded := []WakeRecord(nil), false

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		leaseID := entry.Name()
		leasePath, err := validatedLeasePath(homeDir, leaseID)
		if err != nil {
			return reclaimed, err
		}
		header, lines, err := readLease(homeDir, leaseID, leasePath)
		if err != nil {
			if isLeaseAbsent(err) {
				continue
			}
			return reclaimed, err
		}
		parts := strings.SplitN(header, "\t", 4)
		if len(parts) < 3 {
			if err := applyWakeMutationLocked(homeDir, wakeMutation{leaseAction: wakeLeaseActionRemove, leaseID: leaseID}); err != nil {
				return reclaimed, err
			}
			continue
		}
		expiresAt, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil {
			if err := applyWakeMutationLocked(homeDir, wakeMutation{leaseAction: wakeLeaseActionRemove, leaseID: leaseID}); err != nil {
				return reclaimed, err
			}
			continue
		}
		if reclaimNow < expiresAt {
			continue
		}

		var requeued []WakeRecord
		for _, line := range lines {
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
			requeued = append(requeued, newWakeRecord(wakeParts[2], wakeParts[3], wakeParts[4], now()))
		}

		if len(requeued) == 0 {
			if err := applyWakeMutationLocked(homeDir, wakeMutation{leaseAction: wakeLeaseActionRemove, leaseID: leaseID}); err != nil {
				return reclaimed, err
			}
			continue
		}
		if !queueLoaded {
			queueRecords, err = readWakeQueue(homeDir)
			if err != nil {
				return reclaimed, err
			}
			queueLoaded = true
		}
		queueRecords = append(queueRecords, requeued...)
		if err := applyWakeMutationLocked(homeDir, wakeMutation{
			queueFirst:  true,
			queueSet:    true,
			queueData:   wakeQueueData(queueRecords),
			leaseAction: wakeLeaseActionRemove,
			leaseID:     leaseID,
		}); err != nil {
			return reclaimed, err
		}
		reclaimed += len(requeued)
	}

	return reclaimed, nil
}

func readLease(homeDir, leaseID, leasePath string) (string, []string, error) {
	data, err := os.ReadFile(leasePath)
	if os.IsNotExist(err) {
		return "", nil, fmt.Errorf("lease %q not found or expired: %w", leaseID, os.ErrNotExist)
	}
	if err != nil {
		return "", nil, fmt.Errorf("opening lease: %w", err)
	}
	if string(data) == wakeLeaseTombstone+"\n" || string(data) == wakeLeaseTombstone {
		return "", nil, fmt.Errorf("lease %q not found or expired: %w", leaseID, os.ErrNotExist)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "", nil, fmt.Errorf("lease %q is empty", leaseID)
	}
	return lines[0], lines[1:], nil
}

func isLeaseAbsent(err error) bool {
	return err != nil && (os.IsNotExist(err) || strings.Contains(err.Error(), "not found or expired"))
}

var wakeSeq int64
