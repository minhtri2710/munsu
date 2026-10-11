package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mhome "github.com/minhtri2710/munsu/internal/home"
)

func TestGuardBurnDownDaemonStartRefusesHeldLock(t *testing.T) {
	home := t.TempDir()
	held, acquired, err := mhome.AcquireAFKLock(home)
	if err != nil || !acquired {
		t.Fatalf("AcquireAFKLock = %v, %v; want held", acquired, err)
	}
	defer held.Release()

	ready := make(chan struct{})
	err = (&Daemon{ready: ready}).Start(home)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Daemon.Start error = %v, want held-lock refusal", err)
	}
	t.Logf("Daemon.Start refusal: %v", err)
	select {
	case <-ready:
		t.Log("Daemon.Start readiness seam closed before refusal")
	default:
		t.Fatal("Daemon.Start did not close readiness seam before refusal")
	}
}

func TestGuardBurnDownValidateTargetOwnershipRejectsNil(t *testing.T) {
	if err := ValidateTargetOwnership(nil); err == nil || !strings.Contains(err.Error(), "target is nil") {
		t.Fatalf("ValidateTargetOwnership error = %v, want nil-target refusal", err)
	} else {
		t.Logf("ValidateTargetOwnership refusal: %v", err)
	}
}

func TestGuardBurnDownWatcherRunRefusesHeldWatchLock(t *testing.T) {
	home := t.TempDir()
	acquired, err := AcquireWatch(home)
	if err != nil || !acquired {
		t.Fatalf("AcquireWatch = (%v, %v), want held lock", acquired, err)
	}
	defer ReleaseWatch(home)

	_, err = run(home, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "another watcher is already running") {
		t.Fatalf("run error = %v, want held-watch-lock refusal", err)
	}
	t.Logf("run held-watch-lock refusal: %v", err)
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

	_, err = run(home, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "claiming watcher lease: watcher lease held by pid") {
		t.Fatalf("run error = %v, want live-lease conflict propagation", err)
	}
	t.Logf("run live-lease conflict: %v", err)
}

func TestGuardBurnDownStopWatcherRefusesUnownedPID(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteBeat(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mhome.WriterIdentityPath(home, "watcher"), []byte("schema_version=1\nkind=watcher\npid=9999999\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stop, err := StopWatcher(home)
	if err == nil || !strings.Contains(err.Error(), "ownership could not be verified") || stop.State != StopIdentityMismatch {
		t.Fatalf("StopWatcher = %+v, %v, want ownership refusal", stop, err)
	}
	t.Logf("StopWatcher refusal: %v", err)
}

func TestGuardBurnDownRecoverRejectsNonAcceptedAck(t *testing.T) {
	senderHome := t.TempDir()
	receiverHome := t.TempDir()
	result, err := Report(ReportRequest{
		SenderHome: senderHome, ReceiverHome: receiverHome,
		SenderRank: mhome.RankCaptain, SenderIdentity: "captain-1",
		ReceiverRank: mhome.RankGeneral, ReceiverID: "general-1",
		TaskID: "captain:1", Key: "default", State: "failed", Message: "failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := mhome.NewStore(receiverHome).ReadEnvelope("captain-1", result.MessageID)
	if err != nil || env == nil {
		t.Fatalf("ReadEnvelope = (%v, %v)", env, err)
	}
	if err := mhome.NewStore(receiverHome).WriteAck(&mhome.ProcessingAck{
		MessageID: env.MessageID, SenderRank: env.SenderRank, SenderIdentity: env.SenderIdentity,
		ReceiverRank: env.ReceiverRank, ReceiverID: env.ReceiverID, TaskID: env.TaskID, Key: env.Key,
		PayloadHash: env.PayloadHash, ProcessedAt: time.Now().UnixNano(), Outcome: mhome.OutcomeBlocked,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = Recover(RecoverRequest{SenderHome: senderHome, ReceiverHome: receiverHome, SenderIdentity: "captain-1"})
	if err == nil || !strings.Contains(err.Error(), "is not accepted") {
		t.Fatalf("Recover error = %v, want non-accepted-ack refusal", err)
	}
	t.Logf("Recover refusal: %v", err)
}

func TestGuardBurnDownReportRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(r *ReportRequest)
		wantErr string
	}{
		{name: "empty message", mutate: func(r *ReportRequest) { r.Message = "" }, wantErr: "required identity"},
		{name: "state that is not a material verb", mutate: func(r *ReportRequest) { r.State = "working" }, wantErr: "is not material"},
		{name: "material verb is accepted", mutate: func(*ReportRequest) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			senderHome, receiverHome := t.TempDir(), t.TempDir()
			if err := mhome.SeedCaptainProvenance(receiverHome, "captain-1"); err != nil {
				t.Fatal(err)
			}
			req := ReportRequest{
				SenderHome: senderHome, ReceiverHome: receiverHome,
				SenderRank: mhome.RankSoldier, SenderIdentity: "soldier-1",
				ReceiverRank: mhome.RankCaptain, ReceiverID: "captain-1",
				TaskID: "task-1", State: "done", Message: "finished",
			}
			tc.mutate(&req)
			_, err := Report(req)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Report = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Report error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolveCaptainActivationTarget(t *testing.T) {
	seedProvenance := func(t *testing.T, captainHome, captainID string) {
		t.Helper()
		if err := mhome.SeedCaptainProvenance(captainHome, captainID); err != nil {
			t.Fatal(err)
		}
	}
	setMeta := func(t *testing.T, parentHome, captainID string, meta map[string]string) {
		t.Helper()
		meta["kind"] = "captain"
		if err := mhome.WriteMeta(parentHome, "captain:"+captainID, meta); err != nil {
			t.Fatal(err)
		}
	}
	writeMarker := func(t *testing.T, captainHome, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(captainHome, mhome.CaptainProvenanceMarkerName), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name       string
		noParent   bool
		setup      func(t *testing.T, captainHome, parentHome string)
		wantErr    string
		wantHandle string
		wantSess   string
	}{
		{name: "no parent home", noParent: true, setup: func(t *testing.T, c, _ string) { seedProvenance(t, c, "test-captain") }, wantErr: "no parent home for captain meta lookup"},
		{name: "no provenance marker", setup: func(t *testing.T, _, p string) {
			setMeta(t, p, "test-captain", map[string]string{"herdr_pane_id": "p1"})
		}, wantErr: "has no .munsu-captain-home marker"},
		{name: "marker with a blank id", setup: func(t *testing.T, c, p string) {
			writeMarker(t, c, "munsu-v2\n \n"+c+"\n")
			setMeta(t, p, "test-captain", map[string]string{"herdr_pane_id": "p1"})
		}, wantErr: "empty id"},
		{name: "marker copied from another home", setup: func(t *testing.T, c, p string) {
			writeMarker(t, c, "munsu-v2\ncopied-captain\n/somewhere/else\n")
			setMeta(t, p, "copied-captain", map[string]string{"herdr_pane_id": "p1", "herdr_session": "general"})
		}, wantErr: "copied/moved"},
		{name: "no captain meta", setup: func(t *testing.T, c, _ string) { seedProvenance(t, c, "test-captain") }, wantErr: "reading captain meta captain:test-captain"},
		{name: "meta with a blank pane", setup: func(t *testing.T, c, p string) {
			seedProvenance(t, c, "test-captain")
			setMeta(t, p, "test-captain", map[string]string{"herdr_session": "general", "herdr_pane_id": ""})
		}, wantErr: "captain meta has no herdr_pane_id"},
		{name: "meta pane and session, not the watcher env", setup: func(t *testing.T, c, p string) {
			seedProvenance(t, c, "test-captain")
			setMeta(t, p, "test-captain", map[string]string{"herdr_pane_id": "p2", "herdr_session": "w76"})
			t.Setenv("HERDR_PANE_ID", "w1K:p1")
			t.Setenv("HERDR_ENV", "1")
		}, wantHandle: "w76:p2", wantSess: "w76"},
		{name: "meta without a session", setup: func(t *testing.T, c, p string) {
			seedProvenance(t, c, "test-captain")
			setMeta(t, p, "test-captain", map[string]string{"herdr_pane_id": "p1"})
		}, wantHandle: "default:p1", wantSess: "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captainHome, parentHome := t.TempDir(), t.TempDir()
			tc.setup(t, captainHome, parentHome)
			if tc.noParent {
				parentHome = ""
			}
			target, err := resolveCaptainActivationTarget(captainHome, parentHome)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveCaptainActivationTarget error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if target.Handle != tc.wantHandle || target.Session != tc.wantSess || target.SourceDetail != "captain task meta: captain:test-captain" {
				t.Fatalf("target = %+v, want handle %q session %q from the captain task meta", target, tc.wantHandle, tc.wantSess)
			}
		})
	}
}

func TestActivationRefusesUnusableCaptainMarkerAndReportsOnce(t *testing.T) {
	parentHome := t.TempDir()
	refusals := func(captainHome string) []Record {
		t.Helper()
		recs, _, err := LatestEvents(captainHome, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []Record
		for _, r := range recs {
			if r.Type == activationRefusedEvent {
				out = append(out, r)
			}
		}
		return out
	}

	captainHome := t.TempDir()
	marker := filepath.Join(captainHome, mhome.CaptainProvenanceMarkerName)
	for i := 0; i < 3; i++ {
		ActivateOnReceiptWithTransport(captainHome, parentHome, nil)
	}
	got := refusals(captainHome)
	if len(got) != 1 || !strings.Contains(got[0].Payload, "has no .munsu-captain-home marker") {
		t.Fatalf("refusal events after 3 cycles = %+v, want exactly one missing-marker event", got)
	}

	if err := os.WriteFile(marker, []byte("munsu-v2\n \n"+captainHome+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ActivateOnReceiptWithTransport(captainHome, parentHome, nil)
	got = refusals(captainHome)
	if len(got) != 2 || !strings.Contains(got[1].Payload, "empty id") {
		t.Fatalf("refusal events after the reason changed = %+v, want a second empty-id event", got)
	}
}

func TestActivationReportsCopiedHomeRefusalOnce(t *testing.T) {
	captainHome := t.TempDir()
	parentHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(captainHome, mhome.CaptainProvenanceMarkerName), []byte("munsu-v2\ncopied-captain\n/somewhere/else\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mhome.WriteMeta(parentHome, "captain:copied-captain", map[string]string{
		"kind":          "captain",
		"herdr_session": "general",
		"herdr_pane_id": "p1",
	}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		ActivateOnReceiptWithTransport(captainHome, parentHome, nil)
	}
	recs, _, err := LatestEvents(captainHome, 100)
	if err != nil || len(recs) != 1 || recs[0].Type != activationRefusedEvent || !strings.Contains(recs[0].Payload, "copied/moved") {
		t.Fatalf("events = %+v, %v; want one captain.activation.refused naming copied/moved", recs, err)
	}
}
