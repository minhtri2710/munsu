package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/domain"
	"github.com/minhtri2710/munsu/internal/home"
)

// mustReadWakeQueue reads the queue without draining it.
func mustReadWakeQueue(t *testing.T, homeDir string) []WakeRecord {
	t.Helper()
	data, err := os.ReadFile(home.WakeQueuePath(homeDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var records []WakeRecord
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		f := strings.SplitN(line, "\t", 5)
		if len(f) != 5 {
			t.Fatalf("malformed queue line %q", line)
		}
		records = append(records, WakeRecord{Epoch: f[0], Seq: f[1], Kind: f[2], Key: f[3], Payload: f[4]})
	}
	return records
}

func mergedPollHome(t *testing.T) (homeDir, checkPath string) {
	t.Helper()
	homeDir = t.TempDir()
	stateDir := filepath.Join(homeDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	checkPath = filepath.Join(stateDir, "task-1.check")
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho ready\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return homeDir, checkPath
}

func runMergedPollCycle(t *testing.T, homeDir string, retirement *testRetirementPort) {
	t.Helper()
	resetRecovery()
	if _, err := RunCycleWithProbeAndSender(homeDir, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, retirement, &testCheckValidationPort{}, testTaskStatePort{}); err != nil {
		t.Fatalf("run cycle: %v", err)
	}
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()
	original := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = original
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// unmergedPoll is the resolver of a poll whose PR has not merged.
func unmergedPoll(string, string) (bool, []byte, error) { return false, nil, nil }

func announcedWake(t *testing.T, eventID string, generation uint64, payload string) string {
	t.Helper()
	data, err := json.Marshal(ProcessEventWake{EventID: eventID, Generation: generation, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// SC3: an unresolved poll registers once and spends no process-event wake
// however many cycles it stays unresolved; the check wake keeps surfacing it.
func TestRunCycle_UnresolvedPollSpendsNoProcessEventWake(t *testing.T) {
	homeDir, _ := mergedPollHome(t)
	retirement := &testRetirementPort{observe: unmergedPoll}
	for i := 0; i < 3; i++ {
		runMergedPollCycle(t, homeDir, retirement)
	}
	for _, r := range mustReadWakeQueue(t, homeDir) {
		if r.Kind == ProcessEventWakeKind {
			t.Fatalf("process-event wake queued while unresolved: %#v", r)
		}
	}
	rec := mustReadProcessEvent(t, homeDir, mergedPollEventID("task-1"))
	if rec.Generation != 1 || rec.Resolved || rec.Announced {
		t.Fatalf("record = %+v, want generation 1, unresolved, unannounced", rec)
	}
	if len(retirement.observed) != 3 || len(retirement.retiredPaths) != 0 {
		t.Fatalf("observed %d, retired %d; want 3 and 0", len(retirement.observed), len(retirement.retiredPaths))
	}
}

// A resolver refusal classified as a validation refusal is reported and
// suppresses the check wake; any other resolver error keeps the check wake.
func TestRunCycle_ResolverErrorDispositions(t *testing.T) {
	t.Run("validation refusal is reported and suppresses the wake", func(t *testing.T) {
		homeDir, _ := mergedPollHome(t)
		retirement := &testRetirementPort{observe: func(string, string) (bool, []byte, error) {
			return false, nil, fmt.Errorf("%w: identity unreadable", domain.ErrCheckValidationRefused)
		}}
		out := captureStderr(t, func() { runMergedPollCycle(t, homeDir, retirement) })
		if !strings.Contains(out, "poll check refused (wake suppressed): resolving process-event") {
			t.Fatalf("stderr = %q, want the classified refusal", out)
		}
		if q := mustReadWakeQueue(t, homeDir); len(q) != 0 {
			t.Fatalf("wake queue = %#v, want none", q)
		}
	})
	t.Run("other resolver error keeps the check wake", func(t *testing.T) {
		homeDir, _ := mergedPollHome(t)
		retirement := &testRetirementPort{observe: func(string, string) (bool, []byte, error) {
			return false, nil, errors.New("provider unavailable")
		}}
		runMergedPollCycle(t, homeDir, retirement)
		q := mustReadWakeQueue(t, homeDir)
		if len(q) != 1 || q[0].Kind != "check" || q[0].Key != "task-1" {
			t.Fatalf("wake queue = %#v, want one check wake", q)
		}
	})
}

// A failed action re-queues its wake and retains the check wake; the action
// runs again next cycle with the same capture.
func TestRunCycle_ActionFailureRequeuesAndRetries(t *testing.T) {
	homeDir, checkPath := mergedPollHome(t)
	retirement := &testRetirementPort{retireErr: errors.New("status file locked")}
	runMergedPollCycle(t, homeDir, retirement)
	out := captureStderr(t, func() { runMergedPollCycle(t, homeDir, retirement) })
	if !strings.Contains(out, "merged poll retirement for task task-1 failed (retrying next cycle): status file locked") {
		t.Fatalf("stderr = %q, want the retry log", out)
	}
	q := mustReadWakeQueue(t, homeDir)
	var kinds []string
	for _, r := range q {
		kinds = append(kinds, r.Kind)
	}
	if len(q) != 2 || kinds[0] != ProcessEventWakeKind || kinds[1] != "check" {
		t.Fatalf("wake queue = %#v, want the re-queued process-event wake then the check wake", q)
	}
	retirement.retireErr = nil
	runMergedPollCycle(t, homeDir, retirement)
	if len(retirement.retiredPaths) != 2 || retirement.retiredPaths[1] != checkPath {
		t.Fatalf("retired paths = %#v, want two attempts on %q", retirement.retiredPaths, checkPath)
	}
	rec := mustReadProcessEvent(t, homeDir, mergedPollEventID("task-1"))
	if rec.AckedGeneration != rec.Generation {
		t.Fatalf("record = %+v, want acked after the successful retry", rec)
	}
	if len(retirement.observed) != 1 {
		t.Fatalf("observed %d times, want 1: a queued capture is never re-resolved", len(retirement.observed))
	}
}

// Identity drift: the action refuses the capture as stale, the event is
// re-registered (new generation), the resolver captures afresh and the action
// succeeds on the next pass.
func TestRunCycle_StaleCaptureReregistersAndRecaptures(t *testing.T) {
	homeDir, checkPath := mergedPollHome(t)
	fresh := []byte(`{"headSHA":"new","mergedSHA":"new"}`)
	retirement := &testRetirementPort{}
	runMergedPollCycle(t, homeDir, retirement)

	retirement.retireErr = domain.ErrStaleCapture
	retirement.observe = func(string, string) (bool, []byte, error) { return true, fresh, nil }
	runMergedPollCycle(t, homeDir, retirement)
	rec := mustReadProcessEvent(t, homeDir, mergedPollEventID("task-1"))
	if rec.Generation != 2 || rec.AckedGeneration != 0 || !rec.Resolved || string(rec.Result) != string(fresh) {
		t.Fatalf("record = %+v, want generation 2 re-resolved with the fresh capture, unacked", rec)
	}
	q := mustReadWakeQueue(t, homeDir)
	if len(q) != 1 || q[0].Kind != ProcessEventWakeKind {
		t.Fatalf("wake queue = %#v, want only the generation-2 announcement (no check wake, no re-queue of the stale one)", q)
	}

	retirement.retireErr = nil
	runMergedPollCycle(t, homeDir, retirement)
	if len(retirement.retiredPaths) != 2 || string(retirement.results[1]) != string(fresh) {
		t.Fatalf("retired = %#v results = %s, want a second attempt with the fresh capture", retirement.retiredPaths, retirement.results)
	}
	if _, err := os.Stat(checkPath); !os.IsNotExist(err) {
		t.Fatalf("check after retirement: %v, want absent", err)
	}
	rec = mustReadProcessEvent(t, homeDir, mergedPollEventID("task-1"))
	if rec.AckedGeneration != 2 {
		t.Fatalf("record = %+v, want generation 2 acked", rec)
	}
}

// Crash after the capture was written but before the wake was announced: the
// next cycle's RecoverProcessEvents re-announces and the consumer acts.
func TestRunCycle_CrashAfterCaptureBeforeWakeIsRecovered(t *testing.T) {
	homeDir, checkPath := mergedPollHome(t)
	eventID := mergedPollEventID("task-1")
	if _, err := RegisterProcessEvent(homeDir, eventID, "task-1"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	// Rename 1 is the capture write; the crash lands before announcement.
	if err := evaluateProcessEvent(context.Background(), homeDir, eventID, resolvedWith(string(testMergedResult), &calls), crashAfterRename(1)); err == nil {
		t.Fatal("expected the simulated crash")
	}
	if q := mustReadWakeQueue(t, homeDir); len(q) != 0 {
		t.Fatalf("queue before restart = %#v, want empty (crash was before the wake)", q)
	}
	processEventRecoveryDone.Delete(homeDir)

	retirement := &testRetirementPort{}
	runMergedPollCycle(t, homeDir, retirement)
	if len(retirement.retiredPaths) != 1 || retirement.retiredPaths[0] != checkPath {
		t.Fatalf("retired paths = %#v, want [%q] from the re-announced capture", retirement.retiredPaths, checkPath)
	}
	if len(retirement.observed) != 0 {
		t.Fatalf("observed %d times, want 0: a captured event is not re-resolved", len(retirement.observed))
	}
	rec := mustReadProcessEvent(t, homeDir, eventID)
	if rec.AckedGeneration != 1 {
		t.Fatalf("record = %+v, want acked", rec)
	}
}

// Crash after the wake was consumed and the action completed, before the ack:
// on restart the record is re-announced, the action re-enters and reports
// ErrAlreadyRetired, and the consumer acks without a second publication.
func TestRunCycle_CrashAfterActionBeforeAckConvergesOnAlreadyRetired(t *testing.T) {
	homeDir, checkPath := mergedPollHome(t)
	eventID := mergedPollEventID("task-1")
	retirement := &testRetirementPort{}
	runMergedPollCycle(t, homeDir, retirement)
	// Consume the wake and run the action by hand, then "crash" before ack.
	if wakes, err := home.DrainWakesOfKind(homeDir, ProcessEventWakeKind); err != nil || len(wakes) != 1 {
		t.Fatalf("drain = %#v, %v", wakes, err)
	}
	if err := os.Remove(checkPath); err != nil {
		t.Fatal(err)
	}
	processEventRecoveryDone.Delete(homeDir)

	retirement.retireErr = domain.ErrAlreadyRetired
	runMergedPollCycle(t, homeDir, retirement)
	if len(retirement.retiredPaths) != 1 {
		t.Fatalf("retired paths = %#v, want exactly one re-entry", retirement.retiredPaths)
	}
	rec := mustReadProcessEvent(t, homeDir, eventID)
	if rec.AckedGeneration != 1 {
		t.Fatalf("record = %+v, want acked on ErrAlreadyRetired", rec)
	}
	if q := mustReadWakeQueue(t, homeDir); len(q) != 0 {
		t.Fatalf("wake queue = %#v, want empty", q)
	}

	// After the ack nothing is redelivered, even across another restart.
	processEventRecoveryDone.Delete(homeDir)
	runMergedPollCycle(t, homeDir, retirement)
	if len(retirement.retiredPaths) != 1 || len(retirement.observed) != 1 {
		t.Fatalf("after ack: retired %d observed %d, want 1 and 1", len(retirement.retiredPaths), len(retirement.observed))
	}
}

// Two wakes for the same {EventID, Generation} through two separate consumer
// passes: the first pass acks the generation, so the second pass drops the
// duplicate before the action; the action runs once.
func TestRunCycle_DuplicateWakeConvergesAcrossTwoPasses(t *testing.T) {
	homeDir, checkPath := mergedPollHome(t)
	eventID := mergedPollEventID("task-1")
	retirement := &testRetirementPort{}
	runMergedPollCycle(t, homeDir, retirement)
	q := mustReadWakeQueue(t, homeDir)
	if len(q) != 1 {
		t.Fatalf("queue = %#v, want the one announcement", q)
	}
	// A second identical announcement (what a failed recovery run can leave).
	if err := home.EnqueueWake(homeDir, q[0].Kind, q[0].Key, q[0].Payload); err != nil {
		t.Fatal(err)
	}

	runMergedPollCycle(t, homeDir, retirement) // pass 1: both drained, coalesced, action once
	if len(retirement.retiredPaths) != 1 || retirement.retiredPaths[0] != checkPath {
		t.Fatalf("pass 1 retired = %#v, want one action", retirement.retiredPaths)
	}
	if err := home.EnqueueWake(homeDir, q[0].Kind, q[0].Key, q[0].Payload); err != nil {
		t.Fatal(err)
	}
	runMergedPollCycle(t, homeDir, retirement) // pass 2: acked generation → no-op
	if len(retirement.retiredPaths) != 1 {
		t.Fatalf("pass 2 retired = %#v, want the action still run once", retirement.retiredPaths)
	}
	rec := mustReadProcessEvent(t, homeDir, eventID)
	if rec.Generation != 1 || rec.AckedGeneration != 1 {
		t.Fatalf("record = %+v, want generation 1 acked once", rec)
	}
	if left := mustReadWakeQueue(t, homeDir); len(left) != 0 {
		t.Fatalf("queue = %#v, want empty", left)
	}
}

// After retirement, a new artifact for the same task re-arms the event
// (acked record + artifact present → new generation), and the same PR is
// re-observed.
func TestRunCycle_AckedEventRearmsWhenArtifactReappears(t *testing.T) {
	homeDir, checkPath := mergedPollHome(t)
	retirement := &testRetirementPort{}
	runMergedPollCycle(t, homeDir, retirement)
	runMergedPollCycle(t, homeDir, retirement)
	rec := mustReadProcessEvent(t, homeDir, mergedPollEventID("task-1"))
	if rec.Generation != 1 || rec.AckedGeneration != 1 {
		t.Fatalf("record = %+v, want generation 1 acked", rec)
	}
	if err := os.WriteFile(checkPath, []byte("#!/bin/sh\necho again\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runMergedPollCycle(t, homeDir, retirement)
	rec = mustReadProcessEvent(t, homeDir, mergedPollEventID("task-1"))
	if rec.Generation != 2 || rec.AckedGeneration != 1 || !rec.Resolved {
		t.Fatalf("record = %+v, want generation 2 resolved and unacked", rec)
	}
	if len(retirement.observed) != 2 {
		t.Fatalf("observed %d, want 2", len(retirement.observed))
	}
}

// Malformed or stale announcements are dropped loudly, never acted on, and
// never re-queued.
func TestRunCycle_ConsumerDropsUnusableWakes(t *testing.T) {
	const eventID = mergedPollEventPrefix + "task-1"
	for _, tc := range []struct {
		name, key, payload, want string
		setup                    func(homeDir string)
	}{
		{name: "undecodable payload", key: eventID, payload: "not json", want: "undecodable payload"},
		{name: "no record", key: "merged-poll:ghost", payload: announcedWake(t, "merged-poll:ghost", 1, "ghost"), want: "no record"},
		{name: "superseded generation", key: eventID, payload: announcedWake(t, eventID, 1, "task-1"), want: "generation 1 superseded by 2", setup: func(homeDir string) {
			for i := 0; i < 2; i++ {
				if _, err := RegisterProcessEvent(homeDir, eventID, "task-1"); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{name: "unresolved record", key: eventID, payload: announcedWake(t, eventID, 1, "task-1"), want: "record is not resolved", setup: func(homeDir string) {
			if _, err := RegisterProcessEvent(homeDir, eventID, "task-1"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "not a merged-poll event", key: eventID, payload: announcedWake(t, eventID, 1, "task-1"), want: "not a merged-poll event", setup: func(homeDir string) {
			// The id carries the merged-poll prefix but its record names a
			// different task, so the id and the payload disagree about whose
			// poll this is.
			if _, err := RegisterProcessEvent(homeDir, eventID, "task-2"); err != nil {
				t.Fatal(err)
			}
			calls := 0
			if err := EvaluateProcessEvent(context.Background(), homeDir, eventID, resolvedWith("{}", &calls)); err != nil {
				t.Fatal(err)
			}
			if _, err := home.DrainWakesOfKind(homeDir, ProcessEventWakeKind); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeDir, checkPath := mergedPollHome(t)
			if tc.setup != nil {
				tc.setup(homeDir)
			}
			if err := home.EnqueueWake(homeDir, ProcessEventWakeKind, tc.key, tc.payload); err != nil {
				t.Fatal(err)
			}
			retirement := &testRetirementPort{observe: unmergedPoll}
			out := captureStderr(t, func() { runMergedPollCycle(t, homeDir, retirement) })
			if !strings.Contains(out, "dropped: "+tc.want) {
				t.Fatalf("stderr = %q, want dropped: %s", out, tc.want)
			}
			if len(retirement.retiredPaths) != 0 {
				t.Fatalf("retired = %#v, want no action", retirement.retiredPaths)
			}
			for _, r := range mustReadWakeQueue(t, homeDir) {
				if r.Kind == ProcessEventWakeKind {
					t.Fatalf("dropped wake re-queued: %#v", r)
				}
			}
			if _, err := os.Stat(checkPath); err != nil {
				t.Fatalf("artifact: %v, want untouched", err)
			}
		})
	}
}

// Unregistered task ids: a wake naming an event whose record payload cannot
// be a durable check path is dropped, not acted on.
func TestRunCycle_ConsumerDropsUndurableTaskID(t *testing.T) {
	homeDir, _ := mergedPollHome(t)
	bad := "../escape"
	eventID := mergedPollEventID(bad)
	if _, err := RegisterProcessEvent(homeDir, eventID, bad); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := EvaluateProcessEvent(context.Background(), homeDir, eventID, resolvedWith("{}", &calls)); err != nil {
		t.Fatal(err)
	}
	retirement := &testRetirementPort{observe: unmergedPoll}
	out := captureStderr(t, func() { runMergedPollCycle(t, homeDir, retirement) })
	if !strings.Contains(out, "dropped:") || len(retirement.retiredPaths) != 0 {
		t.Fatalf("stderr = %q retired = %#v; want dropped and no action", out, retirement.retiredPaths)
	}
}

// The consumer never leases: the wake is consumed by the watcher and is not
// claimable by agents in the meantime.
func TestProcessEventWakeIsNotClaimable(t *testing.T) {
	homeDir, _ := mergedPollHome(t)
	runMergedPollCycle(t, homeDir, &testRetirementPort{})
	claim, err := ClaimWakes(homeDir, "munsu:herdr", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claim.Wakes) != 0 {
		t.Fatalf("claimed %#v, want nothing: process-event wakes are reserved", claim.Wakes)
	}
	if HasQueuedWakes(homeDir) {
		t.Fatal("HasQueuedWakes true for a queue holding only the process-event wake")
	}
	if q := mustReadWakeQueue(t, homeDir); len(q) != 1 || q[0].Kind != ProcessEventWakeKind {
		t.Fatalf("queue = %#v, want the announcement still queued", q)
	}
}
