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
	return UplinkNotifyResult{Acknowledged: true}
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

// reportUnderDirectGeneralDispatch files one material soldier report exactly as
// the soldier launch environment does when its parent home is the General.
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

	target, err := resolveReceiverTarget(generalHome, NotificationRef{MessageID: res.MessageID, SenderIdentity: "direct-task"})
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

// Fact 4. Recover filters pending records by receiver rank, so a report
// mislabelled Captain was invisible to a General-rank recovery pass.
//
// Read what this proves narrowly. It calls Recover directly, and proves that
// Recover handles a General-rank envelope correctly WHEN INVOKED. It does not
// prove that anything in production invokes it under direct General dispatch,
// and today nothing does: the only hook that recovers soldier pendings is
// ReconcileCaptainHook, which returns nil at captain_relay.go:61-64 when the
// home has no parent-home -- which a General home never has. So the pending
// record and open evidence this test retires by hand are not retired by any
// live path. That missing pass is tracked as its own issue; this test is not
// end-to-end closure of it, and must not be cited as such.
func TestDirectGeneralDispatchReportIsVisibleToGeneralRankRecovery(t *testing.T) {
	generalHome, identity := directGeneralHome(t)
	res := reportUnderDirectGeneralDispatch(t, generalHome, identity, &countingTransport{})

	recv, err := NewReceiver(generalHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recv.Ack(NotificationRef{MessageID: res.MessageID, SenderIdentity: "direct-task"}); err != nil {
		t.Fatal(err)
	}
	rec, err := Recover(RecoverRequest{SenderHome: generalHome, ReceiverHome: generalHome, ReceiverRank: RankGeneral})
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rec.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1; a General-rank pass must see a report addressed to the General", rec.Accepted)
	}
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

// A target that cannot be resolved is a failed path, not an unavailable pane.
// It must not be reported as queued, and the transport must not be reached.
func TestNotifyParentWithTargetResolverFailsLoudlyOnResolutionError(t *testing.T) {
	transport := &countingTransport{}
	boom := errors.New("captain parent-home unavailable")
	result := NotifyParentWithTargetResolver("sender", "receiver", NotificationRef{MessageID: "m", SenderIdentity: "s"},
		func(string, NotificationRef) (TargetResult, error) {
			return TargetResult{}, boom
		}, transport)
	if result.Err == nil {
		t.Fatal("a failed target resolution must surface as an error")
	}
	if !errors.Is(result.Err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", result.Err, boom)
	}
	if result.Queued || result.Acknowledged {
		t.Fatalf("result = %+v, want neither queued nor acknowledged", result)
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
			return UplinkNotifyResult{Err: fmt.Errorf("resolving receiver target")}
		},
	})
	if err == nil {
		t.Fatal("a failed notification path must not be reported as a successful queue")
	}
	// The failure lands after the durable write, and the caller has to be able
	// to tell that apart from a report that never happened -- otherwise the
	// repair for it is a second report over the same state.
	if !errors.Is(err, ErrReportedNotNotified) {
		t.Fatalf("err = %v, want it to identify itself as durable-but-not-notified", err)
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

// The loudness added for #562 stops at Report. Recover keeps counting a failed
// path as a queued retry, because its captain branch carries a pre-existing
// unmet precondition (herdr_pane_id, written only by HerdrBackend) that this
// branch's reproduction never covered. Widening the boundary would convert that
// silent failure into a startup failure through ReconcileCaptainHook, so both
// sides are pinned here: loud through Report, retried through Recover.
func TestTheNotifyFailureBoundaryStopsAtReport(t *testing.T) {
	failing := func(NotificationRef) UplinkNotifyResult {
		return UplinkNotifyResult{Err: fmt.Errorf("resolving receiver target")}
	}
	generalHome, identity := directGeneralHome(t)
	if _, err := Report(ReportRequest{
		SenderHome: generalHome, ReceiverHome: generalHome,
		SenderRank: RankSoldier, SenderIdentity: "direct-task",
		ReceiverRank: RankGeneral, ReceiverID: identity,
		TaskID: "direct-task", Key: "default", State: "failed", Message: "direct dispatch failure",
		Notify: failing,
	}); err == nil {
		t.Fatal("Report must refuse a failed notification path, not report it queued")
	}

	// Report refused after writing durable state, so the envelope is pending:
	// the same failure now has to reach Recover's retry path.
	res, err := Recover(RecoverRequest{
		SenderHome: generalHome, ReceiverHome: generalHome, ReceiverRank: RankGeneral, ForceNotify: true,
		Notify: failing,
	})
	if err != nil {
		t.Fatalf("Recover must retry a failed notification path, not fail closed on it: %v", err)
	}
	if res.Queued != 1 || res.Notified != 0 {
		t.Fatalf("recover result = %+v, want exactly one queued retry", *res)
	}
}
