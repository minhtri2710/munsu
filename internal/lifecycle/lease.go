package lifecycle

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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
}

// ClaimWakes claims up to limit wake records from the queue under a lease.
// Unacked wakes that have expired leases are reclaimed (re-enqueued then claimed).
// Returns the claim result or an error.
func ClaimWakes(homeDir, consumer string, leaseCaptains, limit int) (*ClaimResult, error) {
	if leaseCaptains < 0 {
		leaseCaptains = 0
	}
	if limit < 1 {
		limit = 10
	}

	leaseDir := LeaseDir(homeDir)
	if err := os.MkdirAll(leaseDir, 0755); err != nil {
		return nil, fmt.Errorf("creating lease directory: %w", err)
	}

	leaseID := fmt.Sprintf("lease-%d", time.Now().UnixNano())
	expiresAt := time.Now().Unix() + int64(leaseCaptains)

	// Reclaim expired leases first — re-enqueue their wakes
	reclaimed := reclaimExpiredLeases(homeDir)

	// Read the current wake queue
	qPath := QueuePath(homeDir)
	queueFile, err := os.Open(qPath)
	var queueRecords []WakeRecord
	if err == nil {
		defer queueFile.Close()
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
	}

	// Take up to limit records
	take := limit
	if take > len(queueRecords) {
		take = len(queueRecords)
	}

	claimed := queueRecords[:take]
	remaining := queueRecords[take:]

	// Write lease file
	leasePath := LeaseFilePath(homeDir, leaseID)
	f, err := os.Create(leasePath)
	if err != nil {
		return nil, fmt.Errorf("creating lease file: %w", err)
	}
	defer f.Close()

	// Header: leaseID, consumer, expiresAt, claimedAt
	header := fmt.Sprintf("%s\t%s\t%d\t%d\n", leaseID, consumer, expiresAt, time.Now().Unix())
	if _, err := f.WriteString(header); err != nil {
		return nil, fmt.Errorf("writing lease header: %w", err)
	}

	var resultWakes []ClaimedWakeRecord
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
		os.Remove(qPath)
	}

	return &ClaimResult{
		LeaseID:   leaseID,
		Consumer:  consumer,
		ExpiresAt: expiresAt,
		Wakes:     resultWakes,
		Reclaimed: reclaimed,
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
func reclaimExpiredLeases(homeDir string) int {
	leaseDir := LeaseDir(homeDir)
	entries, err := os.ReadDir(leaseDir)
	if err != nil {
		return 0
	}

	now := time.Now().Unix()
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
			if err := EnqueueWake(homeDir, wakeParts[2], wakeParts[3], wakeParts[4]); err == nil {
				enqueued++
			}
		}
		f.Close()
		os.Remove(leasePath)
		reclaimed += enqueued
	}

	return reclaimed
}

// ClaimExpiryGrace returns the grace period before expired leases are reclaimed.
func ClaimExpiryGrace() time.Duration { return defaultLeaseGrace }
