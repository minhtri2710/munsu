package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

func TestGuardBurnDownDaemonStartRefusesHeldLock(t *testing.T) {
	home := t.TempDir()
	lockPath := filepath.Join(home, afkLockFile)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\t%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))), 0644); err != nil {
		t.Fatal(err)
	}

	ready := make(chan struct{})
	err := (&Daemon{ready: ready}).Start(home)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Daemon.Start error = %v, want held-lock refusal", err)
	}
	select {
	case <-ready:
	default:
		t.Fatal("Daemon.Start did not close readiness seam before refusal")
	}
}

func TestGuardBurnDownValidateTargetOwnershipRejectsNil(t *testing.T) {
	if err := ValidateTargetOwnership(nil); err == nil || !strings.Contains(err.Error(), "target is nil") {
		t.Fatalf("ValidateTargetOwnership error = %v, want nil-target refusal", err)
	}
}

func TestGuardBurnDownWatcherRunRefusesHeldWatchLock(t *testing.T) {
	home := t.TempDir()
	acquired, err := AcquireWatch(home)
	if err != nil || !acquired {
		t.Fatalf("AcquireWatch = (%v, %v), want held lock", acquired, err)
	}
	defer ReleaseWatch(home)

	_, err = run(home, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "another watcher is already running") {
		t.Fatalf("run error = %v, want held-watch-lock refusal", err)
	}
}

func TestWatcherRunPropagatesLiveLeaseConflict(t *testing.T) {
	home := t.TempDir()
	lease := mhome.WatcherLease{
		Home:      mhome.Canonical(home),
		PID:       os.Getppid(),
		StartedAt: time.Now().Unix(),
		UpdatedAt: time.Now().UnixNano(),
	}
	data, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mhome.WatcherLeasePath(home)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mhome.WatcherLeasePath(home), data, 0644); err != nil {
		t.Fatal(err)
	}

	_, err = run(home, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "claiming watcher lease: watcher lease held by pid") {
		t.Fatalf("run error = %v, want live-lease conflict propagation", err)
	}
}

func TestGuardBurnDownStopRunningWatcherRefusesUnownedPID(t *testing.T) {
	home := t.TempDir()
	WriteBeat(home)
	if err := os.WriteFile(mhome.WriterIdentityPath(home, "watcher"), []byte("schema_version=1\nkind=watcher\npid=9999999\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := stopRunningWatcher(home)
	if err == nil || !strings.Contains(err.Error(), "ownership could not be verified") {
		t.Fatalf("stopRunningWatcher error = %v, want ownership refusal", err)
	}
}

func TestGuardBurnDownRecoverRejectsNonAcceptedAck(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()
	if err := WriteHomeIdentity(receiverHome, "general-1", RankGeneral); err != nil {
		t.Fatal(err)
	}
	result, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: RankCaptain, SenderIdentity: "captain-1",
		ReceiverRank: RankGeneral, ReceiverID: "general-1",
		TaskID: "captain:1", Key: "default", State: "failed", Message: "failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := NewStore(receiverHome).ReadEnvelope("captain-1", result.MessageID)
	if err != nil || env == nil {
		t.Fatalf("ReadEnvelope = (%v, %v)", env, err)
	}
	if err := NewStore(receiverHome).WriteAck(&ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank, SenderIdentity: env.SenderIdentity,
		ReceiverRank: env.ReceiverRank, ReceiverID: env.ReceiverID, TaskID: env.TaskID, Key: env.Key,
		PayloadHash: env.PayloadHash, ProcessedAt: time.Now().UnixNano(), Outcome: OutcomeBlocked,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = Recover(RecoverRequest{SenderHome: senderHome, ReceiverHome: receiverHome, SenderIdentity: "captain-1"})
	if err == nil || !strings.Contains(err.Error(), "is not accepted") {
		t.Fatalf("Recover error = %v, want non-accepted-ack refusal", err)
	}
}

func TestGuardBurnDownReportRejectsNonMaterialState(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()
	if err := WriteHomeIdentity(receiverHome, "captain-1", RankCaptain); err != nil {
		t.Fatal(err)
	}

	_, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: RankSoldier, SenderIdentity: "soldier-1",
		ReceiverRank: RankCaptain, ReceiverID: "captain-1",
		TaskID: "task-1", State: "working", Message: "in progress",
	})
	if err == nil || !strings.Contains(err.Error(), "is not material") {
		t.Fatalf("Report error = %v, want non-material-state refusal", err)
	}
}

func TestGuardBurnDownReportRejectsMissingRequiredFields(t *testing.T) {
	_, err := Report(ReportRequest{})
	if err == nil || !strings.Contains(err.Error(), "required identity") {
		t.Fatalf("Report error = %v, want missing-required-fields refusal", err)
	}
}

func TestGuardBurnDownResolveCaptainActivationTargetRejectsEmptyPane(t *testing.T) {
	captainHome := t.TempDir()
	parentHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(captainHome, ProvenanceMarkerName), []byte("munsu-v2\ntest-captain\n"), 0644); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(parentHome, "state", "captain:test-captain.meta")
	if err := os.MkdirAll(filepath.Dir(metaPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, []byte("kind=captain\nherdr_session=general\nherdr_pane_id=\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveCaptainActivationTarget(captainHome, parentHome)
	if err == nil || !strings.Contains(err.Error(), "no herdr_pane_id") {
		t.Fatalf("resolveCaptainActivationTarget error = %v, want empty-pane refusal", err)
	}
}
