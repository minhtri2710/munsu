package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/home"
)

// reportDuringAFK files one direct-dispatch report per state, then runs the AFK
// path the daemon runs: drain the queue into a digest, feed the digester, flush
// the durable digest file. It returns the message ids by state.
func reportDuringAFK(t *testing.T, generalHome, identity string, states ...string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, state := range states {
		res, err := Report(ReportRequest{
			SenderHome: generalHome, ReceiverHome: generalHome,
			SenderRank: RankSoldier, SenderIdentity: "direct-task",
			ReceiverRank: RankGeneral, ReceiverID: identity,
			TaskID: "direct-task", Key: state, State: state, Message: "afk " + state,
		})
		if err != nil {
			t.Fatalf("Report %s: %v", state, err)
		}
		ids[state] = res.MessageID
	}
	flushDigest(t, generalHome)
	return ids
}

func flushDigest(t *testing.T, homeDir string) {
	t.Helper()
	digest, err := OneCycle(homeDir)
	if err != nil || digest == nil {
		t.Fatalf("OneCycle = %+v err = %v, want a digest", digest, err)
	}
	d := NewDigester(homeDir)
	d.Feed(digest)
	if err := d.Flush(time.Now()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// An uplink wake drained while the General is away keeps its NotificationRef
// into the durable digest, escalated entry and routine entry alike.
func TestUplinkWakeKeepsItsRefThroughTheAFKDigest(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	ids := reportDuringAFK(t, generalHome, identity, "failed", "done")

	be, err := readDigestFile(filepath.Join(generalHome, digestFile))
	if err != nil || be == nil || len(be.Entries) != 2 {
		t.Fatalf("digest = %+v err = %v, want 2 entries", be, err)
	}
	if be.EscalatedCount != 1 || be.RoutineCount != 1 {
		t.Fatalf("digest counts = %d escalated, %d routine, want one of each", be.EscalatedCount, be.RoutineCount)
	}
	for _, entry := range be.Entries {
		state := strings.SplitN(entry.Payload, ":", 2)[0]
		want := NotificationRef{MessageID: ids[state], SenderIdentity: "direct-task"}.Encode()
		if entry.Ref != want {
			t.Fatalf("%s entry ref = %q, want %q", state, entry.Ref, want)
		}
	}
}

// munsu afk return hands the digest to the General and that counts as its
// acknowledgment: every drained uplink report is accepted, the next recovery
// pass retires the sender's pending reports, and teardown continuity passes.
func TestAFKReturnAcceptsTheDrainedUplinkReports(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	ids := reportDuringAFK(t, generalHome, identity, "failed", "done")
	if err := VerifyRetirementContinuity(generalHome, "direct-task"); err == nil {
		t.Fatal("continuity must refuse while the reports are unacknowledged")
	}

	report, err := Return(generalHome)
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if len(report.UnacceptedReports) != 0 {
		t.Fatalf("unaccepted = %v, want none", report.UnacceptedReports)
	}
	// The routine report is accepted, so the General is shown it: key and payload.
	wantRoutine := "direct-task: done: afk done [task=direct-task key=done]"
	if len(report.RoutineReports) != 1 || report.RoutineReports[0] != wantRoutine || !strings.Contains(report.String(), wantRoutine) {
		t.Fatalf("routine reports = %q, want %q printed in:\n%s", report.RoutineReports, wantRoutine, report.String())
	}
	for state, id := range ids {
		if ack, err := NewStore(generalHome).ReadAck("direct-task", id); err != nil || ack == nil || ack.Outcome != OutcomeAccepted {
			t.Fatalf("%s report ack = %+v err = %v, want accepted", state, ack, err)
		}
	}
	if err := ReconcileCaptainHook(generalHome, false, &countingTransport{}); err != nil {
		t.Fatalf("ReconcileCaptainHook: %v", err)
	}
	if err := VerifyRetirementContinuity(generalHome, "direct-task"); err != nil {
		t.Fatalf("continuity after afk return: %v", err)
	}
}

// A digest entry that carries no ref accepts nothing, even with an unacked
// uplink envelope sitting in the same home.
func TestAFKReturnAcceptsNothingForAnEntryWithoutARef(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	res := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})
	if _, err := home.DrainWakesOfKind(generalHome, "uplink"); err != nil { // no digest entry names the report
		t.Fatal(err)
	}
	if err := EnqueueWake(generalHome, "signal", "direct-task", "needs-decision: x"); err != nil {
		t.Fatal(err)
	}
	flushDigest(t, generalHome)

	report, err := Return(generalHome)
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if report.DigestedCount != 1 || len(report.UnacceptedReports) != 0 {
		t.Fatalf("report = %+v, want the signal entry drained and nothing unaccepted", report)
	}
	if ack, err := NewStore(generalHome).ReadAck("direct-task", res.MessageID); err != nil || ack != nil {
		t.Fatalf("ack = %+v err = %v, want none for an envelope no digest entry names", ack, err)
	}
}

// A failed accept is reported with the ref to retry and never costs the
// General the digest: the return still lists the escalation and drains the file.
func TestAFKReturnReportsAConflictingAckAndKeepsTheDigest(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	ids := reportDuringAFK(t, generalHome, identity, "failed")
	writeConflictingAck(t, generalHome, ids["failed"])

	report, err := Return(generalHome)
	if err != nil {
		t.Fatalf("Return must report the failed accept, not abort: %v", err)
	}
	if report.DigestedCount != 1 || len(report.Escalations) != 1 {
		t.Fatalf("report = %+v, want the drained escalation kept", report)
	}
	wantRef := NotificationRef{MessageID: ids["failed"], SenderIdentity: "direct-task"}.Encode()
	if len(report.UnacceptedReports) != 1 || !strings.Contains(report.UnacceptedReports[0].Reason, "conflicting") || report.UnacceptedReports[0].Ref != wantRef {
		t.Fatalf("unaccepted = %v, want the conflicting refusal with the ref %s", report.UnacceptedReports, wantRef)
	}
	if !report.HasActionable() || !strings.Contains(report.String(), "not accepted") {
		t.Fatalf("a failed accept must be actionable and printed:\n%s", report.String())
	}
	if ack, err := NewStore(generalHome).ReadAck("direct-task", ids["failed"]); err != nil || ack == nil || ack.Outcome != OutcomeDone {
		t.Fatalf("existing ack = %+v err = %v, want it untouched", ack, err)
	}
}

// writeConflictingAck records a different outcome for the report, so accepting
// it fails closed.
func writeConflictingAck(t *testing.T, generalHome, messageID string) {
	t.Helper()
	env, err := NewStore(generalHome).ReadEnvelope("direct-task", messageID)
	if err != nil || env == nil {
		t.Fatalf("ReadEnvelope: %v", err)
	}
	if err := NewStore(generalHome).WriteAck(&ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank, SenderIdentity: env.SenderIdentity,
		ReceiverRank: env.ReceiverRank, ReceiverID: env.ReceiverID, TaskID: env.TaskID, Key: env.Key,
		PayloadHash: env.PayloadHash, Outcome: OutcomeDone, ProcessedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
}

// A failed accept of a routine entry is the only thing wrong with the return,
// and that alone makes it actionable, with the entry's text in the report.
func TestAFKReturnWithOnlyAnUnacceptedRoutineReportIsActionable(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	ids := reportDuringAFK(t, generalHome, identity, "done")
	writeConflictingAck(t, generalHome, ids["done"])

	report, err := Return(generalHome)
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	if len(report.Escalations)+len(report.WedgeAlarms)+len(report.BlockedItems) != 0 || report.LossyStop {
		t.Fatalf("report = %+v, want nothing actionable but the unaccepted report", report)
	}
	if len(report.UnacceptedReports) != 1 || !strings.Contains(report.UnacceptedReports[0].Payload, "afk done") {
		t.Fatalf("unaccepted = %+v, want the routine entry with its payload", report.UnacceptedReports)
	}
	if !report.HasActionable() {
		t.Fatal("an unaccepted report must make the return actionable")
	}
}

// The digest is the only copy of the refs, so it is removed after they are
// accepted, never before. A removal that fails after acceptance is not a failed
// return: the acks are written and the file stays, the report names the file and
// the error and is actionable, and the re-run replays the idempotent ack and
// finishes clean.
func TestAFKReturnKeepsTheDigestUntilItsReportsAreAccepted(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	ids := reportDuringAFK(t, generalHome, identity, "failed")
	digestPath := filepath.Join(generalHome, digestFile)

	real := removeDigestFile
	t.Cleanup(func() { removeDigestFile = real })
	removeDigestFile = func(string) error { return errors.New("injected removal failure") }
	failed, err := Return(generalHome)
	if err != nil {
		t.Fatalf("a digest that cannot be removed after acceptance must not fail the return: %v", err)
	}
	if !strings.Contains(failed.DigestNotRemoved, digestPath) || !strings.Contains(failed.DigestNotRemoved, "injected removal failure") {
		t.Fatalf("DigestNotRemoved = %q, want the path and the error", failed.DigestNotRemoved)
	}
	if !failed.HasActionable() || !strings.Contains(failed.String(), "digest not removed") {
		t.Fatalf("a leftover digest must be actionable and printed:\n%s", failed.String())
	}
	if ack, err := NewStore(generalHome).ReadAck("direct-task", ids["failed"]); err != nil || ack == nil || ack.Outcome != OutcomeAccepted {
		t.Fatalf("ack = %+v err = %v, want the report accepted before the removal was attempted", ack, err)
	}
	if _, err := os.Stat(digestPath); err != nil {
		t.Fatalf("digest must survive a failed removal: %v", err)
	}

	removeDigestFile = real
	report, err := Return(generalHome)
	if err != nil || len(report.UnacceptedReports) != 0 || report.DigestNotRemoved != "" {
		t.Fatalf("re-run = %+v err = %v, want a clean replay", report, err)
	}
	if _, err := os.Stat(digestPath); !os.IsNotExist(err) {
		t.Fatalf("digest remains after the re-run: %v", err)
	}
}

// A digest that holds refs is not stale: clearing session artifacts at the next
// AFK start keeps it, so the next return still accepts what it carries.
func TestClearStaleArtifactsKeepsADigestHoldingRefs(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	reportDuringAFK(t, generalHome, identity, "failed")

	if err := ClearStaleArtifacts(generalHome); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(generalHome, digestFile)); err != nil {
		t.Fatalf("a digest holding refs must survive the stale clear: %v", err)
	}
}
