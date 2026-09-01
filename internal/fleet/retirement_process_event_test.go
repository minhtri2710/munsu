//go:build integration

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	mhome "github.com/minhtri2710/munsu/internal/home"
	"github.com/minhtri2710/munsu/internal/orchestrator"
	"github.com/minhtri2710/munsu/internal/taskauthority"
)

// The two tests below drive the real merged-poll resolver and action through
// the watcher cycle: the process-event source, the consumer and the fleet
// retirement action all run unmocked, with only the endpoint, sender and
// state ports stubbed.

type cycleRetirementPort struct {
	auth    *taskauthority.Canonical
	actions int
}

func (p *cycleRetirementPort) ObserveMergedPoll(homeDir, taskID string) (bool, []byte, error) {
	return ObserveMergedPoll(homeDir, taskID)
}

func (p *cycleRetirementPort) RetireMergedPoll(homeDir, taskID, checkPath string, result []byte) error {
	p.actions++
	return RetireMergedPoll(homeDir, taskID, checkPath, result, p.auth)
}

type cycleProbe struct{}

func (cycleProbe) Probe(string, map[string]string) (bool, error) { return false, nil }

type cycleSender struct{}

func (cycleSender) Alive(string, map[string]string) (bool, error) { return false, nil }
func (cycleSender) Send(string, map[string]string, string) mhome.BoundSendResult {
	return mhome.BoundSendResult{}
}

type cycleHooks struct{}

func (cycleHooks) Reconcile(string, bool) error { return nil }
func (cycleHooks) Activate(string)              {}

type cycleChecks struct{}

func (cycleChecks) ValidateCheck(path string) error { return ValidateCheckWithLstat(path) }

type cycleStates struct{}

func (cycleStates) ReadTaskState(string, string) (*orchestrator.ObservedTaskState, error) {
	return &orchestrator.ObservedTaskState{}, nil
}

func runSupervisionCycle(t *testing.T, home string, port *cycleRetirementPort) {
	t.Helper()
	if _, err := orchestrator.RunCycleWithProbeAndSender(home, cycleProbe{}, cycleSender{}, cycleHooks{}, port, cycleChecks{}, cycleStates{}); err != nil {
		t.Fatalf("RunCycleWithProbeAndSender: %v", err)
	}
}

// freshenCheck makes the check newer than the meta projection the authority
// seeding may have rewritten, so discovery accepts it instead of refusing it
// as stale.
func freshenCheck(t *testing.T, checkPath string) {
	t.Helper()
	data, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkPath, data, 0755); err != nil {
		t.Fatal(err)
	}
}

func publicationCount(t *testing.T, home, taskID string) int {
	t.Helper()
	lines, err := mhome.ReadStatus(home, taskID)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadStatus: %v", err)
	}
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "done [key=pr-merged") {
			n++
		}
	}
	return n
}

func quarantineFiles(t *testing.T, home string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(retirementDirPath(home), "*.quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// processEventRecordFor reads the durable process-event record for eventID
// by scanning the state directory: the record path is the source's own
// business, so the test matches on the decoded event id rather than on a
// path it would otherwise have to re-derive.
func processEventRecordFor(t *testing.T, home, eventID string) *orchestrator.ProcessEventRecord {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(mhome.StateDir(home), "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var rec orchestrator.ProcessEventRecord
		if json.Unmarshal(data, &rec) == nil && rec.EventID == eventID {
			return &rec
		}
	}
	t.Fatalf("no process-event record for %q", eventID)
	return nil
}

// drainMergedPollWake takes the single announced process-event wake off the
// queue and returns it.
func drainMergedPollWake(t *testing.T, home string) mhome.WakeRecord {
	t.Helper()
	wakes, err := mhome.DrainWakesOfKind(home, mhome.ProcessEventWakeKind)
	if err != nil {
		t.Fatalf("DrainWakesOfKind: %v", err)
	}
	if len(wakes) != 1 {
		t.Fatalf("process-event wakes = %d, want exactly one announcement", len(wakes))
	}
	return wakes[0]
}

func assertNoProcessEventWake(t *testing.T, home string) {
	t.Helper()
	wakes, err := mhome.DrainWakesOfKind(home, mhome.ProcessEventWakeKind)
	if err != nil {
		t.Fatalf("DrainWakesOfKind: %v", err)
	}
	if len(wakes) != 0 {
		t.Fatalf("process-event wakes still queued: %+v", wakes)
	}
}

// Duplicate-wake convergence: two wakes for the same {EventID, Generation}
// pass through two separate consumer passes. The first runs the action once;
// the second finds the generation acked and is a no-op — the action does not
// run again and the status file holds exactly one publication line.
func TestProcessEvent_DuplicateMergedPollWakeConvergesAcrossTwoPasses(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()
	auth := retirementPollAuth(t, home, taskID)
	freshenCheck(t, checkPath)
	port := &cycleRetirementPort{auth: auth}

	// Cycle 1: the loop registers the event, the resolver captures the merge
	// and the source announces exactly one wake. No action yet.
	runSupervisionCycle(t, home, port)
	if port.actions != 0 {
		t.Fatalf("actions after capture = %d, want 0 (action runs only on the wake)", port.actions)
	}
	announced := drainMergedPollWake(t, home)
	var wake orchestrator.ProcessEventWake
	if err := json.Unmarshal([]byte(announced.Payload), &wake); err != nil {
		t.Fatalf("announced wake payload undecodable: %v", err)
	}
	rec := processEventRecordFor(t, home, wake.EventID)
	if !rec.Resolved || rec.Generation != 1 || rec.AckedGeneration != 0 {
		t.Fatalf("record after capture = %+v, want resolved generation 1 unacked", rec)
	}

	// Pass 1: the announcement is consumed; the real action retires the poll
	// and the consumer acks the generation.
	if err := mhome.EnqueueWake(home, announced.Kind, announced.Key, announced.Payload); err != nil {
		t.Fatal(err)
	}
	runSupervisionCycle(t, home, port)
	if port.actions != 1 {
		t.Fatalf("actions after pass 1 = %d, want 1", port.actions)
	}
	if _, err := os.Lstat(checkPath); !os.IsNotExist(err) {
		t.Fatalf("check must be retired after pass 1: %v", err)
	}
	if rec := processEventRecordFor(t, home, wake.EventID); rec.AckedGeneration != 1 {
		t.Fatalf("record after pass 1 = %+v, want generation 1 acked", rec)
	}

	// Pass 2: a duplicate of the same {EventID, Generation} arrives after the
	// ack (a re-announcement that raced the consumer). It is dropped on the
	// acked generation: no action, no second publication.
	if err := mhome.EnqueueWake(home, announced.Kind, announced.Key, announced.Payload); err != nil {
		t.Fatal(err)
	}
	runSupervisionCycle(t, home, port)
	if port.actions != 1 {
		t.Fatalf("actions after pass 2 = %d, want 1 (duplicate must be a no-op)", port.actions)
	}
	if n := publicationCount(t, home, taskID); n != 1 {
		t.Fatalf("publication lines = %d, want exactly 1", n)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatalf("retirement journal must be gone, got %+v", rec)
	}
	if rec := processEventRecordFor(t, home, wake.EventID); rec.Generation != 1 || rec.AckedGeneration != 1 {
		t.Fatalf("record after pass 2 = %+v, want generation 1 still acked", rec)
	}
	assertNoProcessEventWake(t, home)
}

// Crash-mid-quarantine re-entry via the journal: the process dies after the
// public->private rename and before the quarantined file is removed. The
// intermediate state is built by the real action through its rename seam,
// then re-entry is driven the way a restart drives it — the source re-announces
// the captured-but-unacked event, the consumer runs the action, the action
// finds the journal and finishes through recovery, and the consumer acks.
func TestProcessEvent_CrashMidQuarantineReentersThroughJournal(t *testing.T) {
	home, taskID, checkPath, cleanup := setupMergedPollTest(t, "0000111122223333444455556666777788889999", "main")
	defer cleanup()
	restore := installMockMergeStatus(t, true, "0000111122223333444455556666777788889999", "aaaabbbbccccddddeeeeffff0000111122223333")
	defer restore()
	auth := retirementPollAuth(t, home, taskID)
	freshenCheck(t, checkPath)
	eventID := "merged-poll:" + taskID

	// Capture and announce through the source, exactly as the loop does.
	if _, err := orchestrator.RegisterProcessEvent(home, eventID, taskID); err != nil {
		t.Fatalf("RegisterProcessEvent: %v", err)
	}
	if err := orchestrator.EvaluateProcessEvent(context.Background(), home, eventID, func(context.Context) (bool, []byte, error) {
		return ObserveMergedPoll(home, taskID)
	}); err != nil {
		t.Fatalf("EvaluateProcessEvent: %v", err)
	}
	drainMergedPollWake(t, home) // consumed by the process that is about to crash
	rec := processEventRecordFor(t, home, eventID)
	if !rec.Resolved || rec.AckedGeneration != 0 {
		t.Fatalf("record after capture = %+v, want resolved and unacked", rec)
	}

	// The crash: the rename succeeds, then the process dies before the journal
	// records the quarantine and before the removal.
	crashed := errors.New("process crashed after rename")
	err := retireMergedPoll(home, taskID, checkPath, rec.Result, auth, pollContentDigest, func(from, to string) error {
		if err := mhome.RenameDurable(from, to); err != nil {
			return err
		}
		return crashed
	})
	if !errors.Is(err, crashed) {
		t.Fatalf("crash injection did not surface: %v", err)
	}

	// The half-quarantined intermediate state, verified before re-entry.
	if _, statErr := os.Lstat(checkPath); !os.IsNotExist(statErr) {
		t.Fatalf("public poll must be gone after the rename: %v", statErr)
	}
	if q := quarantineFiles(t, home); len(q) != 1 {
		t.Fatalf("quarantine files = %v, want the renamed poll", q)
	}
	journal := readRetirementRecordOrNil(t, home, taskID)
	if journal == nil || journal.Quarantined {
		t.Fatalf("journal = %+v, want a pending record that has not recorded the quarantine", journal)
	}
	if n := publicationCount(t, home, taskID); n != 1 {
		t.Fatalf("publication lines before re-entry = %d, want 1", n)
	}

	// Restart: the first cycle on this home re-announces the unacked event,
	// the consumer runs the action, and the journal routes it into recovery.
	port := &cycleRetirementPort{auth: auth}
	runSupervisionCycle(t, home, port)
	if port.actions != 1 {
		t.Fatalf("actions on re-entry = %d, want 1", port.actions)
	}
	if q := quarantineFiles(t, home); len(q) != 0 {
		t.Fatalf("quarantine files after re-entry = %v, want none", q)
	}
	if _, statErr := os.Lstat(checkPath); !os.IsNotExist(statErr) {
		t.Fatalf("public poll must stay gone: %v", statErr)
	}
	if n := publicationCount(t, home, taskID); n != 1 {
		t.Fatalf("publication lines after re-entry = %d, want exactly 1", n)
	}
	if rec := readRetirementRecordOrNil(t, home, taskID); rec != nil {
		t.Fatalf("journal must be gone after re-entry, got %+v", rec)
	}
	if rec := processEventRecordFor(t, home, eventID); rec.AckedGeneration != rec.Generation {
		t.Fatalf("record after re-entry = %+v, want acked", rec)
	}
	assertNoProcessEventWake(t, home)
	if !errors.Is(RetireMergedPoll(home, taskID, checkPath, mergedResultForTest("", ""), auth), domain.ErrAlreadyRetired) {
		t.Fatal("a later attempt must report the retirement as already done")
	}
}
