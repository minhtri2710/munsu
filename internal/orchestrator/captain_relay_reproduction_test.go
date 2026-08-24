//go:build integration

package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/minhtri2710/munsu/internal/config"
)

type reproductionNotificationTransport struct {
	calls  int
	target TargetResult
}

func (t *reproductionNotificationTransport) Notify(_ string, target TargetResult, _ string) UplinkNotifyResult {
	t.calls++
	t.target = target
	return UplinkNotifyResult{Outcome: UplinkNotifyAcknowledged}
}

func TestReproductionCaptainRelayMissingPane(t *testing.T) {
	captainHome := t.TempDir()
	parentHome := t.TempDir()
	t.Setenv("MUNSU_PARENT_STATUS", parentHome)
	t.Setenv("TMUX_PANE", "%reproduction")
	if err := config.Set(captainHome, "parent-home", parentHome); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parentHome, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentHome, "state", "captain:captain-reproduction.meta"), []byte("kind=captain\nbackend=tmux\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env := &Envelope{
		Kind: "uplink-report", SenderRank: RankSoldier, SenderIdentity: "soldier-reproduction",
		ReceiverRank: RankCaptain, ReceiverID: "captain-reproduction", TaskID: "task-reproduction",
		Payload: "reproduction", PayloadHash: PayloadHashHex("reproduction"),
	}
	if err := NewStore(captainHome).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(captainHome).WritePending(env); err != nil {
		t.Fatal(err)
	}

	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
	directTarget, directErr := ResolveTargetWithSource(captainHome)
	t.Logf("direct_target=%+v direct_error=%v", directTarget, directErr)
	crossTarget, crossErr := resolveReceiverTarget(captainHome, false, ref)
	t.Logf("cross_process_target=%+v cross_process_resolver_error=%v", crossTarget, crossErr)
	crossTransport := &reproductionNotificationTransport{}
	crossResult := NotifyParentWithTransport(filepath.Join(captainHome, "different-sender"), captainHome, ref, crossTransport)
	t.Logf("cross_process_notify_result=%+v calls=%d", crossResult, crossTransport.calls)
	selfTarget, selfErr := resolveReceiverTarget(captainHome, true, ref)
	t.Logf("self_target=%+v self_error=%v", selfTarget, selfErr)
	transport := &reproductionNotificationTransport{}

	recovery, recoveryErr := Recover(RecoverRequest{
		SenderHome: captainHome, ReceiverHome: captainHome, ReceiverRank: RankCaptain,
		ForceNotify: true,
		Notify: func(ref NotificationRef) UplinkNotifyResult {
			return NotifyParentWithTransport(captainHome, captainHome, ref, transport)
		},
	})
	t.Logf("recover_result=%+v recover_error=%v", recovery, recoveryErr)
	reconcileErr := ReconcileCaptainHook(captainHome, true, transport)
	t.Logf("reconcile_error=%v", reconcileErr)
	if err := NewStore(captainHome).WriteAck(&ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank, SenderIdentity: env.SenderIdentity,
		ReceiverRank: env.ReceiverRank, ReceiverID: env.ReceiverID, TaskID: env.TaskID, Key: env.Key,
		PayloadHash: env.PayloadHash, Outcome: OutcomeAccepted, ProcessedAt: time.Now().UnixNano(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(RecoverRequest{SenderHome: captainHome, ReceiverHome: captainHome, ReceiverRank: RankCaptain}); err != nil {
		t.Fatal(err)
	}
	pending, err := NewStore(captainHome).ListPending(env.SenderIdentity)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pending_after=%d messages=%v", len(pending), pending)
	if crossErr == nil || crossErr.Error() != "captain meta has no herdr_pane_id" || crossTarget.Handle != "" {
		t.Fatalf("unexpected cross-process resolution: target=%+v error=%v", crossTarget, crossErr)
	}
	if !crossResult.Queued() || crossResult.Acknowledged() || crossTransport.calls != 0 {
		t.Fatalf("cross-process notification was not refused: result=%+v calls=%d", crossResult, crossTransport.calls)
	}
	if selfErr != nil || selfTarget.Handle != "%reproduction" {
		t.Fatalf("unexpected self target: %+v error=%v", selfTarget, selfErr)
	}
	if recoveryErr != nil || recovery == nil || recovery.Accepted != 0 || recovery.Notified != 1 || recovery.Queued != 0 || transport.calls < 1 || transport.target.Handle != "%reproduction" {
		t.Fatalf("unexpected recovery: result=%+v error=%v calls=%d target=%+v", recovery, recoveryErr, transport.calls, transport.target)
	}
	if reconcileErr != nil {
		t.Fatalf("unexpected reconcile error: %v", reconcileErr)
	}
	if len(pending) != 0 {
		t.Fatalf("pending count=%d", len(pending))
	}
}
