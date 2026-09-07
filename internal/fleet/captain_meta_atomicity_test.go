package fleet

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

// concurrentMetaWriters runs writers goroutines that each stamp their own key
// onto the captain task meta through UpdateMeta while fn runs against the same
// meta, then reports how many of those keys survived. A guard path that reads a
// snapshot, mutates it and writes it back unlocked erases the keys that landed
// inside its window; one that reads and writes under the lock cannot.
func concurrentMetaWriters(t *testing.T, parent, id string, writers int, fn func()) map[string]string {
	t.Helper()
	taskID := taskIDForCaptain(id)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, writers+1)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := mhome.UpdateMeta(parent, taskID, func(meta map[string]string) error {
				time.Sleep(time.Millisecond)
				meta[fmt.Sprintf("race_%02d", i)] = "persisted"
				return nil
			}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		fn()
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent meta writer: %v", err)
	}

	meta, err := mhome.ReadMeta(parent, taskID)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	survived := 0
	for i := 0; i < writers; i++ {
		if meta[fmt.Sprintf("race_%02d", i)] == "persisted" {
			survived++
		}
	}
	t.Logf("concurrent writer keys surviving: %d/%d", survived, writers)
	if survived != writers {
		t.Fatalf("surviving concurrent writer keys = %d, want %d; meta=%v", survived, writers, meta)
	}
	return meta
}

// TestClearRelaunchGuardDoesNotEraseConcurrentWrites proves clearing a resolved
// guard is one locked read-mutate-write cycle: every concurrent projection key
// that lands during the clear survives it, and the guard is still cleared.
func TestClearRelaunchGuardDoesNotEraseConcurrentWrites(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "atomic-clear")
	writeCaptainMeta(t, parent, "atomic-clear", captainHome, "w1")
	writeRelaunchGuard(t, parent, "atomic-clear", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))

	var clearErr error
	meta := concurrentMetaWriters(t, parent, "atomic-clear", 16, func() {
		clearErr = clearRelaunchGuard(parent, taskIDForCaptain("atomic-clear"))
	})
	if clearErr != nil {
		t.Fatalf("clearRelaunchGuard: %v", clearErr)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared", meta["relaunch_liveness"])
	}
	if _, ok := meta[relaunchGuardUntilField]; ok {
		t.Fatalf("%s = %q, want cleared", relaunchGuardUntilField, meta[relaunchGuardUntilField])
	}
}

// TestConsultRelaunchGuardExpiredClearDoesNotEraseConcurrentWrites proves the
// expired-guard clear is one locked cycle: the guard is cleared, the relaunch
// is allowed, and no concurrent projection key is erased.
func TestConsultRelaunchGuardExpiredClearDoesNotEraseConcurrentWrites(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "atomic-consult")
	writeCaptainMeta(t, parent, "atomic-consult", captainHome, "w1")
	writeRelaunchGuard(t, parent, "atomic-consult", strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10))

	var refused bool
	var gErr error
	meta := concurrentMetaWriters(t, parent, "atomic-consult", 16, func() {
		refused, _, gErr = consultRelaunchGuard(parent, taskIDForCaptain("atomic-consult"), time.Now())
	})
	if gErr != nil {
		t.Fatalf("consultRelaunchGuard: %v", gErr)
	}
	if refused {
		t.Fatal("expired guard refused the relaunch")
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared after expiry", meta["relaunch_liveness"])
	}
}

// TestConsultRelaunchGuardNormalizeDoesNotEraseConcurrentWrites proves the
// deadline normalization write is one locked cycle: the normalized deadline is
// the one the refusal was decided on, and no concurrent key is erased.
func TestConsultRelaunchGuardNormalizeDoesNotEraseConcurrentWrites(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "atomic-normalize")
	writeCaptainMeta(t, parent, "atomic-normalize", captainHome, "w1")
	writeRelaunchGuard(t, parent, "atomic-normalize", "not-a-timestamp")

	now := time.Now()
	var refused bool
	var remaining time.Duration
	var gErr error
	meta := concurrentMetaWriters(t, parent, "atomic-normalize", 16, func() {
		refused, remaining, gErr = consultRelaunchGuard(parent, taskIDForCaptain("atomic-normalize"), now)
	})
	if gErr != nil {
		t.Fatalf("consultRelaunchGuard: %v", gErr)
	}
	if !refused || remaining <= 0 {
		t.Fatalf("refused=%v remaining=%s, want a refusal inside the normalized window", refused, remaining)
	}
	want := strconv.FormatInt(now.Add(relaunchGuardTTL).Unix(), 10)
	if meta[relaunchGuardUntilField] != want {
		t.Fatalf("%s = %q, want the normalized deadline %q", relaunchGuardUntilField, meta[relaunchGuardUntilField], want)
	}
}

// TestProveRelaunchArmDoesNotEraseConcurrentWrites proves arming the guard
// after an elapsed proof window is one locked cycle rather than a snapshot read
// held across the probe loop's sleeps.
func TestProveRelaunchArmDoesNotEraseConcurrentWrites(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "atomic-arm")
	writeCaptainMeta(t, parent, "atomic-arm", captainHome, "w1")

	var proven bool
	var pErr error
	meta := concurrentMetaWriters(t, parent, "atomic-arm", 16, func() {
		proven, pErr = proveRelaunch(parent, Info{ID: "atomic-arm", Home: captainHome},
			&testProbeEndpoint{result: CaptainProbeResult{Absent: true}},
			func(time.Duration) {}, time.Now)
	})
	if pErr != nil {
		t.Fatalf("proveRelaunch: %v", pErr)
	}
	if proven {
		t.Fatal("proveRelaunch proved liveness against an absent endpoint")
	}
	if meta["relaunch_liveness"] != "unproven" {
		t.Fatalf("relaunch_liveness = %q, want unproven after the proof window elapsed", meta["relaunch_liveness"])
	}
}

// TestProveRelaunchClearDoesNotEraseConcurrentWrites proves the proven-liveness
// clear is one locked cycle taken at the moment liveness is proven, not a write
// of the snapshot read before the probes.
func TestProveRelaunchClearDoesNotEraseConcurrentWrites(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "atomic-prove")
	writeCaptainMeta(t, parent, "atomic-prove", captainHome, "w1")
	writeRelaunchGuard(t, parent, "atomic-prove", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))

	var proven bool
	var pErr error
	meta := concurrentMetaWriters(t, parent, "atomic-prove", 16, func() {
		proven, pErr = proveRelaunch(parent, Info{ID: "atomic-prove", Home: captainHome},
			&testProbeEndpoint{result: CaptainProbeResult{PaneAlive: true, AgentAlive: true}},
			func(time.Duration) {}, time.Now)
	})
	if pErr != nil {
		t.Fatalf("proveRelaunch: %v", pErr)
	}
	if !proven {
		t.Fatal("proveRelaunch did not prove liveness against a live endpoint")
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Fatalf("relaunch_liveness = %q, want cleared after proven liveness", meta["relaunch_liveness"])
	}
}

// TestSendNudgeAppliedMarkerDoesNotEraseConcurrentWrites proves the applied_*
// stamp written after the nudge round-trip is overlaid onto the meta as it
// stands at write time, not onto the snapshot validated before the send, so a
// projection write that lands during the send survives it.
func TestSendNudgeAppliedMarkerDoesNotEraseConcurrentWrites(t *testing.T) {
	parent, captainHome, id, digest := newGuardNudgeValidFixture(t)
	marker, err := readNudgeMarker(parent, id)
	if err != nil || marker == nil {
		t.Fatalf("readNudgeMarker: %v", err)
	}

	var nErr error
	meta := concurrentMetaWriters(t, parent, id, 16, func() {
		nErr = sendNudge(parent, Info{ID: id, Home: captainHome},
			&testNudgeEndpoint{result: NudgeResult{Status: "submitted", Acknowledged: true}})
	})
	if nErr != nil {
		t.Fatalf("sendNudge: %v", nErr)
	}
	if meta["applied_commit"] != marker["commit"] {
		t.Fatalf("applied_commit = %q, want %q", meta["applied_commit"], marker["commit"])
	}
	if meta["applied_digest"] != digest {
		t.Fatalf("applied_digest = %q, want %q", meta["applied_digest"], digest)
	}
}

// TestConsultRelaunchGuardRefusesForeignMeta proves the guard consult refuses a
// task meta that is not a captain's rather than treating the missing guard
// state as permission to relaunch.
func TestConsultRelaunchGuardRefusesForeignMeta(t *testing.T) {
	parent := t.TempDir()
	taskID := taskIDForCaptain("foreign-consult")
	if err := mhome.WriteMeta(parent, taskID, map[string]string{"kind": "soldier"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	refused, _, err := consultRelaunchGuard(parent, taskID, time.Now())
	if err == nil {
		t.Fatal("consultRelaunchGuard accepted a meta with kind=soldier")
	}
	if refused {
		t.Fatal("a refused consult reported refused=true, which callers read as an armed guard")
	}
	if !strings.Contains(err.Error(), "expected captain") {
		t.Errorf("error = %v, want it to name the expected kind", err)
	}
}

// TestProveRelaunchRefusesForeignMetaOnArm proves the arm path refuses a task
// meta that is not a captain's instead of creating guard state on it.
func TestProveRelaunchRefusesForeignMetaOnArm(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "foreign-arm")
	if err := mhome.WriteMeta(parent, taskIDForCaptain("foreign-arm"), map[string]string{"kind": "soldier"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	proven, err := proveRelaunch(parent, Info{ID: "foreign-arm", Home: captainHome},
		&testProbeEndpoint{result: CaptainProbeResult{Absent: true}},
		func(time.Duration) {}, time.Now)
	if proven {
		t.Fatal("proveRelaunch proved liveness against an absent endpoint")
	}
	if err == nil || !strings.Contains(err.Error(), "expected captain") {
		t.Fatalf("error = %v, want a refusal naming the expected kind", err)
	}
	meta, readErr := mhome.ReadMeta(parent, taskIDForCaptain("foreign-arm"))
	if readErr != nil {
		t.Fatalf("ReadMeta: %v", readErr)
	}
	if _, ok := meta["relaunch_liveness"]; ok {
		t.Error("a refused arm wrote guard state onto a foreign meta")
	}
}

// TestClearRelaunchGuardLeavesUnarmedMetaAlone proves an unarmed guard writes
// nothing at all, so the clear that runs on every proven-alive captain is a
// read and not a rewrite of its meta.
func TestClearRelaunchGuardLeavesUnarmedMetaAlone(t *testing.T) {
	parent := t.TempDir()
	captainHome := seedCaptainForTest(t, parent, "unarmed-clear")
	writeCaptainMeta(t, parent, "unarmed-clear", captainHome, "w1")
	p, err := mhome.MetaFilePath(parent, taskIDForCaptain("unarmed-clear"))
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat meta: %v", err)
	}
	beforeBody, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading meta: %v", err)
	}

	if err := clearRelaunchGuard(parent, taskIDForCaptain("unarmed-clear")); err != nil {
		t.Fatalf("clearRelaunchGuard: %v", err)
	}

	after, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat meta back: %v", err)
	}
	afterBody, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading meta back: %v", err)
	}
	if string(beforeBody) != string(afterBody) {
		t.Fatalf("unarmed clear changed meta: before=%q after=%q", beforeBody, afterBody)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("unarmed clear replaced the meta file: mtime %s -> %s", before.ModTime(), after.ModTime())
	}
}

// TestClearRelaunchGuardOnAbsentMetaWritesNothing proves a captain with no task
// meta is left with none, rather than having one created by the clear that runs
// on every proven-alive captain.
func TestClearRelaunchGuardOnAbsentMetaWritesNothing(t *testing.T) {
	parent := t.TempDir()
	taskID := taskIDForCaptain("absent-clear")
	p, err := mhome.MetaFilePath(parent, taskID)
	if err != nil {
		t.Fatalf("MetaFilePath: %v", err)
	}

	if err := clearRelaunchGuard(parent, taskID); err != nil {
		t.Fatalf("clearRelaunchGuard: %v", err)
	}
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Fatalf("os.Stat(%s) = %v, want not-exist: the clear created a meta", p, statErr)
	}
}
