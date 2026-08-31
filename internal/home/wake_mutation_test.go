package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverWakeMutationCompletesClaimWhenQueueAlreadyPublished(t *testing.T) {
	home := t.TempDir()
	leaseID := "lease-queue-published"
	leaseData := []byte(leaseID + "\tconsumer\t9999999999\t0\n100\t1\tsignal\ttask\tpayload\n")
	queueData := []byte("200\t2\tsignal\tother\tpayload\n")
	if err := os.MkdirAll(filepath.Dir(WakeQueuePath(home)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := canonicalAtomicWrite(WakeQueuePath(home), queueData); err != nil {
		t.Fatal(err)
	}
	if err := writeWakeMutationJournal(home, wakeMutationJournal{
		State:       "pending",
		QueueFirst:  true,
		QueueSet:    true,
		QueueData:   queueData,
		LeaseAction: wakeLeaseActionWrite,
		LeaseID:     leaseID,
		LeaseData:   leaseData,
	}); err != nil {
		t.Fatal(err)
	}
	if err := withWakeRecoveryForTest(home); err != nil {
		t.Fatal(err)
	}
	lease, err := os.ReadFile(LeaseFilePath(home, leaseID))
	if err != nil {
		t.Fatal(err)
	}
	if string(lease) != string(leaseData) {
		t.Fatalf("lease after recovery = %q", lease)
	}
	queue, err := os.ReadFile(WakeQueuePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(queue) != string(queueData) {
		t.Fatalf("queue after recovery = %q", queue)
	}
}

func TestRecoverWakeMutationCompletesClaimWhenBothTargetsArePublished(t *testing.T) {
	home := t.TempDir()
	leaseID := "lease-both-published"
	leaseData := []byte(leaseID + "\tconsumer\t9999999999\t0\n100\t1\tsignal\ttask\tpayload\n")
	queueData := []byte("200\t2\tsignal\tother\tpayload\n")
	if err := os.MkdirAll(LeaseDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	if err := canonicalAtomicWrite(WakeQueuePath(home), queueData); err != nil {
		t.Fatal(err)
	}
	if err := canonicalAtomicWrite(LeaseFilePath(home, leaseID), leaseData); err != nil {
		t.Fatal(err)
	}
	if err := writeWakeMutationJournal(home, wakeMutationJournal{
		State:       "pending",
		QueueFirst:  true,
		QueueSet:    true,
		QueueData:   queueData,
		LeaseAction: wakeLeaseActionWrite,
		LeaseID:     leaseID,
		LeaseData:   leaseData,
	}); err != nil {
		t.Fatal(err)
	}
	if err := withWakeRecoveryForTest(home); err != nil {
		t.Fatal(err)
	}
	queue, err := os.ReadFile(WakeQueuePath(home))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := os.ReadFile(LeaseFilePath(home, leaseID))
	if err != nil {
		t.Fatal(err)
	}
	if string(queue) != string(queueData) || string(lease) != string(leaseData) {
		t.Fatalf("recovered queue=%q lease=%q", queue, lease)
	}
}

func TestRecoverWakeMutationCompletesClaimTargets(t *testing.T) {
	home := t.TempDir()
	leaseID := "lease-recovery"
	leaseData := []byte(leaseID + "\tconsumer\t9999999999\t0\n100\t1\tsignal\ttask\tpayload\n")
	journal := wakeMutationJournal{
		State:       "pending",
		QueueFirst:  true,
		QueueSet:    true,
		QueueData:   []byte("200\t2\tsignal\tother\tpayload\n"),
		LeaseAction: wakeLeaseActionWrite,
		LeaseID:     leaseID,
		LeaseData:   leaseData,
	}
	if err := writeWakeMutationJournal(home, journal); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireWakeLock(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverWakeMutationLocked(home); err != nil {
		_ = releaseWakeLock(lock)
		t.Fatal(err)
	}
	if err := releaseWakeLock(lock); err != nil {
		t.Fatal(err)
	}

	queue, err := os.ReadFile(WakeQueuePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(queue) != "200\t2\tsignal\tother\tpayload\n" {
		t.Fatalf("queue after recovery = %q", queue)
	}
	lease, err := os.ReadFile(LeaseFilePath(home, leaseID))
	if err != nil {
		t.Fatal(err)
	}
	if string(lease) != string(leaseData) {
		t.Fatalf("lease after recovery = %q", lease)
	}
	completed, err := readWakeMutationJournal(home)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != "complete" {
		t.Fatalf("journal state = %q, want complete", completed.State)
	}
}

func TestRecoverWakeMutationCompletesLeaseRemovalAndQueueTargets(t *testing.T) {
	home := t.TempDir()
	leaseID := "lease-remove-recovery"
	if err := os.MkdirAll(LeaseDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	leasePath := LeaseFilePath(home, leaseID)
	if err := os.WriteFile(leasePath, []byte(leaseID+"\tconsumer\t1\t0\n100\t1\tsignal\ttask\tpayload\n"), 0600); err != nil {
		t.Fatal(err)
	}
	journal := wakeMutationJournal{
		State:       "pending",
		QueueFirst:  true,
		QueueSet:    true,
		QueueData:   []byte("100\tnew-seq\tsignal\ttask\tpayload\n"),
		LeaseAction: wakeLeaseActionRemove,
		LeaseID:     leaseID,
	}
	if err := writeWakeMutationJournal(home, journal); err != nil {
		t.Fatal(err)
	}
	if err := withWakeRecoveryForTest(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leasePath); !os.IsNotExist(err) {
		t.Fatalf("lease after recovery: %v", err)
	}
	queue, err := os.ReadFile(WakeQueuePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(queue), "signal\ttask\tpayload") {
		t.Fatalf("queue after recovery = %q", queue)
	}
}

func withWakeRecoveryForTest(home string) (err error) {
	lock, err := acquireWakeLock(home)
	if err != nil {
		return err
	}
	defer joinWakeLockError(&err, lock)
	return recoverWakeMutationLocked(home)
}

func TestValidatedLeasePathRejectsSymlinkedLeaseFile(t *testing.T) {
	home := t.TempDir()
	leaseDir := LeaseDir(home)
	if err := os.MkdirAll(leaseDir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "state", "other")
	if err := os.WriteFile(outside, []byte("outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(leaseDir, "lease-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := validatedLeasePath(home, "lease-1"); err == nil {
		t.Fatal("validatedLeasePath accepted a symlinked lease file")
	}
}

func TestValidatedLeasePathRejectsSymlinkedLeaseDirectory(t *testing.T) {
	for _, target := range []struct {
		name   string
		target func(string) (string, error)
	}{
		{
			name: "outside state",
			target: func(home string) (string, error) {
				path := filepath.Join(home, "outside")
				return path, os.Mkdir(path, 0700)
			},
		},
		{
			name: "inside state",
			target: func(home string) (string, error) {
				path := filepath.Join(home, "state", "other")
				return path, os.Mkdir(path, 0700)
			},
		},
	} {
		t.Run(target.name, func(t *testing.T) {
			home := t.TempDir()
			state := filepath.Join(home, "state")
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			linkTarget, err := target.target(home)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(linkTarget, filepath.Join(state, ".wake-leases")); err != nil {
				t.Fatal(err)
			}
			if _, err := validatedLeasePath(home, "lease-1"); err == nil {
				t.Fatal("validatedLeasePath accepted a symlinked lease directory")
			}
		})
	}
}
