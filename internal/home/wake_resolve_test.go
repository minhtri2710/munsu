package home

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWakeRequiresExactClaimEventAndSummary(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(WakeQueuePath(home), []byte("100\t1\tsignal\ttask\tpayload\n"), 0644); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimWakes(home, "consumer", 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ lease, event, summary string }{
		{claim.LeaseID, "100:2", "done"},
		{claim.LeaseID, "100:1", ""},
		{"missing", "100:1", "done"},
	} {
		if err := ResolveWake(home, tc.lease, tc.event, tc.summary); err == nil {
			t.Fatalf("ResolveWake(%q,%q,%q) succeeded", tc.lease, tc.event, tc.summary)
		}
	}
	if _, err := os.Stat(LeaseFilePath(home, claim.LeaseID)); err != nil {
		t.Fatalf("failed resolve removed lease: %v", err)
	}
}

func TestPreparedResolutionDoesNotSuppressLeaseReclaim(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(LeaseDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	leaseID := "lease-prepared"
	if err := os.WriteFile(LeaseFilePath(home, leaseID), []byte(leaseID+"\tconsumer\t1\t0\n100\t1\tsignal\ttask\tpayload\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeWakeResolution(home, wakeResolutionRecord{LeaseID: leaseID, EventID: "100:1", Summary: "pending", State: "prepared"}); err != nil {
		t.Fatal(err)
	}
	ReclaimExpiredLeases(home)
	data, err := os.ReadFile(WakeQueuePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "signal\ttask\tpayload") {
		t.Fatalf("prepared resolution suppressed retry: %q", data)
	}
}

func TestResolveWakeAcknowledgesOnceAndRecordsEvidence(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(WakeQueuePath(home), []byte("100\t1\tsignal\ttask\tpayload\n"), 0644); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimWakes(home, "consumer", 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveWake(home, claim.LeaseID, "100:1", "checked"); err != nil {
		t.Fatal(err)
	}
	if err := ResolveWake(home, claim.LeaseID, "100:1", "duplicate"); err != nil {
		t.Fatalf("repeated resolve must be idempotent: %v", err)
	}
	record, err := readWakeResolution(home, claim.LeaseID, "100:1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != "completed" || record.Summary != "checked" {
		t.Fatalf("resolution evidence = %+v", record)
	}
}
