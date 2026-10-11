package orchestrator

import (
	"fmt"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeBeatFile writes a watcher liveness beat file at the given timestamp.
func writeBeatFile(t *testing.T, homeDir string, ts int64) {
	t.Helper()
	path := mhome.WatcherBeatPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := formatBeatContent(ts) + " 12345"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// writeWakeQueue writes tab-separated wake queue entries.
func writeWakeQueue(t *testing.T, homeDir string, lines []string) {
	t.Helper()
	path := mhome.WakeQueuePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// formatBeatContent formats a unix timestamp as the decimal + pid string
// expected by ReadBeat.
func formatBeatContent(ts int64) string {
	// Use the same format as WriteBeat: "<unix_ts> <pid>"
	return fmt.Sprintf("%d %d", ts, 12345)
}

// TestEvaluateGuard_QueuedWakesPendingHasCode verifies queued wakes condition has a stable code.
func TestEvaluateGuard_QueuedWakesPendingHasCode(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())
	writeWakeQueue(t, home, []string{"1780000000\t1\tsignal\tkey\tpayload"})

	result := EvaluateGuard(home, 0, time.Now())

	if len(result.Conditions) == 0 {
		t.Fatal("expected conditions, got none")
	}

	found := false
	for _, c := range result.Conditions {
		if c.Code == ConditionQueuedWakesPending {
			found = true
			if !strings.Contains(c.Message, "QUEUED WAKES PENDING") {
				t.Errorf("message should mention QUEUED WAKES PENDING, got: %s", c.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected condition code %q, got codes: %v", ConditionQueuedWakesPending, result.Conditions)
	}
}

// TestEvaluateGuard_UnusableWakeModeSaysWatcherCannotDeliver verifies the guard
// does not promise watcher delivery while wake-delivery-mode cannot be read.
func TestEvaluateGuard_UnusableWakeModeSaysWatcherCannotDeliver(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())
	writeWakeQueue(t, home, []string{"1780000000\t1\tsignal\tkey\tpayload"})
	if err := os.MkdirAll(filepath.Join(home, "config", "wake-delivery-mode"), 0755); err != nil {
		t.Fatal(err)
	}

	result := EvaluateGuard(home, 0, time.Now())

	for _, c := range result.Conditions {
		if c.Code != ConditionQueuedWakesPending {
			continue
		}
		for _, want := range []string{"the watcher cannot deliver them", "config/wake-delivery-mode", "munsu config set wake-delivery-mode", "munsu wake claim"} {
			if !strings.Contains(c.Message, want) {
				t.Errorf("guard message lacks %q: %s", want, c.Message)
			}
		}
		if strings.Contains(c.Message, "the watcher delivers each as a prompt") {
			t.Errorf("guard message still promises watcher delivery: %s", c.Message)
		}
		return
	}
	t.Fatalf("no %q condition in %v", ConditionQueuedWakesPending, result.Conditions)
}

// TestEvaluateGuard_CodesAreStable verifies condition codes are stable string constants.
func TestEvaluateGuard_CodesAreStable(t *testing.T) {
	// Verify codes are not ad-hoc sprintf strings — they're typed constants
	if string(ConditionQueuedWakesPending) != "queued_wakes_pending" {
		t.Errorf("ConditionQueuedWakesPending should be 'queued_wakes_pending', got %q", ConditionQueuedWakesPending)
	}
	if string(ConditionWatcherAbsent) != "watcher_absent" {
		t.Errorf("ConditionWatcherAbsent should be 'watcher_absent', got %q", ConditionWatcherAbsent)
	}
	if string(ConditionWatcherStale) != "watcher_stale" {
		t.Errorf("ConditionWatcherStale should be 'watcher_stale', got %q", ConditionWatcherStale)
	}
}

// TestEvaluateGuard_UnknownExplicit verifies conditions are explicit, even when empty.
func TestEvaluateGuard_UnknownExplicit(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())

	result := EvaluateGuard(home, 0, time.Now())

	// All clear should have no conditions, not a sentinel value
	if len(result.Conditions) != 0 {
		t.Errorf("expected no conditions for all-clear, got %d", len(result.Conditions))
	}
}

// TestEvaluateGuard_WatcherAbsentNotInEvaluateGuard verifies EvaluateGuard doesn't emit watcher codes.
func TestEvaluateGuard_WatcherAbsentNotInEvaluateGuard(t *testing.T) {
	home := t.TempDir()
	// No beat file written

	result := EvaluateGuard(home, 1, time.Now())

	// EvaluateGuard only produces queued_wakes_pending; watcher_absent comes from the CLI layer
	for _, c := range result.Conditions {
		if c.Code == ConditionWatcherAbsent || c.Code == ConditionWatcherStale {
			t.Errorf("EvaluateGuard should not emit watcher codes (those come from the CLI layer), got %q", c.Code)
		}
	}
}

// TestEvaluateGuard_AgedWakeProducesAgedWakeCondition proves that material
// wake entries older than MaterialWakeAgeThreshold produce the
// ConditionAgedWakePending code, making the guard unhealthy.
func TestEvaluateGuard_AgedWakeProducesAgedWakeCondition(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())

	// Enqueue a material wake with an old timestamp by manipulating the queue file.
	// Direct EnqueueWake adds current time, so write a TSV line manually.
	oldEpoch := time.Now().Add(-MaterialWakeAgeThreshold - time.Minute).Unix()
	queuePath := mhome.WakeQueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	// Realistic signal-wake payload: the DeliverWake producer emits
	// "<taskID>: <state>: <msg> [event=N]", so the material marker is embedded
	// after the "<taskID>: " prefix, not at payload start.
	line := fmt.Sprintf("%d	%d\tsignal\ttask-1\ttask-1: done: PR merged [event=1]\n", oldEpoch, 1)
	os.WriteFile(queuePath, []byte(line), 0644)

	result := EvaluateGuard(home, 1, time.Now())

	foundAgedWake := false
	for _, c := range result.Conditions {
		if c.Code == ConditionAgedWakePending {
			foundAgedWake = true
			if !strings.Contains(c.Message, "aged") {
				t.Errorf("aged wake condition message should contain 'aged', got: %s", c.Message)
			}
			break
		}
	}
	if !foundAgedWake {
		codes := make([]string, len(result.Conditions))
		for i, c := range result.Conditions {
			codes[i] = string(c.Code)
		}
		t.Errorf("expected ConditionAgedWakePending, got codes: %v", codes)
	}
}

func TestEvaluateGuard_FreshWakeNoAgedCondition(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())

	// Enqueue a fresh material wake.
	EnqueueWake(home, "signal", "task-fresh", "task-fresh: done: just finished [event=1]")

	result := EvaluateGuard(home, 1, time.Now())

	for _, c := range result.Conditions {
		if c.Code == ConditionAgedWakePending {
			t.Errorf("fresh material wake should not produce aged condition")
		}
	}
}

func TestHasAgedMaterialWake_Threshold(t *testing.T) {
	home := t.TempDir()

	// Old material wake
	oldEpoch := time.Now().Add(-MaterialWakeAgeThreshold - time.Minute).Unix()
	queuePath := mhome.WakeQueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	line := fmt.Sprintf("%d	%d\tsignal\ttask-old\ttask-old: done: very old [event=1]\n", oldEpoch, 1)
	os.WriteFile(queuePath, []byte(line), 0644)

	if !hasAgedMaterialWake(home, time.Now()) {
		t.Fatal("hasAgedMaterialWake should be true for old material wake")
	}
}

func TestHasAgedMaterialWake_Fresh(t *testing.T) {
	home := t.TempDir()
	EnqueueWake(home, "signal", "task-fresh", "task-fresh: done: fresh [event=1]")

	if hasAgedMaterialWake(home, time.Now()) {
		t.Fatal("hasAgedMaterialWake should be false for fresh wake")
	}
}

func TestHasAgedMaterialWake_NonMaterialWakes(t *testing.T) {
	home := t.TempDir()
	oldEpoch := time.Now().Add(-MaterialWakeAgeThreshold - time.Minute).Unix()
	queuePath := mhome.WakeQueuePath(home)
	os.MkdirAll(filepath.Dir(queuePath), 0755)
	// Routine wake, not material.
	line := fmt.Sprintf("%d	%d\tstale\ttask-routine\tworking: in progress\n", oldEpoch, 1)
	os.WriteFile(queuePath, []byte(line), 0644)

	if hasAgedMaterialWake(home, time.Now()) {
		t.Fatal("hasAgedMaterialWake should be false for non-material (routine) wake")
	}
}

// TestReclaimedAgedMaterialWakeTripsGuard proves the F011 behavior authorized
// by the Human (option A): because reclaim preserves the original Epoch, a
// material wake claimed long ago, expired, and reclaimed keeps its true age, so
// the guard reports it as aged rather than resetting its age on reclaim. A
// restamped Epoch would read as fresh and hide the aged material wake.
func TestReclaimedAgedMaterialWakeTripsGuard(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())
	if err := os.MkdirAll(mhome.LeaseDir(home), 0755); err != nil {
		t.Fatal(err)
	}
	// A material wake emitted well before the threshold, claimed under a lease
	// whose header expiresAt=1 is far in the past so reclaim re-enqueues it.
	oldEpoch := time.Now().Add(-MaterialWakeAgeThreshold - time.Minute).Unix()
	leaseID := "lease-expired"
	content := fmt.Sprintf("%s\tconsumer\t1\t%d\n%d\t1\tsignal\ttask-old\ttask-old: done: PR merged [event=1]\n", leaseID, oldEpoch, oldEpoch)
	if err := os.WriteFile(mhome.LeaseFilePath(home, leaseID), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := mhome.ReclaimExpiredLeases(home); err != nil {
		t.Fatal(err)
	}

	result := EvaluateGuard(home, 1, time.Now())
	for _, c := range result.Conditions {
		if c.Code == ConditionAgedWakePending {
			return
		}
	}
	codes := make([]string, len(result.Conditions))
	for i, c := range result.Conditions {
		codes[i] = string(c.Code)
	}
	t.Fatalf("reclaimed aged material wake did not trip aged_wake_pending; codes: %v", codes)
}

func TestConditionAgedWakePending_Constant(t *testing.T) {
	if string(ConditionAgedWakePending) != "aged_wake_pending" {
		t.Errorf("ConditionAgedWakePending = %q, want 'aged_wake_pending'", ConditionAgedWakePending)
	}
}

func TestMaterialWakeAgeThreshold_Constant(t *testing.T) {
	if MaterialWakeAgeThreshold != 5*time.Minute {
		t.Errorf("MaterialWakeAgeThreshold = %v, want 5m0s", MaterialWakeAgeThreshold)
	}
}

// TestPayloadHasMaterialMarker pins the single predicate both the guard and
// watch use. It must match both producer payload shapes and reject a marker
// that only appears mid-message in a non-material payload.
func TestPayloadHasMaterialMarker(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		payload string
		want    bool
	}{
		// Signal wake: "<taskID>: <state>: <msg> [event=N]" (marker embedded
		// after the taskID). The old HasPrefix-only guard MISSED this shape.
		{"signal done embedded", "task-1", "task-1: done: PR merged [event=7]", true},
		{"signal failed embedded", "t2", "t2: failed: build broke [event=9]", true},
		{"signal needs-decision embedded", "t3", "t3: needs-decision: pick one [event=1]", true},
		{"signal blocked embedded", "t4", "t4: blocked: waiting [event=2]", true},
		// Uplink wake: "<state>: <msg> [task=X key=Y]" (marker at start).
		{"uplink done prefix", "task-1", "done: report ready [task=task-1 key=default]", true},
		{"uplink taskID equals state", "done", "done: finished [task=done key=default]", true},
		// A non-material payload whose message merely contains a marker string
		// must NOT match (this is the false positive pure Contains produced).
		{"working with done in message", "task-1", "task-1: working: almost done: 90% [event=3]", false},
		{"routine non-material", "task-r", "task-r: working: in progress [event=4]", false},
		// Marker without its colon is not a marker.
		{"bare word done", "task-1", "task-1: done finished [event=5]", false},
		{"empty payload", "task-1", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := payloadHasMaterialMarker(tc.key, tc.payload); got != tc.want {
				t.Errorf("payloadHasMaterialMarker(%q, %q) = %v, want %v", tc.key, tc.payload, got, tc.want)
			}
		})
	}
}

// writeClaimedLease writes a lease file the way ClaimWakes lays it out, with
// its mtime set to claimedAt so ClaimState sees a claim that is not fresh.
func writeClaimedLease(t *testing.T, homeDir, leaseID string, expiresAt, claimedAt time.Time, wakeLines ...string) {
	t.Helper()
	if err := os.MkdirAll(mhome.LeaseDir(homeDir), 0755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("%s\tconsumer-a\t%d\t%d\n%s\n", leaseID, expiresAt.Unix(), claimedAt.Unix(), strings.Join(wakeLines, "\n"))
	path := mhome.LeaseFilePath(homeDir, leaseID)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, claimedAt, claimedAt); err != nil {
		t.Fatal(err)
	}
}

// TestEvaluateGuard_ClaimedWakeConditions: a wake held in a lease is in
// neither queue, so only the lease read can say it expired or stalled.
func TestEvaluateGuard_ClaimedWakeConditions(t *testing.T) {
	now := time.Now()
	wake := func(epoch time.Time) string {
		return fmt.Sprintf("%d\t1\tsignal\ttask-1\ttask-1: done: PR merged [event=1]", epoch.Unix())
	}
	cases := []struct {
		name      string
		expiresAt time.Time
		claimedAt time.Time
		want      ConditionCode
	}{
		{"held", now.Add(time.Minute), now.Add(-time.Minute), ""},
		{"expired", now.Add(-time.Minute), now.Add(-2 * time.Minute), ConditionClaimedWakeExpired},
		{"stalled", now.Add(time.Hour), now.Add(-MaterialWakeAgeThreshold - time.Minute), ConditionClaimedWakeStalled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeBeatFile(t, home, now.Unix())
			writeClaimedLease(t, home, "lease-1", tc.expiresAt, tc.claimedAt, wake(tc.claimedAt))

			result := EvaluateGuard(home, 1, now)

			var got []ConditionInfo
			for _, c := range result.Conditions {
				if c.Code == ConditionClaimedWakeExpired || c.Code == ConditionClaimedWakeStalled {
					got = append(got, c)
				}
			}
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("held wake produced conditions %v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Code != tc.want {
				t.Fatalf("conditions = %v, want exactly %q", got, tc.want)
			}
			if !strings.Contains(got[0].Message, "consumer-a") {
				t.Errorf("message %q does not name the claimant", got[0].Message)
			}
		})
	}
}

// A lease directory that cannot be read is unknown, never an empty fleet.
func TestEvaluateGuard_UnreadableLeaseDirectoryIsACondition(t *testing.T) {
	home := t.TempDir()
	writeBeatFile(t, home, time.Now().Unix())
	if err := os.WriteFile(filepath.Join(home, "state", ".wake-leases"), []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	result := EvaluateGuard(home, 1, time.Now())

	for _, c := range result.Conditions {
		if c.Code == ConditionClaimedWakesUnreadable {
			return
		}
	}
	t.Fatalf("conditions = %v, want %q", result.Conditions, ConditionClaimedWakesUnreadable)
}

// ListClaimedWakes counts a wake once: a completed resolution means it is no
// longer unacked, and a wake mid-reclaim (in the lease and the queue) is the
// queued copy's.
func TestListClaimedWakes_OmitsResolvedAndQueued(t *testing.T) {
	now := time.Now()
	home := t.TempDir()
	claimedAt := now.Add(-time.Minute)
	expires := now.Add(time.Minute)
	writeClaimedLease(t, home, "lease-1", expires, claimedAt,
		"100\t1\tsignal\ttask-1\ttask-1: done: kept [event=1]",
		"100\t2\tsignal\ttask-2\tresolved elsewhere",
		"100\t3\tsignal\ttask-3\talso queued")
	resDir := filepath.Join(home, "state/.wake-resolutions")
	if err := os.MkdirAll(resDir, 0755); err != nil {
		t.Fatal(err)
	}
	resName := "lease-1-100_2.json"
	resContent := `{"lease_id":"lease-1","event_id":"100:2","summary":"checked","state":"completed","updated_at":100}`
	if err := os.WriteFile(filepath.Join(resDir, resName), []byte(resContent), 0644); err != nil {
		t.Fatal(err)
	}
	writeWakeQueue(t, home, []string{"100\t3\tsignal\ttask-3\talso queued"})

	got, err := ListClaimedWakes(home, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Wake.Seq != "1" {
		t.Fatalf("claimed wakes = %+v, want only 100:1", got)
	}
	if !got[0].Material || got[0].State != mhome.ClaimHeld || got[0].Consumer != "consumer-a" || !got[0].ClaimedAt.Equal(time.Unix(claimedAt.Unix(), 0)) {
		t.Errorf("claimed wake = %+v, want held material wake of consumer-a claimed at %v", got[0], claimedAt)
	}
}
