//go:build integration

package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minhtri2710/munsu/internal/config"
)

type reproductionNotificationTransport struct{}

func (reproductionNotificationTransport) Notify(string, TargetResult, string) UplinkNotifyResult {
	return UplinkNotifyResult{Acknowledged: true}
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
		Payload: "reproduction",
	}
	if err := NewStore(captainHome).WritePending(env); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(captainHome).WriteEnvelope(env); err != nil {
		t.Fatal(err)
	}

	ref := NotificationRef{MessageID: env.MessageID, SenderIdentity: env.SenderIdentity}
	directTarget, directErr := ResolveTargetWithSource(captainHome)
	t.Logf("direct_target=%+v direct_error=%v", directTarget, directErr)
	_, resolverErr := resolveReceiverTarget(captainHome, ref)
	t.Logf("resolver_error=%v", resolverErr)

	recovery, recoveryErr := Recover(RecoverRequest{
		SenderHome: captainHome, ReceiverHome: captainHome, ReceiverRank: RankCaptain,
		ForceNotify: true,
		Notify: func(NotificationRef) UplinkNotifyResult { return NotifyParentWithTransport(captainHome, captainHome, ref, reproductionNotificationTransport{}) },
	})
	t.Logf("recover_result=%+v recover_error=%v", recovery, recoveryErr)

	reconcileErr := ReconcileCaptainHook(captainHome, true, reproductionNotificationTransport{})
	t.Logf("reconcile_error=%v", reconcileErr)
	pending, err := NewStore(captainHome).ListPending(env.SenderIdentity)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pending_after=%d messages=%v", len(pending), pending)
	if resolverErr == nil || resolverErr.Error() != "captain meta has no herdr_pane_id" {
		t.Fatalf("unexpected resolver error: %v", resolverErr)
	}
	if recoveryErr != nil || recovery == nil || recovery.Accepted != 0 || recovery.Notified != 0 || recovery.Queued != 1 {
		t.Fatalf("unexpected recovery: result=%+v error=%v", recovery, recoveryErr)
	}
	if reconcileErr != nil {
		t.Fatalf("unexpected reconcile error: %v", reconcileErr)
	}
	if len(pending) != 1 {
		t.Fatalf("pending count=%d", len(pending))
	}
}
