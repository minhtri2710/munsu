package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// fakeReviewPort is a ReviewVerdictPort over a map of verdict files: a review
// task generation has a file when files has its key, holding the digest.
type fakeReviewPort struct {
	targets   []ReviewVerdictTarget
	listErr   error
	files     map[string]string
	recordErr error
	observed  []string
	recorded  []string
}

func reviewKey(taskID string, generation uint64) string {
	return fmt.Sprintf("%s:%d", taskID, generation)
}

func (p *fakeReviewPort) WorkingReviews(string) ([]ReviewVerdictTarget, error) {
	return p.targets, p.listErr
}

func (p *fakeReviewPort) ObserveReviewVerdict(_, taskID string, generation uint64) (bool, []byte, error) {
	p.observed = append(p.observed, reviewKey(taskID, generation))
	digest, ok := p.files[reviewKey(taskID, generation)]
	return ok, []byte(digest), nil
}

func (p *fakeReviewPort) RecordReviewVerdict(_, taskID string, generation uint64, digest string) (string, error) {
	p.recorded = append(p.recorded, reviewKey(taskID, generation)+"@"+digest)
	if p.recordErr != nil {
		return "", p.recordErr
	}
	return "recorded PASS verdict for " + taskID, nil
}

// reviewVerdictHome is a General home hosting the review task rev-1, so the
// report of its verdict has a receiver.
func reviewVerdictHome(t *testing.T) string {
	t.Helper()
	generalHome, _ := directGeneralHome(t)
	if err := home.WriteMeta(generalHome, "rev-1", map[string]string{"kind": "review"}); err != nil {
		t.Fatal(err)
	}
	return generalHome
}

func runReviewCycle(t *testing.T, homeDir string, reviews ReviewVerdictPort) {
	t.Helper()
	resetRecovery()
	if _, err := RunCycleWithProbeAndSender(homeDir, testEndpointProbe{}, testCycleSender{}, NoopWatcherHooks{}, reviews, testTaskStatePort{}); err != nil {
		t.Fatalf("run cycle: %v", err)
	}
}

// reportWakes derives the report wakes of the home's report lines and returns
// them, as the next watcher cycle or wake claim would.
func reportWakes(t *testing.T, homeDir string) []WakeRecord {
	t.Helper()
	if err := home.DeriveReportWakes(homeDir); err != nil {
		t.Fatal(err)
	}
	var out []WakeRecord
	for _, r := range mustReadWakeQueue(t, homeDir) {
		if r.Kind == home.ReportWakeKind {
			out = append(out, r)
		}
	}
	return out
}

func TestReviewVerdictEventRegistersOncePerReviewGenerationAndResolvesOnTheFile(t *testing.T) {
	homeDir := t.TempDir()
	port := &fakeReviewPort{targets: []ReviewVerdictTarget{{TaskID: "rev-1", Generation: 1}, {TaskID: "rev-1", Generation: 2}}}

	registerReviewVerdictEvents(homeDir, port)
	for _, gen := range []uint64{1, 2} {
		rec := mustReadProcessEvent(t, homeDir, reviewVerdictEventID("rev-1", gen))
		if rec.Generation != 1 || rec.Resolved || rec.Payload != fmt.Sprintf(`{"taskId":"rev-1","generation":%d}`, gen) {
			t.Fatalf("generation %d record = %+v, want one unresolved registration carrying the target", gen, rec)
		}
	}

	port.files = map[string]string{reviewKey("rev-1", 1): "digest-one"}
	registerReviewVerdictEvents(homeDir, port)
	first := mustReadProcessEvent(t, homeDir, reviewVerdictEventID("rev-1", 1))
	if first.Generation != 1 || !first.Resolved || string(first.Result) != "digest-one" {
		t.Fatalf("record with a file = %+v, want generation 1 resolved with the file digest", first)
	}
	if second := mustReadProcessEvent(t, homeDir, reviewVerdictEventID("rev-1", 2)); second.Resolved {
		t.Fatalf("generation 2 resolved on generation 1's file: %+v", second)
	}

	if err := AckProcessEvent(homeDir, reviewVerdictEventID("rev-1", 1), 1); err != nil {
		t.Fatal(err)
	}
	port.observed = nil
	port.files[reviewKey("rev-1", 1)] = "rewritten-after-ack"
	registerReviewVerdictEvents(homeDir, port)
	for _, key := range port.observed {
		if key == reviewKey("rev-1", 1) {
			t.Fatalf("an acked event was evaluated again (observed %v)", port.observed)
		}
	}
	if acked := mustReadProcessEvent(t, homeDir, reviewVerdictEventID("rev-1", 1)); acked.Generation != 1 || string(acked.Result) != "digest-one" {
		t.Fatalf("acked record = %+v, want it untouched by a file written after the ack", acked)
	}
}

func TestReviewVerdictEventsAreOptionalAndNeverFailTheCycle(t *testing.T) {
	homeDir := t.TempDir()
	registerReviewVerdictEvents(homeDir, nil)

	out := captureStderr(t, func() {
		registerReviewVerdictEvents(homeDir, &fakeReviewPort{listErr: errors.New("fleet unavailable")})
	})
	if !strings.Contains(out, "review-verdict: listing review tasks: fleet unavailable") {
		t.Fatalf("stderr = %q, want the logged listing failure", out)
	}
}

func TestRunCycleRecordsAReviewVerdictFileAndReportsItOnce(t *testing.T) {
	homeDir := reviewVerdictHome(t)
	port := &fakeReviewPort{
		targets: []ReviewVerdictTarget{{TaskID: "rev-1", Generation: 1}},
		files:   map[string]string{reviewKey("rev-1", 1): "digest-one"},
	}

	runReviewCycle(t, homeDir, port)
	if len(port.recorded) != 1 || port.recorded[0] != "rev-1:1@digest-one" {
		t.Fatalf("recorded = %v, want one record of rev-1 generation 1 at the file digest", port.recorded)
	}
	wakes := reportWakes(t, homeDir)
	if len(wakes) != 1 || wakes[0].Key != "rev-1" || wakes[0].Payload != "rev-1: done: recorded PASS verdict for rev-1 [key=review-verdict:digest-one]" {
		t.Fatalf("report wakes = %+v, want one done report keyed by the file digest", wakes)
	}
	if rec := mustReadProcessEvent(t, homeDir, reviewVerdictEventID("rev-1", 1)); rec.AckedGeneration != rec.Generation {
		t.Fatalf("record = %+v, want acked after the report", rec)
	}

	runReviewCycle(t, homeDir, port)
	if len(port.recorded) != 1 || len(reportWakes(t, homeDir)) != 1 {
		t.Fatalf("second cycle recorded %v and reported %d times, want no retry after the ack", port.recorded, len(reportWakes(t, homeDir)))
	}
}

func TestRunCycleReportsARefusedReviewVerdictOnceAndAcksIt(t *testing.T) {
	homeDir := reviewVerdictHome(t)
	port := &fakeReviewPort{
		targets:   []ReviewVerdictTarget{{TaskID: "rev-1", Generation: 1}},
		files:     map[string]string{reviewKey("rev-1", 1): "digest-bad"},
		recordErr: errors.New("verdict does not speak for this head"),
	}

	runReviewCycle(t, homeDir, port)
	wakes := reportWakes(t, homeDir)
	want := "rev-1: failed: review verdict not recorded: verdict does not speak for this head [key=review-verdict:digest-bad]"
	if len(wakes) != 1 || wakes[0].Payload != want {
		t.Fatalf("report wakes = %+v, want one failed report %q", wakes, want)
	}
	if rec := mustReadProcessEvent(t, homeDir, reviewVerdictEventID("rev-1", 1)); rec.AckedGeneration != rec.Generation {
		t.Fatalf("record = %+v, want a refused verdict acked (no retry)", rec)
	}
	runReviewCycle(t, homeDir, port)
	if len(port.recorded) != 1 {
		t.Fatalf("recorded = %v, want the refused file not retried", port.recorded)
	}
}

func TestRecordReviewVerdictWakeKeysAnUnreadableFileByItsOwnKey(t *testing.T) {
	homeDir := reviewVerdictHome(t)
	port := &fakeReviewPort{}
	eventID := reviewVerdictEventID("rev-1", 1)
	rec := &ProcessEventRecord{EventID: eventID, Payload: `{"taskId":"rev-1","generation":1}`}

	recordReviewVerdictWake(homeDir, port, ProcessEventWake{EventID: eventID, Generation: 1}, rec)
	wakes := reportWakes(t, homeDir)
	if len(wakes) != 1 || !strings.HasSuffix(wakes[0].Payload, "[key=review-verdict:unreadable]") {
		t.Fatalf("report wakes = %+v, want a report keyed review-verdict:unreadable", wakes)
	}
}

func TestRecordReviewVerdictWakeDropsWhatItDoesNotOwn(t *testing.T) {
	eventID := reviewVerdictEventID("rev-1", 1)
	cases := []struct {
		name    string
		port    ReviewVerdictPort
		rec     *ProcessEventRecord
		announc string
		want    string
	}{
		{"payload is not a target", &fakeReviewPort{}, &ProcessEventRecord{Payload: "not json"}, eventID, "not a review-verdict event"},
		{"payload names another event", &fakeReviewPort{}, &ProcessEventRecord{Payload: `{"taskId":"rev-2","generation":1}`}, eventID, "not a review-verdict event"},
		{"no port", nil, &ProcessEventRecord{Payload: `{"taskId":"rev-1","generation":1}`}, eventID, "no review-verdict port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeDir := reviewVerdictHome(t)
			out := captureStderr(t, func() {
				recordReviewVerdictWake(homeDir, tc.port, ProcessEventWake{EventID: tc.announc, Generation: 1}, tc.rec)
			})
			if !strings.Contains(out, fmt.Sprintf("process-event wake %q dropped: %s", tc.announc, tc.want)) {
				t.Fatalf("stderr = %q, want the %q drop", out, tc.want)
			}
			if fake, ok := tc.port.(*fakeReviewPort); ok && len(fake.recorded) != 0 {
				t.Fatalf("a dropped wake recorded %v", fake.recorded)
			}
			if wakes := reportWakes(t, homeDir); len(wakes) != 0 {
				t.Fatalf("a dropped wake reported %+v", wakes)
			}
		})
	}
}

// A report that cannot be written leaves the event unacked so a restart
// re-announces it; the verdict record is replaced on re-entry.
func TestRecordReviewVerdictWakeLeavesTheEventUnackedWhenTheReportCannotBeWritten(t *testing.T) {
	homeDir := reviewVerdictHome(t)
	port := &fakeReviewPort{
		targets: []ReviewVerdictTarget{{TaskID: "rev-1", Generation: 1}},
		files:   map[string]string{reviewKey("rev-1", 1): "digest-one"},
	}
	statusPath, err := home.StatusFilePath(homeDir, "rev-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(statusPath, 0700); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() { runReviewCycle(t, homeDir, port) })
	if !strings.Contains(out, "review-verdict report for rev-1 failed (re-announced on restart)") {
		t.Fatalf("stderr = %q, want the logged report failure", out)
	}
	if rec := mustReadProcessEvent(t, homeDir, reviewVerdictEventID("rev-1", 1)); rec.AckedGeneration == rec.Generation {
		t.Fatalf("record = %+v, want it unacked so a restart re-announces it", rec)
	}
}
