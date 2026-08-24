package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countingTransport records how many times the notification transport was
// actually reached. The count is the control for issue #562: the defect there
// was a result that reported "queued" while the transport was never called.
type countingTransport struct {
	calls   int
	handles []string
}

func (t *countingTransport) Notify(_ string, target TargetResult, _ string) UplinkNotifyResult {
	t.calls++
	t.handles = append(t.handles, target.Handle)
	return UplinkNotifyResult{Outcome: UplinkNotifyAcknowledged}
}

// directGeneralHome builds the topology of matrix section 1.1: one General home,
// no Captain anywhere, and a General pane the uplink is expected to reach.
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
	identity, rank, err := ReadHomeIdentity(generalHome)
	if err != nil {
		t.Fatalf("ReadHomeIdentity: %v", err)
	}
	if rank != RankGeneral {
		t.Fatalf("fixture home rank = %q, want %q", rank, RankGeneral)
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
	_, rank, err := ReadHomeIdentity(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: rank, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			return NotifyParentWithTransport(generalHome, generalHome, ref, transport)
		},
	})
	if err != nil {
		t.Fatalf("Report under direct General dispatch: %v", err)
	}
	return res
}

// Fact 1. With the report stamped Captain, this resolver took the Captain
// branch and died on a parent-home key a General home has no reason to hold.
// The General's own pane was configured and reachable the whole time.
func TestDirectGeneralDispatchResolvesTheGeneralPaneNotACaptainParent(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	res := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})

	target, err := resolveReceiverTarget(generalHome, true, NotificationRef{MessageID: res.MessageID, SenderIdentity: "direct-task"})
	if err != nil {
		t.Fatalf("resolving a direct General report must not go through a Captain parent: %v", err)
	}
	if target.Handle != "fleet:p1" {
		t.Fatalf("handle = %q, want the General pane %q", target.Handle, "fleet:p1")
	}
	if target.Source != ConfigSource {
		t.Fatalf("source = %v, want %v", target.Source, ConfigSource)
	}
}

// Fact 2. The defect that decided this issue: a resolution failure was returned
// as {Queued: true} with the transport never reached, so the caller could not
// tell a genuinely unavailable pane from a path that had failed. The call count
// is the assertion, not the returned flags.
func TestDirectGeneralDispatchReachesTheTransportInsteadOfReportingQueued(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	transport := &countingTransport{}
	res := reportUnderDirectGeneralDispatch(t, generalHome, identity, transport)

	if transport.calls != 1 {
		t.Fatalf("transport calls = %d, want 1", transport.calls)
	}
	if len(transport.handles) != 1 || transport.handles[0] != "fleet:p1" {
		t.Fatalf("transport handles = %v, want [fleet:p1]", transport.handles)
	}
	if !res.Notified || res.Queued {
		t.Fatalf("result = %+v, want notified", *res)
	}
}

// Fact 3. The envelope landed in the General's own inbox carrying a Captain
// receiver rank, so the General's receiver refused to read or ack the report
// addressed to it.
func TestDirectGeneralDispatchReportIsReceivableByItsOwnGeneral(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	res := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})

	recv, err := NewReceiver(generalHome)
	if err != nil {
		t.Fatalf("NewReceiver: %v", err)
	}
	ref := NotificationRef{MessageID: res.MessageID, SenderIdentity: "direct-task"}
	env, err := recv.Receive(ref)
	if err != nil {
		t.Fatalf("the receiving General must be able to read its own inbox item: %v", err)
	}
	if env.ReceiverRank != RankGeneral {
		t.Fatalf("receiver rank = %q, want %q", env.ReceiverRank, RankGeneral)
	}
	if _, err := recv.Ack(ref); err != nil {
		t.Fatalf("the receiving General must be able to ack its own inbox item: %v", err)
	}
}

// Fact 4. Under direct General dispatch, a soldier report whose notification
// was queued must be recovered by the production recovery pass (ReconcileCaptainHook)
// once the report is acknowledged, allowing VerifyRetirementContinuity to succeed.
func TestDirectGeneralDispatchRecoveryPassRetiresAcknowledgedReport(t *testing.T) {
	generalHome, identity := directGeneralHome(t)

	// The first notification reaches the transport but encounters a transient
	// failure. The report must still be durable so reconciliation can recover it.
	failed := &failingTransport{}
	res, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: RankGeneral, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			return NotifyParentWithTransport(generalHome, generalHome, ref, failed)
		},
	})
	if err == nil || !errors.Is(err, ErrReportDurable) {
		t.Fatalf("Report error = %v, want durable transient-notify failure", err)
	}
	if res != nil || failed.calls != 1 {
		t.Fatalf("result=%+v transport calls=%d, want durable failure after one transport call", res, failed.calls)
	}
	pending, err := NewStore(generalHome).ListPending("direct-task")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending after transient notify failure = %d err=%v, want 1", len(pending), err)
	}
	messageID := pending[0].MessageID
	t.Logf("transient notify failure: durable message %s remains pending", messageID)
	if err := VerifyRetirementContinuity(generalHome, "direct-task"); err == nil {
		t.Fatal("VerifyRetirementContinuity must refuse while the durable report is pending")
	}
	t.Logf("retirement blocked while message %s is pending", messageID)

	// Reconciliation on the General home must retry the pending uplink.
	retried := &countingTransport{}
	if err := ReconcileCaptainHook(generalHome, true, retried); err != nil {
		t.Fatalf("ReconcileCaptainHook retry: %v", err)
	}
	if retried.calls != 1 {
		t.Fatalf("reconciliation transport calls = %d, want 1", retried.calls)
	}
	t.Logf("General reconciliation retried message %s successfully", messageID)

	// The receiving General acknowledges the retried durable envelope.
	recv, err := NewReceiver(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	ref := NotificationRef{MessageID: messageID, SenderIdentity: "direct-task"}
	if _, err := recv.Ack(ref); err != nil {
		t.Fatal(err)
	}
	t.Logf("General receiver acknowledged message %s", messageID)

	// The next reconciliation cycle observes the ack and retires the outbox item.
	if err := ReconcileCaptainHook(generalHome, false, retried); err != nil {
		t.Fatalf("ReconcileCaptainHook retirement: %v", err)
	}
	pending, err = NewStore(generalHome).ListPending("direct-task")
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after acknowledged reconciliation = %d err=%v, want 0", len(pending), err)
	}
	if err := VerifyRetirementContinuity(generalHome, "direct-task"); err != nil {
		t.Fatalf("VerifyRetirementContinuity refused after recovery pass: %v", err)
	}
	t.Logf("message %s retired from General outbox; task retirement continuity restored", messageID)
}

// The uplink now validates the requested rank against the receiving home rather
// than writing whatever the caller stamped. A rank the home cannot satisfy is
// refused before any durable state is written.
func TestReportRefusesAReceiverRankTheReceivingHomeCannotSatisfy(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	_, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: RankCaptain, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
	})
	if err == nil {
		t.Fatal("a Captain receiver rank in a General home must be refused")
	}
	if pending, listErr := NewStore(generalHome).ListPending("direct-task"); listErr != nil || len(pending) != 0 {
		t.Fatalf("refused report left pending=%d err=%v", len(pending), listErr)
	}
}

// A target that cannot be resolved is unavailable, not a failed transport.
// It remains queued for durable delivery and the transport is not reached.
func TestNotifyParentWithTargetResolverQueuesResolutionError(t *testing.T) {
	transport := &countingTransport{}
	result := NotifyParentWithTargetResolver("sender", "receiver", NotificationRef{MessageID: "m", SenderIdentity: "s"},
		func(string, bool, NotificationRef) (TargetResult, error) {
			return TargetResult{}, errors.New("captain parent-home unavailable")
		}, transport)
	if !result.Queued() || result.Err != nil || result.Acknowledged() {
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
		SenderRank: RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: RankGeneral, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
		Notify: func(NotificationRef) UplinkNotifyResult {
			return UplinkNotifyResult{Outcome: UplinkNotifyFailed, Err: fmt.Errorf("resolving receiver target")}
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
	pending, listErr := NewStore(generalHome).ListPending("direct-task")
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
		SenderRank: RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: RankGeneral, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
	})
	if err == nil {
		t.Fatal("an unreadable receiving home must be refused")
	}
}

type failingTransport struct{ calls int }

func (t *failingTransport) Notify(_ string, _ TargetResult, _ string) UplinkNotifyResult {
	t.calls++
	return UplinkNotifyResult{Outcome: UplinkNotifyFailed, Err: errors.New("transport unavailable")}
}

// Resolver failures stay queued because no transport was invoked; a transport
// failure is loud because the notification path actually ran.
func TestNotifyFailureIsFatalOnlyAfterTransportInvocation(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	transport := &failingTransport{}
	var ref NotificationRef
	result, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: RankGeneral, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
		Notify: func(got NotificationRef) UplinkNotifyResult {
			ref = got
			return NotifyParentWithTransport(generalHome, generalHome, got, transport)
		},
	})
	if err == nil || !errors.Is(err, ErrReportDurable) {
		t.Fatalf("err = %v, want durable transport failure", err)
	}
	if result != nil || transport.calls != 1 || ref.MessageID == "" {
		t.Fatalf("result=%+v transport calls=%d ref=%+v, want one invoked failed transport", result, transport.calls, ref)
	}
}

// Whether re-running report after a notify failure is safe is an operator-facing
// claim, so it is executed here rather than reasoned about. A second report over
// the same task and key supersedes the first: one pending record, the first
// envelope marked superseded, the receiver reading and acking the second, and
// the transport reached exactly once by the run that reached it.
func TestReReportingAfterANotifyFailureSupersedesInsteadOfDoubleWriting(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	report := func(notify func(NotificationRef) UplinkNotifyResult) (*ReportResult, error) {
		return Report(ReportRequest{
			SenderHome: generalHome, ReceiverHome: generalHome,
			SenderRank: RankSoldier, SenderIdentity: "direct-task",
			ReceiverRank: RankGeneral, ReceiverID: identity,
			TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
			Notify: notify,
		})
	}
	if _, err := report(func(NotificationRef) UplinkNotifyResult {
		return UplinkNotifyResult{Outcome: UplinkNotifyFailed, Err: fmt.Errorf("transport blip")}
	}); err == nil {
		t.Fatal("the first report must fail on its notification path")
	}
	firstPending, err := NewStore(generalHome).ListPending("direct-task")
	if err != nil || len(firstPending) != 1 {
		t.Fatalf("pending after the failed report = %d err = %v, want 1", len(firstPending), err)
	}

	transport := &countingTransport{}
	second, err := report(func(ref NotificationRef) UplinkNotifyResult {
		return NotifyParentWithTransport(generalHome, generalHome, ref, transport)
	})
	if err != nil {
		t.Fatalf("re-running report after a notify failure must succeed: %v", err)
	}
	if transport.calls != 1 {
		t.Fatalf("transport calls = %d, want exactly 1", transport.calls)
	}

	pending, err := NewStore(generalHome).ListPending("direct-task")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].MessageID != second.MessageID {
		t.Fatalf("pending = %d records, want only the second report; got %+v", len(pending), pending)
	}
	if !NewStore(generalHome).IsSuperseded("direct-task", firstPending[0].MessageID) {
		t.Fatal("the first report must be marked superseded, not left live alongside the second")
	}
	recv, err := NewReceiver(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	ref := NotificationRef{MessageID: second.MessageID, SenderIdentity: "direct-task"}
	if _, err := recv.Receive(ref); err != nil {
		t.Fatalf("the receiver must be able to read the superseding report: %v", err)
	}
	if _, err := recv.Ack(ref); err != nil {
		t.Fatalf("the receiver must be able to ack the superseding report: %v", err)
	}
}
