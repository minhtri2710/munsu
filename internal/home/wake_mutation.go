package home

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	wakeMutationJournalFile = "state/.wake-mutation.json"
	wakeLeaseTombstone      = "munsu-wake-lease-tombstone-v1"

	wakeLeaseActionNone   = ""
	wakeLeaseActionWrite  = "write"
	wakeLeaseActionRemove = "remove"
)

type wakeMutationJournal struct {
	State       string `json:"state"`
	QueueFirst  bool   `json:"queue_first,omitempty"`
	QueueSet    bool   `json:"queue_set"`
	QueueData   []byte `json:"queue_data,omitempty"`
	LeaseAction string `json:"lease_action,omitempty"`
	LeaseID     string `json:"lease_id,omitempty"`
	LeaseData   []byte `json:"lease_data,omitempty"`
}

type wakeMutation struct {
	queueFirst  bool
	queueSet    bool
	queueData   []byte
	leaseAction string
	leaseID     string
	leaseData   []byte
}

func wakeMutationJournalPath(homeDir string) string {
	return filepath.Join(homeDir, wakeMutationJournalFile)
}

func acquireWakeLock(homeDir string) (*os.File, error) {
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, ".wake-claim.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening wake claim lock: %w", err)
	}
	if err := lockWakeFile(lock); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("locking wake claims: %w", err)
	}
	return lock, nil
}

func releaseWakeLock(lock *os.File) error {
	var errs []error
	if err := unlockWakeFile(lock); err != nil {
		errs = append(errs, err)
	}
	if err := lock.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func joinWakeLockError(err *error, lock *os.File) {
	if releaseErr := releaseWakeLock(lock); releaseErr != nil {
		*err = errors.Join(*err, releaseErr)
	}
}

func validateLeaseID(leaseID string) error {
	if leaseID == "" || leaseID == "." || leaseID == ".." ||
		filepath.Base(leaseID) != leaseID || filepath.VolumeName(leaseID) != "" ||
		strings.ContainsAny(leaseID, `/\:`) || strings.ContainsRune(leaseID, 0) {
		return fmt.Errorf("invalid lease ID %q", leaseID)
	}
	return nil
}

func validatedLeaseDir(homeDir string) (string, error) {
	stateDir := filepath.Join(homeDir, "state")
	if info, err := os.Lstat(stateDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("state directory is a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("checking state directory: %w", err)
	}
	canonicalStateDir, err := filepath.EvalSymlinks(stateDir)
	if err != nil {
		return "", fmt.Errorf("resolving state directory: %w", err)
	}
	leaseRoot := filepath.Join(canonicalStateDir, ".wake-leases")
	if info, err := os.Lstat(leaseRoot); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("lease directory is a symlink")
		}
		if !info.IsDir() {
			return "", fmt.Errorf("lease directory is not a directory")
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking lease directory: %w", err)
	}
	if err := verifyNoFollow(canonicalStateDir, leaseRoot); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("lease directory is not contained: %w", err)
	}
	return leaseRoot, nil
}

func validatedLeasePath(homeDir, leaseID string) (string, error) {
	if err := validateLeaseID(leaseID); err != nil {
		return "", err
	}
	leaseRoot, err := validatedLeaseDir(homeDir)
	if err != nil {
		return "", err
	}
	path, err := joinContained(leaseRoot, leaseID)
	if err != nil {
		return "", fmt.Errorf("lease %q path escapes state: %w", leaseID, err)
	}
	containmentRoot := filepath.Dir(leaseRoot)
	if info, err := os.Stat(leaseRoot); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("lease directory is not a directory")
		}
		containmentRoot = leaseRoot
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("checking lease directory: %w", err)
	}
	if err := verifyNoFollow(containmentRoot, path); err != nil {
		return "", fmt.Errorf("lease %q path is not contained: %w", leaseID, err)
	}
	return path, nil
}

func writeWakeMutationJournal(homeDir string, journal wakeMutationJournal) error {
	data, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("encoding wake mutation journal: %w", err)
	}
	return canonicalAtomicWrite(wakeMutationJournalPath(homeDir), append(data, '\n'))
}

func readWakeMutationJournal(homeDir string) (*wakeMutationJournal, error) {
	data, err := os.ReadFile(wakeMutationJournalPath(homeDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading wake mutation journal: %w", err)
	}
	var journal wakeMutationJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("decoding wake mutation journal: %w", err)
	}
	if journal.State != "pending" && journal.State != "complete" {
		return nil, fmt.Errorf("invalid wake mutation journal state %q", journal.State)
	}
	if journal.LeaseAction != wakeLeaseActionNone && journal.LeaseAction != wakeLeaseActionWrite && journal.LeaseAction != wakeLeaseActionRemove {
		return nil, fmt.Errorf("invalid wake mutation lease action %q", journal.LeaseAction)
	}
	if journal.LeaseAction != wakeLeaseActionNone {
		if _, err := validatedLeasePath(homeDir, journal.LeaseID); err != nil {
			return nil, err
		}
	}
	if journal.State == "pending" && !journal.QueueSet && journal.LeaseAction == wakeLeaseActionNone {
		return nil, fmt.Errorf("empty pending wake mutation journal")
	}
	return &journal, nil
}

func recoverWakeMutationLocked(homeDir string) error {
	journal, err := readWakeMutationJournal(homeDir)
	if err != nil || journal == nil || journal.State == "complete" {
		return err
	}
	if err := applyWakeMutationTargets(homeDir, wakeMutation{
		queueFirst:  journal.QueueFirst,
		queueSet:    journal.QueueSet,
		queueData:   journal.QueueData,
		leaseAction: journal.LeaseAction,
		leaseID:     journal.LeaseID,
		leaseData:   journal.LeaseData,
	}); err != nil {
		return fmt.Errorf("recovering wake mutation: %w", err)
	}
	journal.State = "complete"
	if err := writeWakeMutationJournal(homeDir, *journal); err != nil {
		return fmt.Errorf("completing recovered wake mutation: %w", err)
	}
	return nil
}

func applyWakeMutationLocked(homeDir string, mutation wakeMutation) error {
	if !mutation.queueSet && mutation.leaseAction == wakeLeaseActionNone {
		return fmt.Errorf("empty wake mutation")
	}
	if err := checkWakeMutationWritable(homeDir, mutation); err != nil {
		return err
	}
	if mutation.leaseAction != wakeLeaseActionNone {
		if _, err := validatedLeasePath(homeDir, mutation.leaseID); err != nil {
			return err
		}
	}
	journal := wakeMutationJournal{
		State:       "pending",
		QueueFirst:  mutation.queueFirst,
		QueueSet:    mutation.queueSet,
		QueueData:   mutation.queueData,
		LeaseAction: mutation.leaseAction,
		LeaseID:     mutation.leaseID,
		LeaseData:   mutation.leaseData,
	}
	if err := writeWakeMutationJournal(homeDir, journal); err != nil {
		return err
	}
	if err := applyWakeMutationTargets(homeDir, mutation); err != nil {
		return err
	}
	journal.State = "complete"
	if err := writeWakeMutationJournal(homeDir, journal); err != nil {
		return fmt.Errorf("completing wake mutation: %w", err)
	}
	return nil
}

func checkWakeMutationWritable(homeDir string, mutation wakeMutation) error {
	paths := make([]string, 0, 2)
	if mutation.queueSet {
		paths = append(paths, filepath.Dir(WakeQueuePath(homeDir)))
	}
	if mutation.leaseAction != wakeLeaseActionNone {
		leasePath, err := validatedLeasePath(homeDir, mutation.leaseID)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.Dir(leasePath))
	}
	for _, dir := range paths {
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("checking wake mutation directory: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("wake mutation path %s is not a directory", dir)
		}
		if info.Mode().Perm()&0200 == 0 {
			if mutation.queueSet && dir == filepath.Dir(WakeQueuePath(homeDir)) {
				return fmt.Errorf("removing claimed wake queue: wake mutation directory %s is not writable", dir)
			}
			if mutation.leaseAction == wakeLeaseActionRemove {
				return fmt.Errorf("removing lease file: wake mutation directory %s is not writable", dir)
			}
			return fmt.Errorf("writing lease file: wake mutation directory %s is not writable", dir)
		}
	}
	return nil
}

func applyWakeMutationTargets(homeDir string, mutation wakeMutation) error {
	if mutation.queueFirst && mutation.queueSet {
		if err := applyWakeQueue(homeDir, mutation.queueData); err != nil {
			return err
		}
	}
	if err := applyWakeLeaseAction(homeDir, mutation); err != nil {
		return err
	}
	if !mutation.queueFirst && mutation.queueSet {
		if err := applyWakeQueue(homeDir, mutation.queueData); err != nil {
			return err
		}
	}
	return nil
}

func applyWakeLeaseAction(homeDir string, mutation wakeMutation) error {
	if mutation.leaseAction == wakeLeaseActionNone {
		return nil
	}
	leasePath, err := validatedLeasePath(homeDir, mutation.leaseID)
	if err != nil {
		return err
	}
	if mutation.leaseAction == wakeLeaseActionWrite {
		if err := os.MkdirAll(filepath.Dir(leasePath), 0700); err != nil {
			return fmt.Errorf("creating lease directory: %w", err)
		}
		if _, err := validatedLeasePath(homeDir, mutation.leaseID); err != nil {
			return err
		}
		if err := canonicalAtomicWrite(leasePath, mutation.leaseData); err != nil {
			return fmt.Errorf("writing lease file: %w", err)
		}
		return nil
	}
	if mutation.leaseAction != wakeLeaseActionRemove {
		return fmt.Errorf("invalid wake mutation lease action %q", mutation.leaseAction)
	}
	if err := canonicalAtomicWrite(leasePath, []byte(wakeLeaseTombstone+"\n")); err != nil {
		return fmt.Errorf("tombstoning lease file: %w", err)
	}
	if err := os.Remove(leasePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing lease file: %w", err)
	}
	return nil
}

func readWakeQueue(homeDir string) ([]WakeRecord, error) {
	file, err := os.Open(WakeQueuePath(homeDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening wake queue: %w", err)
	}
	var records []WakeRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 5)
		if len(parts) < 5 {
			continue
		}
		records = append(records, WakeRecord{Epoch: parts[0], Seq: parts[1], Kind: parts[2], Key: parts[3], Payload: parts[4]})
	}
	scanErr := scanner.Err()
	closeErr := file.Close()
	if scanErr != nil || closeErr != nil {
		var errs []error
		if scanErr != nil {
			errs = append(errs, fmt.Errorf("reading wake queue: %w", scanErr))
		}
		if closeErr != nil {
			errs = append(errs, fmt.Errorf("closing wake queue: %w", closeErr))
		}
		return nil, errors.Join(errs...)
	}
	return records, nil
}

func wakeQueueData(records []WakeRecord) []byte {
	if len(records) == 0 {
		return nil
	}
	var b strings.Builder
	for _, record := range records {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", record.Epoch, record.Seq, record.Kind, record.Key, record.Payload)
	}
	return []byte(b.String())
}

func applyWakeQueue(homeDir string, data []byte) error {
	queuePath := WakeQueuePath(homeDir)
	if len(data) > 0 {
		if err := canonicalAtomicWrite(queuePath, data); err != nil {
			return fmt.Errorf("rewriting wake queue: %w", err)
		}
		return nil
	}
	if err := canonicalAtomicWrite(queuePath, nil); err != nil {
		return fmt.Errorf("removing claimed wake queue: %w", err)
	}
	if err := os.Remove(queuePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing claimed wake queue: %w", err)
	}
	return nil
}
