package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minhtri2710/munsu/internal/home"
)

// countingTransport records how many times the notification transport was
// actually reached. The count is the control: a result that reports "queued"
// says nothing about whether the transport ran.
type countingTransport struct{ calls int }

func (t *countingTransport) Notify(_ string, _ TargetResult, _ string) UplinkNotifyResult {
	t.calls++
	return AcknowledgedNotification()
}

// directGeneralHome builds the topology of matrix section 1.1: one General home,
// no Captain anywhere, one soldier task dispatched into that home, and a
// configured General pane. The task record is part of the
// topology, not decoration: it is what lets the receiving home derive that the
// sender is a soldier rather than take its word for it. The configured pane is
// one a notify would reach if the uplink still wrote to the General.
func directGeneralHome(t *testing.T) (string, string) {
	t.Helper()
	generalHome := filepath.Join(t.TempDir(), "general-home")
	if err := os.MkdirAll(filepath.Join(generalHome, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(generalHome, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generalHome, "config", "general-pane"), []byte("fleet:p1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := home.WriteMeta(generalHome, "direct-task", map[string]string{"kind": "ship"}); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	identity, rank, err := home.ReadHomeIdentity(generalHome)
	if err != nil {
		t.Fatalf("ReadHomeIdentity: %v", err)
	}
	if rank != home.RankGeneral {
		t.Fatalf("fixture home rank = %q, want %q", rank, home.RankGeneral)
	}
	return generalHome, identity
}

// reportUnderDirectGeneralDispatch files one material soldier report the way the
// soldier launch environment does when its parent home is the General: the same
// SenderHome production picks (senderHomeForRole returns parentHome for a
// soldier, which here is the General home itself). One difference is deliberate
// and bounds every test below -- report_cmd's Notify closure short-circuits to
// {Queued: true} under a "no-ring" policy without reaching the transport, so
// these tests describe the ring-enabled path only.
func reportUnderDirectGeneralDispatch(t *testing.T, generalHome, identity string, transport NotificationTransport) *ReportResult {
	t.Helper()
	_, rank, err := home.ReadHomeIdentity(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: home.RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: rank, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
		Notify: func(ref home.NotificationRef) UplinkNotifyResult {
			return NotifyParentWithTransport(generalHome, generalHome, ref, transport)
		},
	})
	if err != nil {
		t.Fatalf("Report under direct General dispatch: %v", err)
	}
	return res
}

// The watcher wake dispatch is the only automatic writer into the General's
// pane. A report whose receiver is the General enqueues its wake and reaches no
// transport, whether the sender shares the General's home (direct dispatch) or
// reports from a Captain home. The call count is the assertion, not the flags.
func TestReportToTheGeneralEnqueuesItsWakeAndReachesNoTransport(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	captainHome := t.TempDir()
	cases := []struct {
		name                   string
		senderHome             string
		senderRank             home.Rank
		senderIdentity, taskID string
	}{
		{"direct dispatch", generalHome, home.RankSoldier, "direct-task", "direct-task"},
		{"captain relay", captainHome, home.RankCaptain, "captain-one", "captain:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &countingTransport{}
			res, err := Report(ReportRequest{
				SenderHome: tc.senderHome, ReceiverHome: generalHome,
				SenderRank: tc.senderRank, SenderIdentity: tc.senderIdentity,
				ReceiverRank: home.RankGeneral, ReceiverID: identity,
				TaskID: tc.taskID, Key: tc.name, State: "failed", Message: "failure",
				Notify: func(ref home.NotificationRef) UplinkNotifyResult {
					return NotifyParentWithTransport(tc.senderHome, generalHome, ref, transport)
				},
			})
			if err != nil {
				t.Fatalf("Report: %v", err)
			}
			if transport.calls != 0 {
				t.Fatalf("transport calls = %d, want 0", transport.calls)
			}
			if res.Notified || !res.Queued {
				t.Fatalf("result = %+v, want queued", *res)
			}
			if !HasQueuedWakes(generalHome) {
				t.Fatal("the General's wake must be enqueued")
			}
		})
	}
}

// Fact 3. The envelope landed in the General's own inbox carrying a Captain
// receiver rank, so the General's receiver refused to read or ack the report
// addressed to it.
func TestDirectGeneralDispatchReportIsReceivableByItsOwnGeneral(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	res := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})

	recv, err := home.NewReceiver(generalHome)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	ref := home.NotificationRef{MessageID: res.MessageID, SenderIdentity: "direct-task"}
	env, err := recv.Receive(ref)
	if err != nil {
		t.Fatalf("the receiving General must be able to read its own inbox item: %v", err)
	}
	if env.ReceiverRank != home.RankGeneral {
		t.Fatalf("receiver rank = %q, want %q", env.ReceiverRank, home.RankGeneral)
	}
	if _, err := recv.Ack(ref); err != nil {
		t.Fatalf("the receiving General must be able to ack its own inbox item: %v", err)
	}
}

// Fact 4. Under direct General dispatch, the production recovery pass
// (ReconcileCaptainHook) never reaches the transport for the General's pending
// report, even when forced at startup, and still retires it once the General
// acknowledges it, allowing VerifyRetirementContinuity to succeed.
func TestDirectGeneralDispatchRecoveryPassRetiresAcknowledgedReport(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	res := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})
	messageID := res.MessageID

	transport := &countingTransport{}
	if err := ReconcileCaptainHook(generalHome, true, transport); err != nil {
		t.Fatalf("ReconcileCaptainHook retry: %v", err)
	}
	if transport.calls != 0 {
		t.Fatalf("recovery transport calls = %d, want 0 for a General-bound report", transport.calls)
	}
	pending, err := home.NewStore(generalHome).ListPending("direct-task")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending after the retry pass = %d err=%v, want 1 until the General acknowledges", len(pending), err)
	}

	recv, err := home.NewReceiver(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recv.Ack(home.NotificationRef{MessageID: messageID, SenderIdentity: "direct-task"}); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileCaptainHook(generalHome, false, transport); err != nil {
		t.Fatalf("ReconcileCaptainHook retirement: %v", err)
	}
	pending, err = home.NewStore(generalHome).ListPending("direct-task")
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after acknowledged reconciliation = %d err=%v, want 0", len(pending), err)
	}
	if err := VerifyRetirementContinuity(generalHome, "direct-task"); err != nil {
		t.Fatalf("VerifyRetirementContinuity refused after recovery pass: %v", err)
	}
}

// The uplink now validates the requested rank against the receiving home rather
// than writing whatever the caller stamped. A rank the home cannot satisfy is
// refused before any durable state is written.
func TestReportRefusesAReceiverRankTheReceivingHomeCannotSatisfy(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	_, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: home.RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: home.RankCaptain, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
	})
	if err == nil {
		t.Fatal("a Captain receiver rank in a General home must be refused")
	}
	if pending, listErr := home.NewStore(generalHome).ListPending("direct-task"); listErr != nil || len(pending) != 0 {
		t.Fatalf("refused report left pending=%d err=%v", len(pending), listErr)
	}
}

// A target that cannot be resolved is unavailable, not a failed transport.
// It remains queued for durable delivery and the transport is not reached.
func TestNotifyParentWithTargetResolverQueuesResolutionError(t *testing.T) {
	transport := &countingTransport{}
	result := NotifyParentWithTargetResolver("sender", "receiver", home.NotificationRef{MessageID: "m", SenderIdentity: "s"},
		func(string, bool, home.NotificationRef) (TargetResult, error) {
			return TargetResult{}, errors.New("captain parent-home unavailable")
		}, transport)
	if !result.Queued() || result.Failure() != nil || result.Acknowledged() {
		t.Fatalf("result = %+v, want queued without error or acknowledgement", result)
	}
	if transport.calls != 0 {
		t.Fatalf("transport calls = %d, want 0", transport.calls)
	}
}

// Report must not report success when the notification path failed.
func TestReportFailsClosedWhenTheNotificationPathFails(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	_, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: home.RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: home.RankGeneral, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
		Notify: func(home.NotificationRef) UplinkNotifyResult {
			return FailedNotification(fmt.Errorf("resolving receiver target"))
		},
	})
	if err == nil {
		t.Fatal("a failed notification path must not be reported as a successful queue")
	}
	// The failure lands after the durable commit, and the caller has to be able
	// to tell that apart from a report that never happened -- the two want
	// opposite responses from the operator.
	if !errors.Is(err, ErrReportDurable) {
		t.Fatalf("err = %v, want it to identify the report as already durable", err)
	}
	pending, listErr := home.NewStore(generalHome).ListPending("direct-task")
	if listErr != nil || len(pending) != 1 {
		t.Fatalf("pending = %d err = %v, want the report to have landed durably", len(pending), listErr)
	}
	if !strings.Contains(err.Error(), pending[0].MessageID) {
		t.Fatalf("err = %v, want it to name the durable message id %s", err, pending[0].MessageID)
	}
}

// A receiving home whose provenance cannot be read has no rank to satisfy, so
// the uplink refuses rather than guessing one.
func TestReportRefusesAReceivingHomeWhoseProvenanceIsUnreadable(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	if err := os.WriteFile(filepath.Join(generalHome, ".munsu-captain-home"), []byte("garbage\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: home.RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: home.RankGeneral, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
	})
	if err == nil {
		t.Fatal("an unreadable receiving home must be refused")
	}
}

// Whether re-running report after a notify failure is safe is an operator-facing
// claim, so it is executed here rather than reasoned about. A second report over
// the same task and key supersedes the first: one pending record, the first
// envelope marked superseded, and the receiver reading and acking the second.
func TestReReportingAfterANotifyFailureSupersedesInsteadOfDoubleWriting(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	report := func(notify func(home.NotificationRef) UplinkNotifyResult) (*ReportResult, error) {
		return Report(ReportRequest{
			SenderHome: generalHome, ReceiverHome: generalHome,
			SenderRank: home.RankSoldier, SenderIdentity: "direct-task",
			ReceiverRank: home.RankGeneral, ReceiverID: identity,
			TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
			Notify: notify,
		})
	}
	if _, err := report(func(home.NotificationRef) UplinkNotifyResult {
		return FailedNotification(fmt.Errorf("transport blip"))
	}); err == nil {
		t.Fatal("the first report must fail on its notification path")
	}
	firstPending, err := home.NewStore(generalHome).ListPending("direct-task")
	if err != nil || len(firstPending) != 1 {
		t.Fatalf("pending after the failed report = %d err = %v, want 1", len(firstPending), err)
	}

	second, err := report(nil)
	if err != nil {
		t.Fatalf("re-running report after a notify failure must succeed: %v", err)
	}

	pending, err := home.NewStore(generalHome).ListPending("direct-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].MessageID != second.MessageID {
		t.Fatalf("pending = %d records, want only the second report; got %+v", len(pending), pending)
	}
	if !home.NewStore(generalHome).IsSuperseded("direct-task", firstPending[0].MessageID) {
		t.Fatal("the first report must be marked superseded, not left live alongside the second")
	}
	recv, err := home.NewReceiver(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	ref := home.NotificationRef{MessageID: second.MessageID, SenderIdentity: "direct-task"}
	if _, err := recv.Receive(ref); err != nil {
		t.Fatalf("the receiver must be able to read the superseding report: %v", err)
	}
	if _, err := recv.Ack(ref); err != nil {
		t.Fatalf("the receiver must be able to ack the superseding report: %v", err)
	}
}

// The receiver's wake lands before the sender's pending record, so no crash
// between them can leave a pending report no wake will ever surface to a General
// (its recovery pass does not re-notify one). A wake enqueue that fails stands in
// for the crash: nothing may be pending afterwards.
func TestReportWritesNoPendingRecordWhenItsWakeCannotBeEnqueued(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	if err := os.MkdirAll(home.WakeQueuePath(generalHome), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: home.RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: home.RankGeneral, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
	})
	if err == nil || !strings.Contains(err.Error(), "enqueue receiver wake") {
		t.Fatalf("Report err = %v, want the wake enqueue to fail", err)
	}
	if pending, listErr := home.NewStore(generalHome).ListPending("direct-task"); listErr != nil || len(pending) != 0 {
		t.Fatalf("pending = %d err = %v, want none when the wake was never enqueued", len(pending), listErr)
	}
}
